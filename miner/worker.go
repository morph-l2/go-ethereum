package miner

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/consensus/misc"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/metrics"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/morph-l2/go-ethereum/rollup/fees"
)

var (
	errBlockInterruptedByNewHead  = errors.New("new head arrived while building block")
	errBlockInterruptedByRecommit = errors.New("recommit interrupt while building block")
	errBlockInterruptedByTimeout  = errors.New("timeout while building block")

	// Metrics for the skipped txs
	l1TxGasLimitExceededCounter = metrics.NewRegisteredCounter("miner/skipped_txs/l1/gas_limit_exceeded", nil)
	l1TxStrangeErrCounter       = metrics.NewRegisteredCounter("miner/skipped_txs/l1/strange_err", nil)

	l2CommitTxsTimer      = metrics.NewRegisteredTimer("miner/commit/txs_all", nil)
	l2CommitTxTimer       = metrics.NewRegisteredTimer("miner/commit/tx_all", nil)
	l2CommitTxFailedTimer = metrics.NewRegisteredTimer("miner/commit/tx_all_failed", nil)
	l2CommitTxApplyTimer  = metrics.NewRegisteredTimer("miner/commit/tx_apply", nil)
)

// environment is the worker's current environment and holds all
// information of the sealing block generation.
type environment struct {
	signer types.Signer

	state     *state.StateDB     // apply state changes here
	tcount    int                // tx count in cycle
	blockSize common.StorageSize // approximate size of tx payload in bytes
	l1TxCount int                // l1 msg count in cycle
	gasPool   *core.GasPool      // available gas used to pack transactions

	header   *types.Header
	txs      []*types.Transaction
	receipts []*types.Receipt

	nextL1MsgIndex uint64 // next L1 queue index to be processed
	isSimulate     bool
}

const (
	commitInterruptNone int32 = iota
	commitInterruptNewHead
	commitInterruptResubmit
	commitInterruptTimeout
)

type getWorkResp struct {
	ret *NewBlockResult
	err error
}

// getWorkReq represents a request for getting a new sealing work with provided parameters.
type getWorkReq struct {
	interrupt *int32
	params    *generateParams
	result    chan getWorkResp // non-blocking channel
}

type NewBlockResult struct {
	Block    *types.Block
	State    *state.StateDB
	Receipts types.Receipts
}

// generateParams wraps various of settings for generating sealing task.
type generateParams struct {
	timestamp    uint64             // The timstamp for sealing task
	parentHash   common.Hash        // Parent block hash, empty means the latest chain head
	coinbase     common.Address     // The fee recipient address for including transaction
	transactions types.Transactions // L1Message transactions to include at the start of the block
	simulate     bool
	timeout      time.Duration
}

func (miner *Miner) generateWorkLoop() {
	defer miner.wg.Done()
	for {
		select {
		case req := <-miner.getWorkCh:
			ret, err := miner.generateWork(req.params, req.interrupt)
			req.result <- getWorkResp{
				ret: ret,
				err: err,
			}
			// System stopped
		case <-miner.exitCh:
			return
		}
	}
}

// generateWork generates a sealing block based on the given parameters.
// TODO the produced state data by the transactions will be commit to database, whether the block is confirmed or not.
// TODO this issue will persist until the current zktrie based database optimizes its strategy.
func (miner *Miner) generateWork(genParams *generateParams, interrupt *int32) (*NewBlockResult, error) {
	work, prepareErr := miner.prepareWork(genParams)
	if prepareErr != nil {
		return nil, prepareErr
	}

	// Apply special state transition at Curie block
	if miner.chainConfig.CurieBlock != nil && miner.chainConfig.CurieBlock.Cmp(work.header.Number) == 0 {
		misc.ApplyCurieHardFork(work.state)

		work.header.NextL1MsgIndex = work.nextL1MsgIndex // we do not include any L1 messages at Curie block
		block, finalizeErr := miner.engine.FinalizeAndAssemble(miner.chain, work.header, work.state, types.Transactions{}, nil, types.Receipts{})
		if finalizeErr != nil {
			return nil, finalizeErr
		}
		return &NewBlockResult{
			Block:    block,
			State:    work.state,
			Receipts: work.receipts,
		}, nil
	}

	if work.gasPool == nil {
		work.gasPool = new(core.GasPool).AddGas(work.header.GasLimit)
	}

	fillTxErr := miner.fillTransactions(work, genParams.transactions, interrupt)
	if fillTxErr != nil && errors.Is(fillTxErr, errBlockInterruptedByTimeout) {
		log.Warn("Block building is interrupted", "allowance", common.PrettyDuration(miner.newBlockTimeout))
	}

	block, finalizeErr := miner.engine.FinalizeAndAssemble(miner.chain, work.header, work.state, work.txs, nil, work.receipts)
	if finalizeErr != nil {
		return nil, finalizeErr
	}
	return &NewBlockResult{
		Block:    block,
		State:    work.state,
		Receipts: work.receipts,
	}, nil
}

