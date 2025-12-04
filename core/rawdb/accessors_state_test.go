// Copyright 2025 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package rawdb

import (
	"testing"

	"github.com/morph-l2/go-ethereum/common"
)

func TestDiskStateRoot(t *testing.T) {
	db := NewMemoryDatabase()

	headerRoot := common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1")
	diskRoot := common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb2")

	// Test Write and Read
	WriteDiskStateRoot(db, headerRoot, diskRoot)

	got, err := ReadDiskStateRoot(db, headerRoot)
	if err != nil {
		t.Fatalf("ReadDiskStateRoot failed: %v", err)
	}
	if got != diskRoot {
		t.Fatalf("DiskStateRoot mismatch: got %x, want %x", got, diskRoot)
	}

	// Test reading non-existent mapping
	nonExistent := common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc3")
	_, err = ReadDiskStateRoot(db, nonExistent)
	if err == nil {
		t.Fatal("Expected error for non-existent mapping, got nil")
	}

	// Test Delete
	DeleteDiskStateRoot(db, headerRoot)
	_, err = ReadDiskStateRoot(db, headerRoot)
	if err == nil {
		t.Fatal("Expected error after deletion, got nil")
	}
}

func TestDiskStateRootMultiple(t *testing.T) {
	db := NewMemoryDatabase()

	// Create multiple mappings
	mappings := map[common.Hash]common.Hash{
		common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"): common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"): common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333"): common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
	}

	// Write all mappings
	for headerRoot, diskRoot := range mappings {
		WriteDiskStateRoot(db, headerRoot, diskRoot)
	}

	// Verify all mappings
	for headerRoot, expectedDiskRoot := range mappings {
		gotDiskRoot, err := ReadDiskStateRoot(db, headerRoot)
		if err != nil {
			t.Fatalf("ReadDiskStateRoot failed for %x: %v", headerRoot, err)
		}
		if gotDiskRoot != expectedDiskRoot {
			t.Fatalf("DiskStateRoot mismatch for %x: got %x, want %x", headerRoot, gotDiskRoot, expectedDiskRoot)
		}
	}

	// Delete one mapping
	deleteTarget := common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
	DeleteDiskStateRoot(db, deleteTarget)

	// Verify deletion
	_, err := ReadDiskStateRoot(db, deleteTarget)
	if err == nil {
		t.Fatal("Expected error after deletion, got nil")
	}

	// Verify others still exist
	for headerRoot := range mappings {
		if headerRoot == deleteTarget {
			continue
		}
		_, err := ReadDiskStateRoot(db, headerRoot)
		if err != nil {
			t.Fatalf("Mapping for %x should still exist after deleting another mapping", headerRoot)
		}
	}
}
