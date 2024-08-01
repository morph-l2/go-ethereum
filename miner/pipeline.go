package miner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/core/state"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/core/vm"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/metrics"
	"github.com/scroll-tech/go-ethereum/params"
	"github.com/scroll-tech/go-ethereum/rollup/circuitcapacitychecker"
	"github.com/scroll-tech/go-ethereum/rollup/tracing"
)

type txsOP func(*types.TransactionsByPriceAndNonce)

var (
	txsShift = func(txs *types.TransactionsByPriceAndNonce) {
		txs.Shift()
	}
	txsPop = func(txs *types.TransactionsByPriceAndNonce) {
		txs.Pop()
	}
)

type ErrorWithTrace struct {
	Trace *types.BlockTrace
	err   error
}

func (e *ErrorWithTrace) Error() string {
	return e.err.Error()
}

func (e *ErrorWithTrace) Unwrap() error {
	return e.err
}

var (
	ErrPipelineDone = errors.New("pipeline is done")

	lifetimeTimer = func() metrics.Timer {
		t := metrics.NewCustomTimer(metrics.NewHistogram(metrics.NewExpDecaySample(128, 0.015)), metrics.NewMeter())
		metrics.DefaultRegistry.Register("pipeline/lifetime", t)
		return t
	}()
	applyTimer       = metrics.NewRegisteredTimer("pipeline/apply", nil)
	encodeTimer      = metrics.NewRegisteredTimer("pipeline/encode", nil)
	encodeIdleTimer  = metrics.NewRegisteredTimer("pipeline/encode_idle", nil)
	encodeStallTimer = metrics.NewRegisteredTimer("pipeline/encode_stall", nil)
	cccTimer         = metrics.NewRegisteredTimer("pipeline/ccc", nil)
	cccIdleTimer     = metrics.NewRegisteredTimer("pipeline/ccc_idle", nil)
)

type Pipeline struct {
	start       time.Time
	simulateL1  bool
	chain       *core.BlockChain
	chainDB     ethdb.Database
	txpool      *core.TxPool
	vmConfig    vm.Config
	parent      *types.Block
	startHeader *types.Header
	startState  *state.StateDB
	wg          sync.WaitGroup
	ctx         context.Context
	cancelCtx   context.CancelFunc

	// accumulators
	ccc           *circuitcapacitychecker.CircuitCapacityChecker
	header        *types.Header
	state         *state.StateDB
	blockSize     common.StorageSize
	txs           types.Transactions
	coalescedLogs []*types.Log
	receipts      types.Receipts
	gasPool       *core.GasPool
	skippedL1Txs  []*types.SkippedTransaction

	// com channels
	txnQueue         chan *types.Transaction
	applyStageRespCh <-chan txsOP
	ResultCh         <-chan *Result
}

func NewPipeline(
	chain *core.BlockChain,
	chainDB ethdb.Database,
	txpool *core.TxPool,
	vmConfig vm.Config,
	state *state.StateDB,
	header *types.Header,
	simulateL1 bool,
	ccc *circuitcapacitychecker.CircuitCapacityChecker,
) *Pipeline {
	// make sure we are not sharing a tracer with the caller and not in debug mode
	vmConfig.Tracer = nil
	vmConfig.Debug = false

	state.Copy()
	ctx, cancel := context.WithCancel(context.Background())
	return &Pipeline{
		chain:        chain,
		simulateL1:   simulateL1,
		chainDB:      chainDB,
		txpool:       txpool,
		vmConfig:     vmConfig,
		parent:       chain.GetBlock(header.ParentHash, header.Number.Uint64()-1),
		startHeader:  types.CopyHeader(header),
		startState:   state.Copy(),
		ccc:          ccc,
		state:        state,
		header:       header,
		gasPool:      new(core.GasPool).AddGas(header.GasLimit),
		skippedL1Txs: make([]*types.SkippedTransaction, 0),
		ctx:          ctx,
		cancelCtx:    cancel,
	}
}

