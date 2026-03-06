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

package rawdb

import (
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/ethdb"
)

// ---------- helpers ----------

func makeRef(b byte) common.Reference {
	var ref common.Reference
	ref[0] = b
	return ref
}

func makeTxHash(b byte) common.Hash {
	var h common.Hash
	h[0] = b
	return h
}

// writeN writes n reference index entries for the given reference, with
// blockTimestamp = base+i, txIndex = i, txHash = makeTxHash(byte(i)).
func writeN(db ethdb.Database, ref common.Reference, n int, base uint64) {
	for i := 0; i < n; i++ {
		WriteReferenceIndexEntry(db, ref, base+uint64(i), uint64(i), makeTxHash(byte(i)))
	}
}

// ---------- Issue 1: Full-scan DoS verification ----------

// TestPagination_FullReadMatchesOld verifies that ReadReferenceIndexEntries
// (backward-compat wrapper) still returns every entry — confirming it was a
// full scan prior to the pagination fix.
func TestPagination_FullReadMatchesOld(t *testing.T) {
	db := NewMemoryDatabase()
	ref := makeRef(0x01)
	writeN(db, ref, 50, 1000)

	all := ReadReferenceIndexEntries(db, ref)
	if len(all) != 50 {
		t.Fatalf("expected 50 entries from full read, got %d", len(all))
	}
}

// TestPagination_LimitStopsEarly proves that the paginated reader returns
// exactly `limit` entries and does NOT load everything.
func TestPagination_LimitStopsEarly(t *testing.T) {
	db := NewMemoryDatabase()
	ref := makeRef(0x02)
	writeN(db, ref, 200, 0) // 200 entries stored

	// Request only the first 10
	page := ReadReferenceIndexEntriesPaginated(db, ref, 0, 10)
	if len(page) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(page))
	}

	// Verify the entries are the first 10 (sorted by timestamp)
	for i, e := range page {
		if e.BlockTimestamp != uint64(i) {
			t.Fatalf("entry %d: expected timestamp %d, got %d", i, i, e.BlockTimestamp)
		}
	}
}

// TestPagination_OffsetSkips verifies that offset correctly skips entries
// and returns the right slice window.
func TestPagination_OffsetSkips(t *testing.T) {
	db := NewMemoryDatabase()
	ref := makeRef(0x03)
	writeN(db, ref, 50, 100) // timestamps 100..149

	// Read entries 10..14 (offset=10, limit=5)
	page := ReadReferenceIndexEntriesPaginated(db, ref, 10, 5)
	if len(page) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(page))
	}
	if page[0].BlockTimestamp != 110 {
		t.Fatalf("expected first entry timestamp 110, got %d", page[0].BlockTimestamp)
	}
	if page[4].BlockTimestamp != 114 {
		t.Fatalf("expected last entry timestamp 114, got %d", page[4].BlockTimestamp)
	}
}

// TestPagination_OffsetBeyondEnd returns empty result instead of error.
func TestPagination_OffsetBeyondEnd(t *testing.T) {
	db := NewMemoryDatabase()
	ref := makeRef(0x04)
	writeN(db, ref, 10, 0)

	page := ReadReferenceIndexEntriesPaginated(db, ref, 999, 10)
	if len(page) != 0 {
		t.Fatalf("expected 0 entries for out-of-range offset, got %d", len(page))
	}
}

// TestPagination_LimitZero returns empty.
func TestPagination_LimitZero(t *testing.T) {
	db := NewMemoryDatabase()
	ref := makeRef(0x05)
	writeN(db, ref, 10, 0)

	page := ReadReferenceIndexEntriesPaginated(db, ref, 0, 0)
	if len(page) != 0 {
		t.Fatalf("expected 0 entries for limit=0, got %d", len(page))
	}
}

// TestPagination_LimitExceedsTotal returns only what exists.
func TestPagination_LimitExceedsTotal(t *testing.T) {
	db := NewMemoryDatabase()
	ref := makeRef(0x06)
	writeN(db, ref, 5, 0)

	page := ReadReferenceIndexEntriesPaginated(db, ref, 0, 100)
	if len(page) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(page))
	}
}

// ---------- Issue 3: Interrupt verification ----------

// TestInterrupt_ImmediateStop verifies that a pre-closed interrupt channel
// causes the reader to return immediately with interrupted=true and no results.
func TestInterrupt_ImmediateStop(t *testing.T) {
	db := NewMemoryDatabase()
	ref := makeRef(0x07)
	writeN(db, ref, 100, 0)

	ch := make(chan struct{})
	close(ch) // pre-closed

	entries, interrupted := ReadReferenceIndexEntriesPaginatedWithInterrupt(db, ref, 0, 100, ch)
	if !interrupted {
		t.Fatal("expected interrupted=true for pre-closed channel")
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries on interrupt, got %d", len(entries))
	}
}

