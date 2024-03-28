// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package miner

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	mapset "github.com/deckarep/golang-set"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/consensus"
	"github.com/scroll-tech/go-ethereum/consensus/misc"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/core/state"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/event"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/metrics"
	"github.com/scroll-tech/go-ethereum/params"
	"github.com/scroll-tech/go-ethereum/rollup/circuitcapacitychecker"
	"github.com/scroll-tech/go-ethereum/rollup/tracing"
	"github.com/scroll-tech/go-ethereum/trie"
)

const (
	// resultQueueSize is the size of channel listening to sealing result.
	resultQueueSize = 10

	// txChanSize is the size of channel listening to NewTxsEvent.
	// The number is referenced from the size of tx pool.
	txChanSize = 4096

	// chainHeadChanSize is the size of channel listening to ChainHeadEvent.
	chainHeadChanSize = 10

	// chainSideChanSize is the size of channel listening to ChainSideEvent.
	chainSideChanSize = 10

	// resubmitAdjustChanSize is the size of resubmitting interval adjustment channel.
	resubmitAdjustChanSize = 10

	// miningLogAtDepth is the number of confirmations before logging successful mining.
	miningLogAtDepth = 7

	// minRecommitInterval is the minimal time interval to recreate the mining block with
	// any newly arrived transactions.
	minRecommitInterval = 1 * time.Second

	// maxRecommitInterval is the maximum time interval to recreate the mining block with
	// any newly arrived transactions.
	maxRecommitInterval = 15 * time.Second

	// intervalAdjustRatio is the impact a single interval adjustment has on sealing work
	// resubmitting interval.
	intervalAdjustRatio = 0.1

	// intervalAdjustBias is applied during the new resubmit interval calculation in favor of
	// increasing upper limit or decreasing lower limit so that the limit can be reachable.
	intervalAdjustBias = 200 * 1000.0 * 1000.0

	// staleThreshold is the maximum depth of the acceptable stale block.
	staleThreshold = 7
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
	l2CommitTxsTimer                  = metrics.NewRegisteredTimer("miner/commit/txs_all", nil)
	l2CommitTxTimer                   = metrics.NewRegisteredTimer("miner/commit/tx_all", nil)
	l2CommitTxTraceTimer              = metrics.NewRegisteredTimer("miner/commit/tx_trace", nil)
	l2CommitTxCCCTimer                = metrics.NewRegisteredTimer("miner/commit/tx_ccc", nil)
	l2CommitTxApplyTimer              = metrics.NewRegisteredTimer("miner/commit/tx_apply", nil)
	l2CommitTimer                     = metrics.NewRegisteredTimer("miner/commit/all", nil)
	l2CommitTraceTimer                = metrics.NewRegisteredTimer("miner/commit/trace", nil)
	l2CommitCCCTimer                  = metrics.NewRegisteredTimer("miner/commit/ccc", nil)
	l2CommitNewWorkTimer              = metrics.NewRegisteredTimer("miner/commit/new_work_all", nil)
	l2ResultTimer                     = metrics.NewRegisteredTimer("miner/result/all", nil)
)

// environment is the worker's current environment and holds all of the current state information.
type environment struct {
	signer types.Signer

	state     *state.StateDB     // apply state changes here
	ancestors mapset.Set         // ancestor set (used for checking uncle parent validity)
	family    mapset.Set         // family set (used for checking uncle invalidity)
	uncles    mapset.Set         // uncle set
	tcount    int                // tx count in cycle
	blockSize common.StorageSize // approximate size of tx payload in bytes
	l1TxCount int                // l1 msg count in cycle
	gasPool   *core.GasPool      // available gas used to pack transactions

	header   *types.Header
	txs      []*types.Transaction
	receipts []*types.Receipt

	// circuit capacity check related fields
	traceEnv       *tracing.TraceEnv     // env for tracing
	accRows        *types.RowConsumption // accumulated row consumption for a block
	nextL1MsgIndex uint64                // next L1 queue index to be processed
	isSimulate     bool
}

// task contains all information for consensus engine sealing and result submitting.
type task struct {
	receipts  []*types.Receipt
	state     *state.StateDB
	block     *types.Block
	createdAt time.Time
	accRows   *types.RowConsumption // accumulated row consumption in the circuit side
}

const (
	commitInterruptNone int32 = iota
	commitInterruptNewHead
	commitInterruptResubmit
	commitInterruptTimeout
)

// newWorkReq represents a request for new sealing work submitting with relative interrupt notifier.
type newWorkReq struct {
	interrupt *int32
	noempty   bool
	timestamp int64
}

// intervalAdjust represents a resubmitting interval adjustment.
type intervalAdjust struct {
	ratio float64
	inc   bool
}

// prioritizedTransaction represents a single transaction that
// should be processed as the first transaction in the next block.
type prioritizedTransaction struct {
	blockNumber uint64
	tx          *types.Transaction
}

