// Copyright 2022 The go-ethereum Authors
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

package pathdb

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"sync"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/params"
	dbtypes "github.com/morph-l2/go-ethereum/triedb/types"
)

const (
	// maxDiffLayers is the maximum diff layers allowed in the layer tree.
	maxDiffLayers = 128

	// defaultCleanSize is the default memory allowance of clean cache.
	defaultCleanSize = 16 * 1024 * 1024

	// MaxDirtyBufferSize is the maximum memory allowance of node buffer.
	// Too large nodebuffer will cause the system to pause for a long
	// time when write happens. Also, the largest batch that pebble can
	// support is 4GB, node will panic if batch size exceeds this limit.
	MaxDirtyBufferSize = 256 * 1024 * 1024

	// DefaultDirtyBufferSize is the default memory allowance of node buffer
	// that aggregates the writes from above until it's flushed into the
	// disk. It's meant to be used once the initial sync is finished.
	// Do not increase the buffer size arbitrarily, otherwise the system
	// pause time will increase when the database writes happen.
	DefaultDirtyBufferSize = 64 * 1024 * 1024

	// DefaultBackgroundFlushInterval defines the default the wait interval
	// that background node cache flush disk.
	DefaultBackgroundFlushInterval = 3

	// DefaultBatchRedundancyRate defines the batch size, compatible write
	// size calculation is inaccurate
	DefaultBatchRedundancyRate = 1.1
)

type JournalType int

const (
	JournalKVType JournalType = iota
	JournalFileType
)

// layer is the interface implemented by all state layers which includes some
// public methods and some additional methods for internal usage.
type layer interface {
	// Node retrieves the trie node with the node info. An error will be returned
	// if the read operation exits abnormally. For example, if the layer is already
	// stale, or the associated state is regarded as corrupted. Notably, no error
	// will be returned if the requested node is not found in database.
	Node(path []byte) ([]byte, error)

	// rootHash returns the root hash for which this layer was made.
	rootHash() common.Hash

	// stateID returns the associated state id of layer.
	stateID() uint64

	// parentLayer returns the subsequent layer of it, or nil if the disk was reached.
	parentLayer() layer

	// update creates a new layer on top of the existing layer diff tree with
	// the provided dirty trie nodes along with the state change set.
	//
	// Note, the maps are retained by the method to avoid copying everything.
	update(root common.Hash, id uint64, block uint64, nodes dbtypes.KvMap) *diffLayer

	// journal commits an entire diff hierarchy to disk into a single journal entry.
	// This is meant to be used during shutdown to persist the layer without
	// flattening everything down (bad for reorgs).
	journal(w io.Writer, journalType JournalType) error
}

// Config contains the settings for database.
type Config struct {
	SyncFlush       bool   // Flag of trienodebuffer sync flush cache to disk
	StateHistory    uint64 // Number of recent blocks to maintain state history for
	CleanCacheSize  int    // Maximum memory allowance (in bytes) for caching clean nodes
	DirtyCacheSize  int    // Maximum memory allowance (in bytes) for caching dirty nodes
	ReadOnly        bool   // Flag whether the database is opened in read only mode.
	NoTries         bool
	JournalFilePath string
	JournalFile     bool
}

// sanitize checks the provided user configurations and changes anything that's
// unreasonable or unworkable.
func (c *Config) sanitize() *Config {
	conf := *c
	if conf.DirtyCacheSize > MaxDirtyBufferSize {
		log.Warn("Sanitizing invalid node buffer size", "provided", common.StorageSize(conf.DirtyCacheSize), "updated", common.StorageSize(MaxDirtyBufferSize))
		conf.DirtyCacheSize = MaxDirtyBufferSize
	}
	return &conf
}

// Defaults contains default settings for Ethereum mainnet.
var Defaults = &Config{
	StateHistory:   params.FullImmutabilityThreshold,
	CleanCacheSize: defaultCleanSize,
	DirtyCacheSize: DefaultDirtyBufferSize,
}

// ReadOnly is the config in order to open database in read only mode.
var ReadOnly = &Config{ReadOnly: true}

