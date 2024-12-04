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
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/log"
	dbtypes "github.com/morph-l2/go-ethereum/triedb/types"
)

type RefTrieNode struct {
	refCount uint32
	blob     []byte
}

type HashNodeCache struct {
	lock  sync.RWMutex
	cache map[[sha256.Size]byte]*RefTrieNode
}

func (h *HashNodeCache) length() int {
	if h == nil {
		return 0
	}
	h.lock.RLock()
	defer h.lock.RUnlock()
	return len(h.cache)
}

func (h *HashNodeCache) set(key, val []byte) {
	if h == nil {
		return
	}
	h.lock.Lock()
	defer h.lock.Unlock()
	hash := sha256.Sum256(key)
	if n, ok := h.cache[hash]; ok {
		n.refCount++
		n.blob = val
	} else {
		h.cache[hash] = &RefTrieNode{1, val}
	}
}

func (h *HashNodeCache) Get(key []byte) []byte {
	if h == nil {
		return nil
	}
	h.lock.RLock()
	defer h.lock.RUnlock()
	hash := sha256.Sum256(key)
	if n, ok := h.cache[hash]; ok {
		return n.blob
	}
	return nil
}

func (h *HashNodeCache) del(key []byte) {
	if h == nil {
		return
	}
	h.lock.Lock()
	defer h.lock.Unlock()

	hash := sha256.Sum256(key)
	n, ok := h.cache[hash]
	if !ok {
		return
	}
	if n.refCount > 0 {
		n.refCount--
	}
	if n.refCount == 0 {
		delete(h.cache, hash)
	}
}

func (h *HashNodeCache) Add(ly layer) {
	if h == nil {
		return
	}
	dl, ok := ly.(*diffLayer)
	if !ok {
		return
	}
	beforeAdd := h.length()
	for _, v := range dl.nodes {
		h.set(v.K, v.V)
	}
	diffHashCacheLengthGauge.Update(int64(h.length()))
	log.Debug("Add difflayer to hash map", "root", ly.rootHash(), "block_number", dl.block, "map_len", h.length(), "add_delta", h.length()-beforeAdd)
}

func (h *HashNodeCache) Remove(ly layer) {
	if h == nil {
		return
	}
	dl, ok := ly.(*diffLayer)
	if !ok {
		return
	}
	go func() {
		beforeDel := h.length()
		for _, v := range dl.nodes {
			h.del(v.K)
		}
		diffHashCacheLengthGauge.Update(int64(h.length()))
		log.Debug("Remove difflayer from hash map", "root", ly.rootHash(), "block_number", dl.block, "map_len", h.length(), "del_delta", beforeDel-h.length())
	}()
}

// diffLayer represents a collection of modifications made to the in-memory tries
// along with associated state changes after running a block on top.
//
// The goal of a diff layer is to act as a journal, tracking recent modifications
// made to the state, that have not yet graduated into a semi-immutable state.
type diffLayer struct {
	// Immutables
	root   common.Hash    // Root hash to which this layer diff belongs to
	id     uint64         // Corresponding state id
	block  uint64         // Associated block number
	nodes  dbtypes.KvMap  // Cached trie nodes indexed by owner and path
	memory uint64         // Approximate guess as to how much memory we use
	cache  *HashNodeCache // trienode cache by hash key. cache is immutable, but cache's item can be add/del.

	// mutables
	origin *diskLayer   // The current difflayer corresponds to the underlying disklayer and is updated during cap.
	parent layer        // Parent layer modified by this one, never nil, **can be changed**
	lock   sync.RWMutex // Lock used to protect parent
}

// newDiffLayer creates a new diff layer on top of an existing layer.
func newDiffLayer(parent layer, root common.Hash, id uint64, block uint64, nodes dbtypes.KvMap) *diffLayer {
	var (
		size  int64
		count int
	)
	dl := &diffLayer{
		root:   root,
		id:     id,
		block:  block,
		nodes:  nodes,
		parent: parent,
	}

	switch l := parent.(type) {
	case *diskLayer:
		dl.origin = l
		dl.cache = &HashNodeCache{
			cache: make(map[[sha256.Size]byte]*RefTrieNode),
		}
	case *diffLayer:
		dl.origin = l.originDiskLayer()
		dl.cache = l.cache
	default:
		panic("unknown parent type")
	}

	for _, v := range nodes {
		dl.memory += uint64(len(v.K) + len(v.V))
		size += int64(len(v.K) + len(v.V))
		count += 1
	}

	dirtyWriteMeter.Mark(size)
	diffLayerNodesMeter.Mark(int64(count))
	diffLayerBytesMeter.Mark(int64(dl.memory))
	log.Debug("Created new diff layer", "id", id, "block", block, "nodes", count, "size", common.StorageSize(dl.memory), "root", dl.root)
	return dl
}

