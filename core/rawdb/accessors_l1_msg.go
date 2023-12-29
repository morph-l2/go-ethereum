package rawdb

import (
	"encoding/binary"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/log"
)

// WriteFirstQueueIndexNotInL2Block writes the queue index of the first message
// that is NOT included in the ledger up to and including the provided L2 block.
// The L2 block is identified by its block hash. If the L2 block contains zero
// L1 messages, this value MUST equal its parent's value.
func WriteFirstQueueIndexNotInL2Block(db ethdb.KeyValueWriter, l2BlockHash common.Hash, queueIndex uint64) {
	if err := db.Put(FirstQueueIndexNotInL2BlockKey(l2BlockHash), encodeBigEndian(queueIndex)); err != nil {
		log.Crit("Failed to store first L1 message not in L2 block", "l2BlockHash", l2BlockHash, "err", err)
	}
}

// ReadFirstQueueIndexNotInL2Block retrieves the queue index of the first message
// that is NOT included in the ledger up to and including the provided L2 block.
func ReadFirstQueueIndexNotInL2Block(db ethdb.Reader, l2BlockHash common.Hash) *uint64 {
	data, err := db.Get(FirstQueueIndexNotInL2BlockKey(l2BlockHash))
	if err != nil && isNotFoundErr(err) {
		return nil
	}
	if err != nil {
		log.Crit("Failed to read first L1 message not in L2 block from database", "l2BlockHash", l2BlockHash, "err", err)
	}
	if len(data) == 0 {
		return nil
	}
	queueIndex := binary.BigEndian.Uint64(data)
	return &queueIndex
}
