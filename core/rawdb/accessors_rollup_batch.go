package rawdb

import (
	"bytes"

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

func WriteBatchSignature(db ethdb.KeyValueWriter, batchIndex uint64, signature types.BatchSignature) {
	bz, err := rlp.EncodeToBytes(&signature)
	if err != nil {
		log.Crit("failed to RLP encode batch signature", "batch index", batchIndex, "signer index", signature.Signer, "err", err)
	}

	if err = db.Put(RollupBatchSignatureSignerKey(batchIndex, signature.Signer), bz); err != nil {
		log.Crit("failed to store batch signature", "batch index", batchIndex, "signer index", signature.Signer, "err", err)
	}
}

func ReadBatchSignatures(db ethdb.Iteratee, batchIndex uint64) []*types.BatchSignature {
	prefix := RollupBatchSignatureKey(batchIndex)
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
			log.Crit("Invalid BatchSignature RLP", "batch index", batchIndex, "data", data, "err", err)
		}
		bss = append(bss, bs)
	}
	return bss
}
