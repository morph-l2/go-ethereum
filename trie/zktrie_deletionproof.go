package trie

import (
	"bytes"

	zktrie "github.com/scroll-tech/zktrie/trie"
	zkt "github.com/scroll-tech/zktrie/types"

	"github.com/scroll-tech/go-ethereum/ethdb"
)

// Pick Node from its hash directly from database, notice it has different
// interface with the function of same name in `trie`
func (t *ZkTrie) TryGetNode(nodeHash *zkt.Hash) (*zktrie.Node, error) {
	if bytes.Equal(nodeHash[:], zkt.HashZero[:]) {
		return zktrie.NewEmptyNode(), nil
	}
	nBytes, err := t.db.Get(nodeHash[:])
	if err == zktrie.ErrKeyNotFound {
		return nil, zktrie.ErrKeyNotFound
	} else if err != nil {
		return nil, err
	}
	return zktrie.NewNodeFromBytes(nBytes)
}

type deletionProofTracer struct {
	*ZkTrie
	deletionTracer map[zkt.Hash]struct{}
}

func (t *ZkTrie) NewDeletionTracer() *deletionProofTracer {
	return &deletionProofTracer{
		ZkTrie:         t,
		deletionTracer: map[zkt.Hash]struct{}{zkt.HashZero: {}},
	}
}

// ProveWithDeletion is the implement of Prove, it also return possible sibling node
// (if there is, i.e. the node of key exist and is not the only node in trie)
// so witness generator can predict the final state root after deletion of this key
// the returned sibling node has no key along with it for witness generator must decode
// the node for its purpose
func (t *deletionProofTracer) ProveWithDeletion(key []byte, proofDb ethdb.KeyValueWriter) (siblings [][]byte, err error) {
	var mptPath []*zktrie.Node
	err = t.ZkTrie.ProveWithDeletion(key, 0,
		func(n *zktrie.Node) error {
			nodeHash, err := n.NodeHash()
			if err != nil {
				return err
			}

			if n.Type == zktrie.NodeTypeLeaf {
				preImage := t.GetKey(n.NodeKey.Bytes())
				if len(preImage) > 0 {
					n.KeyPreimage = &zkt.Byte32{}
					copy(n.KeyPreimage[:], preImage)
				}
			} else if n.Type == zktrie.NodeTypeParent {
				mptPath = append(mptPath, n)
			}

			return proofDb.Put(nodeHash[:], n.Value())
		},
		func(delNode *zktrie.Node, n *zktrie.Node) {
			nodeHash, _ := delNode.NodeHash()
			t.deletionTracer[*nodeHash] = struct{}{}

			// the sibling for each leaf should be unique except for EmptyNode
			if n != nil && n.Type != zktrie.NodeTypeEmpty {
				siblings = append(siblings, n.Value())
			}
		},
	)
	if err != nil {
		return
	}

	// now handle mptpath reversively
	for i := len(mptPath); i > 0; i-- {
		n := mptPath[i-1]
		_, deletedL := t.deletionTracer[*n.ChildL]
		_, deletedR := t.deletionTracer[*n.ChildR]
		if deletedL && deletedR {
			nodeHash, _ := n.NodeHash()
			t.deletionTracer[*nodeHash] = struct{}{}
		} else if i != len(mptPath) {
			var siblingHash *zkt.Hash
			if deletedL {
				siblingHash = n.ChildR
			} else if deletedR {
				siblingHash = n.ChildL
			}
			if siblingHash != nil {
				var sibling *zktrie.Node
				sibling, err = t.TryGetNode(siblingHash)
				if err != nil {
					return
				}
				siblings = append(siblings, sibling.Value())
			}
		}
	}

	// we put this special kv pair in db so we can distinguish the type and
	// make suitable Proof
	err = proofDb.Put(magicHash, zktrie.ProofMagicBytes())
	return
}