// worker is the main object which takes care of submitting new work to consensus engine
// and gathering the sealing result.
type worker struct {
	config      *Config
	chainConfig *params.ChainConfig
	engine      consensus.Engine
	eth         Backend
	chain       *core.BlockChain

	// Feeds
	pendingLogsFeed event.Feed

	// Subscriptions
	mux          *event.TypeMux
	txsCh        chan core.NewTxsEvent
	txsSub       event.Subscription
	chainHeadCh  chan core.ChainHeadEvent
	chainHeadSub event.Subscription
	chainSideCh  chan core.ChainSideEvent
	chainSideSub event.Subscription

	// Channels
	newWorkCh          chan *newWorkReq
	getWorkCh          chan *getWorkReq
	taskCh             chan *task
	resultCh           chan *types.Block
	startCh            chan struct{}
	exitCh             chan struct{}
	resubmitIntervalCh chan time.Duration
	resubmitAdjustCh   chan *intervalAdjust

	wg sync.WaitGroup

	current      *environment                 // An environment for current running cycle.
	localUncles  map[common.Hash]*types.Block // A set of side blocks generated locally as the possible uncle blocks.
	remoteUncles map[common.Hash]*types.Block // A set of side blocks as the possible uncle blocks.
	unconfirmed  *unconfirmedBlocks           // A set of locally mined blocks pending canonicalness confirmations.

	mu       sync.RWMutex // The lock used to protect the coinbase and extra fields
	coinbase common.Address
	extra    []byte

	pendingMu    sync.RWMutex
	pendingTasks map[common.Hash]*task

	snapshotMu       sync.RWMutex // The lock used to protect the snapshots below
	snapshotBlock    *types.Block
	snapshotReceipts types.Receipts
	snapshotState    *state.StateDB

	// atomic status counters
	running int32 // The indicator whether the consensus engine is running or not.
	newTxs  int32 // New arrival transaction count since last sealing work submitting.

	// noempty is the flag used to control whether the feature of pre-seal empty
	// block is enabled. The default value is false(pre-seal is enabled by default).
	// But in some special scenario the consensus engine will seal blocks instantaneously,
	// in this case this feature will add all empty blocks into canonical chain
	// non-stop and no real transaction will be included.
	noempty uint32

	// newpayloadTimeout is the maximum timeout allowance for creating block.
	// The default value is 3 seconds but node operator can set it to arbitrary
	// large value. A large timeout allowance may cause Geth to fail creating
	// a non-empty block within the specified time and eventually miss the chance to be a proposer
	// in case there are some computation expensive transactions in txpool.
	newBlockTimeout time.Duration

	// External functions
	isLocalBlock func(block *types.Block) bool // Function used to determine whether the specified block is mined by local miner.
	// Make sure the checker here is used by a single block one time, and must be reset for another block.
	circuitCapacityChecker *circuitcapacitychecker.CircuitCapacityChecker
	prioritizedTx          *prioritizedTransaction

	// Test hooks
	newTaskHook  func(*task)                        // Method to call upon receiving a new sealing task.
	skipSealHook func(*task) bool                   // Method to decide whether skipping the sealing.
	fullTaskHook func()                             // Method to call before pushing the full sealing task.
	resubmitHook func(time.Duration, time.Duration) // Method to call upon updating resubmitting interval.
}

func newWorker(config *Config, chainConfig *params.ChainConfig, engine consensus.Engine, eth Backend, mux *event.TypeMux, isLocalBlock func(*types.Block) bool, init bool) *worker {
	worker := &worker{
		config:                 config,
		chainConfig:            chainConfig,
		engine:                 engine,
		eth:                    eth,
		mux:                    mux,
		chain:                  eth.BlockChain(),
		isLocalBlock:           isLocalBlock,
		localUncles:            make(map[common.Hash]*types.Block),
		remoteUncles:           make(map[common.Hash]*types.Block),
		unconfirmed:            newUnconfirmedBlocks(eth.BlockChain(), miningLogAtDepth),
		pendingTasks:           make(map[common.Hash]*task),
		txsCh:                  make(chan core.NewTxsEvent, txChanSize),
		chainHeadCh:            make(chan core.ChainHeadEvent, chainHeadChanSize),
		chainSideCh:            make(chan core.ChainSideEvent, chainSideChanSize),
		newWorkCh:              make(chan *newWorkReq),
		getWorkCh:              make(chan *getWorkReq),
		taskCh:                 make(chan *task),
		resultCh:               make(chan *types.Block, resultQueueSize),
		exitCh:                 make(chan struct{}),
		startCh:                make(chan struct{}, 1),
		resubmitIntervalCh:     make(chan time.Duration),
		resubmitAdjustCh:       make(chan *intervalAdjust, resubmitAdjustChanSize),
		circuitCapacityChecker: circuitcapacitychecker.NewCircuitCapacityChecker(true),
	}
	log.Info("created new worker", "CircuitCapacityChecker ID", worker.circuitCapacityChecker.ID)

	// Subscribe NewTxsEvent for tx pool
	worker.txsSub = eth.TxPool().SubscribeNewTxsEvent(worker.txsCh)

	// Subscribe events for blockchain
	worker.chainHeadSub = eth.BlockChain().SubscribeChainHeadEvent(worker.chainHeadCh)
	worker.chainSideSub = eth.BlockChain().SubscribeChainSideEvent(worker.chainSideCh)

	// Sanitize recommit interval if the user-specified one is too short.
	recommit := worker.config.Recommit
	if recommit < minRecommitInterval {
		log.Warn("Sanitizing miner recommit interval", "provided", recommit, "updated", minRecommitInterval)
		recommit = minRecommitInterval
	}

	// Sanitize the timeout config for creating block.
	newBlockTimeout := worker.config.NewBlockTimeout
	if newBlockTimeout == 0 {
		log.Warn("Sanitizing new block timeout to default", "provided", newBlockTimeout, "updated", 3*time.Second)
		newBlockTimeout = 3 * time.Second
	}
	if newBlockTimeout < time.Millisecond*100 {
		log.Warn("Low block timeout may cause high amount of non-full blocks", "provided", newBlockTimeout, "default", 3*time.Second)
	}
	worker.newBlockTimeout = newBlockTimeout

	worker.wg.Add(4)
	go worker.mainLoop()
	go worker.newWorkLoop(recommit)
	go worker.resultLoop()
	go worker.taskLoop()

	// Submit first work to initialize pending state.
	if init {
		worker.startCh <- struct{}{}
	}
	return worker
}

// getCCC returns a pointer to this worker's CCC instance.
// Only used in tests.
func (w *worker) getCCC() *circuitcapacitychecker.CircuitCapacityChecker {
	return w.circuitCapacityChecker
}

// setEtherbase sets the etherbase used to initialize the block coinbase field.
func (w *worker) setEtherbase(addr common.Address) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.coinbase = addr
}

func (w *worker) setGasCeil(ceil uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.config.GasCeil = ceil
}

// setExtra sets the content used to initialize the block extra field.
func (w *worker) setExtra(extra []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.extra = extra
}

// setRecommitInterval updates the interval for miner sealing work recommitting.
func (w *worker) setRecommitInterval(interval time.Duration) {
	w.resubmitIntervalCh <- interval
}

// disablePreseal disables pre-sealing mining feature
func (w *worker) disablePreseal() {
	atomic.StoreUint32(&w.noempty, 1)
}

