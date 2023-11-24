package miner

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/consensus/misc"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/state"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/params"
)

// getWorkReq represents a request for getting a new sealing work with provided parameters.
type getWorkReq struct {
	interrupt *int32
	params    *generateParams
	result    chan *newBlockResult // non-blocking channel
}

type newBlockResult struct {
	block          *types.Block
	state          *state.StateDB
	receipts       types.Receipts
	rowConsumption *types.RowConsumption
	skippedTxs     []*types.SkippedTransaction
	err            error
}

// generateParams wraps various of settings for generating sealing task.
type generateParams struct {
	timestamp    uint64             // The timstamp for sealing task
	parentHash   common.Hash        // Parent block hash, empty means the latest chain head
	coinbase     common.Address     // The fee recipient address for including transaction
	transactions types.Transactions // L1Message transactions to include at the start of the block
}

// prepareWork constructs the sealing task according to the given parameters,
// either based on the last chain head or specified parent. In this function
// the pending transactions are not filled yet, only the empty task returned.
func (w *worker) prepareWork(genParams *generateParams) (*environment, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	parent := w.chain.CurrentBlock()
	if genParams.parentHash != (common.Hash{}) {
		parent = w.chain.GetBlockByHash(genParams.parentHash)
	}
	if parent == nil {
		return nil, fmt.Errorf("missing parent")
	}

	timestamp := genParams.timestamp
	if parent.Time() >= genParams.timestamp {
		timestamp = parent.Time() + 1
	}
	coinBase := w.coinbase
	if genParams.coinbase != (common.Address{}) {
		coinBase = genParams.coinbase
	}
	header, err := w.makeHeader(parent, timestamp, coinBase)
	if err != nil {
		return nil, err
	}

	env, err := w.makeEnv(parent, header)
	if err != nil {
		log.Error("Failed to create sealing context", "err", err)
		return nil, err
	}
	return env, nil
}

func (w *worker) makeHeader(parent *types.Block, timestamp uint64, coinBase common.Address) (*types.Header, error) {
	num := parent.Number()
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     num.Add(num, common.Big1),
		GasLimit:   core.CalcGasLimit(parent.GasLimit(), w.config.GasCeil),
		Extra:      w.extra,
		Time:       timestamp,
		Coinbase:   coinBase,
	}
	// Set baseFee and GasLimit if we are on an EIP-1559 chain
	if w.chainConfig.IsLondon(header.Number) {
		if w.chainConfig.Scroll.BaseFeeEnabled() {
			header.BaseFee = misc.CalcBaseFee(w.chainConfig, parent.Header())
		} else {
			// When disabling EIP-2718 or EIP-1559, we do not set baseFeePerGas in RPC response.
			// Setting BaseFee as nil here can help outside SDK calculates l2geth's RLP encoding,
			// otherwise the l2geth's BaseFee is not known from the outside.
			header.BaseFee = nil
		}
		if !w.chainConfig.IsLondon(parent.Number()) {
			parentGasLimit := parent.GasLimit() * params.ElasticityMultiplier
			header.GasLimit = core.CalcGasLimit(parentGasLimit, w.config.GasCeil)
		}
	}
	// Run the consensus preparation with the default or customized consensus engine.
	if err := w.engine.Prepare(w.chain, header); err != nil {
		log.Error("Failed to prepare header for sealing", "err", err)
		return nil, err
	}
	return header, nil
}

