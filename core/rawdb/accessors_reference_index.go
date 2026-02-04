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

package rawdb

import (
	"encoding/binary"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
)

// ReferenceIndexEntry stores the transaction location info for reference index lookups.
type ReferenceIndexEntry struct {
	BlockTimestamp uint64
	TxIndex        uint64
	TxHash         common.Hash
}

// WriteReferenceIndexEntry stores a reference index entry.
// Key format: prefix + reference + blockTimestamp + txIndex + txHash
// Value: nil (empty, we only need the key for lookups)
func WriteReferenceIndexEntry(db ethdb.KeyValueWriter, reference common.Reference, blockTimestamp uint64, txIndex uint64, txHash common.Hash) {
	key := referenceIndexKey(reference, blockTimestamp, txIndex, txHash)
	if err := db.Put(key, nil); err != nil {
		log.Crit("Failed to store reference index entry", "err", err)
	}
}

// WriteReferenceIndexEntriesForBlock writes reference index entries for all MorphTx transactions in a block.
func WriteReferenceIndexEntriesForBlock(db ethdb.KeyValueWriter, block *types.Block) {
	for txIndex, tx := range block.Transactions() {
		if tx.IsMorphTx() {
			if ref := tx.Reference(); ref != nil {
				WriteReferenceIndexEntry(db, *ref, block.Time(), uint64(txIndex), tx.Hash())
			}
		}
	}
}

// DeleteReferenceIndexEntry removes a reference index entry.
func DeleteReferenceIndexEntry(db ethdb.KeyValueWriter, reference common.Reference, blockTimestamp uint64, txIndex uint64, txHash common.Hash) {
	key := referenceIndexKey(reference, blockTimestamp, txIndex, txHash)
	if err := db.Delete(key); err != nil {
		log.Crit("Failed to delete reference index entry", "err", err)
	}
}

// DeleteReferenceIndexEntriesForBlock removes reference index entries for all MorphTx transactions in a block.
func DeleteReferenceIndexEntriesForBlock(db ethdb.KeyValueWriter, block *types.Block) {
	for txIndex, tx := range block.Transactions() {
		if tx.IsMorphTx() {
			if ref := tx.Reference(); ref != nil {
				DeleteReferenceIndexEntry(db, *ref, block.Time(), uint64(txIndex), tx.Hash())
			}
		}
	}
}

// ReadReferenceIndexEntries returns all transaction entries for a given reference.
// Results are sorted by blockTimestamp and txIndex (ascending order due to key structure).
func ReadReferenceIndexEntries(db ethdb.Database, reference common.Reference) []ReferenceIndexEntry {
	prefix := referenceIndexKeyPrefix(reference)
	it := db.NewIterator(prefix, nil)
	defer it.Release()

	var entries []ReferenceIndexEntry
	for it.Next() {
		key := it.Key()
		// Validate key length: prefix(3) + reference(32) + blockTimestamp(8) + txIndex(8) + txHash(32) = 83 bytes
		if len(key) != len(referenceIndexPrefix)+common.ReferenceLength+8+8+common.HashLength {
			continue
		}

		offset := len(referenceIndexPrefix) + common.ReferenceLength
		blockTimestamp := binary.BigEndian.Uint64(key[offset:])
		offset += 8
		txIndex := binary.BigEndian.Uint64(key[offset:])
		offset += 8
		txHash := common.BytesToHash(key[offset:])

		entries = append(entries, ReferenceIndexEntry{
			BlockTimestamp: blockTimestamp,
			TxIndex:        txIndex,
			TxHash:         txHash,
		})
	}

	if it.Error() != nil {
		log.Error("Failed to iterate reference index entries", "reference", reference.Hex(), "err", it.Error())
	}

	return entries
}

// ReadReferenceIndexTail retrieves the block number whose reference index is the oldest stored.
func ReadReferenceIndexTail(db ethdb.KeyValueReader) *uint64 {
	data, err := db.Get(referenceIndexTailKey)
	if err != nil || len(data) == 0 {
		return nil
	}
	number := new(big.Int).SetBytes(data).Uint64()
	return &number
}

// WriteReferenceIndexTail stores the block number whose reference index is the oldest stored.
func WriteReferenceIndexTail(db ethdb.KeyValueWriter, blockNumber uint64) {
	if err := db.Put(referenceIndexTailKey, new(big.Int).SetUint64(blockNumber).Bytes()); err != nil {
		log.Crit("Failed to store reference index tail", "err", err)
	}
}

