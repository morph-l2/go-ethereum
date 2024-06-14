package miner

import (
	"errors"
	"fmt"
	"github.com/scroll-tech/go-ethereum/metrics"
	"sync/atomic"
	"time"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/consensus/misc"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/core/state"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/params"
	"github.com/scroll-tech/go-ethereum/rollup/circuitcapacitychecker"
	"github.com/scroll-tech/go-ethereum/rollup/fees"
	"github.com/scroll-tech/go-ethereum/rollup/tracing"
)

var (
	errBlockInterruptedByNewHead  = errors.New("new head arrived while building block")
	errBlockInterruptedByRecommit = errors.New("recommit interrupt while building block")
	errBlockInterruptedByTimeout  = errors.New("timeout while building block")

	// Metrics for the skipped txs
	l1TxGasLimitExceededCounter       = metrics.NewRegisteredCounter("miner/skipped_txs/l1/gas_limit_exceeded", nil)
	l1TxRowConsumptionOverflowCounter = metrics.NewRegisteredCounter("miner/skipped_txs/l1/row_consumption_overflow", nil)
	l2TxRowConsumptionOverflowCounter = metrics.NewRegisteredCounter("miner/skipped_txs/l2/row_consumption_overflow", nil)
	l1TxCccUnknownErrCounter          = metrics.NewRegisteredCounter("miner/skipped_txs/l1/ccc_unknown_err", nil)
	l2TxCccUnknownErrCounter          = metrics.NewRegisteredCounter("miner/skipped_txs/l2/ccc_unknown_err", nil)
	l1TxStrangeErrCounter             = metrics.NewRegisteredCounter("miner/skipped_txs/l1/strange_err", nil)

	l2CommitTxsTimer                = metrics.NewRegisteredTimer("miner/commit/txs_all", nil)
	l2CommitTxTimer                 = metrics.NewRegisteredTimer("miner/commit/tx_all", nil)
	l2CommitTxFailedTimer           = metrics.NewRegisteredTimer("miner/commit/tx_all_failed", nil)
	l2CommitTxTraceTimer            = metrics.NewRegisteredTimer("miner/commit/tx_trace", nil)
	l2CommitTxTraceStateRevertTimer = metrics.NewRegisteredTimer("miner/commit/tx_trace_state_revert", nil)
	l2CommitTxCCCTimer              = metrics.NewRegisteredTimer("miner/commit/tx_ccc", nil)
	l2CommitTxApplyTimer            = metrics.NewRegisteredTimer("miner/commit/tx_apply", nil)

	l2CommitNewWorkTimer                    = metrics.NewRegisteredTimer("miner/commit/new_work_all", nil)
	l2CommitNewWorkL1CollectTimer           = metrics.NewRegisteredTimer("miner/commit/new_work_collect_l1", nil)
	l2CommitNewWorkPrepareTimer             = metrics.NewRegisteredTimer("miner/commit/new_work_prepare", nil)
	l2CommitNewWorkCommitUncleTimer         = metrics.NewRegisteredTimer("miner/commit/new_work_uncle", nil)
	l2CommitNewWorkTidyPendingTxTimer       = metrics.NewRegisteredTimer("miner/commit/new_work_tidy_pending", nil)
	l2CommitNewWorkCommitL1MsgTimer         = metrics.NewRegisteredTimer("miner/commit/new_work_commit_l1_msg", nil)
	l2CommitNewWorkPrioritizedTxCommitTimer = metrics.NewRegisteredTimer("miner/commit/new_work_prioritized", nil)
	l2CommitNewWorkRemoteLocalCommitTimer   = metrics.NewRegisteredTimer("miner/commit/new_work_remote_local", nil)
	l2CommitNewWorkLocalPriceAndNonceTimer  = metrics.NewRegisteredTimer("miner/commit/new_work_local_price_and_nonce", nil)
	l2CommitNewWorkRemotePriceAndNonceTimer = metrics.NewRegisteredTimer("miner/commit/new_work_remote_price_and_nonce", nil)

	l2CommitTimer      = metrics.NewRegisteredTimer("miner/commit/all", nil)
	l2CommitTraceTimer = metrics.NewRegisteredTimer("miner/commit/trace", nil)
	l2CommitCCCTimer   = metrics.NewRegisteredTimer("miner/commit/ccc", nil)
	l2ResultTimer      = metrics.NewRegisteredTimer("miner/result/all", nil)
)

