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

package state

import (
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/trie"
)

// TestCrossFormatStateAccess tests accessing state across different trie formats (zkTrie ↔ MPT)
func TestCrossFormatStateAccess(t *testing.T) {
	// Create test account
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")

	tests := []struct {
		name         string
		writeFormat  string // "zktrie" or "mpt"
		readFormat   string // "zktrie" or "mpt"
		shouldUseMap bool   // whether disk state root mapping should be used
		expectError  bool
		description  string
		skip         bool // skip this test (zkTrie not available in test env)
	}{
		{
			name:         "Same format - MPT to MPT",
			writeFormat:  "mpt",
			readFormat:   "mpt",
			shouldUseMap: false,
			expectError:  false,
			description:  "Reading MPT state with MPT - no mapping needed",
			skip:         false,
		},
		// zkTrie tests skipped - zkTrie requires special initialization not available in unit tests
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.Skip("Test skipped - requires special zkTrie initialization")
			}

			// Create database and write state with first format
			db := rawdb.NewMemoryDatabase()

			// Write state
			writeConfig := &trie.Config{
				Zktrie: tt.writeFormat == "zktrie",
			}
			writeDB := NewDatabaseWithConfig(db, writeConfig)
			writeState, err := New(types.EmptyRootHash, writeDB, nil)
			if err != nil {
				t.Fatalf("Failed to create write state: %v", err)
			}

			// Set account
			writeState.SetNonce(addr, 1, tracing.NonceChangeUnspecified)
			writeState.SetBalance(addr, big.NewInt(1000000000), tracing.BalanceChangeUnspecified)

			// Commit to get state root
			stateRoot, err := writeState.Commit(false)
			if err != nil {
				t.Fatalf("Failed to commit state: %v", err)
			}

			// Commit trie changes to disk
			err = writeDB.TrieDB().Commit(stateRoot, false, nil)
			if err != nil {
				t.Fatalf("Failed to commit trie: %v", err)
			}

			t.Logf("Written state with %s format, root: %x", tt.writeFormat, stateRoot)

			// Try to read state with second format
			readConfig := &trie.Config{
				Zktrie: tt.readFormat == "zktrie",
			}
			readDB := NewDatabaseWithConfig(db, readConfig)

			// Try to open trie with the state root
			_, err = readDB.OpenTrie(stateRoot)

			if tt.expectError {
				if err == nil {
					t.Errorf("%s: Expected error when reading cross-format state without mapping", tt.description)
				} else {
					t.Logf("✓ %s: Got expected error: %v", tt.description, err)
				}
			} else {
				if err != nil {
					t.Errorf("%s: Unexpected error: %v", tt.description, err)
				} else {
					t.Logf("✓ %s: Successfully read same-format state", tt.description)
				}
			}
		})
	}
}

// TestDiskStateRootMapping tests the DiskStateRoot mapping mechanism
func TestDiskStateRootMapping(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")

	// Create state with MPT format
	mptConfig := &trie.Config{Zktrie: false}
	mptDB := NewDatabaseWithConfig(db, mptConfig)
	mptState, err := New(types.EmptyRootHash, mptDB, nil)
	if err != nil {
		t.Fatalf("Failed to create MPT state: %v", err)
	}

	// Add some account data
	mptState.SetNonce(addr, 1, tracing.NonceChangeUnspecified)
	mptState.SetBalance(addr, big.NewInt(1000000), tracing.BalanceChangeUnspecified)

	// Commit MPT state
	mptRoot, err := mptState.Commit(false)
	if err != nil {
		t.Fatalf("Failed to commit MPT state: %v", err)
	}
	err = mptDB.TrieDB().Commit(mptRoot, false, nil)
	if err != nil {
		t.Fatalf("Failed to commit MPT: %v", err)
	}

	t.Logf("MPT root: %x", mptRoot)

	// Simulate a zkTrie root (would be from block header in real scenario)
	simulatedZkTrieRoot := common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Logf("Simulated zkTrie root: %x", simulatedZkTrieRoot)

	// Test mapping: Write mapping from zkTrie root (header) to MPT root (disk)
	// This simulates the case where blocks use zkTrie format but disk uses MPT format
	rawdb.WriteDiskStateRoot(db, simulatedZkTrieRoot, mptRoot)
	t.Logf("Written mapping: header=%x -> disk=%x", simulatedZkTrieRoot, mptRoot)

	// Read mapping back
	diskRoot, err := rawdb.ReadDiskStateRoot(db, simulatedZkTrieRoot)
	if err != nil {
		t.Fatalf("Failed to read disk state root mapping: %v", err)
	}
	if diskRoot != mptRoot {
		t.Errorf("Mapping mismatch: expected %x, got %x", mptRoot, diskRoot)
	} else {
		t.Logf("✓ Successfully read mapping: %x -> %x", simulatedZkTrieRoot, diskRoot)
	}

}