// prepareWork constructs the sealing task according to the given parameters,
// either based on the last chain head or specified parent. In this function
// the pending transactions are not filled yet, only the empty task returned.
func (miner *Miner) prepareWork(genParams *generateParams) (*environment, error) {
	miner.confMu.RLock()
	defer miner.confMu.RUnlock()

	parent := miner.chain.CurrentBlock()
	if genParams.parentHash != (common.Hash{}) {
		parent = miner.chain.GetBlockByHash(genParams.parentHash)
	}
	if parent == nil {
		return nil, fmt.Errorf("missing parent")
	}

	timestamp := genParams.timestamp
	if parent.Time() > genParams.timestamp {
		timestamp = parent.Time() + 1
	}
	var coinBase common.Address
	if genParams.coinbase != (common.Address{}) {
		coinBase = genParams.coinbase
	}
	header, err := miner.makeHeader(parent, timestamp, coinBase)
	if err != nil {
		return nil, err
	}

	env, err := miner.makeEnv(parent, header)
	if err != nil {
		log.Error("Failed to create sealing context", "err", err)
		return nil, err
	}
	env.isSimulate = genParams.simulate
	return env, nil
}

func (miner *Miner) makeHeader(parent *types.Block, timestamp uint64, coinBase common.Address) (*types.Header, error) {
	num := parent.Number()
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     num.Add(num, common.Big1),
		GasLimit:   core.CalcGasLimit(parent.GasLimit(), miner.config.GasCeil),
		Extra:      miner.config.ExtraData,
		Time:       timestamp,
		Coinbase:   coinBase,
	}

	// Set baseFee if we are on an EIP-1559 chain
	if miner.chainConfig.IsCurie(header.Number) {
		state, err := miner.chain.StateAt(parent.Root())
		if err != nil {
			log.Error("Failed to create mining context", "err", err)
			return nil, err
		}
		parentL1BaseFee := fees.GetL1BaseFee(state)
		header.BaseFee = misc.CalcBaseFee(miner.chainConfig, parent.Header(), parentL1BaseFee)
	}
	// Run the consensus preparation with the default or customized consensus engine.
	if err := miner.engine.Prepare(miner.chain, header); err != nil {
		log.Error("Failed to prepare header for sealing", "err", err)
		return nil, err
	}
	return header, nil
}

func (miner *Miner) makeEnv(parent *types.Block, header *types.Header) (*environment, error) {
	// Retrieve the parent state to execute on top and start a prefetcher for
	// the miner to speed block sealing up a bit
	stateDB, err := miner.chain.StateAt(parent.Root())
	if err != nil {
		return nil, err
	}

	stateDB.StartPrefetcher("miner")

	env := &environment{
		signer:         types.MakeSigner(miner.chainConfig, header.Number, header.Time),
		state:          stateDB,
		header:         header,
		nextL1MsgIndex: parent.Header().NextL1MsgIndex,
	}

	// Keep track of transactions which return errors so they can be removed
	env.tcount = 0
	env.blockSize = 0
	env.l1TxCount = 0
	return env, nil
}

func (miner *Miner) commitTransaction(env *environment, tx *types.Transaction, coinbase common.Address) error {
	var (
		err     error
		receipt *types.Receipt
		snap    = env.state.Snapshot()
		gp      = env.gasPool.Gas()
	)
	common.WithTimer(l2CommitTxApplyTimer, func() {
		receipt, err = core.ApplyTransaction(miner.chainConfig, miner.chain, &coinbase, env.gasPool, env.state, env.header, tx, &env.header.GasUsed, *miner.chain.GetVMConfig())
	})
	if err != nil {
		env.state.RevertToSnapshot(snap)
		env.gasPool.SetGas(gp)
		return err
	}
	env.txs = append(env.txs, tx)
	env.receipts = append(env.receipts, receipt)
	return nil
}

