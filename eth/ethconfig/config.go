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

// Package ethconfig contains the configuration of the ETH and LES protocols.
package ethconfig

import (
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"

	"github.com/morph-l2/go-ethereum/consensus/l2"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/consensus"
	"github.com/morph-l2/go-ethereum/consensus/clique"
	"github.com/morph-l2/go-ethereum/consensus/ethash"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/eth/downloader"
	"github.com/morph-l2/go-ethereum/eth/gasprice"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/miner"
	"github.com/morph-l2/go-ethereum/params"
)

// FullNodeGPO contains default gasprice oracle settings for full node.
var FullNodeGPO = gasprice.Config{
	Blocks:           20,
	Percentile:       60,
	MaxHeaderHistory: 1024,
	MaxBlockHistory:  1024,
	MaxPrice:         gasprice.DefaultMaxPrice,
	IgnorePrice:      gasprice.DefaultIgnorePrice,
}

// LightClientGPO contains default gasprice oracle settings for light client.
var LightClientGPO = gasprice.Config{
	Blocks:           2,
	Percentile:       60,
	MaxHeaderHistory: 300,
	MaxBlockHistory:  5,
	MaxPrice:         gasprice.DefaultMaxPrice,
	IgnorePrice:      gasprice.DefaultIgnorePrice,
}

// Defaults contains default settings for use on the Ethereum main net.
var Defaults = Config{
	SyncMode: downloader.SnapSync,
	Ethash: ethash.Config{
		CacheDir:         "ethash",
		CachesInMem:      2,
		CachesOnDisk:     3,
		CachesLockMmap:   false,
		DatasetsInMem:    1,
		DatasetsOnDisk:   2,
		DatasetsLockMmap: false,
	},
	NetworkId:               1,
	TxLookupLimit:           2350000,
	LightPeers:              100,
	UltraLightFraction:      75,
	DatabaseCache:           512,
	TrieCleanCache:          154,
	TrieCleanCacheJournal:   "triecache",
	TrieCleanCacheRejournal: 60 * time.Minute,
	TrieDirtyCache:          256,
	TrieTimeout:             60 * time.Minute,
	SnapshotCache:           102,
	FilterLogCacheSize:      32,
	Miner:                   miner.DefaultConfig,
	TxPool:                  core.DefaultTxPoolConfig,
	RPCGasCap:               50000000,
	RPCEVMTimeout:           5 * time.Second,
	GPO:                     FullNodeGPO,
	RPCTxFeeCap:             1,  // 1 ether
	MaxBlockRange:           -1, // Default unconfigured value: no block range limit for backward compatibility
}

func init() {
	home := os.Getenv("HOME")
	if home == "" {
		if user, err := user.Current(); err == nil {
			home = user.HomeDir
		}
	}
	if runtime.GOOS == "darwin" {
		Defaults.Ethash.DatasetDir = filepath.Join(home, "Library", "Ethash")
	} else if runtime.GOOS == "windows" {
		localappdata := os.Getenv("LOCALAPPDATA")
		if localappdata != "" {
			Defaults.Ethash.DatasetDir = filepath.Join(localappdata, "Ethash")
		} else {
			Defaults.Ethash.DatasetDir = filepath.Join(home, "AppData", "Local", "Ethash")
		}
	} else {
		Defaults.Ethash.DatasetDir = filepath.Join(home, ".ethash")
	}
}

//go:generate go run github.com/fjl/gencodec -type Config -formats toml -out gen_config.go