// enablePreseal enables pre-sealing mining feature
func (w *worker) enablePreseal() {
	atomic.StoreUint32(&w.noempty, 0)
}

// pending returns the pending state and corresponding block.
func (w *worker) pending() (*types.Block, *state.StateDB) {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	if w.snapshotState == nil {
		return nil, nil
	}
	return w.snapshotBlock, w.snapshotState.Copy()
}

// pendingBlock returns pending block.
func (w *worker) pendingBlock() *types.Block {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	return w.snapshotBlock
}

// pendingBlockAndReceipts returns pending block and corresponding receipts.
func (w *worker) pendingBlockAndReceipts() (*types.Block, types.Receipts) {
	// return a snapshot to avoid contention on currentMu mutex
	w.snapshotMu.RLock()
	defer w.snapshotMu.RUnlock()
	return w.snapshotBlock, w.snapshotReceipts
}

// start sets the running status as 1 and triggers new work submitting.
func (w *worker) start() {
	atomic.StoreInt32(&w.running, 1)
	w.startCh <- struct{}{}
}

// stop sets the running status as 0.
func (w *worker) stop() {
	atomic.StoreInt32(&w.running, 0)
}

// isRunning returns an indicator whether worker is running or not.
func (w *worker) isRunning() bool {
	return atomic.LoadInt32(&w.running) == 1
}

// close terminates all background threads maintained by the worker.
// Note the worker does not support being closed multiple times.
func (w *worker) close() {
	atomic.StoreInt32(&w.running, 0)
	close(w.exitCh)
	w.wg.Wait()
}

// recalcRecommit recalculates the resubmitting interval upon feedback.
func recalcRecommit(minRecommit, prev time.Duration, target float64, inc bool) time.Duration {
	var (
		prevF = float64(prev.Nanoseconds())
		next  float64
	)
	if inc {
		next = prevF*(1-intervalAdjustRatio) + intervalAdjustRatio*(target+intervalAdjustBias)
		max := float64(maxRecommitInterval.Nanoseconds())
		if next > max {
			next = max
		}
	} else {
		next = prevF*(1-intervalAdjustRatio) + intervalAdjustRatio*(target-intervalAdjustBias)
		min := float64(minRecommit.Nanoseconds())
		if next < min {
			next = min
		}
	}
	return time.Duration(int64(next))
}

// newWorkLoop is a standalone goroutine to submit new mining work upon received events.
func (w *worker) newWorkLoop(recommit time.Duration) {
	defer w.wg.Done()
	var (
		interrupt   *int32
		minRecommit = recommit // minimal resubmit interval specified by user.
		timestamp   int64      // timestamp for each round of mining.
	)

	timer := time.NewTimer(0)
	defer timer.Stop()
	<-timer.C // discard the initial tick

	// commit aborts in-flight transaction execution with given signal and resubmits a new one.
	commit := func(noempty bool, s int32) {
		if interrupt != nil {
			atomic.StoreInt32(interrupt, s)
		}
		interrupt = new(int32)
		select {
		case w.newWorkCh <- &newWorkReq{interrupt: interrupt, noempty: noempty, timestamp: timestamp}:
		case <-w.exitCh:
			return
		}
		// we do not need this resubmit loop to acquire higher price transactions here in our cases
		// todo use morph config instead later
		if !w.chainConfig.Scroll.UseZktrie {
			timer.Reset(recommit)
		}
		atomic.StoreInt32(&w.newTxs, 0)
	}
	// clearPending cleans the stale pending tasks.
	clearPending := func(number uint64) {
		w.pendingMu.Lock()
		for h, t := range w.pendingTasks {
			if t.block.NumberU64()+staleThreshold <= number {
				delete(w.pendingTasks, h)
			}
		}
		w.pendingMu.Unlock()
	}

	for {
		select {
		case <-w.startCh:
			clearPending(w.chain.CurrentBlock().NumberU64())
			timestamp = time.Now().Unix()
			commit(false, commitInterruptNewHead)

		case head := <-w.chainHeadCh:
			clearPending(head.Block.NumberU64())
			timestamp = time.Now().Unix()
			commit(true, commitInterruptNewHead)

		case <-timer.C:
			// If mining is running resubmit a new work cycle periodically to pull in
			// higher priced transactions. Disable this overhead for pending blocks.
			if w.isRunning() && (w.chainConfig.Clique == nil || w.chainConfig.Clique.Period > 0) {
				// Short circuit if no new transaction arrives.
				if atomic.LoadInt32(&w.newTxs) == 0 {
					timer.Reset(recommit)
					continue
				}
				commit(true, commitInterruptResubmit)
			}

		case interval := <-w.resubmitIntervalCh:
			// Adjust resubmit interval explicitly by user.
			if interval < minRecommitInterval {
				log.Warn("Sanitizing miner recommit interval", "provided", interval, "updated", minRecommitInterval)
				interval = minRecommitInterval
			}
			log.Info("Miner recommit interval update", "from", minRecommit, "to", interval)
			minRecommit, recommit = interval, interval

			if w.resubmitHook != nil {
				w.resubmitHook(minRecommit, recommit)
			}

		case adjust := <-w.resubmitAdjustCh:
			// Adjust resubmit interval by feedback.
			if adjust.inc {
				before := recommit
				target := float64(recommit.Nanoseconds()) / adjust.ratio
				recommit = recalcRecommit(minRecommit, recommit, target, true)
				log.Trace("Increase miner recommit interval", "from", before, "to", recommit)
			} else {
				before := recommit
				recommit = recalcRecommit(minRecommit, recommit, float64(minRecommit.Nanoseconds()), false)
				log.Trace("Decrease miner recommit interval", "from", before, "to", recommit)
			}

			if w.resubmitHook != nil {
				w.resubmitHook(minRecommit, recommit)
			}

		case <-w.exitCh:
			return
		}
	}
}