// Database is a multiple-layered structure for maintaining in-memory trie nodes.
// It consists of one persistent base layer backed by a key-value store, on top
// of which arbitrarily many in-memory diff layers are stacked. The memory diffs
// can form a tree with branching, but the disk layer is singleton and common to
// all. If a reorg goes deeper than the disk layer, a batch of reverse diffs can
// be applied to rollback. The deepest reorg that can be handled depends on the
// amount of state histories tracked in the disk.
//
// At most one readable and writable database can be opened at the same time in
// the whole system which ensures that only one database writer can operate disk
// state. Unexpected open operations can cause the system to panic.
type Database struct {
	// readOnly is the flag whether the mutation is allowed to be applied.
	// It will be set automatically when the database is journaled during
	// the shutdown to reject all following unexpected mutations.
	readOnly   bool                // Flag if database is opened in read only mode
	bufferSize int                 // Memory allowance (in bytes) for caching dirty nodes
	config     *Config             // Configuration for database
	diskdb     ethdb.KeyValueStore // Persistent storage for matured trie nodes
	tree       *layerTree          // The group for all known layers
	lock       sync.RWMutex        // Lock to prevent mutations from happening at the same time
	dirties    dbtypes.KvMap
}

// New attempts to load an already existing layer from a persistent key-value
// store (with a number of memory layers from a journal). If the journal is not
// matched with the base persistent layer, all the recorded diff layers are discarded.
func New(diskdb ethdb.KeyValueStore, config *Config) *Database {
	if config == nil {
		config = Defaults
	}
	config = config.sanitize()
	db := &Database{
		readOnly:   config.ReadOnly,
		bufferSize: config.DirtyCacheSize,
		config:     config,
		diskdb:     diskdb,
		dirties:    make(dbtypes.KvMap),
	}
	// Construct the layer tree by resolving the in-disk singleton state
	// and in-memory layer journal.
	db.tree = newLayerTree(db.loadLayers())

	return db
}

// Reader retrieves a layer belonging to the given state root.
func (db *Database) Reader(root common.Hash) (layer, error) {
	l := db.tree.get(root)
	if l == nil {
		return nil, fmt.Errorf("state %#x is not available", root)
	}
	return l, nil
}

func (db *Database) CommitGenesis(root common.Hash) error {
	log.Info("pathdb write genesis state to disk", "root", root.Hex())
	batch := db.diskdb.NewBatch()
	for _, v := range db.dirties {
		batch.Put(v.K, v.V)
	}
	for k := range db.dirties {
		delete(db.dirties, k)
	}
	if err := batch.Write(); err != nil {
		return err
	}
	batch.Reset()

	// Update stateID
	rawdb.WriteStateID(db.diskdb, root, 0)
	return nil
}

// Commit traverses downwards the layer tree from a specified layer with the
// provided state root and all the layers below are flattened downwards. It
// can be used alone and mostly for test purposes.
func (db *Database) CommitState(root common.Hash, parentRoot common.Hash, blockNumber uint64, report bool, flush bool, callback func()) error {
	// Hold the lock to prevent concurrent mutations.
	db.lock.Lock()
	defer db.lock.Unlock()

	// Short circuit if the mutation is not allowed.
	if err := db.modifyAllowed(); err != nil {
		return err
	}

	// only 1 entity, state have no changes
	// some block maybe has no txns, so state do not change
	if root == parentRoot && len(db.dirties) == 1 {
		return nil
	}

	if err := db.tree.add(root, parentRoot, blockNumber, db.dirties); err != nil {
		db.dirties = make(dbtypes.KvMap)
		return err
	}
	db.dirties = make(dbtypes.KvMap)

	layers := maxDiffLayers
	if flush {
		layers = 0
	}

	// Keep 128 diff layers in the memory, persistent layer is 129th.
	// - head layer is paired with HEAD state
	// - head-1 layer is paired with HEAD-1 state
	// - head-127 layer(bottom-most diff layer) is paired with HEAD-127 state
	// - head-128 layer(disk layer) is paired with HEAD-128 state
	err := db.tree.cap(root, layers)
	if callback != nil {
		callback()
	}
	return err
}

// Close closes the trie database and the held freezer.
func (db *Database) Close() error {
	db.lock.Lock()
	defer db.lock.Unlock()

	// Set the database to read-only mode to prevent all
	// following mutations.
	db.readOnly = true

	// Release the memory held by clean cache.
	db.tree.bottom().resetCache()

	return nil
}

