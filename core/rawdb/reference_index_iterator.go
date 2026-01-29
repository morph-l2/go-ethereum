// Copyright 2024 The go-ethereum Authors
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

package rawdb

import (
	"runtime"
	"sync/atomic"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/prque"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/rlp"
)

// blockReferenceInfo contains reference information for a block
type blockReferenceInfo struct {
	number         uint64
	blockTimestamp uint64
	references     []referenceEntry
}

// referenceEntry contains a single reference entry
type referenceEntry struct {
	reference common.Reference
	txIndex   uint64
	txHash    common.Hash
}

// iterateReferences iterates over all MorphTx transactions with references in the (canon) block
// number(s) given, and yields the reference entries on a channel. If there is a signal
// received from interrupt channel, the iteration will be aborted and result
// channel will be closed.
func iterateReferences(db ethdb.Database, from uint64, to uint64, reverse bool, interrupt chan struct{}) chan *blockReferenceInfo {
	if to == from {
		return nil
	}
	threads := to - from
	if cpus := runtime.NumCPU(); threads > uint64(cpus) {
		threads = uint64(cpus)
	}

	type numberBodyRlp struct {
		number uint64
		rlp    rlp.RawValue
		header *types.Header
	}

	var (
		rlpCh    = make(chan *numberBodyRlp, threads*2)
		resultCh = make(chan *blockReferenceInfo, threads*2)
	)

	// lookup runs in one instance
	lookup := func() {
		n, end := from, to
		if reverse {
			n, end = to-1, from-1
		}
		defer close(rlpCh)
		for n != end {
			data := ReadCanonicalBodyRLP(db, n)
			hash := ReadCanonicalHash(db, n)
			header := ReadHeader(db, hash, n)
			// Feed the block to the aggregator, or abort on interrupt
			select {
			case rlpCh <- &numberBodyRlp{n, data, header}:
			case <-interrupt:
				return
			}
			if reverse {
				n--
			} else {
				n++
			}
		}
	}

	// process runs in parallel
	nThreadsAlive := int32(threads)
	process := func() {
		defer func() {
			// Last processor closes the result channel
			if atomic.AddInt32(&nThreadsAlive, -1) == 0 {
				close(resultCh)
			}
		}()
		for data := range rlpCh {
			if data.header == nil {
				log.Warn("Failed to read header for reference indexing", "block", data.number)
				continue
			}
			var body types.Body
			if err := rlp.DecodeBytes(data.rlp, &body); err != nil {
				log.Warn("Failed to decode block body", "block", data.number, "error", err)
				continue
			}

			var refs []referenceEntry
			for txIndex, tx := range body.Transactions {
				if tx.IsMorphTx() {
					if ref := tx.Reference(); ref != nil {
						refs = append(refs, referenceEntry{
							reference: *ref,
							txIndex:   uint64(txIndex),
							txHash:    tx.Hash(),
						})
					}
				}
			}

		// Always send result for every block (even if no references) to maintain
		// contiguous block numbers for gap-filling logic in indexReferences
		result := &blockReferenceInfo{
			number:         data.number,
			blockTimestamp: data.header.Time,
			references:     refs,
		}
		// Feed the block to the aggregator, or abort on interrupt
		select {
		case resultCh <- result:
		case <-interrupt:
			return
		}
		}
	}

	go lookup() // start the sequential db accessor
	for i := 0; i < int(threads); i++ {
		go process()
	}
	return resultCh
}

// indexReferences creates reference indices of the specified block range.
//
// This function iterates canonical chain in reverse order, it has one main advantage:
// We can write reference index tail flag periodically even without the whole indexing
// procedure is finished. So that we can resume indexing procedure next time quickly.
//
// There is a passed channel, the whole procedure will be interrupted if any
// signal received.
func indexReferences(db ethdb.Database, from uint64, to uint64, interrupt chan struct{}, hook func(uint64) bool) {
	// short circuit for invalid range
	if from >= to {
		return
	}
	var (
		resultCh = iterateReferences(db, from, to, true, interrupt)
		batch    = db.NewBatch()
		start    = time.Now()
		logged   = start.Add(-7 * time.Second)
		// Since we iterate in reverse, we expect the first number to come
		// in to be [to-1]. Therefore, setting lastNum to means that the
		// prqueue gap-evaluation will work correctly
		lastNum = to
		queue   = prque.New(nil)
		// for stats reporting
		blocks, refs = 0, 0
	)
	for chanDelivery := range resultCh {
		// Push the delivery into the queue and process contiguous ranges.
		// Since we iterate in reverse, so lower numbers have lower prio, and
		// we can use the number directly as prio marker
		queue.Push(chanDelivery, int64(chanDelivery.number))
		for !queue.Empty() {
			// If the next available item is gapped, return
			if _, priority := queue.Peek(); priority != int64(lastNum-1) {
				break
			}
			// For testing
			if hook != nil && !hook(lastNum-1) {
				break
			}
			// Next block available, pop it off and index it
			delivery := queue.PopItem().(*blockReferenceInfo)
			lastNum = delivery.number
			for _, ref := range delivery.references {
				WriteReferenceIndexEntry(batch, ref.reference, delivery.blockTimestamp, ref.txIndex, ref.txHash)
			}
			blocks++
			refs += len(delivery.references)
			// If enough data was accumulated in memory or we're at the last block, dump to disk
			if batch.ValueSize() > ethdb.IdealBatchSize {
				WriteReferenceIndexTail(batch, lastNum) // Also write the tail here
				if err := batch.Write(); err != nil {
					log.Crit("Failed writing batch to db", "error", err)
					return
				}
				batch.Reset()
			}
			// If we've spent too much time already, notify the user of what we're doing
			if time.Since(logged) > 8*time.Second {
				log.Info("Indexing references", "blocks", blocks, "refs", refs, "tail", lastNum, "total", to-from, "elapsed", common.PrettyDuration(time.Since(start)))
				logged = time.Now()
			}
		}
	}
	// Flush the new indexing tail and the last committed data. It can also happen
	// that the last batch is empty because nothing to index, but the tail has to
	// be flushed anyway.
	WriteReferenceIndexTail(batch, lastNum)
	if err := batch.Write(); err != nil {
		log.Crit("Failed writing batch to db", "error", err)
		return
	}
	select {
	case <-interrupt:
		log.Debug("Reference indexing interrupted", "blocks", blocks, "refs", refs, "tail", lastNum, "elapsed", common.PrettyDuration(time.Since(start)))
	default:
		log.Info("Indexed references", "blocks", blocks, "refs", refs, "tail", lastNum, "elapsed", common.PrettyDuration(time.Since(start)))
	}
}

