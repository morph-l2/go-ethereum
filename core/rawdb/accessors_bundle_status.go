package rawdb

import (
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/ethdb"
)

var bundleStatusPrefix = []byte("BS-") // bundle status prefix for the db

// BundleStatusKey returns the key for the bundle status in the database.
func BundleStatusKey(bundleHash common.Hash) []byte {
	return append(bundleStatusPrefix, bundleHash.Bytes()...)
}

// WriteBundleStatus writes the bundle status to the database.
func WriteBundleStatus(db ethdb.Database, bundleHash common.Hash, v []byte) error {
	return db.Put(BundleStatusKey(bundleHash), v)
}

// ReadBundleStatus reads the bundle status from the database.
func ReadBundleStatus(db ethdb.Database, bundleHash common.Hash) ([]byte, error) {
	return db.Get(BundleStatusKey(bundleHash))
}

// DeleteBundleStatus deletes the bundle status from the database.
func DeleteBundleStatus(db ethdb.Database, bundleHash common.Hash) error {
	return db.Delete(BundleStatusKey(bundleHash))
}

// HasBundleStatus checks if the bundle status exists in the database.
func HasBundeleStatus(db ethdb.Database, bundleHash common.Hash) (bool, error) {
	return db.Has(BundleStatusKey(bundleHash))
}
