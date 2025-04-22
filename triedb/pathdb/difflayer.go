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
	"fmt"
	"sync"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/log"
	dbtypes "github.com/morph-l2/go-ethereum/triedb/types"
)

// diffLayer represents a collection of modifications made to the in-memory tries
// along with associated state changes after running a block on top.
//
// The goal of a diff layer is to act as a journal, tracking recent modifications
// made to the state, that have not yet graduated into a semi-immutable state.
type diffLayer struct {
	// Immutables
	root   common.Hash   // Root hash to which this layer diff belongs to
	id     uint64        // Corresponding state id
	block  uint64        // Associated block number
	nodes  dbtypes.KvMap // Cached trie nodes indexed by owner and path
	memory uint64        // Approximate guess as to how much memory we use

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
	case *diffLayer:
		dl.origin = l.originDiskLayer()
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

func (dl *diffLayer) updateOriginDiskLayer(persistLayer *diskLayer) {
	dl.lock.Lock()
	defer dl.lock.Unlock()
	dl.origin = persistLayer
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
	return dl.node(path, 0)
}

// update implements the layer interface, creating a new layer on top of the
// existing layer tree with the specified data items.
func (dl *diffLayer) update(root common.Hash, id uint64, block uint64, nodes dbtypes.KvMap) *diffLayer {
	return newDiffLayer(dl, root, id, block, nodes)
}

// persist flushes the diff layer and all its parent layers to disk layer.
func (dl *diffLayer) persist(force bool, timeFlush bool) (layer, error) {
	if parent, ok := dl.parentLayer().(*diffLayer); ok {
		// Hold the lock to prevent any read operation until the new
		// parent is linked correctly.
		dl.lock.Lock()

		// The merging of diff layers starts at the bottom-most layer,
		// therefore we recurse down here, flattening on the way up
		// (diffToDisk).
		result, err := parent.persist(force, timeFlush)
		if err != nil {
			dl.lock.Unlock()
			return nil, err
		}
		dl.parent = result
		dl.lock.Unlock()
	}
	return diffToDisk(dl, force, timeFlush)
}

// diffToDisk merges a bottom-most diff into the persistent disk layer underneath
// it. The method will panic if called onto a non-bottom-most diff layer.
func diffToDisk(layer *diffLayer, force, timeFlush bool) (layer, error) {
	disk, ok := layer.parentLayer().(*diskLayer)
	if !ok {
		panic(fmt.Sprintf("unknown layer type: %T", layer.parentLayer()))
	}
	return disk.commit(layer, force, timeFlush)
}

func (dl *diffLayer) reset() {
	// Hold the lock, ensure the parent won't be changed during the
	// state accessing.
	dl.lock.RLock()
	defer dl.lock.RUnlock()

	dl.nodes = make(dbtypes.KvMap)
}