// mainLoop is a standalone goroutine to regenerate the sealing task based on the received event.
func (w *worker) mainLoop() {
	defer w.wg.Done()
	defer w.txsSub.Unsubscribe()
	defer w.chainHeadSub.Unsubscribe()
	defer w.chainSideSub.Unsubscribe()
	defer func() {
		if w.current != nil && w.current.state != nil {
			w.current.state.StopPrefetcher()
		}
	}()

	for {
		select {
		case req := <-w.newWorkCh:
			w.commitNewWork(req.interrupt, req.noempty, req.timestamp)
			// new block created.

		case req := <-w.getWorkCh:
			block, stateDB, receipt, rowConsumption, skippedTxs, err := w.generateWork(req.params, req.interrupt)
			req.result <- &newBlockResult{
				err:            err,
				block:          block,
				state:          stateDB,
				receipts:       receipt,
				rowConsumption: rowConsumption,
				skippedTxs:     skippedTxs,
			}

		case ev := <-w.chainSideCh:
			// Short circuit for duplicate side blocks
			if _, exist := w.localUncles[ev.Block.Hash()]; exist {
				continue
			}
			if _, exist := w.remoteUncles[ev.Block.Hash()]; exist {
				continue
			}
			// Add side block to possible uncle block set depending on the author.
			if w.isLocalBlock != nil && w.isLocalBlock(ev.Block) {
				w.localUncles[ev.Block.Hash()] = ev.Block
			} else {
				w.remoteUncles[ev.Block.Hash()] = ev.Block
			}
			// If our mining block contains less than 2 uncle blocks,
			// add the new uncle block if valid and regenerate a mining block.
			if w.isRunning() && w.current != nil && w.current.uncles.Cardinality() < 2 {
				start := time.Now()
				if err := w.commitUncle(w.current, ev.Block.Header()); err == nil {
					var uncles []*types.Header
					w.current.uncles.Each(func(item interface{}) bool {
						hash, ok := item.(common.Hash)
						if !ok {
							return false
						}
						uncle, exist := w.localUncles[hash]
						if !exist {
							uncle, exist = w.remoteUncles[hash]
						}
						if !exist {
							return false
						}
						uncles = append(uncles, uncle.Header())
						return false
					})
					w.commit(uncles, nil, true, start)
				}
			}

		case ev := <-w.txsCh:
			// Apply transactions to the pending state if we're not mining.
			//
			// Note all transactions received may not be continuous with transactions
			// already included in the current mining block. These transactions will
			// be automatically eliminated.
			if !w.isRunning() && w.current != nil {
				// If block is already full, abort
				if gp := w.current.gasPool; gp != nil && gp.Gas() < params.TxGas {
					continue
				}
				w.mu.RLock()
				coinbase := w.coinbase
				w.mu.RUnlock()

				txs := make(map[common.Address]types.Transactions)
				for _, tx := range ev.Txs {
					acc, _ := types.Sender(w.current.signer, tx)
					txs[acc] = append(txs[acc], tx)
				}
				txset := types.NewTransactionsByPriceAndNonce(w.current.signer, txs, w.current.header.BaseFee)
				tcount := w.current.tcount
				// reset circuitCapacityChecker
				w.circuitCapacityChecker.Reset()
				w.commitTransactions(w.current, txset, coinbase, nil)
				// Only update the snapshot if any new transactons were added
				// to the pending block
				if tcount != w.current.tcount {
					w.updateSnapshot()
				}
			} else {
				// Special case, if the consensus engine is 0 period clique(dev mode),
				// submit mining work here since all empty submission will be rejected
				// by clique. Of course the advance sealing(empty submission) is disabled.
				if w.chainConfig.Clique != nil && w.chainConfig.Clique.Period == 0 {
					w.commitNewWork(nil, true, time.Now().Unix())
				}
			}
			atomic.AddInt32(&w.newTxs, int32(len(ev.Txs)))

		// System stopped
		case <-w.exitCh:
			return
		case <-w.txsSub.Err():
			return
		case <-w.chainHeadSub.Err():
			return
		case <-w.chainSideSub.Err():
			return
		}
	}
}

// taskLoop is a standalone goroutine to fetch sealing task from the generator and
// push them to consensus engine.
func (w *worker) taskLoop() {
	defer w.wg.Done()
	var (
		stopCh chan struct{}
		prev   common.Hash
	)

	// interrupt aborts the in-flight sealing task.
	interrupt := func() {
		if stopCh != nil {
			close(stopCh)
			stopCh = nil
		}
	}
	for {
		select {
		case task := <-w.taskCh:
			if w.newTaskHook != nil {
				w.newTaskHook(task)
			}
			// Reject duplicate sealing work due to resubmitting.
			sealHash := w.engine.SealHash(task.block.Header())
			if sealHash == prev {
				continue
			}
			// Interrupt previous sealing operation
			interrupt()
			stopCh, prev = make(chan struct{}), sealHash

			if w.skipSealHook != nil && w.skipSealHook(task) {
				continue
			}
			w.pendingMu.Lock()
			w.pendingTasks[sealHash] = task
			w.pendingMu.Unlock()

			if err := w.engine.Seal(w.chain, task.block, w.resultCh, stopCh); err != nil {
				log.Warn("Block sealing failed", "err", err)
				w.pendingMu.Lock()
				delete(w.pendingTasks, sealHash)
				w.pendingMu.Unlock()
			}
		case <-w.exitCh:
			interrupt()
			return
		}
	}
}