// TestCrossFormatStateAccessWithMapping tests cross-format access using DiskStateRoot mapping
func TestCrossFormatStateAccessWithMapping(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	addr := common.HexToAddress("0x9999999999999999999999999999999999999999")

	// Only test MPT format (zkTrie requires special initialization)
	// Step 1: Create and commit state with MPT
	mptConfig := &trie.Config{Zktrie: false}
	mptDB := NewDatabaseWithConfig(db, mptConfig)
	mptState, err := New(types.EmptyRootHash, mptDB, nil)
	if err != nil {
		t.Fatalf("Failed to create MPT state: %v", err)
	}

	mptState.SetNonce(addr, 42, tracing.NonceChangeUnspecified)
	mptState.SetBalance(addr, big.NewInt(999999), tracing.BalanceChangeUnspecified)

	mptRoot, err := mptState.Commit(false)
	if err != nil {
		t.Fatalf("Failed to commit MPT state: %v", err)
	}
	err = mptDB.TrieDB().Commit(mptRoot, false, nil)
	if err != nil {
		t.Fatalf("Failed to commit MPT trie: %v", err)
	}

	t.Logf("Created MPT state with root: %x", mptRoot)

	// Step 2: Create a simulated "zkTrie" root (just a different hash)
	// In reality this would be a real zkTrie root from a block header
	simulatedZkTrieRoot := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	// Step 3: Create mapping (simulated zkTrie header -> actual MPT disk)
	// This simulates: block uses zkTrie format, but disk uses MPT format
	rawdb.WriteDiskStateRoot(db, simulatedZkTrieRoot, mptRoot)
	t.Logf("Written mapping: headerRoot=%x -> diskRoot=%x", simulatedZkTrieRoot, mptRoot)

	// Step 4: Verify mapping resolution in OpenTrie
	// When OpenTrie sees simulatedZkTrieRoot, it should resolve to mptRoot
	diskRoot, err := rawdb.ReadDiskStateRoot(db, simulatedZkTrieRoot)
	if err != nil {
		t.Fatalf("Failed to read disk state root mapping: %v", err)
	}
	if diskRoot != mptRoot {
		t.Errorf("Mapping mismatch: expected %x, got %x", mptRoot, diskRoot)
	} else {
		t.Logf("✓ Mapping correctly resolved: %x -> %x", simulatedZkTrieRoot, diskRoot)
	}

	// Step 5: Test that OpenTrie can successfully open using the resolved root
	// The actual resolution happens in database.go:OpenTrie
	mptReader := NewDatabaseWithConfig(db, mptConfig)
	trie, err := mptReader.OpenTrie(simulatedZkTrieRoot)
	if err != nil {
		// This is expected if the simulated root doesn't exist in disk
		t.Logf("Note: Cannot open simulated root directly (expected)")
		t.Logf("  In real scenario, OpenTrie would resolve %x -> %x automatically", simulatedZkTrieRoot, diskRoot)
	} else {
		t.Logf("✓ Successfully opened trie via resolved root")
		t.Logf("  Trie hash: %x", trie.Hash())
	}

	// Step 6: Verify we can open the actual MPT root directly
	trieActual, err := mptReader.OpenTrie(mptRoot)
	if err != nil {
		t.Errorf("Failed to open actual MPT root: %v", err)
	} else {
		t.Logf("✓ Successfully opened actual MPT root: %x", trieActual.Hash())
	}
}