func (dl *diffLayer) originDiskLayer() *diskLayer {
	dl.lock.RLock()
	defer dl.lock.RUnlock()
	return dl.origin
}

// rootHash implements the layer interface, returning the root hash of
// corresponding state.
func (dl *diffLayer) rootHash() common.Hash {
	return dl.root
}

// stateID implements the layer interface, returning the state id of the layer.
func (dl *diffLayer) stateID() uint64 {
	return dl.id
}

// parentLayer implements the layer interface, returning the subsequent
// layer of the diff layer.
func (dl *diffLayer) parentLayer() layer {
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	return dl.parent
}

// node retrieves the node with provided node information. It's the internal
// version of Node function with additional accessed layer tracked. No error
// will be returned if node is not found.
func (dl *diffLayer) node(path []byte, depth int) ([]byte, error) {
	// Hold the lock, ensure the parent won't be changed during the
	// state accessing.
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	// If the trie node is known locally, return it
	n, ok := dl.nodes.Get(path)
	if ok {
		return n, nil
	}
	// Trie node unknown to this layer, resolve from parent
	if diff, ok := dl.parent.(*diffLayer); ok {
		return diff.node(path, depth+1)
	}
	// Failed to resolve through diff layers, fallback to disk layer
	return dl.parent.Node(path)
}

// Node implements the layer interface, retrieving the trie node blob with the
// provided node information. No error will be returned if the node is not found.
func (dl *diffLayer) Node(path []byte) ([]byte, error) {
	if n := dl.cache.Get(path); n != nil {
		// The query from the hash map is fastpath,
		// avoiding recursive query of 128 difflayers.
		diffHashCacheHitMeter.Mark(1)
		diffHashCacheReadMeter.Mark(int64(len(n)))
		return n, nil
	}
	diffHashCacheMissMeter.Mark(1)

	persistLayer := dl.originDiskLayer()
	if persistLayer != nil {
		blob, err := persistLayer.Node(path)
		if err != nil {
			// This is a bad case with a very low probability.
			// r/w the difflayer cache and r/w the disklayer are not in the same lock,
			// so in extreme cases, both reading the difflayer cache and reading the disklayer may fail, eg, disklayer is stale.
			// In this case, fallback to the original 128-layer recursive difflayer query path.
			diffHashCacheSlowPathMeter.Mark(1)
			log.Debug("Retry difflayer due to query origin failed", "path", path, "hash", "error", err)
			return dl.node(path, 0)
		} else { // This is the fastpath.
			return blob, nil
		}
	}
	diffHashCacheSlowPathMeter.Mark(1)
	log.Debug("Retry difflayer due to origin is nil", "path", path)
	return dl.node(path, 0)
}

// update implements the layer interface, creating a new layer on top of the
// existing layer tree with the specified data items.
func (dl *diffLayer) update(root common.Hash, id uint64, block uint64, nodes dbtypes.KvMap) *diffLayer {
	return newDiffLayer(dl, root, id, block, nodes)
}

// persist flushes the diff layer and all its parent layers to disk layer.
func (dl *diffLayer) persist(force bool) (layer, error) {
	if parent, ok := dl.parentLayer().(*diffLayer); ok {
		// Hold the lock to prevent any read operation until the new
		// parent is linked correctly.
		dl.lock.Lock()

		// The merging of diff layers starts at the bottom-most layer,
		// therefore we recurse down here, flattening on the way up
		// (diffToDisk).
		result, err := parent.persist(force)
		if err != nil {
			dl.lock.Unlock()
			return nil, err
		}
		dl.parent = result
		dl.lock.Unlock()
	}
	return diffToDisk(dl, force)
}

// diffToDisk merges a bottom-most diff into the persistent disk layer underneath
// it. The method will panic if called onto a non-bottom-most diff layer.
func diffToDisk(layer *diffLayer, force bool) (layer, error) {
	disk, ok := layer.parentLayer().(*diskLayer)
	if !ok {
		panic(fmt.Sprintf("unknown layer type: %T", layer.parentLayer()))
	}
	return disk.commit(layer, force)
}

func (dl *diffLayer) reset() {
	// Hold the lock, ensure the parent won't be changed during the
	// state accessing.
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	dl.nodes = make(dbtypes.KvMap)
}
