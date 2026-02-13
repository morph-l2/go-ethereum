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
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/params"
)

// TestMPTForkTransition tests the behavior around MPT fork time
func TestMPTForkTransition(t *testing.T) {
	forkTime := uint64(1000)

	tests := []struct {
		name        string
		blockTime   uint64
		mptForkTime *uint64
		expectFork  bool
		description string
	}{
		{
			name:        "Before fork time",
			blockTime:   500,
			mptForkTime: &forkTime,
			expectFork:  false,
			description: "Block before MPT fork should not be in fork period",
		},
		{
			name:        "At fork time",
			blockTime:   1000,
			mptForkTime: &forkTime,
			expectFork:  true,
			description: "Block at exact fork time should be in fork period",
		},
		{
			name:        "After fork time",
			blockTime:   1500,
			mptForkTime: &forkTime,
			expectFork:  true,
			description: "Block after fork time should be in fork period",
		},
		{
			name:        "No fork configured",
			blockTime:   1500,
			mptForkTime: nil,
			expectFork:  false,
			description: "Without fork config, should never be in fork period",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &params.ChainConfig{
				MPTForkTime: tt.mptForkTime,
			}

			isFork := config.IsMPTFork(tt.blockTime)
			if isFork != tt.expectFork {
				t.Errorf("%s: Expected IsMPTFork=%v, got %v", tt.description, tt.expectFork, isFork)
			} else {
				t.Logf("✓ %s: IsMPTFork=%v (correct)", tt.description, isFork)
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
		// Note: Cross-format block mapping requires syncing blocks from different format
		// This scenario is tested separately in TestMPTNodeSyncsZkTrieBlocks
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

// TestMPTNodeSyncsZkTrieBlocks tests MPT node syncing zkTrie blocks after fork:
// 1. Generate zkTrie blocks (pre-fork)
// 2. MPT node syncs these blocks via InsertChain
// 3. Verify mappings created automatically (zkTrie header root → MPT disk root)
// 4. Verify state accessible via zkTrie header roots
func TestMPTNodeSyncsZkTrieBlocks(t *testing.T) {
	key, _ := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	address := crypto.PubkeyToAddress(key.PublicKey)
	funds := big.NewInt(1000000000000000)

	// Phase 1: Generate zkTrie chain
	zkTrieDB := rawdb.NewMemoryDatabase()
	zkTrieConfig := params.TestChainConfig.Clone()
	zkTrieConfig.Morph.UseZktrie = true

	zkGspec := &Genesis{
		Config:  zkTrieConfig,
		Alloc:   GenesisAlloc{address: {Balance: funds}},
		BaseFee: big.NewInt(params.InitialBaseFee),
	}
	zkGenesis := zkGspec.MustCommit(zkTrieDB)
	signer := types.LatestSigner(zkTrieConfig)

	// Generate zkTrie blocks with transactions
	zkTrieBlocks, _ := GenerateChain(zkTrieConfig, zkGenesis, ethash.NewFaker(), zkTrieDB, 3, func(i int, b *BlockGen) {
		recipient := common.Address{byte(i + 0x80)}
		tx, _ := types.SignTx(
			types.NewTransaction(
				b.TxNonce(address),
				recipient,
				big.NewInt(int64((i+1)*5000)),
				21000,
				b.header.BaseFee,
				nil,
			),
			signer,
			key,
		)
		b.AddTx(tx)
	})

	// Phase 2: MPT node with zkTrie genesis root
	mptDB := rawdb.NewMemoryDatabase()
	mptConfig := params.TestChainConfig.Clone()
	mptConfig.Morph.UseZktrie = false
	zkGenesisRoot := zkGenesis.Root()
	mptConfig.Morph.GenesisStateRoot = &zkGenesisRoot

	mptGspec := &Genesis{
		Config:  mptConfig,
		Alloc:   GenesisAlloc{address: {Balance: funds}},
		BaseFee: big.NewInt(params.InitialBaseFee),
	}

	_, err := mptGspec.Commit(mptDB)
	if err != nil {
		t.Fatalf("Failed to commit MPT genesis: %v", err)
	}

	// Verify genesis mapping
	if _, err := rawdb.ReadDiskStateRoot(mptDB, zkGenesisRoot); err != nil {
		t.Fatalf("Genesis mapping not created: %v", err)
	}

	// Create MPT blockchain (disable snapshot for cross-format)
	cacheConfig := &CacheConfig{
		TrieCleanLimit: 256,
		TrieDirtyLimit: 256,
		SnapshotLimit:  0,
	}
	mptChain, err := NewBlockChain(mptDB, cacheConfig, mptConfig, ethash.NewFaker(), vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create MPT blockchain: %v", err)
	}
	defer mptChain.Stop()

	// Phase 3: Sync zkTrie blocks (creates mappings automatically)
	n, err := mptChain.InsertChain(zkTrieBlocks)
	if err != nil {
		t.Fatalf("Failed to insert zkTrie blocks: inserted %d, error: %v", n, err)
	}

	// Phase 4: Verify mappings and state access
	for i, block := range zkTrieBlocks {
		zkHeaderRoot := block.Root()
		recipient := common.Address{byte(i + 0x80)}
		expectedBalance := big.NewInt(int64((i + 1) * 5000))

		// Verify mapping exists
		mptDiskRoot, err := rawdb.ReadDiskStateRoot(mptDB, zkHeaderRoot)
		if err != nil {
			t.Errorf("Block %d: No mapping for zkTrie root %x", i+1, zkHeaderRoot[:8])
			continue
		}
		if zkHeaderRoot == mptDiskRoot {
			t.Errorf("Block %d: Roots are same (expected different)", i+1)
			continue
		}

		// Verify mpt state access via zkTrie root
		state, err := mptChain.StateAt(zkHeaderRoot)
		if err != nil {
			t.Errorf("Block %d: Failed to access state: %v", i+1, err)
			continue
		}

		actualBalance := state.GetBalance(recipient)
		if actualBalance.Cmp(expectedBalance) != 0 {
			t.Errorf("Block %d: Balance mismatch: expected %v, got %v", i+1, expectedBalance, actualBalance)
		}
	}
}

// TestZkTrieNodeSyncsMPTBlocks tests zkTrie node syncing MPT blocks after fork:
// This simulates old zkTrie node continuing to run after fork and syncing new MPT blocks
// 1. Start with shared zkTrie genesis
// 2. Generate MPT blocks from another MPT node (post-fork)
// 3. zkTrie node syncs these MPT blocks via InsertChain
// 4. Verify mappings created and state accessible
func TestZkTrieNodeSyncsMPTBlocks(t *testing.T) {
	key, _ := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	address := crypto.PubkeyToAddress(key.PublicKey)
	funds := big.NewInt(1000000000000000)

	// Phase 1: Create shared zkTrie genesis (pre-fork state)
	zkGenesisDB := rawdb.NewMemoryDatabase()
	zkGenesisConfig := params.TestChainConfig.Clone()
	zkGenesisConfig.Morph.UseZktrie = true

	zkGspec := &Genesis{
		Config:  zkGenesisConfig,
		Alloc:   GenesisAlloc{address: {Balance: funds}},
		BaseFee: big.NewInt(params.InitialBaseFee),
	}
	sharedGenesis := zkGspec.MustCommit(zkGenesisDB)

	// Phase 2: MPT node (post-fork) generates MPT blocks using zkTrie genesis root
	mptDB := rawdb.NewMemoryDatabase()
	mptConfig := params.TestChainConfig.Clone()
	mptConfig.Morph.UseZktrie = false
	zkGenesisRoot := sharedGenesis.Root()
	mptConfig.Morph.GenesisStateRoot = &zkGenesisRoot

	mptGspec := &Genesis{
		Config:  mptConfig,
		Alloc:   GenesisAlloc{address: {Balance: funds}},
		BaseFee: big.NewInt(params.InitialBaseFee),
	}
	_, err := mptGspec.Commit(mptDB)
	if err != nil {
		t.Fatalf("Failed to commit MPT genesis: %v", err)
	}

	// MPT node generates blocks (post-fork blocks with MPT state roots)
	signer := types.LatestSigner(mptConfig)
	mptBlocks, _ := GenerateChain(mptConfig, sharedGenesis, ethash.NewFaker(), mptDB, 3, func(i int, b *BlockGen) {
		recipient := common.Address{byte(i + 0x90)}
		tx, _ := types.SignTx(
			types.NewTransaction(
				b.TxNonce(address),
				recipient,
				big.NewInt(int64((i+1)*6000)),
				21000,
				b.header.BaseFee,
				nil,
			),
			signer,
			key,
		)
		b.AddTx(tx)
	})

	// Phase 3: Old zkTrie node syncs these MPT blocks
	zkTrieDB := rawdb.NewMemoryDatabase()
	forkTime := uint64(10)
	zkTrieConfig := params.TestChainConfig.Clone()
	zkTrieConfig.Morph.UseZktrie = true
	zkTrieConfig.MPTForkTime = &forkTime // Enable cross-format validation skipping

	// zkTrie node uses same zkTrie genesis (no GenesisStateRoot override)
	zkGspec2 := &Genesis{
		Config:  zkTrieConfig,
		Alloc:   GenesisAlloc{address: {Balance: funds}},
		BaseFee: big.NewInt(params.InitialBaseFee),
	}
	zkGspec2.MustCommit(zkTrieDB)

	cacheConfig := &CacheConfig{
		TrieCleanLimit: 256,
		TrieDirtyLimit: 256,
		SnapshotLimit:  0,
	}
	zkChain, err := NewBlockChain(zkTrieDB, cacheConfig, zkTrieConfig, ethash.NewFaker(), vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create zkTrie blockchain: %v", err)
	}
	defer zkChain.Stop()

	// Sync MPT blocks (block headers have MPT roots)
	n, err := zkChain.InsertChain(mptBlocks)
	if err != nil {
		t.Fatalf("Failed to insert MPT blocks: inserted %d, error: %v", n, err)
	}

	// Phase 4: Verify mappings and state access
	for i, block := range mptBlocks {
		mptHeaderRoot := block.Root()
		recipient := common.Address{byte(i + 0x90)}
		expectedBalance := big.NewInt(int64((i + 1) * 6000))

		// Verify mapping exists (MPT header root → zkTrie disk root)
		zkDiskRoot, err := rawdb.ReadDiskStateRoot(zkTrieDB, mptHeaderRoot)
		if err != nil {
			t.Errorf("Block %d: No mapping for MPT root %x", i+1, mptHeaderRoot[:8])
			continue
		}
		if mptHeaderRoot == zkDiskRoot {
			t.Errorf("Block %d: Roots are same (expected different)", i+1)
			continue
		}

		// Verify state access via MPT header root
		state, err := zkChain.StateAt(mptHeaderRoot)
		if err != nil {
			t.Errorf("Block %d: Failed to access state: %v", i+1, err)
			continue
		}

		actualBalance := state.GetBalance(recipient)
		if actualBalance.Cmp(expectedBalance) != 0 {
			t.Errorf("Block %d: Balance mismatch: expected %v, got %v", i+1, expectedBalance, actualBalance)
		}
	}
}