// resultLoop is a standalone goroutine to handle sealing result submitting
// and flush relative data to the database.
func (w *worker) resultLoop() {
	defer w.wg.Done()
	for {
		select {
		case block := <-w.resultCh:
			// Short circuit when receiving empty result.
			if block == nil {
				continue
			}
			// Short circuit when receiving duplicate result caused by resubmitting.
			if w.chain.HasBlock(block.Hash(), block.NumberU64()) {
				continue
			}

			var (
				sealhash = w.engine.SealHash(block.Header())
				hash     = block.Hash()
			)

			w.pendingMu.RLock()
			task, exist := w.pendingTasks[sealhash]
			w.pendingMu.RUnlock()

			if !exist {
				log.Error("Block found but no relative pending task", "number", block.Number(), "sealhash", sealhash, "hash", hash)
				continue
			}

			startTime := time.Now()

			// Different block could share same sealhash, deep copy here to prevent write-write conflict.
			var (
				receipts = make([]*types.Receipt, len(task.receipts))
				logs     []*types.Log
			)
			for i, taskReceipt := range task.receipts {
				receipt := new(types.Receipt)
				receipts[i] = receipt
				*receipt = *taskReceipt

				// add block location fields
				receipt.BlockHash = hash
				receipt.BlockNumber = block.Number()
				receipt.TransactionIndex = uint(i)

				// Update the block hash in all logs since it is now available and not when the
				// receipt/log of individual transactions were created.
				receipt.Logs = make([]*types.Log, len(taskReceipt.Logs))
				for i, taskLog := range taskReceipt.Logs {
					l := new(types.Log)
					receipt.Logs[i] = l
					*l = *taskLog
					l.BlockHash = hash
				}
				logs = append(logs, receipt.Logs...)
			}

			// Store circuit row consumption.
			log.Trace(
				"Worker write block row consumption",
				"id", w.circuitCapacityChecker.ID,
				"number", block.Number(),
				"hash", hash.String(),
				"accRows", task.accRows,
			)
			rawdb.WriteBlockRowConsumption(w.eth.ChainDb(), hash, task.accRows)
			// Commit block and state to database.
			_, err := w.chain.WriteBlockWithState(block, receipts, logs, task.state, true)
			if err != nil {
				l2ResultTimer.Update(time.Since(startTime))
				log.Error("Failed writing block to chain", "err", err)
				continue
			}
			log.Info("Successfully sealed new block", "number", block.Number(), "sealhash", sealhash, "hash", hash,
				"elapsed", common.PrettyDuration(time.Since(task.createdAt)))

			// Broadcast the block and announce chain insertion event
			w.mux.Post(core.NewMinedBlockEvent{Block: block})

			// Insert the block into the set of pending ones to resultLoop for confirmations
			w.unconfirmed.Insert(block.NumberU64(), block.Hash())

			l2ResultTimer.Update(time.Since(startTime))

		case <-w.exitCh:
			return
		}
	}
}

func (w *worker) makeEnv(parent *types.Block, header *types.Header) (*environment, error) {
	// Retrieve the parent state to execute on top and start a prefetcher for
	// the miner to speed block sealing up a bit
	stateDB, err := w.chain.StateAt(parent.Root())
	if err != nil {
		return nil, err
	}

	// don't commit the state during tracing for circuit capacity checker, otherwise we cannot revert.
	// and even if we don't commit the state, the `refund` value will still be correct, as explained in `CommitTransaction`
	commitStateAfterApply := false
	traceEnv, err := tracing.CreateTraceEnv(w.chainConfig, w.chain, w.engine, w.eth.ChainDb(), stateDB, parent,
		// new block with a placeholder tx, for traceEnv's ExecutionResults length & TxStorageTraces length
		types.NewBlockWithHeader(header).WithBody([]*types.Transaction{types.NewTx(&types.LegacyTx{})}, nil),
		commitStateAfterApply)
	if err != nil {
		return nil, err
	}

	stateDB.StartPrefetcher("miner")

	env := &environment{
		signer:         types.MakeSigner(w.chainConfig, header.Number),
		state:          stateDB,
		ancestors:      mapset.NewSet(),
		family:         mapset.NewSet(),
		uncles:         mapset.NewSet(),
		header:         header,
		traceEnv:       traceEnv,
		accRows:        nil,
		nextL1MsgIndex: parent.Header().NextL1MsgIndex,
	}
	// when 08 is processed ancestors contain 07 (quick block)
	for _, ancestor := range w.chain.GetBlocksFromHash(parent.Hash(), 7) {
		for _, uncle := range ancestor.Uncles() {
			env.family.Add(uncle.Hash())
		}
		env.family.Add(ancestor.Hash())
		env.ancestors.Add(ancestor.Hash())
	}
	// Keep track of transactions which return errors so they can be removed
	env.tcount = 0
	env.blockSize = 0
	env.l1TxCount = 0
	return env, nil
}

// makeCurrent creates a new environment for the current cycle.
func (w *worker) makeCurrent(parent *types.Block, header *types.Header) error {
	env, err := w.makeEnv(parent, header)
	if err != nil {
		return err
	}

	// Swap out the old work with the new one, terminating any leftover prefetcher
	// processes in the mean time and starting a new one.
	if w.current != nil {
		w.current.discard()
	}
	w.current = env
	return nil
}

// commitUncle adds the given block to uncle block set, returns error if failed to add.
func (w *worker) commitUncle(env *environment, uncle *types.Header) error {
	hash := uncle.Hash()
	if env.uncles.Contains(hash) {
		return errors.New("uncle not unique")
	}
	if env.header.ParentHash == uncle.ParentHash {
		return errors.New("uncle is sibling")
	}
	if !env.ancestors.Contains(uncle.ParentHash) {
		return errors.New("uncle's parent unknown")
	}
	if env.family.Contains(hash) {
		return errors.New("uncle already included")
	}
	env.uncles.Add(uncle.Hash())
	return nil
}

// updateSnapshot updates pending snapshot block and state.
// Note this function assumes the current variable is thread safe.
func (w *worker) updateSnapshot() {
	w.snapshotMu.Lock()
	defer w.snapshotMu.Unlock()

	var uncles []*types.Header
	w.current.uncles.Each(func(item interface{}) bool {
		hash, ok := item.(common.Hash)
		if !ok {
			return false
		}
		uncle, exist := w.localUncles[hash]
		if !exist {
			uncle, exist = w.remoteUncles[hash]
		}
		if !exist {
			return false
		}
		uncles = append(uncles, uncle.Header())
		return false
	})

	w.snapshotBlock = types.NewBlock(
		w.current.header,
		w.current.txs,
		uncles,
		w.current.receipts,
		trie.NewStackTrie(nil),
	)
	w.snapshotReceipts = copyReceipts(w.current.receipts)
	w.snapshotState = w.current.state.Copy()
}