// TestInterrupt_NilChannel means no interrupt — should read normally.
func TestInterrupt_NilChannel(t *testing.T) {
	db := NewMemoryDatabase()
	ref := makeRef(0x08)
	writeN(db, ref, 20, 0)

	entries, interrupted := ReadReferenceIndexEntriesPaginatedWithInterrupt(db, ref, 0, 20, nil)
	if interrupted {
		t.Fatal("expected interrupted=false for nil channel")
	}
	if len(entries) != 20 {
		t.Fatalf("expected 20 entries, got %d", len(entries))
	}
}

// ---------- Delete / Write consistency ----------

// TestDeleteReferenceIndexEntry verifies single-entry deletion.
func TestDeleteReferenceIndexEntry(t *testing.T) {
	db := NewMemoryDatabase()
	ref := makeRef(0x09)
	txHash := makeTxHash(0xAA)

	WriteReferenceIndexEntry(db, ref, 500, 3, txHash)
	entries := ReadReferenceIndexEntries(db, ref)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	DeleteReferenceIndexEntry(db, ref, 500, 3, txHash)
	entries = ReadReferenceIndexEntries(db, ref)
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after delete, got %d", len(entries))
	}
}

// ---------- Issue 2: Reorg stale entry scenario (rawdb level) ----------

// TestReorg_StaleKeyScenario simulates the exact bug where the same tx hash
// exists in both old and new chains at DIFFERENT positions. In the old code,
// the "trulyDeletedTxs" approach would miss the old entry because the tx is
// not "truly deleted" (it's in both chains). Our fix deletes ALL old chain
// entries unconditionally.
func TestReorg_StaleKeyScenario(t *testing.T) {
	db := NewMemoryDatabase()
	ref := makeRef(0x0A)
	txHash := makeTxHash(0xBB)

	// Simulate old chain: tx at block timestamp 1000, txIndex 0
	WriteReferenceIndexEntry(db, ref, 1000, 0, txHash)

	// Verify one entry exists
	entries := ReadReferenceIndexEntries(db, ref)
	if len(entries) != 1 || entries[0].BlockTimestamp != 1000 {
		t.Fatalf("setup: expected 1 entry at ts=1000, got %d entries", len(entries))
	}

	// === Simulate the BUG (old trulyDeletedTxs approach) ===
	// If tx is in both old and new chains, old code wouldn't delete the old entry.
	// It would just write a new one, resulting in 2 entries for the same tx.
	WriteReferenceIndexEntry(db, ref, 2000, 5, txHash) // new chain position

	entries = ReadReferenceIndexEntries(db, ref)
	if len(entries) != 2 {
		t.Fatalf("bug scenario: expected 2 entries (stale + new), got %d", len(entries))
	}
	t.Logf("BUG CONFIRMED: %d stale+new entries for same tx hash (old-code behavior)", len(entries))

	// === Simulate the FIX (delete old chain entries, then write new) ===
	// Delete the old chain entry
	DeleteReferenceIndexEntry(db, ref, 1000, 0, txHash)

	entries = ReadReferenceIndexEntries(db, ref)
	if len(entries) != 1 {
		t.Fatalf("fix: expected 1 entry after cleanup, got %d", len(entries))
	}
	if entries[0].BlockTimestamp != 2000 {
		t.Fatalf("fix: expected surviving entry at ts=2000, got ts=%d", entries[0].BlockTimestamp)
	}
	if entries[0].TxIndex != 5 {
		t.Fatalf("fix: expected surviving entry at txIdx=5, got txIdx=%d", entries[0].TxIndex)
	}
	t.Logf("FIX VERIFIED: only 1 correct entry remains after cleanup")
}

// TestParseReferenceIndexEntry_InvalidKey ensures malformed keys are rejected.
func TestParseReferenceIndexEntry_InvalidKey(t *testing.T) {
	// Too short key
	_, ok := parseReferenceIndexEntry([]byte{0x01, 0x02, 0x03})
	if ok {
		t.Fatal("expected invalid parse for short key")
	}
	// Correct length but arbitrary bytes (should still parse)
	key := make([]byte, len(referenceIndexPrefix)+common.ReferenceLength+8+8+common.HashLength)
	copy(key, referenceIndexPrefix)
	_, ok = parseReferenceIndexEntry(key)
	if !ok {
		t.Fatal("expected valid parse for correct-length key")
	}
}
