package miner

import (
	"errors"
	"fmt"
	"time"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/consensus/misc"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/state"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/metrics"
	"github.com/scroll-tech/go-ethereum/rollup/circuitcapacitychecker"
	"github.com/scroll-tech/go-ethereum/rollup/fees"
	"github.com/scroll-tech/go-ethereum/rollup/tracing"
)

var (
	// Metrics for the skipped txs
	l1TxGasLimitExceededCounter       = metrics.NewRegisteredCounter("miner/skipped_txs/l1/gas_limit_exceeded", nil)
	l1TxRowConsumptionOverflowCounter = metrics.NewRegisteredCounter("miner/skipped_txs/l1/row_consumption_overflow", nil)
	l2TxRowConsumptionOverflowCounter = metrics.NewRegisteredCounter("miner/skipped_txs/l2/row_consumption_overflow", nil)
	l1TxCccUnknownErrCounter          = metrics.NewRegisteredCounter("miner/skipped_txs/l1/ccc_unknown_err", nil)
	l2TxCccUnknownErrCounter          = metrics.NewRegisteredCounter("miner/skipped_txs/l2/ccc_unknown_err", nil)
	l1TxStrangeErrCounter             = metrics.NewRegisteredCounter("miner/skipped_txs/l1/strange_err", nil)
)

type getWorkResp struct {
	ret *NewBlockResult
	err error
}

