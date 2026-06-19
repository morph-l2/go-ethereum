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
