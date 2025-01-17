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
	"sync"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	dbtypes "github.com/morph-l2/go-ethereum/triedb/types"
)

// trienodebuffer is a collection of modified trie nodes to aggregate the disk
// write. The content of the trienodebuffer must be checked before diving into
// disk (since it basically is not-yet-written data).
type trienodebuffer interface {
	// node retrieves the trie node with given node info.
	node(path []byte) ([]byte, bool)

	// commit merges the dirty nodes into the trienodebuffer. This operation won't take
	// the ownership of the nodes map which belongs to the bottom-most diff layer.
	// It will just hold the node references from the given map which are safe to
	// copy.
	commit(nodes dbtypes.KvMap) trienodebuffer

	// flush persists the in-memory dirty trie node into the disk if the configured
	// memory threshold is reached. Note, all data must be written atomically.
	flush(db ethdb.KeyValueStore, clean *fastcache.Cache, id uint64, force bool) error

	// setSize sets the buffer size to the provided number, and invokes a flush
	// operation if the current memory usage exceeds the new limit.
	setSize(size int, db ethdb.KeyValueStore, clean *fastcache.Cache, id uint64) error

	// reset cleans up the disk cache.
	reset()

	// empty returns an indicator if trienodebuffer contains any state transition inside.
	empty() bool

	// getSize return the trienodebuffer used size.
	getSize() (uint64, uint64)

	// getAllNodes return all the trie nodes are cached in trienodebuffer.
	getAllNodes() dbtypes.KvMap

	// getLayers return the size of cached difflayers.
	getLayers() uint64

	// waitAndStopFlushing will block unit writing the trie nodes of trienodebuffer to disk.
	waitAndStopFlushing()
}

func NewTrieNodeBuffer(sync bool, limit int, nodes dbtypes.KvMap, layers uint64) trienodebuffer {
	if sync {
		log.Info("New sync node buffer", "limit", common.StorageSize(limit), "layers", layers)
		return newNodeBuffer(limit, nodes, layers)
	}
	log.Info("New async node buffer", "limit", common.StorageSize(limit), "layers", layers)
	return newAsyncNodeBuffer(limit, nodes, layers)
}

// diskLayer is a low level persistent layer built on top of a key-value store.
type diskLayer struct {
	root   common.Hash      // Immutable, root hash to which this layer was made for
	id     uint64           // Immutable, corresponding state id
	db     *Database        // Path-based trie database
	cleans *fastcache.Cache // GC friendly memory cache of clean node RLPs
	buffer trienodebuffer   // Node buffer to aggregate writes
	stale  bool             // Signals that the layer became stale (state progressed)
	lock   sync.RWMutex     // Lock used to protect stale flag
}

// newDiskLayer creates a new disk layer based on the passing arguments.
func newDiskLayer(root common.Hash, id uint64, db *Database, cleans *fastcache.Cache, buffer trienodebuffer) *diskLayer {
	// Initialize a clean cache if the memory allowance is not zero
	// or reuse the provided cache if it is not nil (inherited from
	// the original disk layer).
	if cleans == nil && db.config.CleanCacheSize != 0 {
		cleans = fastcache.New(db.config.CleanCacheSize)
	}
	return &diskLayer{
		root:   root,
		id:     id,
		db:     db,
		cleans: cleans,
		buffer: buffer,
	}
}

// rootHash implements the layer interface, returning root hash of corresponding state.
func (dl *diskLayer) rootHash() common.Hash {
	return dl.root
}

// stateID implements the layer interface, returning the state id of disk layer.
func (dl *diskLayer) stateID() uint64 {
	return dl.id
}

// parentLayer implements the layer interface, returning nil as there's no layer
// below the disk.
func (dl *diskLayer) parentLayer() layer {
	return nil
}

// isStale return whether this layer has become stale (was flattened across) or if
// it's still live.
func (dl *diskLayer) isStale() bool {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	return dl.stale
}

// markStale sets the stale flag as true.
func (dl *diskLayer) markStale() {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	if dl.stale {
		panic("triedb disk layer is stale") // we've committed into the same base from two children, boom
	}
	dl.stale = true
}

