package rawdb

import (
	"bytes"
	"encoding/binary"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/rlp"
)

func ReadLatestBatchIndex(db ethdb.Reader) *uint64 {
	data, err := db.Get(rollupHeadBatchKey)
	if err != nil && isNotFoundErr(err) {
		return nil
	}
	if err != nil {
		log.Crit("failed to read batchIndex from database", "err", err)
	}
	index := binary.BigEndian.Uint64(data)
	return &index
}

func ReadBatchIndexByHash(db ethdb.Reader, batchHash common.Hash) *uint64 {
	data, err := db.Get(RollupBatchIndexKey(batchHash))
	if err != nil && isNotFoundErr(err) {
		return nil
	}
	if err != nil {
		log.Crit("failed to read batchIndex from database", "err", err)
	}

	index := binary.BigEndian.Uint64(data)
	return &index
}

func ReadRollupBatch(db ethdb.Reader, batchIndex uint64) (*types.RollupBatch, error) {
	data, err := db.Get(RollupBatchKey(batchIndex))
	if err != nil && isNotFoundErr(err) {
		return nil, nil
	}
	if err != nil {
		log.Error("failed to read batch from database", "err", err)
		return nil, err
	}

	rb := new(types.RollupBatch)
	if err = rb.Decode(data); err != nil {
		log.Error("Invalid RollupBatch RLP", "batch index", batchIndex, "data", data, "err", err)
		return nil, err
	}

	return rb, nil
}

func ReadBatchSignatures(db ethdb.Database, batchHash common.Hash) ([]*types.BatchSignature, error) {
	prefix := RollupBatchSignatureKey(batchHash)
	it := db.NewIterator(prefix, nil)
	defer it.Release()

	var bss []*types.BatchSignature
	for it.Next() {
		if it.Key() == nil {
			break
		}
		data := it.Value()
		if data == nil {
			continue
		}
		bs := new(types.BatchSignature)
		if err := rlp.Decode(bytes.NewReader(data), bs); err != nil {
			log.Error("Invalid BatchSignature RLP", "batch hash", batchHash, "data", data, "err", err)
			return nil, err
		}
		bss = append(bss, bs)
	}
	return bss, nil
}

func ReadBatchL1DataFee(db ethdb.Database, batchIndex uint64) *big.Int {
	data, err := db.Get(RollupBatchL1DataFeeKey(batchIndex))
	if err != nil && isNotFoundErr(err) {
		return nil
	}
	if err != nil {
		log.Crit("failed to read batch from database", "err", err)
	}
	return new(big.Int).SetBytes(data)
}

func ReadLatestBatchIndexHasFee(db ethdb.Reader) *uint64 {
	data, err := db.Get(rollupBatchHeadBatchHasFeeKey)
	if err != nil && isNotFoundErr(err) {
		return nil
	}
	if err != nil {
		log.Crit("failed to read batchIndex from database", "err", err)
	}
	index := binary.BigEndian.Uint64(data)
	return &index
}
