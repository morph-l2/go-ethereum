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
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>

package rawdb

import (
	"bytes"
	"fmt"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"

	zktrie "github.com/scroll-tech/zktrie/trie"
	zkt "github.com/scroll-tech/zktrie/types"
)

// HashScheme is the legacy hash-based state scheme with which trie nodes are
// stored in the disk with node hash as the database key. The advantage of this
// scheme is that different versions of trie nodes can be stored in disk, which
// is very beneficial for constructing archive nodes. The drawback is it will
// store different trie nodes on the same path to different locations on the disk
// with no data locality, and it's unfriendly for designing state pruning.
//
// Now this scheme is still kept for backward compatibility, and it will be used
// for archive node and some other tries(e.g. light trie).
const HashScheme = "hash"

const ZkHashScheme = "hashZk"

// PathScheme is the new path-based state scheme with which trie nodes are stored
// in the disk with node path as the database key. This scheme will only store one
// version of state data in the disk, which means that the state pruning operation
// is native. At the same time, this scheme will put adjacent trie nodes in the same
// area of the disk with good data locality property. But this scheme needs to rely
// on extra state diffs to survive deep reorg.
const PathScheme = "path"

// ReadAccountTrieNode retrieves the account trie node and the associated node
// hash with the specified node path.
func ReadAccountTrieNode(db ethdb.KeyValueReader, path []byte) ([]byte, common.Hash) {
	data, err := db.Get(accountTrieNodeKey(path))
	if err != nil {
		return nil, common.Hash{}
	}

	n, err := zktrie.NewNodeFromBytes(data)
	if err != nil {
		return nil, common.Hash{}
	}

	zkHash, err := n.NodeHash()
	if err != nil {
		return nil, common.Hash{}
	}

	return data, common.BytesToHash(zkHash.Bytes())
}

// IsLegacyTrieNode reports whether a provided database entry is a legacy trie
// node. The characteristics of legacy trie node are:
// - the key length is 32 bytes
// - the key is the hash of val
func IsLegacyTrieNode(key []byte, val []byte) bool {
	if len(key) != common.HashLength {
		return false
	}

	n, err := zktrie.NewNodeFromBytes(val)
	if err != nil {
		return false
	}

	zkHash, err := n.NodeHash()
	if err != nil {
		return false
	}

	hash := common.BytesToHash(common.BitReverse(zkHash[:]))
	return bytes.Equal(key[:], hash[:])
}

// HasAccountTrieNode checks the presence of the account trie node with the
// specified node path, regardless of the node hash.
func HasAccountTrieNode(db ethdb.KeyValueReader, path []byte) bool {
	has, err := db.Has(accountTrieNodeKey(path))
	if err != nil {
		return false
	}
	return has
}

// HasLegacyTrieNode checks if the trie node with the provided hash is present in db.
func HasLegacyTrieNode(db ethdb.KeyValueReader, hash common.Hash) bool {
	ok, _ := db.Has(hash.Bytes())
	return ok
}

// ReadStateScheme reads the state scheme of persistent state, or none
// if the state is not present in database.
func ReadStateScheme(db ethdb.Database) string {
	// Check if state in path-based scheme is present.
	if HasAccountTrieNode(db, zkt.TrieRootPathKey[:]) {
		return PathScheme
	}
	// The root node might be deleted during the initial snap sync, check
	// the persistent state id then.
	if id := ReadPersistentStateID(db); id != 0 {
		return PathScheme
	}

	// In a hash-based scheme, the genesis state is consistently stored
	// on the disk. To assess the scheme of the persistent state, it
	// suffices to inspect the scheme of the genesis state.
	header := ReadHeader(db, ReadCanonicalHash(db, 0), 0)
	if header == nil {
		return "" // empty datadir
	}

	if !HasLegacyTrieNode(db, header.Root) {
		return "" // no state in disk
	}
	return HashScheme
}

// ValidateStateScheme used to check state scheme whether is valid.
// Valid state scheme: hash and path.
func ValidateStateScheme(stateScheme string) bool {
	if stateScheme == HashScheme || stateScheme == PathScheme {
		return true
	}
	return false
}

// ParseStateScheme checks if the specified state scheme is compatible with
// the stored state.
//
//   - If the provided scheme is none, use the scheme consistent with persistent
//     state, or fallback to path-based scheme if state is empty.
//
//   - If the provided scheme is hash, use hash-based scheme or error out if not
//     compatible with persistent state scheme.
//
//   - If the provided scheme is path: use path-based scheme or error out if not
//     compatible with persistent state scheme.
func ParseStateScheme(provided string, disk ethdb.Database) (string, error) {
	// If state scheme is not specified, use the scheme consistent
	// with persistent state, or fallback to hash mode if database
	// is empty.
	stored := ReadStateScheme(disk)
	if provided == "" {
		if stored == "" {
			log.Info("State scheme set to default", "scheme", "hash")
			return HashScheme, nil // use default scheme for empty database
		}
		log.Info("State scheme set to already existing disk db", "scheme", stored)
		return stored, nil // reuse scheme of persistent scheme
	}
	// If state scheme is specified, ensure it's valid.
	if provided != HashScheme && provided != PathScheme {
		return "", fmt.Errorf("invalid state scheme %s", provided)
	}
	// If state scheme is specified, ensure it's compatible with
	// persistent state.
	if stored == "" || provided == stored {
		log.Info("State scheme set by user", "scheme", provided)
		return provided, nil
	}
	return "", fmt.Errorf("incompatible state scheme, stored: %s, user provided: %s", stored, provided)
}
