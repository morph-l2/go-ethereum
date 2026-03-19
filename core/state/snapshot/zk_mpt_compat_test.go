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

// Tests for ZK-MPT compatibility fixes:
//   - generateSnapshot resolves zkStateRoot → mptStateRoot via ReadDiskStateRoot
//   - loadSnapshot accepts a snapshot whose disk root is the MPT translation of
//     the requested zkStateRoot
//   - Rebuild stores the disk layer under base.root (the possibly-translated root)
//     as the map key so that Snapshot() lookups work correctly after translation

package snapshot

import (
	"math/big"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/ethdb/memorydb"
	"github.com/morph-l2/go-ethereum/rlp"
	"github.com/morph-l2/go-ethereum/trie"
)

// buildMPTTrie creates a small MPT account trie in diskdb/triedb and returns its root.
func buildMPTTrie(t *testing.T, diskdb *memorydb.Database) (common.Hash, *trie.Database) {
	t.Helper()
	triedb := trie.NewDatabase(diskdb)
	accTrie, _ := trie.NewSecure(common.Hash{}, triedb)
	for _, key := range []string{"acc-1", "acc-2", "acc-3"} {
		acc := &Account{
			Balance:          big.NewInt(1),
			Root:             emptyRoot.Bytes(),
			KeccakCodeHash:   emptyKeccakCode.Bytes(),
			PoseidonCodeHash: emptyPoseidonCode.Bytes(),
		}
		val, _ := rlp.EncodeToBytes(acc)
		accTrie.Update([]byte(key), val)
	}
	mptRoot, _, _ := accTrie.Commit(nil)
	triedb.Commit(mptRoot, false, nil)
	return mptRoot, triedb
}

// writeDoneGenerator writes a completed (Done=true) snapshot generator record
// into the given db — required by loadAndParseJournal before loadSnapshot
// proceeds to the head-root comparison.
func writeDoneGenerator(db ethdb.KeyValueWriter) {
	blob, _ := rlp.EncodeToBytes(&journalGenerator{Done: true})
	rawdb.WriteSnapshotGenerator(db, blob)
}

// ---- generate.go: generateSnapshot root translation -------------------------

// TestGenerateSnapshotTranslatesZkRoot verifies that when a DiskStateRoot
// mapping (zkRoot → mptRoot) exists, generateSnapshot uses mptRoot for both
// WriteSnapshotRoot and the trie walk, allowing generation to complete.
func TestGenerateSnapshotTranslatesZkRoot(t *testing.T) {
	diskdb := memorydb.New()
	mptRoot, triedb := buildMPTTrie(t, diskdb)

	// Simulate ZK-era: block header carries zkRoot, local trie is at mptRoot.
	zkRoot := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	rawdb.WriteDiskStateRoot(diskdb, zkRoot, mptRoot)

	snap := generateSnapshot(diskdb, triedb, 16, zkRoot)

	// diskLayer.root must be mptRoot, not zkRoot.
	if snap.root != mptRoot {
		t.Fatalf("diskLayer.root: got %x, want mptRoot %x", snap.root, mptRoot)
	}
	// SnapshotRoot persisted to DB must be mptRoot.
	if stored := rawdb.ReadSnapshotRoot(diskdb); stored != mptRoot {
		t.Fatalf("SnapshotRoot in DB: got %x, want mptRoot %x", stored, mptRoot)
	}
	// Generation must complete successfully.
	select {
	case <-snap.genPending:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot generation timed out — trie walk likely used zkRoot instead of mptRoot")
	}
	// The generated snapshot data must reproduce mptRoot exactly.
	checkSnapRoot(t, snap, mptRoot)

	stop := make(chan *generatorStats)
	snap.genAbort <- stop
	<-stop
}

// TestGenerateSnapshotNoTranslation verifies that when no DiskStateRoot mapping
// exists (post-Jade-fork MPT blocks where mptRoot IS the block root), root is
// used unchanged and generation still succeeds.
func TestGenerateSnapshotNoTranslation(t *testing.T) {
	diskdb := memorydb.New()
	mptRoot, triedb := buildMPTTrie(t, diskdb)
	// No WriteDiskStateRoot call — no mapping.

	snap := generateSnapshot(diskdb, triedb, 16, mptRoot)

	if snap.root != mptRoot {
		t.Fatalf("diskLayer.root: got %x, want %x", snap.root, mptRoot)
	}
	if stored := rawdb.ReadSnapshotRoot(diskdb); stored != mptRoot {
		t.Fatalf("SnapshotRoot in DB: got %x, want %x", stored, mptRoot)
	}
	select {
	case <-snap.genPending:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot generation timed out")
	}
	checkSnapRoot(t, snap, mptRoot)

	stop := make(chan *generatorStats)
	snap.genAbort <- stop
	<-stop
}

// ---- journal.go: loadSnapshot ZK/MPT mismatch tolerance --------------------