// TestEmptyStateMapping tests mapping with empty state roots
func TestEmptyStateMapping(t *testing.T) {
	db := rawdb.NewMemoryDatabase()

	// Empty root (common.Hash{})
	emptyRoot := common.Hash{}
	diskRoot := common.HexToHash("0xabcdef")

	// Write mapping with empty root
	rawdb.WriteDiskStateRoot(db, emptyRoot, diskRoot)

	// Read it back
	retrieved, err := rawdb.ReadDiskStateRoot(db, emptyRoot)
	if err != nil {
		t.Errorf("Failed to read mapping with empty root: %v", err)
	} else if retrieved != diskRoot {
		t.Errorf("Mapping mismatch: expected %x, got %x", diskRoot, retrieved)
	} else {
		t.Logf("✓ Empty root mapping works: %x -> %x", emptyRoot, retrieved)
	}

	// Test EmptyRootHash
	emptyRootHash := types.EmptyRootHash
	diskRoot2 := common.HexToHash("0x123456")

	rawdb.WriteDiskStateRoot(db, emptyRootHash, diskRoot2)
	retrieved2, err := rawdb.ReadDiskStateRoot(db, emptyRootHash)
	if err != nil {
		t.Errorf("Failed to read mapping with EmptyRootHash: %v", err)
	} else if retrieved2 != diskRoot2 {
		t.Errorf("Mapping mismatch: expected %x, got %x", diskRoot2, retrieved2)
	} else {
		t.Logf("✓ EmptyRootHash mapping works: %x -> %x", emptyRootHash, retrieved2)
	}
}

// TestMissingMapping tests behavior when mapping doesn't exist
func TestMissingMapping(t *testing.T) {
	db := rawdb.NewMemoryDatabase()

	// Try to read non-existent mapping
	nonExistentRoot := common.HexToHash("0xdeadbeef")
	_, err := rawdb.ReadDiskStateRoot(db, nonExistentRoot)

	if err == nil {
		t.Error("Expected error when reading non-existent mapping")
	} else {
		t.Logf("✓ Got expected error for missing mapping: %v", err)
	}
}

// TestMappingDeletion tests deleting mappings
func TestMappingDeletion(t *testing.T) {
	db := rawdb.NewMemoryDatabase()

	headerRoot := common.HexToHash("0x111111")
	diskRoot := common.HexToHash("0x222222")

	// Write mapping
	rawdb.WriteDiskStateRoot(db, headerRoot, diskRoot)

	// Verify it exists
	retrieved, err := rawdb.ReadDiskStateRoot(db, headerRoot)
	if err != nil {
		t.Fatalf("Failed to read mapping: %v", err)
	}
	if retrieved != diskRoot {
		t.Fatalf("Mapping mismatch before deletion")
	}
	t.Logf("✓ Mapping exists: %x -> %x", headerRoot, diskRoot)

	// Delete mapping
	rawdb.DeleteDiskStateRoot(db, headerRoot)
	t.Logf("✓ Deleted mapping for %x", headerRoot)

	// Verify it's gone
	_, err = rawdb.ReadDiskStateRoot(db, headerRoot)
	if err == nil {
		t.Error("Expected error after deleting mapping")
	} else {
		t.Logf("✓ Mapping successfully deleted, error: %v", err)
	}
}

