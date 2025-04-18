package bundlepool

import (
	"github.com/VictoriaMetrics/fastcache"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
)

type BundleStatus struct {
	db    ethdb.Database
	cache *fastcache.Cache
}

func NewBundleStatus(db ethdb.Database) *BundleStatus {
	cache := fastcache.New(20 * 1024 * 1024) // 20MB cache
	return &BundleStatus{
		db:    db,
		cache: cache,
	}
}

// UpdateBundleStatus updates the bundle status in the cache and database.
func (bs *BundleStatus) UpdateBundleStatus(status map[common.Hash]*types.BundleStatus) {
	for hash, status := range status {
		// Update the cache
		bs.cache.Set(hash[:], []byte{byte(status.Status)})
		// Update the database
		if err := rawdb.WriteBundleStatus(bs.db, hash, []byte{byte(status.Status)}); err != nil {
			// Handle error
			log.Error("Failed to write bundle status to db", "hash", hash.Hex(), "error", err)
			continue
		}
	}
}

// GetBundleStatus retrieves the bundle status from the cache or database.
func (bs *BundleStatus) GetBundleStatus(bundleHash common.Hash) *types.BundleStatusCode {
	status := types.BundleStatusNoRun
	if enc := bs.cache.Get(nil, bundleHash[:]); enc != nil {
		status = types.BundleStatusCode(enc[0])
		return &status
	}

	s, err := rawdb.ReadBundleStatus(bs.db, bundleHash)
	if err != nil {
		return &status
	}

	// If the status is not found in the database, return the default status
	if len(s) == 0 {
		return &status
	}

	status = types.BundleStatusCode(s[0])
	return &status
}
