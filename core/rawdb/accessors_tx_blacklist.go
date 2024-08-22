package rawdb

import (
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
)

func GetBlacklistSenders(db ethdb.Database) (senders []common.Address) {
	it := db.NewIterator(blacklistSenderPrefix, nil)
	defer it.Release()

	for it.Next() {
		if it.Key() == nil {
			break
		}
		addressBytes := it.Key()[len(blacklistSenderPrefix):]
		if len(addressBytes) != common.AddressLength {
			log.Error("invalid blacklist sender", "address bytes", hexutil.Encode(addressBytes))
			continue
		}
		senders = append(senders, common.BytesToAddress(addressBytes))
	}
	return
}

func GetBlacklistReceivers(db ethdb.Database) (receivers []common.Address) {
	it := db.NewIterator(blacklistReceiverPreifx, nil)
	defer it.Release()

	for it.Next() {
		if it.Key() == nil {
			break
		}
		addressBytes := it.Key()[len(blacklistReceiverPreifx):]
		if len(addressBytes) != common.AddressLength {
			log.Error("invalid blacklist receiver", "address bytes", hexutil.Encode(addressBytes))
			continue
		}
		receivers = append(receivers, common.BytesToAddress(addressBytes))
	}
	return
}

func WriteBlacklistSender(db ethdb.Database, sender common.Address) error {
	return db.Put(BlacklistSenderKey(sender), nil)
}

func WriteBlacklistReceiver(db ethdb.Database, receiver common.Address) error {
	return db.Put(BlacklistReceiverKey(receiver), nil)
}

func DeleteBlacklistSender(db ethdb.Database, sender common.Address) error {
	return db.Delete(BlacklistSenderKey(sender))
}

func DeleteBlacklistReceiver(db ethdb.Database, receiver common.Address) error {
	return db.Delete(BlacklistReceiverKey(receiver))
}
