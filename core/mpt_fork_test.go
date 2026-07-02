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

package core

import (
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/consensus/ethash"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/params"
)

// TestJadeForkTransition tests the behavior around Jade fork time
func TestJadeForkTransition(t *testing.T) {
	forkTime := uint64(1000)

	tests := []struct {
		name         string
		blockTime    uint64
		jadeForkTime *uint64
		expectFork   bool
		description  string
	}{
		{
			name:         "Before fork time",
			blockTime:    500,
			jadeForkTime: &forkTime,
			expectFork:   false,
			description:  "Block before Jade fork should not be in fork period",
		},
		{
			name:         "At fork time",
			blockTime:    1000,
			jadeForkTime: &forkTime,
			expectFork:   true,
			description:  "Block at exact fork time should be in fork period",
		},
		{
			name:         "After fork time",
			blockTime:    1500,
			jadeForkTime: &forkTime,
			expectFork:   true,
			description:  "Block after fork time should be in fork period",
		},
		{
			name:         "No fork configured",
			blockTime:    1500,
			jadeForkTime: nil,
			expectFork:   false,
			description:  "Without fork config, should never be in fork period",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &params.ChainConfig{
				JadeForkTime: tt.jadeForkTime,
			}

			isFork := config.IsJadeFork(tt.blockTime)
			if isFork != tt.expectFork {
				t.Errorf("%s: Expected IsJadeFork=%v, got %v", tt.description, tt.expectFork, isFork)
			} else {
				t.Logf("✓ %s: IsJadeFork=%v (correct)", tt.description, isFork)
			}
		})
	}
}

// TestDiskStateRootInBlockProcessing tests DiskStateRoot mapping during block processing
// Tests both same-format (no mapping) and cross-format (with mapping) scenarios
func TestDiskStateRootInBlockProcessing(t *testing.T) {
	key, _ := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	address := crypto.PubkeyToAddress(key.PublicKey)
	funds := big.NewInt(1000000000000000)

	tests := []struct {
		name            string
		useZktrie       bool
		genesisOverride bool // Whether to use cross-format genesis
		expectMapping   bool
	}{
		{
			name:            "Pure MPT - no mapping",
			useZktrie:       false,
			genesisOverride: false,
			expectMapping:   false,
		},
		{
			name:            "Pure zkTrie - no mapping",
			useZktrie:       true,
			genesisOverride: false,
			expectMapping:   false,
		},
		// Note: Cross-format (zkTrie<->MPT) block sync was removed together with the
		// retirement of the zkTrie storage mode; only same-format MPT mapping remains.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := rawdb.NewMemoryDatabase()

			config := params.TestChainConfig.Clone()
			config.Morph.UseZktrie = tt.useZktrie

			gspec := &Genesis{
				Config:  config,
				Alloc:   GenesisAlloc{address: {Balance: funds}},
				BaseFee: big.NewInt(params.InitialBaseFee),
			}
			genesis := gspec.MustCommit(db)
			signer := types.LatestSigner(config)

			// Generate blocks with transactions
			chain, _ := GenerateChain(config, genesis, ethash.NewFaker(), db, 3, func(i int, b *BlockGen) {
				to := common.Address{byte(i + 0x20)}
				tx, _ := types.SignTx(
					types.NewTransaction(
						b.TxNonce(address),
						to,
						big.NewInt(int64((i+1)*10000)),
						21000,
						b.header.BaseFee,
						nil,
					),
					signer,
					key,
				)
				b.AddTx(tx)
			})

			// Insert blocks
			cacheConfig := &CacheConfig{
				TrieCleanLimit: 256,
				TrieDirtyLimit: 256,
				SnapshotLimit:  0,
			}
			blockchain, err := NewBlockChain(db, cacheConfig, config, ethash.NewFaker(), vm.Config{}, nil, nil)
			if err != nil {
				t.Fatalf("Failed to create blockchain: %v", err)
			}
			defer blockchain.Stop()

			_, err = blockchain.InsertChain(chain)
			if err != nil {
				t.Fatalf("Failed to insert chain: %v", err)
			}

			// Verify mapping existence matches expectation
			for i, block := range chain {
				stateRoot := block.Root()
				_, err := rawdb.ReadDiskStateRoot(db, stateRoot)
				hasMapping := err == nil

				if hasMapping != tt.expectMapping {
					t.Errorf("Block %d: expected mapping=%v, got=%v", i+1, tt.expectMapping, hasMapping)
				}
			}
		})
	}
}

