package hashdb

import (
	"runtime"
	"sync"
	"time"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/triedb/types"

	zktrie "github.com/scroll-tech/zktrie/trie"
)

// Config defines all necessary options for database.
type Config struct {
	Cache   int    // Memory allowance (MB) to use for caching trie nodes in memory
	Journal string // Journal of clean cache to survive node restarts
}

// Defaults is the default setting for database if it's not specified.
// Notably, clean cache is disabled explicitly,
var Defaults = &Config{
	// Explicitly set clean cache size to 0 to avoid creating fastcache,
	// otherwise database must be closed when it's no longer needed to
	// prevent memory leak.
	Cache: 0,
}

type ZktrieDatabase struct {
	diskdb  ethdb.KeyValueStore // Persistent storage for matured trie nodes
	cleans  *fastcache.Cache    // GC friendly memory cache of clean node RLPs
	prefix  []byte
	dirties types.KvMap

	lock        sync.RWMutex
	dirtiesSize common.StorageSize // Storage size of the dirty node cache (exc. metadata)
}

func NewZkDatabaseWithConfig(diskdb ethdb.KeyValueStore, config *Config) *ZktrieDatabase {
	if config == nil {
		config = Defaults
	}

	var cleans *fastcache.Cache
	if config != nil && config.Cache > 0 {
		if config.Journal == "" {
			cleans = fastcache.New(config.Cache * 1024 * 1024)
		} else {
			cleans = fastcache.LoadFromFileOrNew(config.Journal, config.Cache*1024*1024)
		}
	}

	return &ZktrieDatabase{
		diskdb:  diskdb,
		cleans:  cleans,
		dirties: make(types.KvMap),
	}
}

func (db *ZktrieDatabase) Scheme() string { return rawdb.HashScheme }

func (db *ZktrieDatabase) Size() (common.StorageSize, common.StorageSize, common.StorageSize) {
	db.lock.RLock()
	defer db.lock.RUnlock()

	// db.dirtiesSize only contains the useful data in the cache, but when reporting
	// the total memory consumption, the maintenance metadata is also needed to be
	// counted.
	var metadataSize = common.StorageSize(len(db.dirties))
	return 0, 0, db.dirtiesSize + metadataSize
}

func (db *ZktrieDatabase) CommitState(root common.Hash, parentRoot common.Hash, blockNumber uint64, report bool) error {
	beforeDirtyCount, beforeDirtySize := len(db.dirties), db.dirtiesSize

	start := time.Now()
	if err := db.commitAllDirties(); err != nil {
		return err
	}
	memcacheCommitTimeTimer.Update(time.Since(start))
	memcacheCommitNodesMeter.Mark(int64(beforeDirtyCount - len(db.dirties)))

	logger := log.Debug
	if report {
		logger = log.Info
	}
	logger(
		"Persisted trie from memory database",
		"nodes", beforeDirtyCount-len(db.dirties),
		"size", beforeDirtySize-db.dirtiesSize,
		"time", time.Since(start),
		"livenodes", len(db.dirties),
		"livesize", db.dirtiesSize,
	)
	return nil
}

func (db *ZktrieDatabase) CommitGenesis(root common.Hash) error {
	return db.CommitState(root, common.Hash{}, 0, true)
}

func (db *ZktrieDatabase) commitAllDirties() error {
	batch := db.diskdb.NewBatch()

	db.lock.Lock()
	for _, v := range db.dirties {
		batch.Put(v.K, v.V)
	}
	for k := range db.dirties {
		delete(db.dirties, k)
	}
	db.lock.Unlock()

	if err := batch.Write(); err != nil {
		return err
	}

	batch.Reset()
	return nil
}

func (db *ZktrieDatabase) Close() error                           { return nil }
func (db *ZktrieDatabase) Cap(_ common.StorageSize) error         { return nil }
func (db *ZktrieDatabase) Reference(_ common.Hash, _ common.Hash) {}
func (db *ZktrieDatabase) Dereference(_ common.Hash)              {}

func (db *ZktrieDatabase) Node(hash common.Hash) ([]byte, error) {
	panic("ZktrieDatabase not implement Node()")
}

// Put saves a key:value into the Storage
func (db *ZktrieDatabase) Put(k, v []byte) error {
	k = common.BitReverse(k)

	db.lock.Lock()
	db.dirties.Put(k, v)
	db.lock.Unlock()

	if db.cleans != nil {
		db.cleans.Set(k[:], v)
		memcacheCleanMissMeter.Mark(1)
		memcacheCleanWriteMeter.Mark(int64(len(v)))
	}
	return nil
}

// Get retrieves a value from a key in the Storage
func (db *ZktrieDatabase) Get(key []byte) ([]byte, error) {
	key = common.BitReverse(key[:])

	db.lock.RLock()
	value, ok := db.dirties.Get(key)
	db.lock.RUnlock()
	if ok {
		return value, nil
	}

	if db.cleans != nil {
		if enc := db.cleans.Get(nil, key); enc != nil {
			memcacheCleanHitMeter.Mark(1)
			memcacheCleanReadMeter.Mark(int64(len(enc)))
			return enc, nil
		}
	}

	v, err := db.diskdb.Get(key)
	if rawdb.IsNotFoundErr(err) {
		return nil, zktrie.ErrKeyNotFound
	}
	if err != nil && db.cleans != nil {
		db.cleans.Set(key[:], v)
		memcacheCleanMissMeter.Mark(1)
		memcacheCleanWriteMeter.Mark(int64(len(v)))
	}
	return v, err
}

// saveCache saves clean state cache to given directory path
// using specified CPU cores.
func (db *ZktrieDatabase) saveCache(dir string, threads int) error {
	if db.cleans == nil {
		return nil
	}
	log.Info("Writing clean trie cache to disk", "path", dir, "threads", threads)

	start := time.Now()
	err := db.cleans.SaveToFileConcurrent(dir, threads)
	if err != nil {
		log.Error("Failed to persist clean trie cache", "error", err)
		return err
	}
	log.Info("Persisted the clean trie cache", "path", dir, "elapsed", common.PrettyDuration(time.Since(start)))
	return nil
}

// SaveCache atomically saves fast cache data to the given dir using all
// available CPU cores.
func (db *ZktrieDatabase) SaveCache(dir string) error {
	return db.saveCache(dir, runtime.GOMAXPROCS(0))
}

// SaveCachePeriodically atomically saves fast cache data to the given dir with
// the specified interval. All dump operation will only use a single CPU core.
func (db *ZktrieDatabase) SaveCachePeriodically(dir string, interval time.Duration, stopCh <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			db.saveCache(dir, 1)
		case <-stopCh:
			return
		}
	}
}

func (db *ZktrieDatabase) Reader(root common.Hash) (*zkReader, error) {
	return &zkReader{db: db}, nil
}

type zkReader struct{ db *ZktrieDatabase }

func (z zkReader) Node(path []byte) ([]byte, error) {
	return z.db.Get(path)
}
