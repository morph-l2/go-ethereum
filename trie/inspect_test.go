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
	"encoding/json"
	"math/big"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/crypto/codehash"
	"github.com/morph-l2/go-ethereum/ethdb/memorydb"
	"github.com/morph-l2/go-ethereum/rlp"
)

// makeInspectFixture synthesises an account trie with `size` accounts.
// When withStorage is true every account gets its own small storage trie
// (random slot count between 1 and 256) so the inspector has something
// to count in both the account and storage passes. Accounts also carry a
// random-sized balance to exercise the size-on-disk accounting.
//
// The returned trie is a plain MPT (not SecureTrie): keys are pre-hashed
// so callers can look up accounts via `crypto.Keccak256(address)` — the
// convention that Inspect/InspectContract follow internally.
func makeInspectFixture(t *testing.T, size int, withStorage bool) (*Database, common.Hash) {
	t.Helper()
	db := NewDatabase(memorydb.New())

	random := rand.New(rand.NewSource(0))
	accTrie, err := New(common.Hash{}, db)
	if err != nil {
		t.Fatalf("open account trie: %v", err)
	}
	for i := 0; i < size; i++ {
		var addr [20]byte
		random.Read(addr[:])
		accountKey := crypto.Keccak256(addr[:])

		var storageRoot common.Hash
		if withStorage {
			storageTrie, err := New(common.Hash{}, db)
			if err != nil {
				t.Fatalf("open storage trie: %v", err)
			}
			slots := int(random.Uint32()%256 + 1)
			for j := 0; j < slots; j++ {
				k := make([]byte, 32)
				v := make([]byte, 32)
				random.Read(k)
				random.Read(v)
				storageTrie.Update(k, v)
			}
			var committed common.Hash
			committed, _, err = storageTrie.Commit(nil)
			if err != nil {
				t.Fatalf("commit storage trie: %v", err)
			}
			storageRoot = committed
			if err := db.Commit(storageRoot, false, nil); err != nil {
				t.Fatalf("flush storage trie: %v", err)
			}
		} else {
			storageRoot = emptyRoot
		}

		// Balance uses a random byte length so the RLP payload varies.
		numBytes := random.Uint32() % 33
		balanceBytes := make([]byte, numBytes)
		random.Read(balanceBytes)
		balance := new(big.Int).SetBytes(balanceBytes)

		acc := types.StateAccount{
			Nonce:          uint64(random.Int63()),
			Balance:        balance,
			Root:           storageRoot,
			KeccakCodeHash: codehash.EmptyKeccakCodeHash.Bytes(),
		}
		enc, err := rlp.EncodeToBytes(&acc)
		if err != nil {
			t.Fatalf("encode account: %v", err)
		}
		accTrie.Update(accountKey, enc)
	}
	root, _, err := accTrie.Commit(nil)
	if err != nil {
		t.Fatalf("commit account trie: %v", err)
	}
	if err := db.Commit(root, false, nil); err != nil {
		t.Fatalf("flush account trie: %v", err)
	}
	return db, root
}