// TestValidateStateRootGateAcrossJade verifies the state-root validation gate that
// replaces the retired zkTrie storage mode: state is always MPT, so a header whose
// (legacy zkTrie) state root differs from the locally computed MPT root must be
// ACCEPTED before the Jade fork (gate skips validation) and REJECTED at/after it.
func TestValidateStateRootGateAcrossJade(t *testing.T) {
	jadeTime := uint64(1000)

	// Build a chain config with Jade at jadeTime. State backend is always MPT.
	config := params.TestChainConfig.Clone()
	config.Morph.UseZktrie = false
	config.JadeForkTime = &jadeTime

	db := rawdb.NewMemoryDatabase()
	gspec := &Genesis{
		Config:  config,
		BaseFee: big.NewInt(params.InitialBaseFee),
	}
	genesis := gspec.MustCommit(db)

	// One empty block so its receipts/bloom are trivially valid; we only tamper Root.
	blocks, _ := GenerateChain(config, genesis, ethash.NewFaker(), db, 1, nil)
	block := blocks[0]

	bc, err := NewBlockChain(db, nil, config, ethash.NewFaker(), vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create blockchain: %v", err)
	}
	defer bc.Stop()

	// A fresh, empty MPT state. Its IntermediateRoot is the MPT empty root, which we
	// force to differ from the (mismatched) header root below.
	statedb, err := state.New(common.Hash{}, state.NewDatabase(db), nil)
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	localRoot := statedb.IntermediateRoot(config.IsEIP158(block.Number()))

	// Forge a header whose state root deliberately does NOT match the local MPT root,
	// emulating a legacy zkTrie-format root carried in a pre-Jade header.
	mismatchedRoot := common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if mismatchedRoot == localRoot {
		t.Fatal("test setup: mismatched root unexpectedly equals local root")
	}

	makeBlock := func(blockTime uint64) *types.Block {
		h := block.Header()
		h.Time = blockTime
		h.Root = mismatchedRoot
		return block.WithSeal(h)
	}

	validator := bc.Validator()

	t.Run("pre-Jade accepts mismatched root", func(t *testing.T) {
		preJade := makeBlock(jadeTime - 1)
		if config.IsJadeFork(preJade.Time()) {
			t.Fatal("test setup: block should be pre-Jade")
		}
		// Empty block: nil receipts produce the empty bloom/receipt root carried in
		// the header, so only the state-root gate is exercised.
		if err := validator.ValidateState(preJade, statedb, nil, preJade.GasUsed()); err != nil {
			t.Fatalf("pre-Jade block with mismatched state root should be accepted, got: %v", err)
		}
	})

	t.Run("post-Jade rejects mismatched root", func(t *testing.T) {
		postJade := makeBlock(jadeTime)
		if !config.IsJadeFork(postJade.Time()) {
			t.Fatal("test setup: block should be post-Jade")
		}
		if err := validator.ValidateState(postJade, statedb, nil, postJade.GasUsed()); err == nil {
			t.Fatal("post-Jade block with mismatched state root should be rejected, got nil error")
		}
	})
}

// TestGenesisStateRootMappingAccess verifies that when GenesisStateRoot pins the
// genesis header.Root to a legacy (zkTrie) root, the genesis state committed in MPT
// is still reachable via StateAt(headerRoot) through the DiskStateRoot mapping.
func TestGenesisStateRootMappingAccess(t *testing.T) {
	key, _ := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	address := crypto.PubkeyToAddress(key.PublicKey)
	funds := big.NewInt(1000000000000000)

	db := rawdb.NewMemoryDatabase()
	config := params.TestChainConfig.Clone()
	config.Morph.UseZktrie = false

	// Pin genesis header.Root to a legacy root distinct from the real MPT root.
	legacyRoot := common.HexToHash("0xcafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe")
	config.Morph.GenesisStateRoot = &legacyRoot

	gspec := &Genesis{
		Config:  config,
		Alloc:   GenesisAlloc{address: {Balance: funds}},
		BaseFee: big.NewInt(params.InitialBaseFee),
	}
	genesisBlock := gspec.MustCommit(db)

	// The genesis block hash/root must be pinned to the configured legacy root.
	if genesisBlock.Root() != legacyRoot {
		t.Fatalf("expected genesis root pinned to legacy root %x, got %x", legacyRoot, genesisBlock.Root())
	}

	// A DiskStateRoot(legacyRoot -> mptRoot) mapping must have been recorded.
	mptRoot, err := rawdb.ReadDiskStateRoot(db, legacyRoot)
	if err != nil {
		t.Fatalf("expected DiskStateRoot mapping for legacy genesis root: %v", err)
	}
	if mptRoot == legacyRoot {
		t.Fatal("mapped MPT root should differ from the legacy genesis root")
	}

	bc, err := NewBlockChain(db, nil, config, ethash.NewFaker(), vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create blockchain: %v", err)
	}
	defer bc.Stop()

	// StateAt(headerRoot) must resolve the legacy root via the mapping and expose
	// the genesis allocation committed in MPT.
	st, err := bc.StateAt(genesisBlock.Root())
	if err != nil {
		t.Fatalf("StateAt(genesis header root) failed: %v", err)
	}
	if got := st.GetBalance(address); got.Cmp(funds) != 0 {
		t.Fatalf("genesis balance mismatch via legacy root: got %v, want %v", got, funds)
	}
}