func (w *worker) commitTransaction(env *environment, tx *types.Transaction, coinbase common.Address) ([]*types.Log, *types.BlockTrace, error) {
	var accRows *types.RowConsumption
	var traces *types.BlockTrace
	var err error

	// do not do CCC checks on follower nodes
	if w.isRunning() {
		defer func(t0 time.Time) {
			l2CommitTxTimer.Update(time.Since(t0))
		}(time.Now())

		// do gas limit check up-front and do not run CCC if it fails
		if env.gasPool.Gas() < tx.Gas() {
			return nil, nil, core.ErrGasLimitReached
		}

		snap := env.state.Snapshot()

		log.Trace(
			"Worker apply ccc for tx",
			"id", w.circuitCapacityChecker.ID,
			"txHash", tx.Hash().Hex(),
		)

		// 1. we have to check circuit capacity before `core.ApplyTransaction`,
		// because if the tx can be successfully executed but circuit capacity overflows, it will be inconvenient to revert.
		// 2. even if we don't commit to the state during the tracing (which means `clearJournalAndRefund` is not called during the tracing),
		// the `refund` value will still be correct, because:
		// 2.1 when starting handling the first tx, `state.refund` is 0 by default,
		// 2.2 after tracing, the state is either committed in `core.ApplyTransaction`, or reverted, so the `state.refund` can be cleared,
		// 2.3 when starting handling the following txs, `state.refund` comes as 0
		withTimer(l2CommitTxTraceTimer, func() {
			traces, err = env.traceEnv.GetBlockTrace(
				types.NewBlockWithHeader(env.header).WithBody([]*types.Transaction{tx}, nil),
			)
		})
		// `w.current.traceEnv.State` & `w.current.state` share a same pointer to the state, so only need to revert `w.current.state`
		// revert to snapshot for calling `core.ApplyMessage` again, (both `traceEnv.GetBlockTrace` & `core.ApplyTransaction` will call `core.ApplyMessage`)
		env.state.RevertToSnapshot(snap)
		if err != nil {
			return nil, nil, err
		}
		withTimer(l2CommitTxCCCTimer, func() {
			accRows, err = w.circuitCapacityChecker.ApplyTransaction(traces)
		})
		if err != nil {
			return nil, traces, err
		}
		log.Trace(
			"Worker apply ccc for tx result",
			"id", w.circuitCapacityChecker.ID,
			"txHash", tx.Hash().Hex(),
			"accRows", accRows,
		)
	}

	// create new snapshot for `core.ApplyTransaction`
	snap := env.state.Snapshot()

	var receipt *types.Receipt
	withTimer(l2CommitTxApplyTimer, func() {
		receipt, err = core.ApplyTransaction(w.chainConfig, w.chain, &coinbase, env.gasPool, env.state, env.header, tx, &env.header.GasUsed, *w.chain.GetVMConfig())
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

func (w *worker) commitTransactions(env *environment, txs *types.TransactionsByPriceAndNonce, coinbase common.Address, interrupt *int32) (error, bool, []*types.SkippedTransaction) {
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
		skippedTxs    = make([]*types.SkippedTransaction, 0)
	)

loop:
	for {
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
		if !w.chainConfig.Scroll.IsValidTxCount(env.tcount + 1) {
			log.Trace("Transaction count limit reached", "have", env.tcount, "want", w.chainConfig.Scroll.MaxTxPerBlock)
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
		if !tx.IsL1MessageTx() && !w.chainConfig.Scroll.IsValidBlockSize(env.blockSize+tx.Size()) {
			log.Trace("Block size limit reached", "have", env.blockSize, "want", w.chainConfig.Scroll.MaxTxPayloadBytesPerBlock, "tx", tx.Size())
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
		if tx.Protected() && !w.chainConfig.IsEIP155(env.header.Number) {
			log.Trace("Ignoring reply protected transaction", "hash", tx.Hash(), "eip155", w.chainConfig.EIP155Block)

			txs.Pop()
			continue
		}
		// Start executing the transaction
		env.state.Prepare(tx.Hash(), env.tcount)

		logs, traces, err := w.commitTransaction(env, tx, coinbase)
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
			if w.config.StoreSkippedTxTraces {
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
					w.prioritizedTx = &prioritizedTransaction{
						blockNumber: env.header.Number.Uint64() + 1,
						tx:          tx,
					}
					atomic.AddInt32(&w.newTxs, int32(1))
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
					w.eth.TxPool().RemoveTx(tx.Hash(), true)
					l2TxRowConsumptionOverflowCounter.Inc(1)
				}

				// Reset ccc so that we can process other transactions for this block
				w.circuitCapacityChecker.Reset()
				log.Trace("Worker reset ccc", "id", w.circuitCapacityChecker.ID)
				circuitCapacityReached = false

				var storeTraces *types.BlockTrace
				if w.config.StoreSkippedTxTraces {
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
			if w.config.StoreSkippedTxTraces {
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
			w.checkCurrentTxNumWithCCC(env.tcount)
			break loop

		case errors.Is(err, circuitcapacitychecker.ErrUnknown) && !tx.IsL1MessageTx():
			// Circuit capacity check: unknown circuit capacity checker error for L2MessageTx, skip the account
			log.Trace("Unknown circuit capacity checker error for L2MessageTx", "tx", tx.Hash().String())
			log.Info("Skipping L2 message", "tx", tx.Hash().String(), "block", env.header.Number, "reason", "unknown row consumption error")
			// TODO: propagate more info about the error from CCC
			if w.config.StoreSkippedTxTraces {
				rawdb.WriteSkippedTransaction(w.eth.ChainDb(), tx, traces, "unknown circuit capacity checker error", env.header.Number.Uint64(), nil)
			} else {
				rawdb.WriteSkippedTransaction(w.eth.ChainDb(), tx, nil, "unknown circuit capacity checker error", env.header.Number.Uint64(), nil)
			}
			l2TxCccUnknownErrCounter.Inc(1)

			// Normally we would do `txs.Pop()` here.
			// However, after `ErrUnknown`, ccc might remain in an
			// inconsistent state, so we cannot pack more transactions.
			w.eth.TxPool().RemoveTx(tx.Hash(), true)
			circuitCapacityReached = true
			w.checkCurrentTxNumWithCCC(env.tcount)
			break loop

		case errors.Is(err, core.ErrInsufficientFunds) || errors.Is(errors.Unwrap(err), core.ErrInsufficientFunds):
			log.Trace("Skipping tx with insufficient funds", "sender", from, "tx", tx.Hash().String())
			txs.Pop()
			w.eth.TxPool().RemoveTx(tx.Hash(), true)

		default:
			// Strange error, discard the transaction and get the next in line (note, the
			// nonce-too-high clause will prevent us from executing in vain).
			log.Debug("Transaction failed, account skipped", "hash", tx.Hash().String(), "err", err)
			if tx.IsL1MessageTx() {
				queueIndex := tx.AsL1MessageTx().QueueIndex
				log.Info("Skipping L1 message", "queueIndex", queueIndex, "tx", tx.Hash().String(), "block", env.header.Number, "reason", "strange error", "err", err)
				env.nextL1MsgIndex = queueIndex + 1

				var storeTraces *types.BlockTrace
				if w.config.StoreSkippedTxTraces {
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

	if !w.isRunning() && len(coalescedLogs) > 0 {
		// We don't push the pendingLogsEvent while we are mining. The reason is that
		// when we are mining, the worker will regenerate a mining block every 3 seconds.
		// In order to avoid pushing the repeated pendingLog, we disable the pending log pushing.

		// make a copy, the state caches the logs and these logs get "upgraded" from pending to mined
		// logs by filling in the block hash when the block was mined by the local miner. This can
		// cause a race condition if a log was "upgraded" before the PendingLogsEvent is processed.
		cpy := make([]*types.Log, len(coalescedLogs))
		for i, l := range coalescedLogs {
			cpy[i] = new(types.Log)
			*cpy[i] = *l
		}
		w.pendingLogsFeed.Send(cpy)
	}
	// Notify resubmit loop to decrease resubmitting interval if current interval is larger
	// than the user-specified one.
	if interrupt != nil {
		w.resubmitAdjustCh <- &intervalAdjust{inc: false}
	}
	return nil, circuitCapacityReached, skippedTxs
}

func (w *worker) checkCurrentTxNumWithCCC(expected int) {
	match, got, err := w.circuitCapacityChecker.CheckTxNum(expected)
	if err != nil {
		log.Error("failed to CheckTxNum in ccc", "err", err)
		return
	}
	if !match {
		log.Error("tx count in miner is different with CCC", "current env tcount", expected, "got", got)
	}
}

// commitNewWork generates several new sealing tasks based on the parent block.
func (w *worker) commitNewWork(interrupt *int32, noempty bool, timestamp int64) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	defer func(t0 time.Time) {
		l2CommitNewWorkTimer.Update(time.Since(t0))
	}(time.Now())

	tstart := time.Now()
	parent := w.chain.CurrentBlock()
	w.circuitCapacityChecker.Reset()
	log.Trace("Worker reset ccc", "id", w.circuitCapacityChecker.ID)

	if parent.Time() >= uint64(timestamp) {
		timestamp = int64(parent.Time() + 1)
	}
	num := parent.Number()
	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     num.Add(num, common.Big1),
		GasLimit:   core.CalcGasLimit(parent.GasLimit(), w.config.GasCeil),
		Extra:      w.extra,
		Time:       uint64(timestamp),
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
	// Only set the coinbase if our consensus engine is running (avoid spurious block rewards)
	if w.isRunning() {
		if w.coinbase == (common.Address{}) {
			log.Error("Refusing to mine without etherbase")
			return
		}
		header.Coinbase = w.coinbase
	}
	if err := w.engine.Prepare(w.chain, header); err != nil {
		log.Error("Failed to prepare header for mining", "err", err)
		return
	}
	// If we are care about TheDAO hard-fork check whether to override the extra-data or not
	if daoBlock := w.chainConfig.DAOForkBlock; daoBlock != nil {
		// Check whether the block is among the fork extra-override range
		limit := new(big.Int).Add(daoBlock, params.DAOForkExtraRange)
		if header.Number.Cmp(daoBlock) >= 0 && header.Number.Cmp(limit) < 0 {
			// Depending whether we support or oppose the fork, override differently
			if w.chainConfig.DAOForkSupport {
				header.Extra = common.CopyBytes(params.DAOForkBlockExtra)
			} else if bytes.Equal(header.Extra, params.DAOForkBlockExtra) {
				header.Extra = []byte{} // If miner opposes, don't let it use the reserved extra-data
			}
		}
	}
	// Could potentially happen if starting to mine in an odd state.
	err := w.makeCurrent(parent, header)
	if err != nil {
		log.Error("Failed to create mining context", "err", err)
		return
	}
	// Create the current work task and check any fork transitions needed
	env := w.current
	if w.chainConfig.DAOForkSupport && w.chainConfig.DAOForkBlock != nil && w.chainConfig.DAOForkBlock.Cmp(header.Number) == 0 {
		misc.ApplyDAOHardFork(env.state)
	}
	// Accumulate the uncles for the current block
	uncles := make([]*types.Header, 0, 2)
	commitUncles := func(blocks map[common.Hash]*types.Block) {
		// Clean up stale uncle blocks first
		for hash, uncle := range blocks {
			if uncle.NumberU64()+staleThreshold <= header.Number.Uint64() {
				delete(blocks, hash)
			}
		}
		for hash, uncle := range blocks {
			if len(uncles) == 2 {
				break
			}
			if err := w.commitUncle(env, uncle.Header()); err != nil {
				log.Trace("Possible uncle rejected", "hash", hash, "reason", err)
			} else {
				log.Debug("Committing new uncle to block", "hash", hash)
				uncles = append(uncles, uncle.Header())
			}
		}
	}
	// Prefer to locally generated uncle
	commitUncles(w.localUncles)
	commitUncles(w.remoteUncles)

	// Create an empty block based on temporary copied state for
	// sealing in advance without waiting block execution finished.
	if !noempty && atomic.LoadUint32(&w.noempty) == 0 {
		w.commit(uncles, nil, false, tstart)
	}

	err, _ = w.fillTransactions(w.current, nil, interrupt)
	switch {
	case err == nil:
		// The entire block is filled, decrease resubmit interval in case
		// of current interval is larger than the user-specified one.
		w.adjustResubmitInterval(&intervalAdjust{inc: false})

	case errors.Is(err, errBlockInterruptedByRecommit):
		// Notify resubmit loop to increase resubmitting interval if the
		// interruption is due to frequent commits.
		gaslimit := w.current.header.GasLimit
		ratio := float64(gaslimit-w.current.gasPool.Gas()) / float64(gaslimit)
		if ratio < 0.1 {
			ratio = 0.1
		}
		w.adjustResubmitInterval(&intervalAdjust{
			ratio: ratio,
			inc:   true,
		})

	case errors.Is(err, errBlockInterruptedByNewHead):
		// If the block building is interrupted by newhead event, discard it
		// totally. Committing the interrupted block introduces unnecessary
		// delay, and possibly causes miner to mine on the previous head,
		// which could result in higher uncle rate.
		w.current.discard()
		return
	}

	w.commit(uncles, w.fullTaskHook, true, tstart)
}

// commit runs any post-transaction state modifications, assembles the final block
// and commits new work if consensus engine is running.
func (w *worker) commit(uncles []*types.Header, interval func(), update bool, start time.Time) error {
	defer func(t0 time.Time) {
		l2CommitTimer.Update(time.Since(t0))
	}(time.Now())

	// set w.current.accRows for empty-but-not-genesis block
	if (w.current.header.Number.Uint64() != 0) &&
		(w.current.accRows == nil || len(*w.current.accRows) == 0) && w.isRunning() {
		log.Trace(
			"Worker apply ccc for empty block",
			"id", w.circuitCapacityChecker.ID,
			"number", w.current.header.Number,
			"hash", w.current.header.Hash().String(),
		)
		var traces *types.BlockTrace
		var err error
		withTimer(l2CommitTraceTimer, func() {
			traces, err = w.current.traceEnv.GetBlockTrace(types.NewBlockWithHeader(w.current.header))
		})
		if err != nil {
			return err
		}
		// truncate ExecutionResults&TxStorageTraces, because we declare their lengths with a dummy tx before;
		// however, we need to clean it up for an empty block
		traces.ExecutionResults = traces.ExecutionResults[:0]
		traces.TxStorageTraces = traces.TxStorageTraces[:0]
		var accRows *types.RowConsumption
		withTimer(l2CommitCCCTimer, func() {
			accRows, err = w.circuitCapacityChecker.ApplyBlock(traces)
		})
		if err != nil {
			return err
		}
		log.Trace(
			"Worker apply ccc for empty block result",
			"id", w.circuitCapacityChecker.ID,
			"number", w.current.header.Number,
			"hash", w.current.header.Hash().String(),
			"accRows", accRows,
		)
		w.current.accRows = accRows
	}

	if w.isRunning() {
		if interval != nil {
			interval()
		}

		// If we use zkTrie, then skip executing FinalizeAndAssemble, as it may commit the state data to database that would cause some state data produced by the transactions that has not been confirmed being flushed to disk.
		if !w.chainConfig.Scroll.UseZktrie {
			// Deep copy receipts here to avoid interaction between different tasks.
			receipts := copyReceipts(w.current.receipts)
			s := w.current.state.Copy()
			block, err := w.engine.FinalizeAndAssemble(w.chain, w.current.header, s, w.current.txs, uncles, receipts)
			if err != nil {
				return err
			}
			// If we're post merge, just ignore
			if !w.isTTDReached(block.Header()) {
				select {
				case w.taskCh <- &task{receipts: receipts, state: s, block: block, createdAt: time.Now(), accRows: w.current.accRows}:
					w.unconfirmed.Shift(block.NumberU64() - 1)
					log.Info("Commit new mining work", "number", block.Number(), "sealhash", w.engine.SealHash(block.Header()),
						"uncles", len(uncles), "txs", w.current.tcount,
						"gas", block.GasUsed(), "fees", totalFees(block, receipts),
						"elapsed", common.PrettyDuration(time.Since(start)))

				case <-w.exitCh:
					log.Info("Worker has exited")
				}
			}
		}
	}
	if update {
		w.updateSnapshot()
	}
	return nil
}

// isTTDReached returns the indicator if the given block has reached the total
// terminal difficulty for The Merge transition.
func (w *worker) isTTDReached(header *types.Header) bool {
	td, ttd := w.chain.GetTd(header.ParentHash, header.Number.Uint64()-1), w.chain.Config().TerminalTotalDifficulty
	return td != nil && ttd != nil && td.Cmp(ttd) >= 0
}

// copyReceipts makes a deep copy of the given receipts.
func copyReceipts(receipts []*types.Receipt) []*types.Receipt {
	result := make([]*types.Receipt, len(receipts))
	for i, l := range receipts {
		cpy := *l
		result[i] = &cpy
	}
	return result
}

// postSideBlock fires a side chain event, only use it for testing.
func (w *worker) postSideBlock(event core.ChainSideEvent) {
	select {
	case w.chainSideCh <- event:
	case <-w.exitCh:
	}
}

// adjustResubmitInterval adjusts the resubmit interval.
func (w *worker) adjustResubmitInterval(message *intervalAdjust) {
	select {
	case w.resubmitAdjustCh <- message:
	default:
		log.Warn("the resubmitAdjustCh is full, discard the message")
	}
}

// totalFees computes total consumed miner fees in ETH. Block transactions and receipts have to have the same order.
func totalFees(block *types.Block, receipts []*types.Receipt) *big.Float {
	feesWei := new(big.Int)
	for i, tx := range block.Transactions() {
		minerFee, _ := tx.EffectiveGasTip(block.BaseFee())
		feesWei.Add(feesWei, new(big.Int).Mul(new(big.Int).SetUint64(receipts[i].GasUsed), minerFee))
	}
	return new(big.Float).Quo(new(big.Float).SetInt(feesWei), new(big.Float).SetInt(big.NewInt(params.Ether)))
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

func withTimer(timer metrics.Timer, f func()) {
	if metrics.Enabled {
		timer.Time(f)
	} else {
		f()
	}
}
