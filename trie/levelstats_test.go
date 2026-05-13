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

import "testing"

func TestLevelStatsAddLeafDepthBounds(t *testing.T) {
	stats := NewLevelStats()
	stats.AddLeaf(15)

	if got := stats.LeafDepths()[15]; got != 1 {
		t.Fatalf("leaf count at depth 15 = %d, want 1", got)
	}
	if got := stats.MaxDepth(); got != 15 {
		t.Fatalf("MaxDepth = %d, want 15", got)
	}
	if got := stats.TotalNodes(); got != 1 {
		t.Fatalf("TotalNodes = %d, want 1", got)
	}
}

func TestLevelStatsAddLeafPanicsOnDepth16(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for depth >= 16")
		}
	}()
	NewLevelStats().AddLeaf(16)
}

func TestLevelStatsAddNodeClassification(t *testing.T) {
	stats := NewLevelStats()
	stats.add(&shortNode{}, 3)
	stats.add(&fullNode{}, 4)
	stats.add(valueNode{0x01}, 5)

	short, full, value, _ := stats.level[3].load()
	if short != 1 || full != 0 || value != 0 {
		t.Fatalf("level 3 = short=%d full=%d value=%d, want short=1", short, full, value)
	}
	_, full, _, _ = stats.level[4].load()
	if full != 1 {
		t.Fatalf("level 4 full = %d, want 1", full)
	}
	_, _, value, _ = stats.level[5].load()
	if value != 1 {
		t.Fatalf("level 5 value = %d, want 1", value)
	}
	if got := stats.TotalNodes(); got != 3 {
		t.Fatalf("TotalNodes = %d, want 3", got)
	}
}

func TestLevelStatsAddSizeAccumulates(t *testing.T) {
	stats := NewLevelStats()
	stats.addSize(2, 100)
	stats.addSize(2, 50)
	_, _, _, size := stats.level[2].load()
	if size != 150 {
		t.Fatalf("size accumulation = %d, want 150", size)
	}
}

func TestLevelStatsStatEmpty(t *testing.T) {
	var s stat
	if !s.empty() {
		t.Fatalf("fresh stat should be empty")
	}
	s.short.Add(1)
	if s.empty() {
		t.Fatalf("stat with nonzero short must not be empty")
	}
}

func TestLevelStatsStatAdd(t *testing.T) {
	var a, b stat
	a.short.Store(1)
	a.full.Store(2)
	a.value.Store(3)
	a.size.Store(4)
	b.short.Store(10)
	b.full.Store(20)
	b.value.Store(30)
	b.size.Store(40)

	sum := (&stat{}).add(&a).add(&b)
	s, f, v, sz := sum.load()
	if s != 11 || f != 22 || v != 33 || sz != 44 {
		t.Fatalf("stat add: got %d/%d/%d/%d, want 11/22/33/44", s, f, v, sz)
	}
}
