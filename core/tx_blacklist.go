package core

import (
	"sync"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/ethdb"
)

type TxBlacklist struct {
	senders   map[common.Address]struct{}
	receivers map[common.Address]struct{}

	db     ethdb.Database
	signer types.Signer
	lock   sync.RWMutex
}

func NewTxBlacklist(db ethdb.Database, signer types.Signer) *TxBlacklist {
	// load blacklist from database
	senders := make(map[common.Address]struct{})
	receivers := make(map[common.Address]struct{})
	senderList := rawdb.GetBlacklistSenders(db)
	for _, sender := range senderList {
		senders[sender] = struct{}{}
	}
	receiverList := rawdb.GetBlacklistReceivers(db)
	for _, receiver := range receiverList {
		receivers[receiver] = struct{}{}
	}

	return &TxBlacklist{
		senders:   senders,
		receivers: receivers,
		db:        db,
		signer:    signer,
	}
}

func (tb *TxBlacklist) AddSender(addr common.Address) error {
	tb.lock.Lock()
	tb.senders[addr] = struct{}{}
	tb.lock.Unlock()
	return rawdb.WriteBlacklistSender(tb.db, addr)
}

func (tb *TxBlacklist) AddReceiver(addr common.Address) error {
	tb.lock.Lock()
	tb.receivers[addr] = struct{}{}
	tb.lock.Unlock()
	return rawdb.WriteBlacklistReceiver(tb.db, addr)
}

func (tb *TxBlacklist) RemoveSender(addr common.Address) error {
	tb.lock.Lock()
	delete(tb.senders, addr)
	tb.lock.Unlock()
	return rawdb.DeleteBlacklistSender(tb.db, addr)
}

func (tb *TxBlacklist) RemoveReceiver(addr common.Address) error {
	tb.lock.Lock()
	delete(tb.receivers, addr)
	tb.lock.Unlock()
	return rawdb.DeleteBlacklistReceiver(tb.db, addr)
}

func (tb *TxBlacklist) GetBlacklistSenders() (addrs []common.Address) {
	tb.lock.RLock()
	defer tb.lock.RUnlock()
	for addr := range tb.senders {
		addrs = append(addrs, addr)
	}
	return
}

func (tb *TxBlacklist) GetBlacklistReceivers() (addrs []common.Address) {
	tb.lock.RLock()
	defer tb.lock.RUnlock()
	for addr := range tb.receivers {
		addrs = append(addrs, addr)
	}
	return
}

func (tb *TxBlacklist) Validate(tx *types.Transaction) bool {
	from, err := types.Sender(tb.signer, tx)
	if err != nil {
		return false
	}
	if _, ok := tb.senders[from]; ok {
		return false
	}
	if tx.To() != nil {
		if _, ok := tb.receivers[*tx.To()]; ok {
			return false
		}
	}
	return true
}
