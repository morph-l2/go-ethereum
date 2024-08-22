package rawdb

import "github.com/morph-l2/go-ethereum/common"

var (
	blacklistSenderPrefix   = []byte("BL-s")
	blacklistReceiverPreifx = []byte("BL-r")
)

func BlacklistSenderKey(addr common.Address) []byte {
	return append(blacklistSenderPrefix, addr.Bytes()...)
}

func BlacklistReceiverKey(addr common.Address) []byte {
	return append(blacklistReceiverPreifx, addr.Bytes()...)
}