// TestLoadSnapshotAcceptsZkMptMismatch verifies that loadSnapshot succeeds when
// the snapshot was stored under mptRoot but is requested with the corresponding
// zkRoot (MPT node syncing ZK-era blocks, snapshot already generated with fix).
func TestLoadSnapshotAcceptsZkMptMismatch(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	triedb := trie.NewDatabase(db)

	mptRoot := common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	zkRoot := common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	// Snapshot is stored at mptRoot (written by generateSnapshot after translation).
	rawdb.WriteSnapshotRoot(db, mptRoot)
	writeDoneGenerator(db)
	// The ZK→MPT mapping tells loadSnapshot they correspond.
	rawdb.WriteDiskStateRoot(db, zkRoot, mptRoot)

	snap, disabled, err := loadSnapshot(db, triedb, 16, zkRoot, false /* recovery */)
	if err != nil {
		t.Fatalf("loadSnapshot with ZK/MPT mismatch should succeed, got: %v", err)
	}
	if disabled {
		t.Fatal("snapshot should not be disabled")
	}
	if snap.Root() != mptRoot {
		t.Fatalf("snapshot root: got %x, want mptRoot %x", snap.Root(), mptRoot)
	}
}

// TestLoadSnapshotRejectsTrueMismatch verifies that a genuine root mismatch
// (no DiskStateRoot mapping, and not in recovery mode) is still rejected so
// that the ZK/MPT tolerance does not become a security hole.
func TestLoadSnapshotRejectsTrueMismatch(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	triedb := trie.NewDatabase(db)

	storedRoot := common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
	requestedRoot := common.HexToHash("0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")

	// Snapshot stored at storedRoot, no DiskStateRoot mapping for requestedRoot.
	rawdb.WriteSnapshotRoot(db, storedRoot)
	writeDoneGenerator(db)
	// Intentionally no WriteDiskStateRoot call.

	_, _, err := loadSnapshot(db, triedb, 16, requestedRoot, false /* recovery */)
	if err == nil {
		t.Fatal("loadSnapshot should return error for genuine root mismatch without DiskStateRoot mapping")
	}
}

// ---- snapshot.go: Rebuild uses base.root as map key ------------------------

// TestRebuildZkRootMapKeyConsistency verifies that after Rebuild(zkRoot), the
// snapshot tree's layers map is keyed by mptRoot (base.root after translation),
// not zkRoot, so that Snapshot(mptRoot) and DiskRoot() return correct results.
func TestRebuildZkRootMapKeyConsistency(t *testing.T) {
	diskdb := memorydb.New()
	mptRoot, triedb := buildMPTTrie(t, diskdb)

	zkRoot := common.HexToHash("0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	rawdb.WriteDiskStateRoot(diskdb, zkRoot, mptRoot)

	snaps := &Tree{
		diskdb: diskdb,
		triedb: triedb,
		cache:  16,
		layers: make(map[common.Hash]snapshot),
	}
	snaps.Rebuild(zkRoot)

	// Wait for generation to complete.
	select {
	case <-snaps.disklayer().genPending:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot generation timed out")
	}

	// DiskRoot() must return mptRoot.
	if got := snaps.DiskRoot(); got != mptRoot {
		t.Fatalf("DiskRoot(): got %x, want mptRoot %x", got, mptRoot)
	}
	// The layers map must be keyed by mptRoot so Snapshot(mptRoot) finds it.
	if snaps.Snapshot(mptRoot) == nil {
		t.Fatal("Snapshot(mptRoot) returned nil — map key was not translated from zkRoot")
	}
	// Snapshot(zkRoot) should NOT return a layer (key is mptRoot, not zkRoot).
	if snaps.Snapshot(zkRoot) != nil {
		t.Fatal("Snapshot(zkRoot) returned non-nil — map should be keyed by mptRoot only")
	}
	// Verify layers map has exactly one entry (the disk layer at mptRoot).
	snaps.lock.RLock()
	count := len(snaps.layers)
	snaps.lock.RUnlock()
	if count != 1 {
		t.Fatalf("layers map has %d entries, want 1", count)
	}

	// Cap/Update should work correctly using mptRoot as parent.
	diffRoot := common.HexToHash("0xff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00")
	if err := snaps.Update(diffRoot, mptRoot, nil, map[common.Hash][]byte{
		common.HexToHash("0xab"): randomAccount(),
	}, nil); err != nil {
		t.Fatalf("Update after Rebuild failed: %v", err)
	}
	if snaps.Snapshot(diffRoot) == nil {
		t.Fatal("diffLayer not found after Update — parent lookup using mptRoot failed")
	}

	stop := make(chan *generatorStats)
	snaps.disklayer().genAbort <- stop
	<-stop
}

// TestRebuildWithoutTranslation verifies that when no DiskStateRoot mapping
// exists, Rebuild(mptRoot) stores the disk layer under mptRoot as before —
// ensuring no regression for the post-Jade-fork (pure MPT) case.
func TestRebuildWithoutTranslation(t *testing.T) {
	diskdb := memorydb.New()
	mptRoot, triedb := buildMPTTrie(t, diskdb)
	// No DiskStateRoot mapping — pure MPT case.

	snaps := &Tree{
		diskdb: diskdb,
		triedb: triedb,
		cache:  16,
		layers: make(map[common.Hash]snapshot),
	}
	snaps.Rebuild(mptRoot)

	select {
	case <-snaps.disklayer().genPending:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot generation timed out")
	}

	if got := snaps.DiskRoot(); got != mptRoot {
		t.Fatalf("DiskRoot(): got %x, want %x", got, mptRoot)
	}
	if snaps.Snapshot(mptRoot) == nil {
		t.Fatal("Snapshot(mptRoot) returned nil after Rebuild(mptRoot)")
	}

	stop := make(chan *generatorStats)
	snaps.disklayer().genAbort <- stop
	<-stop
}