// TestInspectRoundTripsDump walks a small account trie with storage,
// writes the pass-1 dump to a temp directory, then independently runs
// Summarize against the same dump and checks that both produce identical
// JSON summaries. This is the upstream parity invariant: Inspect and
// Summarize must agree on every aggregate.
func TestInspectRoundTripsDump(t *testing.T) {
	db, root := makeInspectFixture(t, 11, true)

	tempDir := t.TempDir()
	dumpPath := filepath.Join(tempDir, "trie-dump.bin")
	inspectJSON := filepath.Join(tempDir, "trie-summary.json")
	reanalysisJSON := filepath.Join(tempDir, "trie-summary-reanalysis.json")

	if err := Inspect(db, root, &InspectConfig{
		TopN:     1,
		DumpPath: dumpPath,
		Path:     inspectJSON,
	}); err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if err := Summarize(dumpPath, &InspectConfig{
		TopN: 1,
		Path: reanalysisJSON,
	}); err != nil {
		t.Fatalf("summarize failed: %v", err)
	}

	inspectOut := loadInspectJSON(t, inspectJSON)
	reanalysisOut := loadInspectJSON(t, reanalysisJSON)

	if len(inspectOut.StorageSummary.Levels) == 0 {
		t.Fatal("expected StorageSummary.Levels to be populated")
	}
	if inspectOut.AccountTrie.Summary.Size == 0 {
		t.Fatal("expected account trie size summary to be populated")
	}
	if inspectOut.StorageSummary.Totals.Size == 0 {
		t.Fatal("expected storage trie size summary to be populated")
	}
	if !reflect.DeepEqual(inspectOut.AccountTrie, reanalysisOut.AccountTrie) {
		t.Fatal("account trie summary mismatch between inspect and summarize")
	}
	if !reflect.DeepEqual(inspectOut.StorageSummary, reanalysisOut.StorageSummary) {
		t.Fatal("storage summary mismatch between inspect and summarize")
	}

	assertStorageTotalsMatchLevels(t, inspectOut)
	assertStorageTotalsMatchLevels(t, reanalysisOut)
	assertAccountTotalsMatchLevels(t, inspectOut.AccountTrie)
	assertAccountTotalsMatchLevels(t, reanalysisOut.AccountTrie)

	var histogramTotal uint64
	for _, count := range inspectOut.StorageSummary.DepthHistogram {
		histogramTotal += count
	}
	if histogramTotal != inspectOut.StorageSummary.TotalStorageTries {
		t.Fatalf("depth histogram total %d does not match total storage tries %d",
			histogramTotal, inspectOut.StorageSummary.TotalStorageTries)
	}
}

// TestInspectNoStorageSkipsWalk confirms the NoStorage option short-
// circuits per-account storage walks while still producing an account-
// trie report.
func TestInspectNoStorageSkipsWalk(t *testing.T) {
	db, root := makeInspectFixture(t, 5, true)

	tempDir := t.TempDir()
	dumpPath := filepath.Join(tempDir, "trie-dump.bin")
	jsonPath := filepath.Join(tempDir, "trie-summary.json")

	if err := Inspect(db, root, &InspectConfig{
		NoStorage: true,
		TopN:      3,
		DumpPath:  dumpPath,
		Path:      jsonPath,
	}); err != nil {
		t.Fatalf("inspect failed: %v", err)
	}

	out := loadInspectJSON(t, jsonPath)
	if out.StorageSummary.TotalStorageTries != 0 {
		t.Fatalf("expected 0 storage tries with NoStorage, got %d", out.StorageSummary.TotalStorageTries)
	}
	if out.StorageSummary.Totals.Size != 0 {
		t.Fatalf("expected zero storage size with NoStorage, got %d", out.StorageSummary.Totals.Size)
	}
	if out.AccountTrie.Summary.Size == 0 {
		t.Fatal("account trie size should still be populated")
	}
}

// TestInspectEmptyRootEmitsAccountSentinel verifies that running Inspect
// against an empty trie emits exactly one (account) record with no
// storage entries — guarding against regressions where an early-return
// path forgets to write the sentinel.
func TestInspectEmptyRootEmitsAccountSentinel(t *testing.T) {
	db := NewDatabase(memorydb.New())

	tempDir := t.TempDir()
	dumpPath := filepath.Join(tempDir, "trie-dump.bin")
	jsonPath := filepath.Join(tempDir, "trie-summary.json")

	// emptyRoot corresponds to an empty MPT; `New` accepts it directly.
	if err := Inspect(db, emptyRoot, &InspectConfig{
		TopN:     3,
		DumpPath: dumpPath,
		Path:     jsonPath,
	}); err != nil {
		t.Fatalf("inspect empty trie: %v", err)
	}

	info, err := os.Stat(dumpPath)
	if err != nil {
		t.Fatalf("stat dump: %v", err)
	}
	if info.Size() != inspectDumpRecordSize {
		t.Fatalf("expected exactly one sentinel record (%d bytes), got %d", inspectDumpRecordSize, info.Size())
	}
	out := loadInspectJSON(t, jsonPath)
	if out.StorageSummary.TotalStorageTries != 0 {
		t.Fatalf("expected 0 storage tries for empty trie, got %d", out.StorageSummary.TotalStorageTries)
	}
}

