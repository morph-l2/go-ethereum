// Copyright 2022 The go-ethereum Authors
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

package pathdb

import (
	"bytes"
	"fmt"
	"time"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	dbtypes "github.com/morph-l2/go-ethereum/triedb/types"
)

var _ trienodebuffer = &nodebuffer{}

// nodebuffer is a collection of modified trie nodes to aggregate the disk
// write. The content of the nodebuffer must be checked before diving into
// disk (since it basically is not-yet-written data).
type nodebuffer struct {
	layers uint64        // The number of diff layers aggregated inside
	size   uint64        // The size of aggregated writes
	limit  uint64        // The maximum memory allowance in bytes
	nodes  dbtypes.KvMap // The dirty node set, mapped by owner and path
}

// newNodeBuffer initializes the node buffer with the provided nodes.
func newNodeBuffer(limit int, nodes dbtypes.KvMap, layers uint64) *nodebuffer {
	if nodes == nil {
		nodes = make(dbtypes.KvMap)
	}
	var size uint64
	for _, v := range nodes {
		size += uint64(len(v.K) + len(v.K))
	}
	return &nodebuffer{
		layers: layers,
		nodes:  nodes,
		size:   size,
		limit:  uint64(limit),
	}
}

// node retrieves the trie node with given node info.
func (b *nodebuffer) node(path []byte) ([]byte, error) {
	n, ok := b.nodes.Get(path)
	if !ok {
		return nil, nil
	}

	return n, nil
}

// commit merges the dirty nodes into the nodebuffer. This operation won't take
// the ownership of the nodes map which belongs to the bottom-most diff layer.
// It will just hold the node references from the given map which are safe to
// copy.
func (b *nodebuffer) commit(nodes dbtypes.KvMap) trienodebuffer {
	var (
		delta         int64
		overwrite     int64
		overwriteSize int64
	)

	for _, v := range nodes {
		current, exist := b.nodes.Get(v.K)
		if !exist {
			b.nodes.Put(v.K, v.V)
			delta += int64(len(v.K) + len(v.V))

			continue
		}

		if !bytes.Equal(current, v.V) {
			delta += int64(len(v.V) - len(current))
			overwrite++
			overwriteSize += int64(len(v.V) + len(v.K))
		}

		b.nodes.Put(v.K, v.V)
	}

	b.updateSize(delta)
	b.layers++
	gcNodesMeter.Mark(overwrite)
	gcBytesMeter.Mark(overwriteSize)
	return b
}

// updateSize updates the total cache size by the given delta.
func (b *nodebuffer) updateSize(delta int64) {
	size := int64(b.size) + delta
	if size >= 0 {
		b.size = uint64(size)
		return
	}
	s := b.size
	b.size = 0
	log.Error("Invalid pathdb buffer size", "prev", common.StorageSize(s), "delta", common.StorageSize(delta))
}

// reset cleans up the disk cache.
func (b *nodebuffer) reset() {
	b.layers = 0
	b.size = 0
	b.nodes = make(dbtypes.KvMap)
}

// empty returns an indicator if nodebuffer contains any state transition inside.
func (b *nodebuffer) empty() bool {
	return b.layers == 0
}

// setSize sets the buffer size to the provided number, and invokes a flush
// operation if the current memory usage exceeds the new limit.
func (b *nodebuffer) setSize(size int, db ethdb.KeyValueStore, clean *fastcache.Cache, id uint64) error {
	b.limit = uint64(size)
	return b.flush(db, clean, id, false)
}

// flush persists the in-memory dirty trie node into the disk if the configured
// memory threshold is reached. Note, all data must be written atomically.
func (b *nodebuffer) flush(db ethdb.KeyValueStore, clean *fastcache.Cache, id uint64, force bool) error {
	if b.size <= b.limit && !force {
		return nil
	}
	// Ensure the target state id is aligned with the internal counter.
	head := rawdb.ReadPersistentStateID(db)
	if head+b.layers != id {
		return fmt.Errorf("buffer layers (%d) cannot be applied on top of persisted state id (%d) to reach requested state id (%d)", b.layers, head, id)
	}
	var (
		start = time.Now()
		// Although the calculation of b.size has been as accurate as possible,
		// some omissions were still found during testing and code review, but
		// we are still not sure if it is completely accurate. For better protection,
		// some redundancy is added here.
		batch = db.NewBatchWithSize(int(float64(b.size) * DefaultBatchRedundancyRate))
	)
	nodes := writeNodes(batch, b.nodes, clean)
	rawdb.WritePersistentStateID(batch, id)

	// Flush all mutations in a single batch
	size := batch.ValueSize()
	if err := batch.Write(); err != nil {
		return err
	}
	commitBytesMeter.Mark(int64(size))
	commitNodesMeter.Mark(int64(nodes))
	commitTimeTimer.UpdateSince(start)
	log.Debug("Persisted pathdb nodes", "nodes", len(b.nodes), "bytes", common.StorageSize(size), "elapsed", common.PrettyDuration(time.Since(start)))
	b.reset()
	return nil
}

func (b *nodebuffer) waitAndStopFlushing() {}

// writeNodes writes the trie nodes into the provided database batch.
// Note this function will also inject all the newly written nodes
// into clean cache.
func writeNodes(batch ethdb.Batch, nodes dbtypes.KvMap, clean *fastcache.Cache) (total int) {
	for _, v := range nodes {
		rawdb.WriteTrieNodeByKey(batch, v.K, v.V)

		if clean != nil {
			clean.Set(v.K, v.V)
		}

		total += 1
	}

	return total
}

// getSize return the nodebuffer used size.
func (b *nodebuffer) getSize() (uint64, uint64) {
	return b.size, 0
}

// getAllNodes return all the trie nodes are cached in nodebuffer.
func (b *nodebuffer) getAllNodes() dbtypes.KvMap {
	return b.nodes
}

// getLayers return the size of cached difflayers.
func (b *nodebuffer) getLayers() uint64 {
	return b.layers
}