// IndexReferences creates reference indices of the specified block range.
//
// This function iterates canonical chain in reverse order, it has one main advantage:
// We can write reference index tail flag periodically even without the whole indexing
// procedure is finished. So that we can resume indexing procedure next time quickly.
//
// There is a passed channel, the whole procedure will be interrupted if any
// signal received.
func IndexReferences(db ethdb.Database, from uint64, to uint64, interrupt chan struct{}) {
	indexReferences(db, from, to, interrupt, nil)
}

// unindexReferences removes reference indices of the specified block range.
//
// There is a passed channel, the whole procedure will be interrupted if any
// signal received.
func unindexReferences(db ethdb.Database, from uint64, to uint64, interrupt chan struct{}, hook func(uint64) bool) {
	// short circuit for invalid range
	if from >= to {
		return
	}
	var (
		resultCh = iterateReferences(db, from, to, false, interrupt)
		batch    = db.NewBatch()
		start    = time.Now()
		logged   = start.Add(-7 * time.Second)
		// we expect the first number to come in to be [from]. Therefore, setting
		// nextNum to from means that the prqueue gap-evaluation will work correctly
		nextNum = from
		queue   = prque.New(nil)
		// for stats reporting
		blocks, refs = 0, 0
	)
	// Otherwise spin up the concurrent iterator and unindexer
	for delivery := range resultCh {
		// Push the delivery into the queue and process contiguous ranges.
		queue.Push(delivery, -int64(delivery.number))
		for !queue.Empty() {
			// If the next available item is gapped, return
			if _, priority := queue.Peek(); -priority != int64(nextNum) {
				break
			}
			// For testing
			if hook != nil && !hook(nextNum) {
				break
			}
			delivery := queue.PopItem().(*blockReferenceInfo)
			nextNum = delivery.number + 1
			for _, ref := range delivery.references {
				DeleteReferenceIndexEntry(batch, ref.reference, delivery.blockTimestamp, ref.txIndex, ref.txHash)
			}
			refs += len(delivery.references)
			blocks++

			// If enough data was accumulated in memory or we're at the last block, dump to disk
			// A batch counts the size of deletion as '1', so we need to flush more
			// often than that.
			if blocks%1000 == 0 {
				WriteReferenceIndexTail(batch, nextNum)
				if err := batch.Write(); err != nil {
					log.Crit("Failed writing batch to db", "error", err)
					return
				}
				batch.Reset()
			}
			// If we've spent too much time already, notify the user of what we're doing
			if time.Since(logged) > 8*time.Second {
				log.Info("Unindexing references", "blocks", blocks, "refs", refs, "total", to-from, "elapsed", common.PrettyDuration(time.Since(start)))
				logged = time.Now()
			}
		}
	}
	// Flush the new indexing tail and the last committed data. It can also happen
	// that the last batch is empty because nothing to unindex, but the tail has to
	// be flushed anyway.
	WriteReferenceIndexTail(batch, nextNum)
	if err := batch.Write(); err != nil {
		log.Crit("Failed writing batch to db", "error", err)
		return
	}
	select {
	case <-interrupt:
		log.Debug("Reference unindexing interrupted", "blocks", blocks, "refs", refs, "tail", to, "elapsed", common.PrettyDuration(time.Since(start)))
	default:
		log.Info("Unindexed references", "blocks", blocks, "refs", refs, "tail", to, "elapsed", common.PrettyDuration(time.Since(start)))
	}
}

// UnindexReferences removes reference indices of the specified block range.
//
// There is a passed channel, the whole procedure will be interrupted if any
// signal received.
func UnindexReferences(db ethdb.Database, from uint64, to uint64, interrupt chan struct{}) {
	unindexReferences(db, from, to, interrupt, nil)
}

