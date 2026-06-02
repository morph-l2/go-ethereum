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

package trie

import (
	"fmt"
	"sync/atomic"
)

// trieStatLevels is the maximum number of levels LevelStats tracks. While
// a secure MPT with 32-byte keys can theoretically reach depth 64, every
// production trie observed in practice finishes well before depth 16.
// Keeping the fixed bound matches upstream go-ethereum PR #28892 so that
// on-disk dump records have a stable size and layout.
const trieStatLevels = 16

// LevelStats tracks the type and count of trie nodes at each level in a
// Merkle Patricia Trie. It is safe for concurrent use: every counter is an
// atomic value so multiple walker goroutines can record nodes without
// external synchronization.
//
// Level indexing follows the walker's notion of "depth from the root" and
// must not exceed trieStatLevels-1; attempts to record nodes at depth >=
// trieStatLevels trigger a panic. Callers that operate on arbitrary trie
// data should pre-validate depth before invoking add / AddLeaf.
type LevelStats struct {
	level [trieStatLevels]stat
}

// NewLevelStats creates an empty trie statistics collector.
func NewLevelStats() *LevelStats {
	return &LevelStats{}
}

// MaxDepth iterates each level and finds the deepest level with at least
// one trie node. Returns zero for an empty LevelStats.
func (s *LevelStats) MaxDepth() int {
	depth := 0
	for i := range s.level {
		if s.level[i].short.Load() != 0 || s.level[i].full.Load() != 0 || s.level[i].value.Load() != 0 {
			depth = i
		}
	}
	return depth
}

// TotalNodes returns the total number of nodes across all levels and node
// types. Hash nodes are not counted — only their resolved underlying nodes
// contribute.
func (s *LevelStats) TotalNodes() uint64 {
	var total uint64
	for i := range s.level {
		total += s.level[i].short.Load() + s.level[i].full.Load() + s.level[i].value.Load()
	}
	return total
}

// add increases the node count by one for the specified node type and
// depth. hashNodes do not have their own slot: the caller is expected to
// resolve them and record the resulting short/full/value node instead.
func (s *LevelStats) add(n node, depth uint32) {
	d := int(depth)
	switch (n).(type) {
	case *shortNode:
		s.level[d].short.Add(1)
	case *fullNode:
		s.level[d].full.Add(1)
	case valueNode:
		s.level[d].value.Add(1)
	default:
		panic(fmt.Sprintf("%T: invalid node: %v", n, n))
	}
}

// addSize increases the raw byte-size tally at the specified depth. It is
// used to account for the encoded size of resolved hash-referenced nodes,
// which the walker learns on lookup but whose node type is recorded
// separately via add() once decoded.
func (s *LevelStats) addSize(depth uint32, size uint64) {
	s.level[depth].size.Add(size)
}

// AddLeaf records a leaf observation at the given depth. The leaf bucket
// reuses the value-node counter so stateless witness stats and inspect-
// trie share the same storage layout. Panics when depth is outside
// [0, trieStatLevels-1].
func (s *LevelStats) AddLeaf(depth int) {
	s.level[depth].value.Add(1)
}

// LeafDepths returns leaf counts grouped by depth. The returned array is a
// snapshot; subsequent updates to LevelStats do not affect it.
func (s *LevelStats) LeafDepths() [trieStatLevels]int64 {
	var leaves [trieStatLevels]int64
	for i := range s.level {
		leaves[i] = int64(s.level[i].value.Load())
	}
	return leaves
}

// stat is a specific level's count of each node type plus raw byte-size.
type stat struct {
	short atomic.Uint64
	full  atomic.Uint64
	value atomic.Uint64
	size  atomic.Uint64
}

// empty returns whether there are any trie nodes at the level.
func (s *stat) empty() bool {
	return s.full.Load() == 0 && s.short.Load() == 0 && s.value.Load() == 0 && s.size.Load() == 0
}

// load atomically reads every field of the stat.
func (s *stat) load() (uint64, uint64, uint64, uint64) {
	return s.short.Load(), s.full.Load(), s.value.Load(), s.size.Load()
}

// add folds other into s in place and returns s. Used when aggregating
// per-level totals into a grand total.
func (s *stat) add(other *stat) *stat {
	s.short.Add(other.short.Load())
	s.full.Add(other.full.Load())
	s.value.Add(other.value.Load())
	s.size.Add(other.size.Load())
	return s
}