// getWorkReq represents a request for getting a new sealing work with provided parameters.
type getWorkReq struct {
	params *generateParams
	result chan getWorkResp // non-blocking channel
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
			ret, err := miner.generateWork(req.params)
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

func (miner *Miner) makeHeader(parent *types.Block, timestamp uint64, coinBase common.Address) (*types.Header, error) {
	num := parent.Number()
	header := &types.Header{
		ParentHash:     parent.Hash(),
		Number:         num.Add(num, common.Big1),
		GasLimit:       core.CalcGasLimit(parent.GasLimit(), miner.config.GasCeil),
		NextL1MsgIndex: parent.Header().NextL1MsgIndex,
		Extra:          miner.config.ExtraData,
		Time:           timestamp,
		Coinbase:       coinBase,
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

func (miner *Miner) generateWork(genParams *generateParams) (*NewBlockResult, error) {
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

	// Retrieve the parent state to execute on top and start a prefetcher for
	// the miner to speed block sealing up a bit
	stateDB, err := miner.chain.StateAt(parent.Root())
	if err != nil {
		return nil, err
	}

	// Apply special state transition at Curie block
	if miner.chainConfig.CurieBlock != nil && miner.chainConfig.CurieBlock.Cmp(header.Number) == 0 {
		misc.ApplyCurieHardFork(stateDB)

		block, finalizeErr := miner.engine.FinalizeAndAssemble(miner.chain, header, stateDB, types.Transactions{}, nil, types.Receipts{})
		if finalizeErr != nil {
			return nil, finalizeErr
		}
		return &NewBlockResult{
			Block:          block,
			State:          stateDB,
			Receipts:       nil,
			RowConsumption: &types.RowConsumption{},
			SkippedTxs:     nil,
		}, nil
	}

	var ccc *circuitcapacitychecker.CircuitCapacityChecker
	if !genParams.skipCCC {
		ccc = miner.GetCCC()
	}
	pipeline := NewPipeline(miner.chain, miner.chainDB, miner.txpool, *miner.chain.GetVMConfig(), stateDB, header, genParams.simulate, ccc)
	result, err := miner.startPipeline(pipeline, genParams)
	if err != nil {
		return nil, err
	}

	return miner.handlePipelineResult(pipeline, result)

}

func (miner *Miner) startPipeline(
	pipeline *Pipeline,
	genParams *generateParams,
) (*Result, error) {
	var pending map[common.Address]types.Transactions

	// Do not collect txns from txpool, if `simulate` is true
	if !genParams.simulate {
		pending = miner.txpool.PendingWithMax(false, miner.config.MaxAccountsNum)
	}
	// if no txs, return immediately without starting pipeline
	if genParams.transactions.Len() == 0 && len(pending) == 0 {
		log.Info("no txs found, return immediately")
		return nil, nil
	}

	defer pipeline.Release()
	header := pipeline.header
	if err := pipeline.Start(genParams.timeout); err != nil {
		log.Error("failed to start pipeline", "err", err)
		return nil, err
	}

	signer := types.MakeSigner(miner.chainConfig, header.Number)
	var l1Messages *types.TransactionsByPriceAndNonce
	if len(genParams.transactions) > 0 {
		l1Txs := make(map[common.Address]types.Transactions)
		for _, tx := range genParams.transactions {
			sender, _ := types.Sender(signer, tx)
			senderTxs, ok := l1Txs[sender]
			if ok {
				senderTxs = append(senderTxs, tx)
				l1Txs[sender] = senderTxs
			} else {
				l1Txs[sender] = types.Transactions{tx}
			}
		}
		l1Messages = types.NewTransactionsByPriceAndNonce(signer, l1Txs, header.BaseFee)
		if result := pipeline.TryPushTxns(l1Messages); result != nil {
			return result, nil
		}
	}

	// Split the pending transactions into locals and remotes
	// Fill the block with all available pending transactions.
	localTxs, remoteTxs := make(map[common.Address]types.Transactions), pending
	for _, account := range miner.txpool.Locals() {
		if txs := remoteTxs[account]; len(txs) > 0 {
			delete(remoteTxs, account)
			localTxs[account] = txs
		}
	}

	if len(localTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(signer, localTxs, header.BaseFee)
		if result := pipeline.TryPushTxns(txs); result != nil {
			return result, nil
		}
	}
	if len(remoteTxs) > 0 {
		txs := types.NewTransactionsByPriceAndNonce(signer, remoteTxs, header.BaseFee)
		if result := pipeline.TryPushTxns(txs); result != nil {
			return result, nil
		}
	}
	// stop the pipieline if all the txs are consumed
	pipeline.Stop()

	select {
	case res := <-pipeline.ResultCh:
		return res, nil
	case <-pipeline.ctx.Done():
		return nil, ErrPipelineDone
	}

}

func (miner *Miner) handlePipelineResult(pipeline *Pipeline, res *Result) (*NewBlockResult, error) {
	var (
		header     *types.Header
		state      *state.StateDB
		txs        types.Transactions
		receipts   types.Receipts
		rows       *types.RowConsumption
		skippedTxs []*types.SkippedTransaction
	)

	skippedTxs = pipeline.skippedL1Txs
	if res == nil {
		header = pipeline.startHeader
		state = pipeline.startState
		txs = types.Transactions{}
		receipts = types.Receipts{}
	} else if res.FinalBlock == nil {
		return nil, errors.New("pipeline returns with empty final block")
	} else {
		header = res.FinalBlock.Header
		state = res.FinalBlock.State
		txs = res.FinalBlock.Txs
		receipts = res.FinalBlock.Receipts
		rows = res.Rows
	}

	// if ccc is enabled, and it is an empty block
	if pipeline.ccc != nil && (res == nil || res.Rows == nil) {
		miner.circuitCapacityChecker.Reset()
		log.Trace(
			"Worker apply ccc for empty block",
			"id", miner.circuitCapacityChecker.ID,
			"number", header.Number,
			"hash", header.Hash().String(),
		)
		// don't commit the state during tracing for circuit capacity checker, otherwise we cannot revert.
		// and even if we don't commit the state, the `refund` value will still be correct, as explained in `CommitTransaction`
		commitStateAfterApply := false
		traceEnv, err := tracing.CreateTraceEnv(miner.chainConfig, miner.chain, miner.engine, miner.chainDB, state, pipeline.parent,
			// new block with a placeholder tx, for traceEnv's ExecutionResults length & TxStorageTraces length
			types.NewBlockWithHeader(header).WithBody([]*types.Transaction{types.NewTx(&types.LegacyTx{})}, nil),
			commitStateAfterApply)
		if err != nil {
			return nil, err
		}

		traces, err := traceEnv.GetBlockTrace(types.NewBlockWithHeader(header))
		if err != nil {
			return nil, err
		}
		// truncate ExecutionResults&TxStorageTraces, because we declare their lengths with a dummy tx before;
		// however, we need to clean it up for an empty block
		traces.ExecutionResults = traces.ExecutionResults[:0]
		traces.TxStorageTraces = traces.TxStorageTraces[:0]
		rows, err = miner.circuitCapacityChecker.ApplyBlock(traces)
		if err != nil {
			return nil, err
		}
		log.Info(
			"Worker apply ccc for empty block result",
			"id", miner.circuitCapacityChecker.ID,
			"number", header.Number,
			"hash", header.Hash().String(),
			"accRows", rows,
		)
	}

	block, finalizeErr := miner.engine.FinalizeAndAssemble(miner.chain, header, state, txs, nil, receipts)
	if finalizeErr != nil {
		return nil, finalizeErr
	}
	return &NewBlockResult{
		Block:          block,
		State:          state,
		Receipts:       receipts,
		RowConsumption: rows,
		SkippedTxs:     skippedTxs,
	}, nil
}