func (miner *Miner) commitTransactions(env *environment, txs *types.TransactionsByPriceAndNonce, coinbase common.Address, interrupt *int32) error {
	defer func(t0 time.Time) {
		l2CommitTxsTimer.Update(time.Since(t0))
	}(time.Now())

	// Short circuit if current is nil
	if env == nil {
		return errors.New("no env found")
	}

	gasLimit := env.header.GasLimit
	if env.gasPool == nil {
		env.gasPool = new(core.GasPool).AddGas(gasLimit)
	}

	var loops int64

loop:
	for {

		loops++
		if interrupt != nil {
			if signal := atomic.LoadInt32(interrupt); signal != commitInterruptNone {
				return signalToErr(signal)
			}
		}
		if env.gasPool.Gas() < params.TxGas {
			log.Trace("Not enough gas for further transactions", "have", env.gasPool, "want", params.TxGas)
			break
		}
		// Retrieve the next transaction and abort if all done
		tx := txs.Peek()
		if tx == nil {
			break
		}

		// If we have collected enough transactions then we're done
		// Originally we only limit l2txs count, but now strictly limit total txs number.
		if !miner.chainConfig.Morph.IsValidTxCount(env.tcount + 1) {
			log.Trace("Transaction count limit reached", "have", env.tcount, "want", miner.chainConfig.Morph.MaxTxPerBlock)
			break
		}
		if tx.IsL1MessageTx() && !env.isSimulate && tx.AsL1MessageTx().QueueIndex != env.nextL1MsgIndex {
			log.Error(
				"Unexpected L1 message queue index in worker",
				"expected", env.nextL1MsgIndex,
				"got", tx.AsL1MessageTx().QueueIndex,
			)
			break
		}
		if !tx.IsL1MessageTx() && !miner.chainConfig.Morph.IsValidBlockSize(env.blockSize+tx.Size()) {
			log.Trace("Block size limit reached", "have", env.blockSize, "want", miner.chainConfig.Morph.MaxTxPayloadBytesPerBlock, "tx", tx.Size())
			txs.Pop() // skip transactions from this account
			continue
		}
		// Error may be ignored here. The error has already been checked
		// during transaction acceptance in the transaction pool.
		//
		// We use the eip155 signer regardless of the current hf.
		from, _ := types.Sender(env.signer, tx)
		// Check whether the tx is replay protected. If we're not in the EIP155 hf
		// phase, start ignoring the sender until we do.
		if tx.Protected() && !miner.chainConfig.IsEIP155(env.header.Number) {
			log.Trace("Ignoring reply protected transaction", "hash", tx.Hash(), "eip155", miner.chainConfig.EIP155Block)

			txs.Pop()
			continue
		}
		// Start executing the transaction
		env.state.SetTxContext(tx.Hash(), env.tcount)

		err := miner.commitTransaction(env, tx, coinbase)
		if err != nil {
			log.Error("Transaction inclusion failed", "hash", tx.Hash(), "err", err)
		}
		switch {
		case errors.Is(err, core.ErrGasLimitReached) && tx.IsL1MessageTx():
			// If this block already contains some L1 messages,
			// terminate here and try again in the next block.
			if env.l1TxCount == 0 {
				l1TxGasLimitExceededCounter.Inc(1)
				log.Warn("Single L1 message gas limit exceeded for current block", "L1 message gas limit", tx.Gas(), "block gas limit", env.header.GasLimit, "tx hash", tx.Hash())
			}
			break loop

		case errors.Is(err, core.ErrGasLimitReached):
			// Pop the current out-of-gas transaction without shifting in the next from the account
			log.Trace("Gas limit exceeded for current block", "sender", from)
			txs.Pop()

		case errors.Is(err, core.ErrNonceTooLow):
			// New head notification data race between the transaction pool and miner, shift
			log.Trace("Skipping transaction with low nonce", "sender", from, "nonce", tx.Nonce())
			txs.Shift()

		case errors.Is(err, core.ErrNonceTooHigh):
			// Reorg notification data race between the transaction pool and miner, skip account =
			log.Trace("Skipping account with hight nonce", "sender", from, "nonce", tx.Nonce())
			txs.Pop()

		case errors.Is(err, nil):
			// Everything ok, collect the logs and shift in the next transaction from the same account
			env.tcount++
			txs.Shift()

			if tx.IsL1MessageTx() {
				queueIndex := tx.AsL1MessageTx().QueueIndex
				log.Debug("Including L1 message", "queueIndex", queueIndex, "tx", tx.Hash().String())
				env.l1TxCount++
				env.nextL1MsgIndex = queueIndex + 1
			} else {
				// only consider block size limit for L2 transactions
				env.blockSize += tx.Size()
			}

		case errors.Is(err, core.ErrTxTypeNotSupported):
			// Pop the unsupported transaction without shifting in the next from the account
			log.Trace("Skipping unsupported transaction type", "sender", from, "type", tx.Type())
			txs.Pop()

		case errors.Is(err, core.ErrInsufficientFunds) || errors.Is(errors.Unwrap(err), core.ErrInsufficientFunds):
			log.Trace("Skipping tx with insufficient funds", "sender", from, "tx", tx.Hash().String())
			txs.Pop()
			miner.txpool.RemoveTx(tx.Hash(), true)

		default:
			// Strange error, discard the transaction and get the next in line (note, the
			// nonce-too-high clause will prevent us from executing in vain).
			log.Debug("Transaction failed, account skipped", "hash", tx.Hash().String(), "err", err)
			if tx.IsL1MessageTx() {
				log.Warn("L1 messages encounter strange error, stops filling following L1 messages")
				l1TxStrangeErrCounter.Inc(1)
				break loop
			}
			txs.Shift()
		}
	}

	return nil
}