func (p *Pipeline) Start(timeout time.Duration) error {
	p.start = time.Now()
	p.txnQueue = make(chan *types.Transaction)
	applyStageRespCh, applyToEncodeCh, err := p.traceAndApplyStage(p.txnQueue)
	if err != nil {
		log.Error("Failed starting traceAndApplyStage", "err", err)
		return err
	}
	p.applyStageRespCh = applyStageRespCh
	encodeToCccCh := p.encodeStage(applyToEncodeCh)
	p.ResultCh = p.cccStage(encodeToCccCh, timeout)

	return nil
}

// Stop forces pipeline to stop its operation and return whatever progress it has so far
func (p *Pipeline) Stop() {
	if p.txnQueue != nil {
		close(p.txnQueue)
		p.txnQueue = nil
	}
}

func (p *Pipeline) TryPushTxns(txs *types.TransactionsByPriceAndNonce) *Result {
	for {
		tx := txs.Peek()
		if tx == nil {
			break
		}

		result, txsOP, err := p.TryPushTxn(tx)
		if result != nil {
			return result
		}

		// pipeline is done
		if err != nil {
			p.Stop()
			return nil
		}

		if txsOP == nil {
			log.Error("txsOP is nil, skip assembling the following transactions", "cause tx", tx.Hash().String())
			return nil
		}
		txsOP(txs)
	}

	return nil
}

func (p *Pipeline) TryPushTxn(tx *types.Transaction) (*Result, txsOP, error) {
	if p.txnQueue == nil {
		return nil, nil, ErrPipelineDone
	}

	select {
	case p.txnQueue <- tx:
	case <-p.ctx.Done():
		return nil, nil, ErrPipelineDone
	case res := <-p.ResultCh:
		return res, nil, nil
	}

	select {
	case txsOP, valid := <-p.applyStageRespCh:
		if !valid {
			return nil, nil, ErrPipelineDone
		}
		return nil, txsOP, nil
	case res := <-p.ResultCh:
		return res, nil, nil
	}
}

// Release releases all resources related to the pipeline
func (p *Pipeline) Release() {
	p.cancelCtx()
	p.wg.Wait()
}

