package eth

import (
	"context"
	"errors"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/txpool"
)

var ErrBlacklistDisabled = errors.New("blacklist is not enabled")

// BlacklistAPI provides private RPC methods to manange blacklist
type BlacklistAPI struct {
	blacklist *txpool.Blacklist
}

func NewBlocklistAPI(eth *Ethereum) *BlacklistAPI {
	return &BlacklistAPI{
		blacklist: eth.txPool.GetBlacklist(),
	}
}

func (api *BlacklistAPI) AddSender(ctx context.Context, sender common.Address) error {
	if api.blacklist == nil {
		return ErrBlacklistDisabled
	}
	return api.blacklist.AddSender(sender)
}

func (api *BlacklistAPI) AddReceiver(ctx context.Context, receiver common.Address) error {
	if api.blacklist == nil {
		return ErrBlacklistDisabled
	}
	return api.blacklist.AddReceiver(receiver)
}

func (api *BlacklistAPI) RemoveSender(ctx context.Context, sender common.Address) error {
	if api.blacklist == nil {
		return ErrBlacklistDisabled
	}
	return api.blacklist.RemoveSender(sender)
}

func (api *BlacklistAPI) RemoveReceiver(ctx context.Context, receiver common.Address) error {
	if api.blacklist == nil {
		return ErrBlacklistDisabled
	}
	return api.blacklist.RemoveReceiver(receiver)
}

type blacklist struct {
	Sender    []common.Address
	Receivers []common.Address
}

func (api *BlacklistAPI) Show(ctx context.Context) (*blacklist, error) {
	if api.blacklist == nil {
		return nil, ErrBlacklistDisabled
	}
	senders := api.blacklist.GetBlacklistSenders()
	receivers := api.blacklist.GetBlacklistReceivers()
	return &blacklist{
		Sender:    senders,
		Receivers: receivers,
	}, nil
}