// Node implements the layer interface, retrieving the trie node with the
// provided node info. No error will be returned if the node is not found.
func (dl *diskLayer) Node(path []byte) ([]byte, error) {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if dl.stale {
		return nil, errSnapshotStale
	}
	// Try to retrieve the trie node from the not-yet-written
	// node buffer first. Note the buffer is lock free since
	// it's impossible to mutate the buffer before tagging the
	// layer as stale.
	n, found := dl.buffer.node(path)
	if found {
		dirtyHitMeter.Mark(1)
		dirtyReadMeter.Mark(int64(len(n)))
		return n, nil
	}
	dirtyMissMeter.Mark(1)

	// Try to retrieve the trie node from the clean memory cache
	key := path
	if dl.cleans != nil {
		if blob := dl.cleans.Get(nil, key); len(blob) > 0 {
			cleanHitMeter.Mark(1)
			cleanReadMeter.Mark(int64(len(blob)))
			return blob, nil
		}
		cleanMissMeter.Mark(1)
	}

	// Try to retrieve the trie node from the disk.
	n, err := rawdb.ReadTrieNodeByKey(dl.db.diskdb, path)
	if err == nil {
		if dl.cleans != nil && len(n) > 0 {
			dl.cleans.Set(key, n)
			cleanWriteMeter.Mark(int64(len(n)))
		}
		return n, nil
	}
	return nil, err
}

// update implements the layer interface, returning a new diff layer on top
// with the given state set.
func (dl *diskLayer) update(root common.Hash, id uint64, block uint64, nodes dbtypes.KvMap) *diffLayer {
	return newDiffLayer(dl, root, id, block, nodes)
}

// commit merges the given bottom-most diff layer into the node buffer
// and returns a newly constructed disk layer. Note the current disk
// layer must be tagged as stale first to prevent re-access.
func (dl *diskLayer) commit(bottom *diffLayer, force bool) (*diskLayer, error) {
	dl.lock.Lock()
	defer dl.lock.Unlock()

	// Construct and store the state history first. If crash happens after storing
	// the state history but without flushing the corresponding states(journal),
	// the stored state history will be truncated from head in the next restart.

	// Mark the diskLayer as stale before applying any mutations on top.
	dl.stale = true

	// Store the root->id lookup afterwards. All stored lookups are identified
	// by the **unique** state root. It's impossible that in the same chain
	// blocks are not adjacent but have the same root.
	if dl.id == 0 {
		rawdb.WriteStateID(dl.db.diskdb, dl.root, 0)
	}
	rawdb.WriteStateID(dl.db.diskdb, bottom.rootHash(), bottom.stateID())

	// Construct a new disk layer by merging the nodes from the provided diff
	// layer, and flush the content in disk layer if there are too many nodes
	// cached. The clean cache is inherited from the original disk layer.

	ndl := newDiskLayer(bottom.root, bottom.stateID(), dl.db, dl.cleans, dl.buffer.commit(bottom.nodes))

	if err := ndl.buffer.flush(ndl.db.diskdb, ndl.cleans, ndl.id, force); err != nil {
		return nil, err
	}

	return ndl, nil
}

// setBufferSize sets the trie node buffer size to the provided value.
func (dl *diskLayer) setBufferSize(size int) error {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if dl.stale {
		return errSnapshotStale
	}
	return dl.buffer.setSize(size, dl.db.diskdb, dl.cleans, dl.id)
}

// size returns the approximate size of cached nodes in the disk layer.
func (dl *diskLayer) size() (common.StorageSize, common.StorageSize) {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	if dl.stale {
		return 0, 0
	}
	dirtyNodes, dirtyimmutableNodes := dl.buffer.getSize()
	return common.StorageSize(dirtyNodes), common.StorageSize(dirtyimmutableNodes)
}

// resetCache releases the memory held by clean cache to prevent memory leak.
func (dl *diskLayer) resetCache() {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	// Stale disk layer loses the ownership of clean cache.
	if dl.stale {
		return
	}
	if dl.cleans != nil {
		dl.cleans.Reset()
	}
}