// Config contains configuration options for of the ETH and LES protocols.
type Config struct {
	// The genesis block, which is inserted if the database is empty.
	// If nil, the Ethereum main net block is used.
	Genesis *core.Genesis `toml:",omitempty"`

	// Protocol options
	NetworkId uint64 // Network ID to use for selecting peers to connect to
	SyncMode  downloader.SyncMode

	// This can be set to list of enrtree:// URLs which will be queried for
	// for nodes to connect to.
	EthDiscoveryURLs  []string
	SnapDiscoveryURLs []string

	NoPruning  bool // Whether to disable pruning and flush everything to disk
	NoPrefetch bool // Whether to disable prefetching and only load state on demand

	TxLookupLimit uint64 `toml:",omitempty"` // The maximum number of blocks from head whose tx indices are reserved.

	// Whitelist of required block number -> hash values to accept
	Whitelist map[uint64]common.Hash `toml:"-"`

	// Light client options
	LightServ          int  `toml:",omitempty"` // Maximum percentage of time allowed for serving LES requests
	LightIngress       int  `toml:",omitempty"` // Incoming bandwidth limit for light servers
	LightEgress        int  `toml:",omitempty"` // Outgoing bandwidth limit for light servers
	LightPeers         int  `toml:",omitempty"` // Maximum number of LES client peers
	LightNoPrune       bool `toml:",omitempty"` // Whether to disable light chain pruning
	LightNoSyncServe   bool `toml:",omitempty"` // Whether to serve light clients before syncing
	SyncFromCheckpoint bool `toml:",omitempty"` // Whether to sync the header chain from the configured checkpoint

	// Ultra Light client options
	UltraLightServers      []string `toml:",omitempty"` // List of trusted ultra light servers
	UltraLightFraction     int      `toml:",omitempty"` // Percentage of trusted servers to accept an announcement
	UltraLightOnlyAnnounce bool     `toml:",omitempty"` // Whether to only announce headers, or also serve them

	// Database options
	SkipBcVersionCheck bool `toml:"-"`
	DatabaseHandles    int  `toml:"-"`
	DatabaseCache      int
	DatabaseFreezer    string

	TrieCleanCache          int
	TrieCleanCacheJournal   string        `toml:",omitempty"` // Disk journal directory for trie cache to survive node restarts
	TrieCleanCacheRejournal time.Duration `toml:",omitempty"` // Time interval to regenerate the journal for clean cache
	TrieDirtyCache          int
	TrieTimeout             time.Duration
	SnapshotCache           int
	Preimages               bool

	// This is the number of blocks for which logs will be cached in the filter system.
	FilterLogCacheSize int

	// Mining options
	Miner miner.Config

	// Ethash options
	Ethash ethash.Config

	// Transaction pool options
	TxPool core.TxPoolConfig

	// Gas Price Oracle options
	GPO gasprice.Config

	// Enables tracking of SHA3 preimages in the VM
	EnablePreimageRecording bool

	// Miscellaneous options
	DocRoot string `toml:"-"`

	// RPCGasCap is the global gas cap for eth-call variants.
	RPCGasCap uint64

	// RPCEVMTimeout is the global timeout for eth-call.
	RPCEVMTimeout time.Duration

	// RPCTxFeeCap is the global transaction fee(price * gaslimit) cap for
	// send-transction variants. The unit is ether.
	RPCTxFeeCap float64

	// Checkpoint is a hardcoded checkpoint which can be nil.
	Checkpoint *params.TrustedCheckpoint `toml:",omitempty"`

	// CheckpointOracle is the configuration for checkpoint oracle.
	CheckpointOracle *params.CheckpointOracleConfig `toml:",omitempty"`

	// Arrow Glacier block override (TODO: remove after the fork)
	OverrideArrowGlacier *big.Int `toml:",omitempty"`

	// Check circuit capacity in block validator
	CheckCircuitCapacity bool

	// Max block range for eth_getLogs api method
	MaxBlockRange int64

	// Morph203Time override
	OverrideMorph203Time *uint64 `toml:",omitempty"`
}

// CreateConsensusEngine creates a consensus engine for the given chain config.
// Clique is allowed for now to live standalone, but ethash is forbidden and can
// only exist on already merged networks.
func CreateConsensusEngine(config *params.ChainConfig, db ethdb.Database) (consensus.Engine, error) {
	// Wrap previously supported consensus engines into their post-merge counterpart
	if config.Clique != nil {
		return l2.New(clique.New(config.Clique, db), config), nil
	}
	return l2.New(ethash.NewFaker(), config), nil
}
