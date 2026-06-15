// Copyright 2025 The go-ethereum Authors
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

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/ethdb/memorydb"
)

var (
	_ types.ListHasher = (*ListHasher)(nil)
	_ types.ListHasher = (*StackTrie)(nil)
)

// ListHasher wraps a regular Merkle Patricia Trie for derivable list hashing.
// It exists as a correctness oracle; StackTrie is the optimized production path.
type ListHasher struct {
	tr *Trie
}

// NewListHasher initializes a list hasher.
func NewListHasher() *ListHasher {
	tr, _ := New(common.Hash{}, NewDatabase(memorydb.New()))
	return &ListHasher{tr: tr}
}

// Reset clears the internal trie state.
func (h *ListHasher) Reset() {
	h.tr.Reset()
}

// Update inserts a key-value pair into the trie.
func (h *ListHasher) Update(key []byte, value []byte) error {
	key, value = bytes.Clone(key), bytes.Clone(value)
	return h.tr.TryUpdate(key, value)
}

// Hash computes the root hash of all inserted key-value pairs.
func (h *ListHasher) Hash() common.Hash {
	return h.tr.Hash()
}