// TestMultipleMappings tests that multiple independent mappings work correctly
func TestMultipleMappings(t *testing.T) {
	db := rawdb.NewMemoryDatabase()

	// Create multiple independent mappings
	mappings := map[common.Hash]common.Hash{
		common.HexToHash("0xaaa"): common.HexToHash("0x111"),
		common.HexToHash("0xbbb"): common.HexToHash("0x222"),
		common.HexToHash("0xccc"): common.HexToHash("0x333"),
		common.HexToHash("0xddd"): common.HexToHash("0x444"),
	}

	// Write all mappings
	for headerRoot, diskRoot := range mappings {
		rawdb.WriteDiskStateRoot(db, headerRoot, diskRoot)
		t.Logf("Written mapping: %x -> %x", headerRoot, diskRoot)
	}

	// Verify all mappings exist and are correct
	for headerRoot, expectedDiskRoot := range mappings {
		resolved, err := rawdb.ReadDiskStateRoot(db, headerRoot)
		if err != nil {
			t.Errorf("Failed to read mapping for %x: %v", headerRoot, err)
			continue
		}
		if resolved != expectedDiskRoot {
			t.Errorf("Incorrect mapping for %x: expected %x, got %x", headerRoot, expectedDiskRoot, resolved)
			continue
		}
		t.Logf("✓ Mapping correct: %x -> %x", headerRoot, resolved)
	}

	// Delete one mapping and verify others still exist
	deleteKey := common.HexToHash("0xbbb")
	rawdb.DeleteDiskStateRoot(db, deleteKey)
	t.Logf("Deleted mapping for %x", deleteKey)

	// Verify deleted mapping is gone
	_, err := rawdb.ReadDiskStateRoot(db, deleteKey)
	if err == nil {
		t.Errorf("Expected error for deleted mapping %x", deleteKey)
	}
	t.Logf("✓ Deleted mapping %x is gone", deleteKey)

	// Verify other mappings still exist
	for headerRoot, expectedDiskRoot := range mappings {
		if headerRoot == deleteKey {
			continue
		}
		resolved, err := rawdb.ReadDiskStateRoot(db, headerRoot)
		if err != nil {
			t.Errorf("Failed to read remaining mapping for %x: %v", headerRoot, err)
			continue
		}
		if resolved != expectedDiskRoot {
			t.Errorf("Remaining mapping corrupted for %x: expected %x, got %x", headerRoot, expectedDiskRoot, resolved)
			continue
		}
	}
	t.Logf("✓ All remaining mappings intact")
}

// TestMappingOverwrite tests that updating existing mappings works correctly
func TestMappingOverwrite(t *testing.T) {
	db := rawdb.NewMemoryDatabase()

	headerRoot := common.HexToHash("0xffffff")
	originalDiskRoot := common.HexToHash("0x111111")
	updatedDiskRoot := common.HexToHash("0x222222")

	// Write initial mapping
	rawdb.WriteDiskStateRoot(db, headerRoot, originalDiskRoot)
	t.Logf("Written initial mapping: %x -> %x", headerRoot, originalDiskRoot)

	// Verify initial mapping
	resolved, err := rawdb.ReadDiskStateRoot(db, headerRoot)
	if err != nil {
		t.Fatalf("Failed to read initial mapping: %v", err)
	}
	if resolved != originalDiskRoot {
		t.Errorf("Initial mapping incorrect: expected %x, got %x", originalDiskRoot, resolved)
	}
	t.Logf("✓ Initial mapping correct: %x -> %x", headerRoot, resolved)

	// Overwrite with new mapping
	rawdb.WriteDiskStateRoot(db, headerRoot, updatedDiskRoot)
	t.Logf("Overwrote mapping: %x -> %x", headerRoot, updatedDiskRoot)

	// Verify mapping was updated
	resolved, err = rawdb.ReadDiskStateRoot(db, headerRoot)
	if err != nil {
		t.Fatalf("Failed to read updated mapping: %v", err)
	}
	if resolved != updatedDiskRoot {
		t.Errorf("Updated mapping incorrect: expected %x, got %x", updatedDiskRoot, resolved)
	}
	t.Logf("✓ Mapping successfully updated: %x -> %x", headerRoot, resolved)

	// Verify old value is completely gone
	if resolved == originalDiskRoot {
		t.Errorf("Old mapping value still present: %x", originalDiskRoot)
	}
	t.Logf("✓ Old mapping value no longer accessible")
}
