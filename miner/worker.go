package miner

import (
	"fmt"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/consensus/misc"
	"github.com/scroll-tech/go-ethereum/core/state"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/rollup/tracing"
)

func (miner *Miner) generateWork2(genParams *generateParams) (*NewBlockResult, error) {
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

	pipeline, result, err := miner.startNewPipeline(genParams, header, stateDB)
	if err != nil {
		return nil, err
	}

	miner.handlePipelineResult(pipeline, result)

	return nil, nil

}

func (miner *Miner) startNewPipeline(
	genParams *generateParams,
	header *types.Header,
	stateDB *state.StateDB,
) (*Pipeline, *Result, error) {
	pipeline := NewPipeline(miner.chain, miner.chainDB, miner.txpool, *miner.chain.GetVMConfig(), stateDB, header, miner.GetCCC(), genParams.skipCCC)

	if err := pipeline.Start(genParams.timeout); err != nil {
		log.Error("failed to start pipeline", "err", err)
		return nil, nil, err
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
			return pipeline, result, nil
		}
	}

	return nil, nil, nil

}

func (miner *Miner) handlePipelineResult(pipeline *Pipeline, res *Result) (*NewBlockResult, error) {
	pipeline.Release()

	var (
		header     *types.Header
		state      *state.StateDB
		txs        types.Transactions
		receipts   types.Receipts
		rows       *types.RowConsumption
		skippedTxs []*types.SkippedTransaction
	)

	skippedTxs = pipeline.skippedL1Txs
	if res.FinalBlock == nil {
		header = pipeline.header
		state = pipeline.state
		txs = types.Transactions{}
		receipts = types.Receipts{}

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

	} else {
		header = res.FinalBlock.Header
		state = res.FinalBlock.State
		txs = res.FinalBlock.Txs
		receipts = res.FinalBlock.Receipts
		rows = res.Rows
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

func (miner *Miner) prepareHeader(genParams *generateParams) (*types.Header, error) {
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
	return miner.makeHeader(parent, timestamp, coinBase)
}