// fillTransactions retrieves the pending transactions from the txpool and fills them
// into the given sealing block. The transaction selection and ordering strategy can
// be customized with the plugin in the future.
func (w *worker) fillTransactions(env *environment, l1Transactions types.Transactions, interrupt *int32) (error, []*types.SkippedTransaction) {
	var (
		err                    error
		circuitCapacityReached bool
		skippedTxs             []*types.SkippedTransaction
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
		err, circuitCapacityReached, skippedTxs = w.commitTransactions(env, txs, env.header.Coinbase, interrupt)
		if err != nil || circuitCapacityReached {
			return err, skippedTxs
		}
	}

	// Split the pending transactions into locals and remotes
	// Fill the block with all available pending transactions.
	pending := w.eth.TxPool().Pending(true)
	localTxs, remoteTxs := make(map[common.Address]types.Transactions), pending
	for _, account := range w.eth.TxPool().Locals() {
		if txs := remoteTxs[account]; len(txs) > 0 {
			delete(remoteTxs, account)
			localTxs[account] = txs
		}
	}

	if w.prioritizedTx != nil && env.header.Number.Uint64() > w.prioritizedTx.blockNumber {
		w.prioritizedTx = nil
	}
	if w.prioritizedTx != nil && env.header.Number.Uint64() == w.prioritizedTx.blockNumber {
		tx := w.prioritizedTx.tx
		from, _ := types.Sender(w.current.signer, tx) // error already checked before
		txList := map[common.Address]types.Transactions{from: []*types.Transaction{tx}}
		txs := types.NewTransactionsByPriceAndNonce(env.signer, txList, env.header.BaseFee)
		err, circuitCapacityReached, _ = w.commitTransactions(env, txs, w.coinbase, interrupt)
		if err != nil || circuitCapacityReached {
			return err, skippedTxs
		}
	}

	if len(localTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(env.signer, localTxs, env.header.BaseFee)
		err, circuitCapacityReached, _ = w.commitTransactions(env, txs, env.header.Coinbase, interrupt)
		if err != nil || circuitCapacityReached {
			return err, skippedTxs
		}
	}
	if len(remoteTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(env.signer, remoteTxs, env.header.BaseFee)
		err, _, _ = w.commitTransactions(env, txs, env.header.Coinbase, nil) // always return false
	}

	return err, skippedTxs
}

// generateWork generates a sealing block based on the given parameters.
// TODO the produced state data by the transactions will be commit to database, whether the block is confirmed or not.
// TODO this issue will persist until the current zktrie based database optimizes its strategy.
func (w *worker) generateWork(genParams *generateParams, interrupt *int32) (block *types.Block, state *state.StateDB, receipts types.Receipts, rc *types.RowConsumption, skippedTxs []*types.SkippedTransaction, err error) {
	// reset circuitCapacityChecker for a new block
	w.circuitCapacityChecker.Reset()
	work, prepareErr := w.prepareWork(genParams)
	if prepareErr != nil {
		err = prepareErr
		return
	}
	defer work.discard()
	if work.gasPool == nil {
		work.gasPool = new(core.GasPool).AddGas(work.header.GasLimit)
	}

	fillTxErr, skippedTxs := w.fillTransactions(work, genParams.transactions, interrupt)
	if fillTxErr != nil && errors.Is(fillTxErr, errBlockInterruptedByTimeout) {
		log.Warn("Block building is interrupted", "allowance", common.PrettyDuration(w.newBlockTimeout))
	}

	block, finalizeErr := w.engine.FinalizeAndAssemble(w.chain, work.header, work.state, work.txs, nil, work.receipts)
	if finalizeErr != nil {
		err = finalizeErr
		return
	}
	return block, work.state, work.receipts, work.accRows, skippedTxs, nil
}

func (env *environment) discard() {
	if env.state == nil {
		return
	}
	env.state.StopPrefetcher()
}

// getSealingBlockAndState sealing a new block based on parentHash.
func (w *worker) getSealingBlockAndState(parentHash common.Hash, timestamp time.Time, transactions types.Transactions) (*types.Block, *state.StateDB, types.Receipts, *types.RowConsumption, []*types.SkippedTransaction, error) {
	interrupt := new(int32)
	timer := time.AfterFunc(w.newBlockTimeout, func() {
		atomic.StoreInt32(interrupt, commitInterruptTimeout)
	})
	defer timer.Stop()

	req := &getWorkReq{
		interrupt: interrupt,
		params: &generateParams{
			parentHash:   parentHash,
			timestamp:    uint64(timestamp.Unix()),
			transactions: transactions,
		},
		result: make(chan *newBlockResult, 1),
	}
	select {
	case w.getWorkCh <- req:
		result := <-req.result
		return result.block, result.state, result.receipts, result.rowConsumption, result.skippedTxs, result.err
	case <-w.exitCh:
		return nil, nil, nil, nil, nil, errors.New("miner closed")
	}
}

func (w *worker) simulateL1Messages(genParams *generateParams, transactions types.Transactions) ([]*types.Transaction, []*types.SkippedTransaction, error) {
	if transactions.Len() == 0 {
		return nil, nil, nil
	}

	env, err := w.prepareWork(genParams)
	if err != nil {
		return nil, nil, err
	}
	env.isSimulate = true

	l1Txs := make(map[common.Address]types.Transactions)
	for _, tx := range transactions {
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
	_, _, skippedTxs := w.commitTransactions(env, txs, env.header.Coinbase, nil)

	return env.txs, skippedTxs, nil
}