func (p *Pipeline) traceAndApplyStage(txsIn <-chan *types.Transaction) (<-chan txsOP, <-chan *BlockCandidate, error) {
	p.state.StartPrefetcher("miner")
	downstreamCh := make(chan *BlockCandidate, p.downstreamChCapacity())
	resCh := make(chan txsOP)
	p.wg.Add(1)
	go func() {
		defer func() {
			close(downstreamCh)
			close(resCh)
			p.state.StopPrefetcher()
			p.wg.Done()
		}()

		var tx *types.Transaction
		for {
			// If we don't have enough gas for any further transactions then we're done
			if p.gasPool.Gas() < params.TxGas {
				return
			}

			// If we have collected enough transactions then we're done
			if !p.chain.Config().Scroll.IsValidTxCount(p.txs.Len() + 1) {
				return
			}

			select {
			case tx = <-txsIn:
				if tx == nil {
					log.Info("stop traceAndApply stage")
					return
				}
			case <-p.ctx.Done():
				return
			}
			log.Info("received new tx", "hash", tx.Hash().String(), "stage", "traceAndApplyStage")
			if !p.simulateL1 && tx.IsL1MessageTx() && tx.AsL1MessageTx().QueueIndex != p.header.NextL1MsgIndex {
				// Continue, we might still be able to include some L2 messages
				log.Error(
					"Unexpected L1 message queue index in worker",
					"expected", p.header.NextL1MsgIndex,
					"got", tx.AsL1MessageTx().QueueIndex,
				)
				sendCancellable(resCh, txsShift, p.ctx.Done()) // shift the txs, includes more L2 txns if possible
				continue
			}

			if !tx.IsL1MessageTx() && !p.chain.Config().Scroll.IsValidBlockSize(p.blockSize+tx.Size()) {
				// can't fit this txn in this block, silently ignore and continue looking for more txns
				log.Trace("Block size limit reached", "have", p.blockSize, "want", p.chain.Config().Scroll.MaxTxPayloadBytesPerBlock, "tx", tx.Size())

				sendCancellable(resCh, txsPop, p.ctx.Done()) // shift the txs, includes other txns if possible
				continue
			}

			// Start executing the transaction
			applyStart := time.Now()
			p.state.SetTxContext(tx.Hash(), p.txs.Len())
			receipt, trace, err := p.traceAndApply(tx)

			log.Info("finished tx", "hash", tx.Hash().String(), "err", err, "stage", "traceAndApplyStage")
			var txsOP txsOP
			switch {
			case errors.Is(err, core.ErrGasLimitReached) && tx.IsL1MessageTx():
				if p.txs.Len() > 0 {
					txsOP = txsShift
					break
				}
				queueIndex := tx.L1MessageQueueIndex()
				log.Info("Skipping L1 message", "queueIndex", queueIndex, "tx", tx.Hash().String(), "block", p.header.Number, "reason", "gas limit exceeded")
				p.header.NextL1MsgIndex = queueIndex + 1
				p.skippedL1Txs = append(p.skippedL1Txs, &types.SkippedTransaction{
					Tx:     tx,
					Reason: "gas limit exceeded",
				})
				txsOP = txsShift
				l1TxGasLimitExceededCounter.Inc(1)
			case errors.Is(err, core.ErrGasLimitReached):
				log.Trace("Gas limit exceeded for current block")
				txsOP = txsPop
			case errors.Is(err, core.ErrNonceTooLow):
				log.Trace("Skipping transaction with low nonce", "nonce", tx.Nonce())
				txsOP = txsShift
			case errors.Is(err, core.ErrNonceTooHigh):
				log.Trace("Skipping account with hight nonce", "nonce", tx.Nonce())
				txsOP = txsPop
			case errors.Is(err, core.ErrTxTypeNotSupported):
				// Pop the unsupported transaction without shifting in the next from the account
				log.Trace("Skipping unsupported transaction type", "type", tx.Type())
				txsOP = txsPop
			case errors.Is(err, core.ErrInsufficientFunds) || errors.Is(errors.Unwrap(err), core.ErrInsufficientFunds):
				log.Trace("Skipping tx with insufficient funds", "tx", tx.Hash().String())
				txsOP = txsPop
				p.txpool.RemoveTx(tx.Hash(), true)
			case errors.Is(err, nil):
				// Everything ok, collect the logs and shift in the next transaction from the same account
				p.coalescedLogs = append(p.coalescedLogs, receipt.Logs...)
				p.txs = append(p.txs, tx)
				p.receipts = append(p.receipts, receipt)

				if !tx.IsL1MessageTx() {
					// only consider block size limit for L2 transactions
					p.blockSize += tx.Size()
				} else {
					queueIndex := tx.AsL1MessageTx().QueueIndex
					log.Debug("Including L1 message", "queueIndex", queueIndex, "tx", tx.Hash().String())
					p.header.NextL1MsgIndex = queueIndex + 1
				}
				txsOP = txsShift

				if sendCancellable(downstreamCh, &BlockCandidate{
					LastTrace: trace,

					Header:        types.CopyHeader(p.header),
					State:         p.state.Copy(),
					Txs:           p.txs,
					Receipts:      p.receipts,
					CoalescedLogs: p.coalescedLogs,
				}, p.ctx.Done()) {
					// next stage terminated and caller terminated us as well
					return
				}

			default:
				// Strange error, discard the transaction and get the next in line (note, the
				// nonce-too-high clause will prevent us from executing in vain).
				log.Debug("Transaction failed, account skipped", "hash", tx.Hash().String(), "err", err)
				if tx.IsL1MessageTx() {
					queueIndex := tx.AsL1MessageTx().QueueIndex
					log.Info("Skipping L1 message", "queueIndex", queueIndex, "tx", tx.Hash().String(), "block", p.header.Number, "reason", "strange error", "err", err)
					p.header.NextL1MsgIndex = queueIndex + 1

					p.skippedL1Txs = append(p.skippedL1Txs, &types.SkippedTransaction{
						Tx:     tx,
						Reason: fmt.Sprintf("strange error: %v", err),
						Trace:  trace,
					})
					l1TxStrangeErrCounter.Inc(1)
				}
				txsOP = txsShift
			}

			applyTimer.UpdateSince(applyStart)
			sendCancellable(resCh, txsOP, p.ctx.Done())
		}
	}()

	return resCh, downstreamCh, nil
}