// TestInspectRejectsMissingRoot ensures that asking the inspector for a
// root whose nodes are not in the database surfaces an error rather than
// silently producing an empty report.
func TestInspectRejectsMissingRoot(t *testing.T) {
	db := NewDatabase(memorydb.New())
	tempDir := t.TempDir()

	unknown := common.HexToHash("0x9999999999999999999999999999999999999999999999999999999999999999")
	err := Inspect(db, unknown, &InspectConfig{
		TopN:     1,
		DumpPath: filepath.Join(tempDir, "trie-dump.bin"),
	})
	if err == nil {
		t.Fatal("expected error for unknown root, got nil")
	}
}

// TestSummarizeRejectsTruncatedDump ensures Summarize fails fast on a
// dump that is not a multiple of the record size.
func TestSummarizeRejectsTruncatedDump(t *testing.T) {
	tempDir := t.TempDir()
	dumpPath := filepath.Join(tempDir, "truncated.bin")
	if err := os.WriteFile(dumpPath, []byte{1, 2, 3}, 0o644); err != nil {
		t.Fatalf("write truncated dump: %v", err)
	}
	err := Summarize(dumpPath, &InspectConfig{TopN: 1})
	if err == nil {
		t.Fatal("expected error for truncated dump")
	}
}

// TestSummarizeRejectsMissingAccountSentinel ensures Summarize refuses
// dumps that contain only storage records (no account sentinel).
func TestSummarizeRejectsMissingAccountSentinel(t *testing.T) {
	tempDir := t.TempDir()
	dumpPath := filepath.Join(tempDir, "no-sentinel.bin")

	// One record with a non-zero owner (storage) and no account
	// sentinel. The record itself is all zeros except for the owner.
	var raw [inspectDumpRecordSize]byte
	raw[0] = 0x11
	if err := os.WriteFile(dumpPath, raw[:], 0o644); err != nil {
		t.Fatalf("write dump: %v", err)
	}
	err := Summarize(dumpPath, &InspectConfig{TopN: 1})
	if err == nil {
		t.Fatal("expected error for dump without account sentinel")
	}
}

// TestInspectContract inspects a single contract with populated storage
// and snapshot data, mirroring the upstream exercise of InspectContract.
func TestInspectContract(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	db := NewDatabase(diskdb)

	address := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	accountHash := crypto.Keccak256Hash(address.Bytes())

	storageTrie, err := New(common.Hash{}, db)
	if err != nil {
		t.Fatalf("open storage trie: %v", err)
	}
	storageSlots := make(map[common.Hash][]byte)
	for i := 0; i < 10; i++ {
		k := crypto.Keccak256Hash([]byte{byte(i)})
		v := []byte{byte(i + 1)}
		storageTrie.Update(k.Bytes(), v)
		storageSlots[k] = v
	}
	storageRoot, _, err := storageTrie.Commit(nil)
	if err != nil {
		t.Fatalf("commit storage trie: %v", err)
	}
	if err := db.Commit(storageRoot, false, nil); err != nil {
		t.Fatalf("flush storage trie: %v", err)
	}

	account := types.StateAccount{
		Nonce:          1,
		Balance:        big.NewInt(1000),
		Root:           storageRoot,
		KeccakCodeHash: codehash.EmptyKeccakCodeHash.Bytes(),
	}
	accountRLP, err := rlp.EncodeToBytes(&account)
	if err != nil {
		t.Fatalf("encode account: %v", err)
	}

	// Plain Trie: keys are stored by their hashed form (matching state db
	// layout), which is what InspectContract reads via
	// accountTrie.TryGet(crypto.Keccak256(address)).
	accountTrie, err := New(common.Hash{}, db)
	if err != nil {
		t.Fatalf("open account trie: %v", err)
	}
	accountTrie.Update(crypto.Keccak256(address.Bytes()), accountRLP)
	stateRoot, _, err := accountTrie.Commit(nil)
	if err != nil {
		t.Fatalf("commit account trie: %v", err)
	}
	if err := db.Commit(stateRoot, false, nil); err != nil {
		t.Fatalf("flush account trie: %v", err)
	}

	// Populate snapshot entries so InspectContract can exercise the
	// snapshot accounting code path as well.
	rawdb.WriteAccountSnapshot(diskdb, accountHash, accountRLP)
	for k, v := range storageSlots {
		rawdb.WriteStorageSnapshot(diskdb, accountHash, k, v)
	}

	if err := InspectContract(db, diskdb, stateRoot, address); err != nil {
		t.Fatalf("InspectContract failed: %v", err)
	}
}

