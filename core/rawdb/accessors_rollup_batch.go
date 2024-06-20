package rawdb

import (
	"bytes"
	"encoding/binary"
	"math/big"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/rlp"
)

func WriteRollupBatch(db ethdb.KeyValueWriter, batch *types.RollupBatch) {
	bz, err := batch.Encode()
	if err != nil {
		log.Crit("failed to RLP encode batch", "batch index", batch.Index, "err", err)
	}
	if err = db.Put(RollupBatchKey(batch.Index), bz); err != nil {
		log.Crit("failed to store batch", "batch index", batch.Index, "err", err)
	}

	// stores batchHash -> batchIndex
	if err = db.Put(RollupBatchIndexKey(batch.Hash), encodeBigEndian(batch.Index)); err != nil {
		log.Crit("failed to store batch index", "batch hash", batch.Hash.Hex(), "batch index", batch.Index, "err", err)
	}

	// stores latest batch index
	if err = db.Put(rollupHeadBatchKey, encodeBigEndian(batch.Index)); err != nil {
		log.Crit("failed to store latest batch index", "batch index", batch.Index, "err", err)
	}
}

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

func WriteBatchSignature(db ethdb.KeyValueWriter, batchHash common.Hash, signature types.BatchSignature) {
	bz, err := rlp.EncodeToBytes(&signature)
	if err != nil {
		log.Crit("failed to RLP encode batch signature", "batch hash", batchHash, "signer index", signature.Signer, "err", err)
	}

	if err = db.Put(RollupBatchSignatureSignerKey(batchHash, signature.Signer), bz); err != nil {
		log.Crit("failed to store batch signature", "batch index", batchHash, "signer", signature.Signer, "err", err)
	}
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

func WriteBatchL1DataFee(db ethdb.Database, batchIndex uint64, l1DataFee *big.Int) {
	if err := db.Put(RollupBatchL1DataFeeKey(batchIndex), l1DataFee.Bytes()); err != nil {
		log.Crit("failed to store batch l1DataFee", "batch index", batchIndex, "l1DataFee", l1DataFee.String(), "err", err)
	}
}

func WriteHeadBatchIndexHasFee(db ethdb.Database, batchIndex uint64) {
	if err := db.Put(rollupBatchHeadBatchHasFeeKey, encodeBigEndian(batchIndex)); err != nil {
		log.Crit("failed to store head batch which has fee collected", "batch index", batchIndex, "err", err)
	}
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
