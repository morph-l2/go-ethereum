// Copyright 2026 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"flag"
	"math/big"
	"testing"

	"gopkg.in/urfave/cli.v1"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/ethdb"
)

func newInspectTrieContext(t *testing.T, args ...string) *cli.Context {
	t.Helper()

	set := flag.NewFlagSet("inspect-trie-test", flag.ContinueOnError)
	if err := set.Parse(args); err != nil {
		t.Fatalf("parse args: %v", err)
	}
	return cli.NewContext(nil, set, nil)
}

func writeCanonicalHeader(db ethdb.KeyValueWriter, header *types.Header) {
	rawdb.WriteHeader(db, header)
	rawdb.WriteCanonicalHash(db, header.Hash(), header.Number.Uint64())
}

func TestResolveInspectTargetSnapshotUsesHeadMetadata(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	root := common.HexToHash("0x1111")
	header := &types.Header{
		Number: big.NewInt(42),
		Time:   123456,
		Root:   root,
	}
	writeCanonicalHeader(db, header)
	rawdb.WriteHeadHeaderHash(db, header.Hash())
	rawdb.WriteSnapshotRoot(db, root)

	gotRoot, gotNumber, gotTime, gotKnown, err := resolveInspectTarget(newInspectTrieContext(t, "snapshot"), db)
	if err != nil {
		t.Fatalf("resolveInspectTarget returned error: %v", err)
	}
	if gotRoot != root || gotNumber != header.Number.Uint64() || gotTime != header.Time || !gotKnown {
		t.Fatalf("unexpected snapshot target: root=%x number=%d time=%d known=%v", gotRoot, gotNumber, gotTime, gotKnown)
	}
}

func TestResolveInspectTargetSnapshotUsesRecoveryMetadata(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	snapshotRoot := common.HexToHash("0x2222")
	recoveryHeader := &types.Header{
		Number: big.NewInt(12),
		Time:   98765,
		Root:   snapshotRoot,
	}
	headHeader := &types.Header{
		Number: big.NewInt(15),
		Time:   99999,
		Root:   common.HexToHash("0x3333"),
	}
	writeCanonicalHeader(db, recoveryHeader)
	writeCanonicalHeader(db, headHeader)
	rawdb.WriteHeadHeaderHash(db, headHeader.Hash())
	rawdb.WriteSnapshotRoot(db, snapshotRoot)
	rawdb.WriteSnapshotRecoveryNumber(db, recoveryHeader.Number.Uint64())

	gotRoot, gotNumber, gotTime, gotKnown, err := resolveInspectTarget(newInspectTrieContext(t, "snapshot"), db)
	if err != nil {
		t.Fatalf("resolveInspectTarget returned error: %v", err)
	}
	if gotRoot != snapshotRoot || gotNumber != recoveryHeader.Number.Uint64() || gotTime != recoveryHeader.Time || !gotKnown {
		t.Fatalf("unexpected recovery snapshot target: root=%x number=%d time=%d known=%v", gotRoot, gotNumber, gotTime, gotKnown)
	}
}

func TestResolveInspectTargetSnapshotUnknownMetadata(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	snapshotRoot := common.HexToHash("0x4444")
	headHeader := &types.Header{
		Number: big.NewInt(8),
		Time:   55555,
		Root:   common.HexToHash("0x5555"),
	}
	writeCanonicalHeader(db, headHeader)
	rawdb.WriteHeadHeaderHash(db, headHeader.Hash())
	rawdb.WriteSnapshotRoot(db, snapshotRoot)

	gotRoot, gotNumber, gotTime, gotKnown, err := resolveInspectTarget(newInspectTrieContext(t, "snapshot"), db)
	if err != nil {
		t.Fatalf("resolveInspectTarget returned error: %v", err)
	}
	if gotRoot != snapshotRoot || gotNumber != 0 || gotTime != 0 || gotKnown {
		t.Fatalf("unexpected unknown snapshot target: root=%x number=%d time=%d known=%v", gotRoot, gotNumber, gotTime, gotKnown)
	}
}