// TestInspectContractRejectsMissingAccount asserts that InspectContract
// refuses to operate on an address that is absent from the account trie,
// instead of silently producing an empty snapshot.
func TestInspectContractRejectsMissingAccount(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	db := NewDatabase(diskdb)
	// A valid but empty state root; any address lookup returns nil.
	err := InspectContract(db, diskdb, emptyRoot, common.HexToAddress("0xaabb"))
	if err == nil {
		t.Fatal("expected error for missing account")
	}
}

// TestInspectContractRejectsStorageless asserts that InspectContract
// refuses to operate on accounts that exist but have no storage trie.
func TestInspectContractRejectsStorageless(t *testing.T) {
	diskdb := rawdb.NewMemoryDatabase()
	db := NewDatabase(diskdb)

	address := common.HexToAddress("0xcafebabe0000000000000000000000000000dead")
	account := types.StateAccount{
		Nonce:          0,
		Balance:        big.NewInt(0),
		Root:           emptyRoot,
		KeccakCodeHash: codehash.EmptyKeccakCodeHash.Bytes(),
	}
	enc, err := rlp.EncodeToBytes(&account)
	if err != nil {
		t.Fatalf("encode account: %v", err)
	}
	accTrie, err := New(common.Hash{}, db)
	if err != nil {
		t.Fatalf("open account trie: %v", err)
	}
	accTrie.Update(crypto.Keccak256(address.Bytes()), enc)
	root, _, err := accTrie.Commit(nil)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := db.Commit(root, false, nil); err != nil {
		t.Fatalf("flush: %v", err)
	}

	err = InspectContract(db, diskdb, root, address)
	if err == nil {
		t.Fatal("expected error for storageless account")
	}
}

// -----------------------------------------------------------------------
// JSON shape helpers
// -----------------------------------------------------------------------

// inspectJSONOutput mirrors the shape of inspectSummary's MarshalJSON
// output — using storageStats for AccountTrie avoids a parallel type
// definition since only Levels and Summary are populated by inspect.
type inspectJSONOutput struct {
	AccountTrie    storageStats `json:"AccountTrie"`
	StorageSummary struct {
		TotalStorageTries uint64                 `json:"TotalStorageTries"`
		Totals            jsonLevel              `json:"Totals"`
		Levels            []jsonLevel            `json:"Levels"`
		DepthHistogram    [trieStatLevels]uint64 `json:"DepthHistogram"`
	} `json:"StorageSummary"`
}

func loadInspectJSON(t *testing.T, path string) inspectJSONOutput {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	var out inspectJSONOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("failed to decode %s: %v", path, err)
	}
	return out
}

func assertStorageTotalsMatchLevels(t *testing.T, out inspectJSONOutput) {
	t.Helper()
	var fromLevels jsonLevel
	for _, level := range out.StorageSummary.Levels {
		fromLevels.Short += level.Short
		fromLevels.Full += level.Full
		fromLevels.Value += level.Value
		fromLevels.Size += level.Size
	}
	if fromLevels != out.StorageSummary.Totals {
		t.Fatalf("storage totals mismatch: levels=%+v totals=%+v", fromLevels, out.StorageSummary.Totals)
	}
}

func assertAccountTotalsMatchLevels(t *testing.T, account storageStats) {
	t.Helper()
	var fromLevels jsonLevel
	for _, level := range account.Levels {
		fromLevels.Short += level.Short
		fromLevels.Full += level.Full
		fromLevels.Value += level.Value
		fromLevels.Size += level.Size
	}
	if fromLevels != account.Summary {
		t.Fatalf("account totals mismatch: levels=%+v totals=%+v", fromLevels, account.Summary)
	}
}