// prioritizedTransaction represents a single transaction that
// should be processed as the first transaction in the next block.
type prioritizedTransaction struct {
	blockNumber uint64
	tx          *types.Transaction
}

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

	// circuit capacity check related fields
	skipCCC        bool                  // skip CCC
	traceEnv       *tracing.TraceEnv     // env for tracing
	accRows        *types.RowConsumption // accumulated row consumption for a block
	nextL1MsgIndex uint64                // next L1 queue index to be processed
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
	Block          *types.Block
	State          *state.StateDB
	Receipts       types.Receipts
	RowConsumption *types.RowConsumption
	SkippedTxs     []*types.SkippedTransaction
}

// generateParams wraps various of settings for generating sealing task.
type generateParams struct {
	timestamp    uint64             // The timstamp for sealing task
	parentHash   common.Hash        // Parent block hash, empty means the latest chain head
	coinbase     common.Address     // The fee recipient address for including transaction
	transactions types.Transactions // L1Message transactions to include at the start of the block
	skipCCC      bool
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
	// reset circuitCapacityChecker for a new block
	miner.resetCCC(genParams.skipCCC)
	work, prepareErr := miner.prepareWork(genParams)
	if prepareErr != nil {
		return nil, prepareErr
	}

	if work.gasPool == nil {
		work.gasPool = new(core.GasPool).AddGas(work.header.GasLimit)
	}

	fillTxErr, skippedTxs := miner.fillTransactions(work, genParams.transactions, interrupt)
	if fillTxErr != nil && errors.Is(fillTxErr, errBlockInterruptedByTimeout) {
		log.Warn("Block building is interrupted", "allowance", common.PrettyDuration(miner.newBlockTimeout))
	}

	// calculate rows if
	// 1. it is an empty block, and
	// 2. it is not a simulation, and
	// 3. it does not skip CCC
	if work.accRows == nil && !genParams.simulate && !genParams.skipCCC {
		log.Trace(
			"Worker apply ccc for empty block",
			"id", miner.circuitCapacityChecker.ID,
			"number", work.header.Number,
			"hash", work.header.Hash().String(),
		)
		var (
			traces *types.BlockTrace
			err    error
		)
		withTimer(l2CommitTraceTimer, func() {
			traces, err = work.traceEnv.GetBlockTrace(types.NewBlockWithHeader(work.header))
		})
		if err != nil {
			return nil, err
		}
		// truncate ExecutionResults&TxStorageTraces, because we declare their lengths with a dummy tx before;
		// however, we need to clean it up for an empty block
		traces.ExecutionResults = traces.ExecutionResults[:0]
		traces.TxStorageTraces = traces.TxStorageTraces[:0]
		var accRows *types.RowConsumption
		withTimer(l2CommitCCCTimer, func() {
			accRows, err = miner.circuitCapacityChecker.ApplyBlock(traces)
		})
		if err != nil {
			return nil, err
		}
		log.Trace(
			"Worker apply ccc for empty block result",
			"id", miner.circuitCapacityChecker.ID,
			"number", work.header.Number,
			"hash", work.header.Hash().String(),
			"accRows", accRows,
		)
		work.accRows = accRows
	}

	block, finalizeErr := miner.engine.FinalizeAndAssemble(miner.chain, work.header, work.state, work.txs, nil, work.receipts)
	if finalizeErr != nil {
		return nil, finalizeErr
	}
	return &NewBlockResult{
		Block:          block,
		State:          work.state,
		Receipts:       work.receipts,
		RowConsumption: work.accRows,
		SkippedTxs:     skippedTxs,
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
	if parent.Time() >= genParams.timestamp {
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
	env.skipCCC = genParams.skipCCC
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

	// don't commit the state during tracing for circuit capacity checker, otherwise we cannot revert.
	// and even if we don't commit the state, the `refund` value will still be correct, as explained in `CommitTransaction`
	commitStateAfterApply := false
	traceEnv, err := tracing.CreateTraceEnv(miner.chainConfig, miner.chain, miner.engine, miner.chainDB, stateDB, parent,
		// new block with a placeholder tx, for traceEnv's ExecutionResults length & TxStorageTraces length
		types.NewBlockWithHeader(header).WithBody([]*types.Transaction{types.NewTx(&types.LegacyTx{})}, nil),
		commitStateAfterApply)
	if err != nil {
		return nil, err
	}

	stateDB.StartPrefetcher("miner")

	env := &environment{
		signer:         types.MakeSigner(miner.chainConfig, header.Number),
		state:          stateDB,
		header:         header,
		traceEnv:       traceEnv,
		accRows:        nil,
		nextL1MsgIndex: parent.Header().NextL1MsgIndex,
	}

	// Keep track of transactions which return errors so they can be removed
	env.tcount = 0
	env.blockSize = 0
	env.l1TxCount = 0
	return env, nil
}

func (miner *Miner) commitTransaction(env *environment, tx *types.Transaction, coinbase common.Address) ([]*types.Log, *types.BlockTrace, error) {
	var accRows *types.RowConsumption
	var traces *types.BlockTrace
	var err error

	// do not do CCC when it is called from `commitNewWork`
	if !env.skipCCC {
		defer func(t0 time.Time) {
			l2CommitTxTimer.Update(time.Since(t0))
			if err != nil {
				l2CommitTxFailedTimer.Update(time.Since(t0))
			}
		}(time.Now())

		// do gas limit check up-front and do not run CCC if it fails
		if env.gasPool.Gas() < tx.Gas() {
			return nil, nil, core.ErrGasLimitReached
		}

		snap := env.state.Snapshot()

		log.Trace(
			"Worker apply ccc for tx",
			"id", miner.circuitCapacityChecker.ID,
			"txHash", tx.Hash().Hex(),
		)

		// 1. we have to check circuit capacity before `core.ApplyTransaction`,
		// because if the tx can be successfully executed but circuit capacity overflows, it will be inconvenient to revert.
		// 2. even if we don't commit to the state during the tracing (which means `clearJournalAndRefund` is not called during the tracing),
		// the `refund` value will still be correct, because:
		// 2.1 when starting handling the first tx, `state.refund` is 0 by default,
		// 2.2 after tracing, the state is either committed in `core.ApplyTransaction`, or reverted, so the `state.refund` can be cleared,
		// 2.3 when starting handling the following txs, `state.refund` comes as 0
		common.WithTimer(l2CommitTxTraceTimer, func() {
			traces, err = env.traceEnv.GetBlockTrace(
				types.NewBlockWithHeader(env.header).WithBody([]*types.Transaction{tx}, nil),
			)
		})
		common.WithTimer(l2CommitTxTraceStateRevertTimer, func() {
			// `env.traceEnv.State` & `env.state` share a same pointer to the state, so only need to revert `env.state`
			// revert to snapshot for calling `core.ApplyMessage` again, (both `traceEnv.GetBlockTrace` & `core.ApplyTransaction` will call `core.ApplyMessage`)
			env.state.RevertToSnapshot(snap)
		})
		if err != nil {
			return nil, nil, err
		}
		common.WithTimer(l2CommitTxCCCTimer, func() {
			accRows, err = miner.circuitCapacityChecker.ApplyTransaction(traces)
		})
		if err != nil {
			return nil, traces, err
		}
		log.Trace(
			"Worker apply ccc for tx result",
			"id", miner.circuitCapacityChecker.ID,
			"txHash", tx.Hash().Hex(),
			"accRows", accRows,
		)
	}

	// create new snapshot for `core.ApplyTransaction`
	snap := env.state.Snapshot()

	var receipt *types.Receipt
	common.WithTimer(l2CommitTxApplyTimer, func() {
		receipt, err = core.ApplyTransaction(miner.chainConfig, miner.chain, &coinbase, env.gasPool, env.state, env.header, tx, &env.header.GasUsed, *miner.chain.GetVMConfig())
	})
	if err != nil {
		env.state.RevertToSnapshot(snap)

		if accRows != nil {
			// At this point, we have called CCC but the transaction failed in `ApplyTransaction`.
			// If we skip this tx and continue to pack more, the next tx will likely fail with
			// `circuitcapacitychecker.ErrUnknown`. However, at this point we cannot decide whether
			// we should seal the block or skip the tx and continue, so we simply return the error.
			log.Error(
				"GetBlockTrace passed but ApplyTransaction failed, ccc is left in inconsistent state",
				"blockNumber", env.header.Number,
				"txHash", tx.Hash().Hex(),
				"err", err,
			)
		}

		return nil, traces, err
	}
	env.txs = append(env.txs, tx)
	env.receipts = append(env.receipts, receipt)
	env.accRows = accRows

	return receipt.Logs, traces, nil
}

func (miner *Miner) commitTransactions(env *environment, txs *types.TransactionsByPriceAndNonce, coinbase common.Address, interrupt *int32) (error, bool, []*types.SkippedTransaction) {
	defer func(t0 time.Time) {
		l2CommitTxsTimer.Update(time.Since(t0))
	}(time.Now())

	var circuitCapacityReached bool

	// Short circuit if current is nil
	if env == nil {
		return errors.New("no env found"), circuitCapacityReached, nil
	}

	gasLimit := env.header.GasLimit
	if env.gasPool == nil {
		env.gasPool = new(core.GasPool).AddGas(gasLimit)
	}

	var (
		coalescedLogs []*types.Log
		loops         int64
		skippedTxs    = make([]*types.SkippedTransaction, 0)
	)

loop:
	for {

		loops++
		if interrupt != nil {
			if signal := atomic.LoadInt32(interrupt); signal != commitInterruptNone {
				return signalToErr(signal), circuitCapacityReached, skippedTxs
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
		if !miner.chainConfig.Scroll.IsValidTxCount(env.tcount + 1) {
			log.Trace("Transaction count limit reached", "have", env.tcount, "want", miner.chainConfig.Scroll.MaxTxPerBlock)
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
		if !tx.IsL1MessageTx() && !miner.chainConfig.Scroll.IsValidBlockSize(env.blockSize+tx.Size()) {
			log.Trace("Block size limit reached", "have", env.blockSize, "want", miner.chainConfig.Scroll.MaxTxPayloadBytesPerBlock, "tx", tx.Size())
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

		logs, traces, err := miner.commitTransaction(env, tx, coinbase)
		switch {
		case errors.Is(err, core.ErrGasLimitReached) && tx.IsL1MessageTx():
			// If this block already contains some L1 messages,
			// terminate here and try again in the next block.
			if env.l1TxCount > 0 {
				break loop
			}
			// A single L1 message leads to out-of-gas. Skip it.
			queueIndex := tx.AsL1MessageTx().QueueIndex
			log.Info("Skipping L1 message", "queueIndex", queueIndex, "tx", tx.Hash().String(), "block", env.header.Number, "reason", "gas limit exceeded")
			env.nextL1MsgIndex = queueIndex + 1
			txs.Shift()

			var storeTraces *types.BlockTrace
			if miner.config.StoreSkippedTxTraces {
				storeTraces = traces
			}
			skippedTxs = append(skippedTxs, &types.SkippedTransaction{
				Tx:     *tx,
				Reason: "gas limit exceeded",
				Trace:  storeTraces,
			})
			l1TxGasLimitExceededCounter.Inc(1)

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
			coalescedLogs = append(coalescedLogs, logs...)
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

		// Circuit capacity check
		case errors.Is(err, circuitcapacitychecker.ErrBlockRowConsumptionOverflow):
			if env.tcount >= 1 {
				// 1. Circuit capacity limit reached in a block, and it's not the first tx:
				// don't pop or shift, just quit the loop immediately;
				// though it might still be possible to add some "smaller" txs,
				// but it's a trade-off between tracing overhead & block usage rate
				log.Trace("Circuit capacity limit reached in a block", "acc_rows", env.accRows, "tx", tx.Hash().String())
				log.Info("Skipping message", "tx", tx.Hash().String(), "block", env.header.Number, "reason", "accumulated row consumption overflow")

				if !tx.IsL1MessageTx() {
					// Prioritize transaction for the next block.
					// If there are no new L1 messages, this transaction will be the 1st transaction in the next block,
					// at which point we can definitively decide if we should skip it or not.
					log.Debug("Prioritizing transaction for next block", "blockNumber", env.header.Number.Uint64()+1, "tx", tx.Hash().String())
					miner.prioritizedTx = &prioritizedTransaction{
						blockNumber: env.header.Number.Uint64() + 1,
						tx:          tx,
					}
				}

				circuitCapacityReached = true
				break loop
			} else {
				// 2. Circuit capacity limit reached in a block, and it's the first tx: skip the tx
				log.Trace("Circuit capacity limit reached for a single tx", "tx", tx.Hash().String())

				if tx.IsL1MessageTx() {
					// Skip L1 message transaction,
					// shift to the next from the account because we shouldn't skip the entire txs from the same account
					txs.Shift()

					queueIndex := tx.AsL1MessageTx().QueueIndex
					log.Info("Skipping L1 message", "queueIndex", queueIndex, "tx", tx.Hash().String(), "block", env.header.Number, "reason", "row consumption overflow")
					env.nextL1MsgIndex = queueIndex + 1
					l1TxRowConsumptionOverflowCounter.Inc(1)
				} else {
					// Skip L2 transaction and all other transactions from the same sender account
					log.Info("Skipping L2 message", "tx", tx.Hash().String(), "block", env.header.Number, "reason", "first tx row consumption overflow")
					txs.Pop()
					miner.txpool.RemoveTx(tx.Hash(), true)
					l2TxRowConsumptionOverflowCounter.Inc(1)
				}

				// Reset ccc so that we can process other transactions for this block
				miner.resetCCC(env.skipCCC)
				log.Trace("Worker reset ccc", "id", miner.circuitCapacityChecker.ID)
				circuitCapacityReached = false

				var storeTraces *types.BlockTrace
				if miner.config.StoreSkippedTxTraces {
					storeTraces = traces
				}
				skippedTxs = append(skippedTxs, &types.SkippedTransaction{
					Tx:     *tx,
					Reason: "row consumption overflow",
					Trace:  storeTraces,
				})
			}

		case errors.Is(err, circuitcapacitychecker.ErrUnknown) && tx.IsL1MessageTx():
			// Circuit capacity check: unknown circuit capacity checker error for L1MessageTx,
			// shift to the next from the account because we shouldn't skip the entire txs from the same account
			queueIndex := tx.AsL1MessageTx().QueueIndex
			log.Trace("Unknown circuit capacity checker error for L1MessageTx", "tx", tx.Hash().String(), "queueIndex", queueIndex)
			log.Info("Skipping L1 message", "queueIndex", queueIndex, "tx", tx.Hash().String(), "block", env.header.Number, "reason", "unknown row consumption error")
			env.nextL1MsgIndex = queueIndex + 1
			// TODO: propagate more info about the error from CCC
			var storeTraces *types.BlockTrace
			if miner.config.StoreSkippedTxTraces {
				storeTraces = traces
			}
			skippedTxs = append(skippedTxs, &types.SkippedTransaction{
				Tx:     *tx,
				Reason: "unknown circuit capacity checker error",
				Trace:  storeTraces,
			})
			l1TxCccUnknownErrCounter.Inc(1)

			// Normally we would do `txs.Shift()` here.
			// However, after `ErrUnknown`, ccc might remain in an
			// inconsistent state, so we cannot pack more transactions.
			circuitCapacityReached = true
			miner.checkCurrentTxNumWithCCC(env.tcount)
			break loop

		case errors.Is(err, circuitcapacitychecker.ErrUnknown) && !tx.IsL1MessageTx():
			// Circuit capacity check: unknown circuit capacity checker error for L2MessageTx, skip the account
			log.Trace("Unknown circuit capacity checker error for L2MessageTx", "tx", tx.Hash().String())
			log.Info("Skipping L2 message", "tx", tx.Hash().String(), "block", env.header.Number, "reason", "unknown row consumption error")
			// TODO: propagate more info about the error from CCC
			if miner.config.StoreSkippedTxTraces {
				rawdb.WriteSkippedTransaction(miner.chainDB, tx, traces, "unknown circuit capacity checker error", env.header.Number.Uint64(), nil)
			} else {
				rawdb.WriteSkippedTransaction(miner.chainDB, tx, nil, "unknown circuit capacity checker error", env.header.Number.Uint64(), nil)
			}
			l2TxCccUnknownErrCounter.Inc(1)

			// Normally we would do `txs.Pop()` here.
			// However, after `ErrUnknown`, ccc might remain in an
			// inconsistent state, so we cannot pack more transactions.
			miner.txpool.RemoveTx(tx.Hash(), true)
			circuitCapacityReached = true
			miner.checkCurrentTxNumWithCCC(env.tcount)
			break loop

		case errors.Is(err, core.ErrInsufficientFunds) || errors.Is(errors.Unwrap(err), core.ErrInsufficientFunds):
			log.Trace("Skipping tx with insufficient funds", "sender", from, "tx", tx.Hash().String())
			txs.Pop()
			miner.txpool.RemoveTx(tx.Hash(), true)

		default:
			// Strange error, discard the transaction and get the next in line (note, the
			// nonce-too-high clause will prevent us from executing in vain).
			log.Debug("Transaction failed, account skipped", "hash", tx.Hash().String(), "err", err)
			if tx.IsL1MessageTx() {
				queueIndex := tx.AsL1MessageTx().QueueIndex
				log.Info("Skipping L1 message", "queueIndex", queueIndex, "tx", tx.Hash().String(), "block", env.header.Number, "reason", "strange error", "err", err)
				env.nextL1MsgIndex = queueIndex + 1

				var storeTraces *types.BlockTrace
				if miner.config.StoreSkippedTxTraces {
					storeTraces = traces
				}
				skippedTxs = append(skippedTxs, &types.SkippedTransaction{
					Tx:     *tx,
					Reason: fmt.Sprintf("strange error: %v", err),
					Trace:  storeTraces,
				})
				l1TxStrangeErrCounter.Inc(1)
			}
			txs.Shift()
		}
	}

	return nil, circuitCapacityReached, skippedTxs
}

// fillTransactions retrieves the pending transactions from the txpool and fills them
// into the given sealing block. The transaction selection and ordering strategy can
// be customized with the plugin in the future.
func (miner *Miner) fillTransactions(env *environment, l1Transactions types.Transactions, interrupt *int32) (error, []*types.SkippedTransaction) {
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
		err, circuitCapacityReached, skippedTxs = miner.commitTransactions(env, txs, env.header.Coinbase, interrupt)
		if err != nil || circuitCapacityReached {
			return err, skippedTxs
		}
	}

	// Giving up involving L2 transactions if it is simulation for L1Messages
	if env.isSimulate {
		return err, skippedTxs
	}

	// Split the pending transactions into locals and remotes
	// Fill the block with all available pending transactions.
	pending := miner.txpool.PendingWithMax(false, miner.config.MaxAccountsNum)
	localTxs, remoteTxs := make(map[common.Address]types.Transactions), pending
	for _, account := range miner.txpool.Locals() {
		if txs := remoteTxs[account]; len(txs) > 0 {
			delete(remoteTxs, account)
			localTxs[account] = txs
		}
	}

	if miner.prioritizedTx != nil && env.header.Number.Uint64() > miner.prioritizedTx.blockNumber {
		miner.prioritizedTx = nil
	}
	if miner.prioritizedTx != nil && env.header.Number.Uint64() == miner.prioritizedTx.blockNumber {
		tx := miner.prioritizedTx.tx
		from, _ := types.Sender(env.signer, tx) // error already checked before
		txList := map[common.Address]types.Transactions{from: []*types.Transaction{tx}}
		txs := types.NewTransactionsByPriceAndNonce(env.signer, txList, env.header.BaseFee)
		err, circuitCapacityReached, _ = miner.commitTransactions(env, txs, env.header.Coinbase, interrupt)
		if err != nil || circuitCapacityReached {
			return err, skippedTxs
		}
	}

	if len(localTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(env.signer, localTxs, env.header.BaseFee)
		err, circuitCapacityReached, _ = miner.commitTransactions(env, txs, env.header.Coinbase, interrupt)
		if err != nil || circuitCapacityReached {
			return err, skippedTxs
		}
	}
	if len(remoteTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(env.signer, remoteTxs, env.header.BaseFee)
		err, _, _ = miner.commitTransactions(env, txs, env.header.Coinbase, nil) // always return false
	}

	return err, skippedTxs
}

func (miner *Miner) resetCCC(skip bool) {
	if !skip {
		miner.circuitCapacityChecker.Reset()
	}
}

func (miner *Miner) checkCurrentTxNumWithCCC(expected int) {
	match, got, err := miner.circuitCapacityChecker.CheckTxNum(expected)
	if err != nil {
		log.Error("failed to CheckTxNum in ccc", "err", err)
		return
	}
	if !match {
		log.Error("tx count in miner is different with CCC", "current env tcount", expected, "got", got)
	}
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