// fillTransactions retrieves the pending transactions from the txpool and fills them
// into the given sealing block. The transaction selection and ordering strategy can
// be customized with the plugin in the future.
func (miner *Miner) fillTransactions(env *environment, l1Transactions types.Transactions, interrupt *int32) error {
	var (
		err error
	)

	defer func(env *environment) {
		if env.header != nil {
			env.header.NextL1MsgIndex = env.nextL1MsgIndex
		}
	}(env)

	if len(l1Transactions) > 0 {
		l1Txs := make(map[common.Address]types.Transactions)
		for _, tx := range l1Transactions {
			sender, _ := types.Sender(env.signer, tx)
			senderTxs, ok := l1Txs[sender]
			if ok {
				senderTxs = append(senderTxs, tx)
				l1Txs[sender] = senderTxs
			} else {
				l1Txs[sender] = types.Transactions{tx}
			}
		}
		txs := types.NewTransactionsByPriceAndNonce(env.signer, l1Txs, env.header.BaseFee)
		log.Info("Committing L1 messages", "count", len(l1Transactions))
		err = miner.commitTransactions(env, txs, env.header.Coinbase, interrupt)
		if err != nil {
			return err
		}
	}

	// Giving up involving L2 transactions if it is simulation for L1Messages
	if env.isSimulate {
		return err
	}

	// Split the pending transactions into locals and remotes
	// Fill the block with all available pending transactions.
	pending := miner.txpool.PendingWithMax(miner.config.GasPrice, env.header.BaseFee, miner.config.MaxAccountsNum)
	localTxs, remoteTxs := make(map[common.Address]types.Transactions), pending
	for _, account := range miner.txpool.Locals() {
		if txs := remoteTxs[account]; len(txs) > 0 {
			delete(remoteTxs, account)
			localTxs[account] = txs
		}
	}

	if len(localTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(env.signer, localTxs, env.header.BaseFee)
		err = miner.commitTransactions(env, txs, env.header.Coinbase, interrupt)
		if err != nil {
			return err
		}
	}
	if len(remoteTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(env.signer, remoteTxs, env.header.BaseFee)
		err = miner.commitTransactions(env, txs, env.header.Coinbase, nil) // always return false
	}

	return err
}

func withTimer(timer metrics.Timer, f func()) {
	if metrics.Enabled {
		timer.Time(f)
	} else {
		f()
	}
}

// signalToErr converts the interruption signal to a concrete error type for return.
// The given signal must be a valid interruption signal.
func signalToErr(signal int32) error {
	switch signal {
	case commitInterruptNewHead:
		return errBlockInterruptedByNewHead
	case commitInterruptResubmit:
		return errBlockInterruptedByRecommit
	case commitInterruptTimeout:
		return errBlockInterruptedByTimeout
	default:
		panic(fmt.Errorf("undefined signal %d", signal))
	}
}
