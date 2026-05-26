// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package scwallet

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum"
	"github.com/morph-l2/go-ethereum/accounts"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/log"
)

func TestSelfDeriveWithoutPairing(t *testing.T) {
	wallet := newTestWalletWithoutPairing()
	wallet.session = &Session{}
	wallet.deriveChain = emptyChainStateReader{}
	wallet.deriveNextPaths = []accounts.DerivationPath{accounts.DefaultBaseDerivationPath}
	wallet.deriveNextAddrs = make([]common.Address, 1)
	wallet.deriveReq = make(chan chan struct{})
	wallet.deriveQuit = make(chan chan error)

	go wallet.selfDerive()
	reqc := make(chan struct{})
	wallet.deriveReq <- reqc
	select {
	case <-reqc:
	case <-time.After(time.Second):
		t.Fatal("self-derivation did not complete")
	}

	errc := make(chan error)
	wallet.deriveQuit <- errc
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestPinAccountWithoutPairing(t *testing.T) {
	wallet := newTestWalletWithoutPairing()
	path := accounts.DefaultBaseDerivationPath
	account := wallet.makeAccount(common.HexToAddress("0x1234"), path)

	if err := wallet.pinAccount(account, path); err != nil {
		t.Fatal(err)
	}
	if len(wallet.Hub.pairings) != 0 {
		t.Fatalf("unexpected pairing entries: %d", len(wallet.Hub.pairings))
	}
}

func TestFindAccountPathWithoutPairing(t *testing.T) {
	wallet := newTestWalletWithoutPairing()
	want := accounts.DefaultBaseDerivationPath
	account := wallet.makeAccount(common.HexToAddress("0x1234"), want)

	have, err := wallet.findAccountPath(account)
	if err != nil {
		t.Fatal(err)
	}
	if have.String() != want.String() {
		t.Fatalf("path mismatch: have %s, want %s", have, want)
	}
}

func newTestWalletWithoutPairing() *Wallet {
	return &Wallet{
		Hub:       &Hub{scheme: "smartcard", pairings: make(map[string]smartcardPairing)},
		PublicKey: []byte{0, 1, 2, 3},
		log:       log.New(),
	}
}

var _ ethereum.ChainStateReader = emptyChainStateReader{}

type emptyChainStateReader struct{}

func (emptyChainStateReader) BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error) {
	return new(big.Int), nil
}

func (emptyChainStateReader) StorageAt(context.Context, common.Address, common.Hash, *big.Int) ([]byte, error) {
	return nil, nil
}

func (emptyChainStateReader) CodeAt(context.Context, common.Address, *big.Int) ([]byte, error) {
	return nil, nil
}

func (emptyChainStateReader) NonceAt(context.Context, common.Address, *big.Int) (uint64, error) {
	return 0, nil
}
