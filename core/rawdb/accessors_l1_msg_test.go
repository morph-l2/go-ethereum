package rawdb

import (
	"testing"

	"github.com/scroll-tech/go-ethereum/common"
)

func TestReadWriteLastL1MessageInL2Block(t *testing.T) {
	inputs := []uint64{
		1,
		1 << 2,
		1 << 8,
		1 << 16,
		1 << 32,
	}

	db := NewMemoryDatabase()
	for _, num := range inputs {
		l2BlockHash := common.Hash{byte(num)}
		WriteFirstQueueIndexNotInL2Block(db, l2BlockHash, num)
		got := ReadFirstQueueIndexNotInL2Block(db, l2BlockHash)

		if got == nil || *got != num {
			t.Fatal("Enqueue index mismatch", "expected", num, "got", got)
		}
	}
}