func (p *Pipeline) encodeStage(traces <-chan *BlockCandidate) <-chan *BlockCandidate {
	downstreamCh := make(chan *BlockCandidate, p.downstreamChCapacity())
	p.wg.Add(1)

	go func() {
		defer func() {
			close(downstreamCh)
			p.wg.Done()
		}()
		buffer := new(bytes.Buffer)
		for {
			idleStart := time.Now()
			select {
			case trace := <-traces:
				if trace == nil {
					log.Info("stop encode stage")
					return
				}
				encodeIdleTimer.UpdateSince(idleStart)

				encodeStart := time.Now()
				if p.ccc != nil {
					log.Info("received tx", "hash", trace.LastTrace.Transactions[0].TxHash, "stage", "encode")
					trace.RustTrace = circuitcapacitychecker.MakeRustTrace(trace.LastTrace, buffer)
					if trace.RustTrace == nil {
						log.Error("making rust trace", "txHash", trace.LastTrace.Transactions[0].TxHash)
					}
				}
				encodeTimer.UpdateSince(encodeStart)

				stallStart := time.Now()
				if sendCancellable(downstreamCh, trace, p.ctx.Done()) && trace.RustTrace != nil {
					// failed to send the trace downstream, free it here.
					circuitcapacitychecker.FreeRustTrace(trace.RustTrace)
				}
				encodeStallTimer.UpdateSince(stallStart)
			case <-p.ctx.Done():
				return
			}
		}
	}()
	return downstreamCh
}