// Size returns the current storage size of the memory cache in front of the
// persistent database layer.
func (db *Database) Size() (diffs common.StorageSize, nodes common.StorageSize, immutableNodes common.StorageSize) {
	db.tree.forEach(func(layer layer) {
		if diff, ok := layer.(*diffLayer); ok {
			diffs += common.StorageSize(diff.memory)
		}
		if disk, ok := layer.(*diskLayer); ok {
			nodes, immutableNodes = disk.size()
		}
	})
	return diffs, nodes, immutableNodes
}

// Initialized returns an indicator if the state data is already
// initialized in path-based scheme.
func (db *Database) Initialized(genesisRoot common.Hash) bool {
	var inited bool
	db.tree.forEach(func(layer layer) {
		if layer.rootHash() != types.EmptyRootHash {
			inited = true
		}
	})

	return inited
}

// SetBufferSize sets the node buffer size to the provided value(in bytes).
func (db *Database) SetBufferSize(size int) error {
	db.lock.Lock()
	defer db.lock.Unlock()

	if size > MaxDirtyBufferSize {
		log.Info("Capped node buffer size", "provided", common.StorageSize(size), "adjusted", common.StorageSize(MaxDirtyBufferSize))
		size = MaxDirtyBufferSize
	}
	db.bufferSize = size
	return db.tree.bottom().setBufferSize(db.bufferSize)
}

// Scheme returns the node scheme used in the database.
func (db *Database) Scheme() string {
	return rawdb.PathScheme
}

// Head return the top non-fork difflayer/disklayer root hash for rewinding.
func (db *Database) Head() common.Hash {
	db.lock.Lock()
	defer db.lock.Unlock()
	return db.tree.front()
}

// modifyAllowed returns the indicator if mutation is allowed. This function
// assumes the db.lock is already held.
func (db *Database) modifyAllowed() error {
	if db.readOnly {
		return errDatabaseReadOnly
	}
	return nil
}

// GetAllRootHash returns all diffLayer and diskLayer root hash
func (db *Database) GetAllRootHash() [][]string {
	db.lock.Lock()
	defer db.lock.Unlock()

	data := make([][]string, 0, len(db.tree.layers))
	for _, v := range db.tree.layers {
		if dl, ok := v.(*diffLayer); ok {
			data = append(data, []string{fmt.Sprintf("%d", dl.block), dl.rootHash().String()})
		}
	}
	sort.Slice(data, func(i, j int) bool {
		block1, _ := strconv.Atoi(data[i][0])
		block2, _ := strconv.Atoi(data[j][0])
		return block1 > block2
	})

	data = append(data, []string{"-1", db.tree.bottom().rootHash().String()})
	return data
}

// DetermineJournalTypeForWriter is used when persisting the journal. It determines JournalType based on the config passed in by the Config.
func (db *Database) DetermineJournalTypeForWriter() JournalType {
	if db.config.JournalFile {
		return JournalFileType
	} else {
		return JournalKVType
	}
}

// DetermineJournalTypeForReader is used when loading the journal. It loads based on whether JournalKV or JournalFile currently exists.
func (db *Database) DetermineJournalTypeForReader() JournalType {
	if journal := rawdb.ReadTrieJournal(db.diskdb); len(journal) != 0 {
		return JournalKVType
	}

	if fileInfo, stateErr := os.Stat(db.config.JournalFilePath); stateErr == nil && !fileInfo.IsDir() {
		return JournalFileType
	}

	return JournalKVType
}

func (db *Database) DeleteTrieJournal(writer ethdb.KeyValueWriter) error {
	// To prevent any remnants of old journals after converting from JournalKV to JournalFile or vice versa, all deletions must be completed.
	rawdb.DeleteTrieJournal(writer)

	// delete from journal file, may not exist
	filePath := db.config.JournalFilePath
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil
	}
	errRemove := os.Remove(filePath)
	if errRemove != nil {
		log.Crit("Failed to remove tries journal", "journal path", filePath, "err", errRemove)
	}
	return nil
}

// zk-trie put dirties
func (db *Database) Put(k, v []byte) error {
	db.lock.Lock()
	defer db.lock.Unlock()

	key := rawdb.CompactStorageTrieNodeKey(k[:])
	db.dirties.Put(key, v)
	return nil
}
