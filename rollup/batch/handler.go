package batch

import (
	"math/big"
	"sync"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/log"
)

const newBatchChanelBuffer = 5

type Handler struct {
	db                     ethdb.Database
	bc                     *core.BlockChain
	latestBatchIndex       uint64
	latestBatchIndexHasFee uint64
	newBatchCh             chan *types.RollupBatch

	logger log.Logger
	stopCh chan struct{}
	wg     sync.WaitGroup
}

func NewBatchHandler(db ethdb.Database, bc *core.BlockChain) *Handler {
	var (
		latestBatchIndex       uint64
		latestBatchIndexHasFee uint64
	)
	index := rawdb.ReadLatestBatchIndex(db)
	if index != nil {
		latestBatchIndex = *index
	}
	index = rawdb.ReadLatestBatchIndexHasFee(db)
	if index != nil {
		latestBatchIndexHasFee = *index
	}
	logger := log.New("component", "batchHandler")
	return &Handler{
		db:     db,
		bc:     bc,
		logger: logger,

		latestBatchIndex:       latestBatchIndex,
		latestBatchIndexHasFee: latestBatchIndexHasFee,

		newBatchCh: make(chan *types.RollupBatch, newBatchChanelBuffer),
		stopCh:     make(chan struct{}),
	}
}

func (h *Handler) Start() {
	h.fillMissingFeeCalc()
	go h.CollectL1FeeLoop()
}

func (h *Handler) Stop() {
	h.logger.Info("batch handler is stopping")
	h.stopCh <- struct{}{}
	h.wg.Wait()
	h.logger.Info("batch handler is stopped")
}

func (h *Handler) ImportBatch(batch *types.RollupBatch, signatures []types.BatchSignature) error {
	h.newBatchCh <- batch

	dbBatch := h.db.NewBatch()
	rawdb.WriteRollupBatch(dbBatch, batch)
	for _, signature := range signatures {
		rawdb.WriteBatchSignature(dbBatch, batch.Hash, signature)
	}
	return dbBatch.Write()
}

func (h *Handler) ImportBatchSig(batchHash common.Hash, signature types.BatchSignature) {
	rawdb.WriteBatchSignature(h.db, batchHash, signature)
}

func (h *Handler) CollectL1FeeLoop() {
	h.logger.Info("start to collect L1Fee for new batches")
	h.wg.Add(1)
	defer func() {
		h.logger.Info("stop collecting L1Fee")
		h.wg.Done()
	}()
	for {
		select {
		case newBatch := <-h.newBatchCh:
			if h.latestBatchIndex < newBatch.Index {
				h.latestBatchIndex = newBatch.Index
			}
			h.calculateL1FeeForBatch(newBatch.Index, newBatch.Chunks)
			rawdb.WriteHeadBatchIndexHasFee(h.db, newBatch.Index)
			h.latestBatchIndexHasFee = newBatch.Index
		case <-h.stopCh:
			close(h.newBatchCh)
			return
		}
	}
}

func (h *Handler) calculateL1FeeForBatch(index uint64, chunks [][]byte) {
	blockContexts, err := BlockContextsFromChunks(chunks)
	if err != nil {
		h.logger.Error("failed to parse block contexts from chunks", "err", err)
		return
	}

	receivedL1Fee := big.NewInt(0)
	for _, bc := range blockContexts {
		hash := rawdb.ReadCanonicalHash(h.db, bc.Number)
		receipts := h.bc.GetReceiptsByHash(hash)
		for _, receipt := range receipts {
			if receipt.L1Fee != nil {
				receivedL1Fee = new(big.Int).Add(receivedL1Fee, receipt.L1Fee)
			}
		}
	}
	rawdb.WriteBatchL1DataFee(h.db, index, receivedL1Fee)
	h.logger.Info("complete L1Fee calculation on batch", "index", index, "l1Fee", receivedL1Fee.String())
}

func (h *Handler) fillMissingFeeCalc() {
	h.logger.Info("start L1Fee calculation on missing batches", "latestBatchIndex", h.latestBatchIndex, "latestBatchIndexHasFee", h.latestBatchIndexHasFee)
	if h.latestBatchIndexHasFee == 0 || h.latestBatchIndexHasFee >= h.latestBatchIndex {
		h.logger.Info("no missing batches found")
		return
	}
	for h.latestBatchIndexHasFee != 0 && h.latestBatchIndexHasFee < h.latestBatchIndex {
		processIndex := h.latestBatchIndexHasFee + 1
		batch := rawdb.ReadRollupBatch(h.db, processIndex)
		if batch == nil {
			// skip this batch if it is removed from db somehow
			h.logger.Error("no batch found", "index", processIndex, "method", "fillMissingFeeCalc")
			h.latestBatchIndexHasFee++
			continue
		}
		h.calculateL1FeeForBatch(processIndex, batch.Chunks)
		h.latestBatchIndexHasFee++
	}
	rawdb.WriteHeadBatchIndexHasFee(h.db, h.latestBatchIndex)
	h.logger.Info("complete L1Fee calculation on missing batches")
}