func (p *Pipeline) cccStage(candidates <-chan *BlockCandidate, timeout time.Duration) <-chan *Result {
	if p.ccc != nil {
		p.ccc.Reset()
	}
	resultCh := make(chan *Result, 1)
	var lastCandidate *BlockCandidate
	var lastAccRows *types.RowConsumption
	var deadlineReached bool

	p.wg.Add(1)
	go func() {
		deadlineTimer := time.NewTimer(timeout)
		defer func() {
			close(resultCh)
			deadlineTimer.Stop()
			lifetimeTimer.UpdateSince(p.start)
			// consume candidates and free all rust traces
			for candidate := range candidates {
				if candidate == nil {
					break
				}
				if candidate.RustTrace != nil {
					circuitcapacitychecker.FreeRustTrace(candidate.RustTrace)
				}
			}
			p.wg.Done()
		}()

		for {
			idleStart := time.Now()
			select {
			case <-p.ctx.Done():
				return
			case <-deadlineTimer.C:
				cccIdleTimer.UpdateSince(idleStart)
				// note: currently we don't allow empty blocks, but if we ever do; make sure to CCC check it first
				if lastCandidate != nil {
					resultCh <- &Result{
						Rows:       lastAccRows,
						FinalBlock: lastCandidate,
					}
					return
				}
				deadlineReached = true
			case candidate := <-candidates:
				cccIdleTimer.UpdateSince(idleStart)
				cccStart := time.Now()

				// close the block immediately when apply stage is done
				if candidate == nil {
					// if the lastCandidate is nil here, meaning the previous apply stage did not include any transactions, and shut down the txQueue.
					// but it still may skip some l1 txs due to reaching gas limit
					// so we set the lastCandidate as the pipeline accumulated result
					if lastCandidate == nil {
						lastCandidate = &BlockCandidate{
							Header:   p.header,
							State:    p.state,
							Txs:      p.txs,
							Receipts: p.receipts,
						}
					}
					resultCh <- &Result{
						Rows:       lastAccRows,
						FinalBlock: lastCandidate,
					}
					log.Info("stop ccc stage")
					return
				}

				if p.ccc == nil { // ccc is disabled during this pipeline
					lastCandidate = candidate
					if deadlineReached {
						resultCh <- &Result{
							Rows:       lastAccRows,
							FinalBlock: lastCandidate,
						}
						return
					}
					continue
				}

				var (
					accRows  *types.RowConsumption
					err      error
					skipL1Tx bool
				)
				lastTxn := candidate.Txs[candidate.Txs.Len()-1]
				if candidate.RustTrace != nil {
					log.Info("received tx", "hash", lastTxn.Hash().String(), "stage", "ccc")
					accRows, err = p.ccc.ApplyTransactionRustTrace(candidate.RustTrace)
					log.Info("finished tx", "hash", lastTxn.Hash().String(), "error", err, "stage", "ccc")
				} else {
					err = errors.New("no rust trace")
				}
				cccTimer.UpdateSince(cccStart)
				switch {
				case errors.Is(err, nil):
					lastCandidate = candidate
					lastAccRows = accRows
					if deadlineReached {
						resultCh <- &Result{
							Rows:       lastAccRows,
							FinalBlock: lastCandidate,
						}
						return
					}
					// if no error happens, and dealine has not been reached, then continue consuming txs
					continue
				case errors.Is(err, circuitcapacitychecker.ErrBlockRowConsumptionOverflow):
					if candidate.Txs.Len() == 1 { // It is the first tx that causes row consumption overflow
						if lastTxn.IsL1MessageTx() {
							p.skippedL1Txs = append(p.skippedL1Txs, &types.SkippedTransaction{
								Tx:     lastTxn,
								Trace:  candidate.LastTrace,
								Reason: "row consumption overflow",
							})
							skipL1Tx = true
							l1TxRowConsumptionOverflowCounter.Inc(1)
						} else {
							p.txpool.RemoveTx(lastTxn.Hash(), true)
							l2TxRowConsumptionOverflowCounter.Inc(1)
						}
					}

				// the error here could be:
				// 1. circuitcapacitychecker.ErrUnknown
				// 2. no rust trace
				// 3. other strange errors
				default:
					log.Trace("Unknown circuit capacity checker error", "tx", lastTxn.Hash().String())
					log.Info("Skipping message", "tx", lastTxn.Hash().String(), "block", p.header.Number, "error", err)
					if lastTxn.IsL1MessageTx() {
						p.skippedL1Txs = append(p.skippedL1Txs, &types.SkippedTransaction{
							Tx:     lastTxn,
							Trace:  candidate.LastTrace,
							Reason: err.Error(),
						})
						skipL1Tx = true
						l1TxCccUnknownErrCounter.Inc(1)
					} else {
						rawdb.WriteSkippedTransaction(p.chainDB, lastTxn, candidate.LastTrace, err.Error(), p.header.Number.Uint64(), nil)
						p.txpool.RemoveTx(lastTxn.Hash(), true)
						l2TxCccUnknownErrCounter.Inc(1)
					}
				}

				finalBlock := lastCandidate
				// if the lastCandidate is nil here, meaning this tx is the first one passed from apply stage, but it fails
				// build the finalBlock with no transactions
				if finalBlock == nil {
					finalBlock = &BlockCandidate{
						Header:   p.startHeader,
						State:    p.startState,
						Txs:      types.Transactions{},
						Receipts: types.Receipts{},
					}

					// copy the NextL1MsgIndex from accumulated header
					// in order to make sure we do not lose any skipped l1 transactions from apply stage
					finalBlock.Header.NextL1MsgIndex = p.header.NextL1MsgIndex
				}

				// if this is a l1 tx needs to be skipped
				if skipL1Tx {
					tx := lastTxn.AsL1MessageTx()
					finalBlock.Header.NextL1MsgIndex = tx.QueueIndex + 1
				}

				// ccc error, construct final result, close the block
				resultCh <- &Result{
					Rows:       lastAccRows,
					FinalBlock: finalBlock,
				}
				return
			}

		}
	}()
	return resultCh
}

