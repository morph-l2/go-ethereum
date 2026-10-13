// Copyright 2017 The go-ethereum Authors
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
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/consensus/ethash"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/params"
)

func TestInvalidCliqueConfig(t *testing.T) {
	block := DefaultGoerliGenesisBlock()
	block.ExtraData = []byte{}
	if _, err := block.Commit(nil); err == nil {
		t.Fatal("Expected error on invalid clique config")
	}
}

func TestSetupGenesis(t *testing.T) {
	var (
		customghash = common.HexToHash("0x700380ab70d789c462c4e8f0db082842095321f390d0a3f25f400f0746db32bc")
		customg     = Genesis{
			Config: &params.ChainConfig{HomesteadBlock: big.NewInt(3)},
			Alloc: GenesisAlloc{
				{1}: {Balance: big.NewInt(1), Storage: map[common.Hash]common.Hash{{1}: {1}}},
			},
		}
		oldcustomg = customg
	)
	oldcustomg.Config = &params.ChainConfig{HomesteadBlock: big.NewInt(2)}
	tests := []struct {
		name       string
		fn         func(ethdb.Database) (*params.ChainConfig, common.Hash, error)
		wantConfig *params.ChainConfig
		wantHash   common.Hash
		wantErr    error
	}{
		{
			name: "genesis without ChainConfig",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				return SetupGenesisBlock(db, new(Genesis))
			},
			wantErr:    errGenesisNoConfig,
			wantConfig: params.AllEthashProtocolChanges,
		},
		// {
		// 	name: "no block in DB, genesis == nil",
		// 	fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
		// 		return SetupGenesisBlock(db, nil)
		// 	},
		// 	wantHash:   params.MainnetGenesisHash,
		// 	wantConfig: params.MainnetChainConfig,
		// },
		// {
		// 	name: "mainnet block in DB, genesis == nil",
		// 	fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
		// 		DefaultGenesisBlock().MustCommit(db)
		// 		return SetupGenesisBlock(db, nil)
		// 	},
		// 	wantHash:   params.MainnetGenesisHash,
		// 	wantConfig: params.MainnetChainConfig,
		// },
		{
			name: "custom block in DB, genesis == nil",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				customg.MustCommit(db)
				return SetupGenesisBlock(db, nil)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		// {
		// 	name: "custom block in DB, genesis == ropsten",
		// 	fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
		// 		customg.MustCommit(db)
		// 		return SetupGenesisBlock(db, DefaultRopstenGenesisBlock())
		// 	},
		// 	wantErr:    &GenesisMismatchError{Stored: customghash, New: params.RopstenGenesisHash},
		// 	wantHash:   params.RopstenGenesisHash,
		// 	wantConfig: params.RopstenChainConfig,
		// },
		{
			name: "compatible config in DB",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				oldcustomg.MustCommit(db)
				return SetupGenesisBlock(db, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		{
			name: "incompatible config in DB",
			fn: func(db ethdb.Database) (*params.ChainConfig, common.Hash, error) {
				// Commit the 'old' genesis block with Homestead transition at #2.
				// Advance to block #4, past the homestead transition block of customg.
				genesis := oldcustomg.MustCommit(db)

				bc, _ := NewBlockChain(db, nil, oldcustomg.Config, ethash.NewFullFaker(), vm.Config{}, nil, nil)
				defer bc.Stop()

				blocks, _ := GenerateChain(oldcustomg.Config, genesis, ethash.NewFaker(), db, 4, nil)
				bc.InsertChain(blocks)
				bc.CurrentBlock()
				// This should return a compatibility error.
				return SetupGenesisBlock(db, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
			wantErr: &params.ConfigCompatError{
				What:          "Homestead fork block",
				StoredBlock:   big.NewInt(2),
				NewBlock:      big.NewInt(3),
				RewindToBlock: 1,
			},
		},
	}

	for _, test := range tests {
		db := rawdb.NewMemoryDatabase()
		config, hash, err := test.fn(db)
		// Check the return values.
		if !reflect.DeepEqual(err, test.wantErr) {
			spew := spew.ConfigState{DisablePointerAddresses: true, DisableCapacities: true}
			t.Errorf("%s: returned error %#v, want %#v", test.name, spew.NewFormatter(err), spew.NewFormatter(test.wantErr))
		}
		if !reflect.DeepEqual(config, test.wantConfig) {
			t.Errorf("%s:\nreturned %v\nwant     %v", test.name, config, test.wantConfig)
		}
		if hash != test.wantHash {
			t.Errorf("%s: returned hash %s, want %s", test.name, hash.Hex(), test.wantHash.Hex())
		} else if err == nil {
			// Check database content.
			stored := rawdb.ReadBlock(db, test.wantHash, 0)
			if stored.Hash() != test.wantHash {
				t.Errorf("%s: block in DB has hash %s, want %s", test.name, stored.Hash(), test.wantHash)
			}
		}
	}
}

// TestSetupGenesisMaxTxPayloadBytesPerBlock checks that the per-block tx payload limit is
// a property of the chain rather than of the binary. A network without a built-in preset
// flag (--morph / --morph-hoodi) passes no genesis at startup, so it must enforce the value
// persisted by `geth init`; and re-running init with an updated genesis must replace that
// value in place, without changing the genesis hash.
func TestSetupGenesisMaxTxPayloadBytesPerBlock(t *testing.T) {
	feeVault := common.HexToAddress("0x000000000000000000000000000000000000dead")
	genesisWithLimit := func(limit int) *Genesis {
		return &Genesis{
			Config: &params.ChainConfig{
				HomesteadBlock: big.NewInt(0),
				Morph: params.MorphConfig{
					FeeVaultAddress:           &feeVault,
					MaxTxPayloadBytesPerBlock: &limit,
				},
			},
			Alloc: GenesisAlloc{
				{1}: {Balance: big.NewInt(1)},
			},
		}
	}

	// Both deliberately differ from params.MorphMaxTxPayloadBytesPerBlock so that a
	// regression to a binary-level limit fails this test instead of passing silently.
	const (
		initialLimit = 150 * 1024
		updatedLimit = 300 * 1024
	)
	if params.MorphMaxTxPayloadBytesPerBlock <= updatedLimit {
		t.Fatalf("test needs params.MorphMaxTxPayloadBytesPerBlock (%d) to exceed %d",
			params.MorphMaxTxPayloadBytesPerBlock, updatedLimit)
	}

	// `geth init` with a genesis carrying this network's own limit.
	db := rawdb.NewMemoryDatabase()
	initialHash := genesisWithLimit(initialLimit).MustCommit(db).Hash()

	// Starting without a preset flag passes no genesis, so the config has to come from
	// the database rather than from any built-in default.
	config, hash, err := SetupGenesisBlock(db, nil)
	if err != nil {
		t.Fatalf("SetupGenesisBlock(db, nil): %v", err)
	}
	if hash != initialHash {
		t.Fatalf("genesis hash = %s, want %s", hash.Hex(), initialHash.Hex())
	}
	if got := config.Morph.MaxTxPayloadBytesPerBlock; got == nil || *got != initialLimit {
		t.Fatalf("stored MaxTxPayloadBytesPerBlock = %v, want %d", got, initialLimit)
	}
	if !config.Morph.IsValidBlockSize(common.StorageSize(initialLimit)) {
		t.Errorf("IsValidBlockSize(%d) = false, want true", initialLimit)
	}
	if config.Morph.IsValidBlockSize(common.StorageSize(initialLimit + 1)) {
		t.Errorf("IsValidBlockSize(%d) = true, want false: the enforced limit is not coming from the chain config",
			initialLimit+1)
	}

	// Re-running `geth init` with an updated genesis rewrites the stored config in place.
	// The limit is not part of the genesis header, so the hash is unchanged and the
	// existing chain data stays usable.
	config, hash, err = SetupGenesisBlock(db, genesisWithLimit(updatedLimit))
	if err != nil {
		t.Fatalf("re-init with updated genesis: %v", err)
	}
	if hash != initialHash {
		t.Fatalf("re-init changed the genesis hash to %s, want %s", hash.Hex(), initialHash.Hex())
	}
	if got := config.Morph.MaxTxPayloadBytesPerBlock; got == nil || *got != updatedLimit {
		t.Fatalf("after re-init MaxTxPayloadBytesPerBlock = %v, want %d", got, updatedLimit)
	}

	// ... and the updated value is what a subsequent flagless start reads back.
	config, _, err = SetupGenesisBlock(db, nil)
	if err != nil {
		t.Fatalf("SetupGenesisBlock(db, nil) after re-init: %v", err)
	}
	if got := config.Morph.MaxTxPayloadBytesPerBlock; got == nil || *got != updatedLimit {
		t.Fatalf("persisted MaxTxPayloadBytesPerBlock = %v, want %d", got, updatedLimit)
	}
}

// TestGenesisHashes checks the congruity of default genesis data to
// corresponding hardcoded genesis hash values.
func TestGenesisHashes(t *testing.T) {
	for i, c := range []struct {
		genesis *Genesis
		want    common.Hash
	}{
		// {DefaultGenesisBlock(), params.MainnetGenesisHash},
		// {DefaultGoerliGenesisBlock(), params.GoerliGenesisHash},
		// {DefaultRopstenGenesisBlock(), params.RopstenGenesisHash},
		// {DefaultRinkebyGenesisBlock(), params.RinkebyGenesisHash},
		// {DefaultSepoliaGenesisBlock(), params.SepoliaGenesisHash},
		{DefaultMorphMainnetGenesisBlock(), params.MorphMainnetGenesisHash},
		{DefaultMorphHoodiGenesisBlock(), params.MorphHoodiGenesisHash},
	} {
		// Test via MustCommit
		if have := c.genesis.MustCommit(rawdb.NewMemoryDatabase()).Hash(); have != c.want {
			t.Errorf("case: %d a), want: %s, got: %s", i, c.want.Hex(), have.Hex())
		}
		// Test via ToBlock
		if have := c.genesis.ToBlock(nil).Hash(); have != c.want {
			t.Errorf("case: %d a), want: %s, got: %s", i, c.want.Hex(), have.Hex())
		}
	}
}

func TestGenesis_Commit(t *testing.T) {
	genesis := &Genesis{
		BaseFee: big.NewInt(params.InitialBaseFee),
		Config:  params.TestChainConfig,
		// difficulty is nil
	}

	db := rawdb.NewMemoryDatabase()
	genesisBlock, err := genesis.Commit(db)
	if err != nil {
		t.Fatal(err)
	}

	if genesis.Difficulty != nil {
		t.Fatalf("assumption wrong")
	}

	// This value should have been set as default in the ToBlock method.
	if genesisBlock.Difficulty().Cmp(params.GenesisDifficulty) != 0 {
		t.Errorf("assumption wrong: want: %d, got: %v", params.GenesisDifficulty, genesisBlock.Difficulty())
	}

	// Expect the stored total difficulty to be the difficulty of the genesis block.
	stored := rawdb.ReadTd(db, genesisBlock.Hash(), genesisBlock.NumberU64())

	if stored.Cmp(genesisBlock.Difficulty()) != 0 {
		t.Errorf("inequal difficulty; stored: %v, genesisBlock: %v", stored, genesisBlock.Difficulty())
	}
}
