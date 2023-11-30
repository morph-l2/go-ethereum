package rawdb

import (
	"bytes"
	"encoding/binary"
	"github.com/scroll-tech/go-ethereum/common"

	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/rlp"
)

func WriteRollupBatch(db ethdb.KeyValueWriter, batch types.RollupBatch) {
	bz, err := rlp.EncodeToBytes(&batch)
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

func ReadRollupBatch(db ethdb.Reader, batchIndex uint64) *types.RollupBatch {
	data, err := db.Get(RollupBatchKey(batchIndex))
	if err != nil && isNotFoundErr(err) {
		return nil
	}
	if err != nil {
		log.Crit("failed to read batch from database", "err", err)
	}

	rb := new(types.RollupBatch)
	if err = rlp.Decode(bytes.NewReader(data), rb); err != nil {
		log.Crit("Invalid RollupBatch RLP", "batch index", batchIndex, "data", data, "err", err)
	}
	return rb
}

func WriteBatchSignature(db ethdb.KeyValueWriter, batchHash common.Hash, signature types.BatchSignature) {
	bz, err := rlp.EncodeToBytes(&signature)
	if err != nil {
		log.Crit("failed to RLP encode batch signature", "batch hash", batchHash, "signer index", signature.Signer, "err", err)
	}

	if err = db.Put(RollupBatchSignatureSignerKey(batchHash, signature.Signer), bz); err != nil {
		log.Crit("failed to store batch signature", "batch index", batchHash, "signer index", signature.Signer, "err", err)
	}
}

func ReadBatchSignatures(db ethdb.Database, batchHash common.Hash) []*types.BatchSignature {
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
			log.Crit("Invalid BatchSignature RLP", "batch hash", batchHash, "data", data, "err", err)
		}
		bss = append(bss, bs)
	}
	return bss
}