func (p *Pipeline) traceAndApply(tx *types.Transaction) (*types.Receipt, *types.BlockTrace, error) {
	var (
		trace *types.BlockTrace
		err   error
	)

	// do gas limit check up-front and do not run CCC if it fails
	if p.gasPool.Gas() < tx.Gas() {
		return nil, nil, core.ErrGasLimitReached
	}

	if p.ccc != nil {
		// don't commit the state during tracing for circuit capacity checker, otherwise we cannot revert.
		// and even if we don't commit the state, the `refund` value will still be correct, as explained in `CommitTransaction`
		commitStateAfterApply := false
		snap := p.state.Snapshot()

		// 1. we have to check circuit capacity before `core.ApplyTransaction`,
		// because if the tx can be successfully executed but circuit capacity overflows, it will be inconvenient to revert.
		// 2. even if we don't commit to the state during the tracing (which means `clearJournalAndRefund` is not called during the tracing),
		// the `refund` value will still be correct, because:
		// 2.1 when starting handling the first tx, `state.refund` is 0 by default,
		// 2.2 after tracing, the state is either committed in `core.ApplyTransaction`, or reverted, so the `state.refund` can be cleared,
		// 2.3 when starting handling the following txs, `state.refund` comes as 0
		trace, err = tracing.NewTracerWrapper().CreateTraceEnvAndGetBlockTrace(p.chain.Config(), p.chain, p.chain.Engine(), p.chain.Database(),
			p.state, p.parent, types.NewBlockWithHeader(p.header).WithBody([]*types.Transaction{tx}, nil), commitStateAfterApply)

		p.state.RevertToSnapshot(snap)
		if err != nil {
			return nil, nil, err
		}
	}

	// create new snapshot for `core.ApplyTransaction`
	snap := p.state.Snapshot()

	var receipt *types.Receipt
	receipt, err = core.ApplyTransaction(p.chain.Config(), p.chain, nil /* coinbase will default to chainConfig.Scroll.FeeVaultAddress */, p.gasPool,
		p.state, p.header, tx, &p.header.GasUsed, p.vmConfig)
	if err != nil {
		p.state.RevertToSnapshot(snap)
		return nil, trace, err
	}
	return receipt, trace, nil
}

type BlockCandidate struct {
	LastTrace *types.BlockTrace
	RustTrace unsafe.Pointer

	// accumulated state
	Header        *types.Header
	State         *state.StateDB
	Txs           types.Transactions
	Receipts      types.Receipts
	CoalescedLogs []*types.Log
}

type Result struct {
	Rows       *types.RowConsumption
	FinalBlock *BlockCandidate
}

// downstreamChCapacity returns the channel capacity that should be used for downstream channels.
// It aims to minimize stalls caused by different computational costs of different transactions
func (p *Pipeline) downstreamChCapacity() int {
	cap := 1
	if p.chain.Config().Scroll.MaxTxPerBlock != nil {
		cap = *p.chain.Config().Scroll.MaxTxPerBlock
	}
	return cap
}

// sendCancellable tries to send msg to resCh but allows send operation to be cancelled
// by closing cancelCh. Returns true if cancelled.
func sendCancellable[T any, C comparable](resCh chan T, msg T, cancelCh <-chan C) bool {
	var zeroC C

	select {
	case resCh <- msg:
		return false
	case cancelSignal := <-cancelCh:
		if cancelSignal != zeroC {
			panic("shouldn't have happened")
		}
		return true
	}
}
