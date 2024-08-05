// Copyright 2019 The go-ethereum Authors
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

package trie

import (
	"bytes"
	"errors"
	"sync"

	zktrie "github.com/scroll-tech/zktrie/trie"
	zkt "github.com/scroll-tech/zktrie/types"
	"golang.org/x/crypto/sha3"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/crypto"
)

// leafChanSize is the size of the leafCh. It's a pretty arbitrary number, to allow
// some parallelism but not incur too much memory overhead.
const ZkLeafChanSize = 200

// leaf represents a trie leaf value
type ZkLeaf struct {
	size int         // size of the rlp data (estimate)
	hash common.Hash // hash of rlp data
	node node        // the node to commit
}

// committer is a type used for the trie Commit operation. A committer has some
// internal preallocated temp space, and also a callback that is invoked when
// leaves are committed. The leafs are passed through the `leafCh`,  to allow
// some level of parallelism.
// By 'some level' of parallelism, it's still the case that all leaves will be
// processed sequentially - onleaf will never be called in parallel or out of order.
type zk_committer struct {
	tmp sliceBuffer
	sha crypto.KeccakState

	onleaf   LeafCallback
	zkLeafCh chan *ZkLeaf
}

// committers live in a global sync.Pool
var zk_committerPool = sync.Pool{
	New: func() interface{} {
		return &zk_committer{
			tmp: make(sliceBuffer, 0, 550), // cap is as large as a full fullNode.
			sha: sha3.NewLegacyKeccak256().(crypto.KeccakState),
		}
	},
}

// newCommitter creates a new committer or picks one from the pool.
func newZkCommitter() *zk_committer {
	return zk_committerPool.Get().(*zk_committer)
}

func returnZkCommitterToPool(h *zk_committer) {
	h.onleaf = nil
	h.zkLeafCh = nil
	zk_committerPool.Put(h)
}

// Commit collapses a node down into a hash node and inserts it into the database
func (c *zk_committer) Commit(rootHash common.Hash, db *Database) (*zktrie.Node, int, error) {
	if db == nil {
		return nil, 0, errors.New("no db provided")
	}

	db.rawLock.Lock()
	defer db.rawLock.Unlock()

	n, childCommitted, err := c.commit(rootHash, db)

	for k := range db.rawDirties {
		delete(db.rawDirties, k)
	}

	return n, childCommitted, err
}

// commit collapses a node down into a hash node and inserts it into the database
func (c *zk_committer) commit(nodeHash common.Hash, db *Database) (*zktrie.Node, int, error) {
	nodeKey := zkt.NewHashFromBytes(nodeHash[:])
	if nodeVal, ok := db.rawDirties.Get(BitReverse(nodeKey[:])); ok {
		if node, err := zktrie.NewNodeFromBytes(nodeVal); err == nil {
			switch node.Type {
			case zktrie.NodeTypeEmpty_New:
				return nil, 0, nil
			case zktrie.NodeTypeLeaf_New:

				nodeCopy := node.Copy()
				c.store(nodeCopy, nodeHash, db)

				return nodeCopy, 1, nil
			case zktrie.NodeTypeBranch_0, zktrie.NodeTypeBranch_1, zktrie.NodeTypeBranch_2, zktrie.NodeTypeBranch_3:
				var childCommittedL int
				if !bytes.Equal(node.ChildL[:], common.Hash{}.Bytes()) {
					_, childCommitted, _ := c.commit(common.BytesToHash(node.ChildL.Bytes()), db)
					childCommittedL = childCommitted
					childCommittedL += 1
				}

				var childCommittedR int
				if !bytes.Equal(node.ChildR[:], common.Hash{}.Bytes()) {
					_, childCommitted, _ := c.commit(common.BytesToHash(node.ChildR.Bytes()), db)
					childCommittedR = childCommitted
					childCommittedR += 1
				}

				nodeCopy := node.Copy()
				c.store(nodeCopy, nodeHash, db)

				return nodeCopy, childCommittedL + childCommittedR, nil
			case zktrie.NodeTypeEmpty, zktrie.NodeTypeLeaf, zktrie.NodeTypeParent:
				panic("encounter unsupported deprecated node type")
			default:
				panic("unreachable")
			}
		}
	}

	return nil, 0, nil
}

// store hashes the node n and if we have a storage layer specified, it writes
// the key/value pair to it and tracks any node->child references as well as any
// node->external trie references.
func (c *zk_committer) store(n *zktrie.Node, nodeHash common.Hash, db *Database) {
	// Larger nodes are replaced by their hash and stored in the database.
	var (
		size int
	)
	if n == nil {
		// This was not generated - must be a small node stored in the parent.
		// In theory, we should apply the leafCall here if it's not nil(embedded
		// node usually contains value). But small value(less than 32bytes) is
		// not our target.
		return
	} else {
		// We have the hash already, estimate the RLP encoding-size of the node.
		// The size is used for mem tracking, does not need to be exact
		size = zkEstimateSize(n)
	}
	// If we're using channel-based leaf-reporting, send to channel.
	// The leaf channel will be active only when there an active leaf-callback
	if c.zkLeafCh != nil {
		c.zkLeafCh <- &ZkLeaf{
			size: size,
			hash: nodeHash,
			node: rawZkNode{n},
		}
	} else if db != nil {
		// No leaf-callback used, but there's still a database. Do serial
		// insertion
		db.lock.Lock()
		db.insert(nodeHash, size, rawZkNode{n})
		db.lock.Unlock()
	}
}

// commitLoop does the actual insert + leaf callback for nodes.
func (c *zk_committer) commitLoop(db *Database) {
	for item := range c.zkLeafCh {
		var (
			hash = item.hash
			size = item.size
			n    = item.node
		)
		// // We are pooling the trie nodes into an intermediate memory cache
		db.lock.Lock()
		db.insert(hash, size, n)
		db.lock.Unlock()

		if c.onleaf != nil {
			if node, ok := n.(rawZkNode); ok {
				if node.n.Type == zktrie.NodeTypeLeaf_New {
					c.onleaf(nil, nil, node.n.Data(), hash)
				}
			}
		}
	}
}

// estimateSize estimates the size of an rlp-encoded node, without actually
// rlp-encoding it (zero allocs). This method has been experimentally tried, and with a trie
// with 1000 leafs, the only errors above 1% are on small shortnodes, where this
// method overestimates by 2 or 3 bytes (e.g. 37 instead of 35)
func zkEstimateSize(n *zktrie.Node) int {
	return len(n.CanonicalValue())
}
