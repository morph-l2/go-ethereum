package miner

import (
	"fmt"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/consensus/misc"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/log"
)

func (miner *Miner) startNewPipeline(genParams *generateParams) (*NewBlockResult, error) {
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

		header.NextL1MsgIndex = parent.Header().NextL1MsgIndex // we do not include any L1 messages at Curie block
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

	miner.currentPipeline = NewPipeline(miner.chain, *miner.chain.GetVMConfig(), stateDB, header, miner.GetCCC(), parent.Header().NextL1MsgIndex, genParams.skipCCC)

	if err := miner.currentPipeline.Start(genParams.timeout); err != nil {
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
		if result := miner.currentPipeline.TryPushTxns(l1Messages, w.onTxFailingInPipeline); result != nil {
			miner.handlePipelineResult(result)
			return
		}
	}

}

func (miner *Miner) handlePipelineResult(res *Result) *NewBlockResult {
	miner.currentPipeline.Release()
	miner.currentPipeline = nil

	// Rows being nil without an OverflowingTx means that block didn't go thru CCC,
	if res.Rows == nil && res.OverflowingTx == nil {
		return nil
	}

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
