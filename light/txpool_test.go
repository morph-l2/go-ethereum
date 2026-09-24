// Copyright 2016 The go-ethereum Authors
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

package light

import (
	"context"
	"errors"
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/consensus/ethash"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/params"
)

type testTxRelay struct {
	send, discard, mined chan int
}

func (self *testTxRelay) Send(txs types.Transactions) {
	self.send <- len(txs)
}

func (self *testTxRelay) NewHead(head common.Hash, mined []common.Hash, rollback []common.Hash) {
	m := len(mined)
	if m != 0 {
		self.mined <- m
	}
}

func (self *testTxRelay) Discard(hashes []common.Hash) {
	self.discard <- len(hashes)
}

const poolTestTxs = 1000
const poolTestBlocks = 100

// test tx 0..n-1
var testTx [poolTestTxs]*types.Transaction

// txs sent before block i
func sentTx(i int) int {
	return int(math.Pow(float64(i)/float64(poolTestBlocks), 0.9) * poolTestTxs)
}

// txs included in block i or before that (minedTx(i) <= sentTx(i))
func minedTx(i int) int {
	return int(math.Pow(float64(i)/float64(poolTestBlocks), 1.1) * poolTestTxs)
}

func txPoolTestChainGen(i int, block *core.BlockGen) {
	s := minedTx(i)
	e := minedTx(i + 1)
	for i := s; i < e; i++ {
		block.AddTx(testTx[i])
	}
}

func TestTxPool(t *testing.T) {
	for i := range testTx {
		testTx[i], _ = types.SignTx(types.NewTransaction(uint64(i), acc1Addr, big.NewInt(10000), params.TxGas, big.NewInt(params.InitialBaseFee), nil), types.HomesteadSigner{}, testBankKey)
	}

	var (
		sdb   = rawdb.NewMemoryDatabase()
		ldb   = rawdb.NewMemoryDatabase()
		gspec = core.Genesis{
			Alloc:   core.GenesisAlloc{testBankAddress: {Balance: testBankFunds}},
			BaseFee: big.NewInt(params.InitialBaseFee),
		}
		genesis = gspec.MustCommit(sdb)
	)
	gspec.MustCommit(ldb)
	// Assemble the test environment
	blockchain, _ := core.NewBlockChain(sdb, nil, params.TestChainConfig, ethash.NewFullFaker(), vm.Config{}, nil, nil)
	gchain, _ := core.GenerateChain(params.TestChainConfig, genesis, ethash.NewFaker(), sdb, poolTestBlocks, txPoolTestChainGen)
	if _, err := blockchain.InsertChain(gchain); err != nil {
		panic(err)
	}

	odr := &testOdr{sdb: sdb, ldb: ldb, indexerConfig: TestClientIndexerConfig}
	relay := &testTxRelay{
		send:    make(chan int, 1),
		discard: make(chan int, 1),
		mined:   make(chan int, 1),
	}
	lightchain, _ := NewLightChain(odr, params.TestChainConfig, ethash.NewFullFaker(), nil)
	txPermanent = 50
	pool := NewTxPool(params.TestChainConfig, lightchain, relay)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	for ii, block := range gchain {
		i := ii + 1
		s := sentTx(i - 1)
		e := sentTx(i)
		for i := s; i < e; i++ {
			pool.Add(ctx, testTx[i])
			got := <-relay.send
			exp := 1
			if got != exp {
				t.Errorf("relay.Send expected len = %d, got %d", exp, got)
			}
		}

		if _, err := lightchain.InsertHeaderChain([]*types.Header{block.Header()}, 1); err != nil {
			panic(err)
		}

		got := <-relay.mined
		exp := minedTx(i) - minedTx(i-1)
		if got != exp {
			t.Errorf("relay.NewHead expected len(mined) = %d, got %d", exp, got)
		}

		exp = 0
		if i > int(txPermanent)+1 {
			exp = minedTx(i-int(txPermanent)-1) - minedTx(i-int(txPermanent)-2)
		}
		if exp != 0 {
			got = <-relay.discard
			if got != exp {
				t.Errorf("relay.Discard expected len = %d, got %d", exp, got)
			}
		}
	}
}

func setupLightTxPoolWithConfigAndGasLimit(t *testing.T, config *params.ChainConfig, gasLimit uint64) (*TxPool, *LightChain, *core.BlockChain, *types.Block, *testTxRelay, *testOdr) {
	t.Helper()

	sdb := rawdb.NewMemoryDatabase()
	ldb := rawdb.NewMemoryDatabase()
	gspec := core.Genesis{
		Config:   config,
		Alloc:    core.GenesisAlloc{testBankAddress: {Balance: testBankFunds}},
		BaseFee:  big.NewInt(params.InitialBaseFee),
		GasLimit: gasLimit,
	}
	genesis := gspec.MustCommit(sdb)
	gspec.MustCommit(ldb)

	blockchain, err := core.NewBlockChain(sdb, nil, config, ethash.NewFullFaker(), vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("create blockchain: %v", err)
	}
	odr := &testOdr{sdb: sdb, ldb: ldb, indexerConfig: TestClientIndexerConfig}
	relay := &testTxRelay{
		send:    make(chan int, 10),
		discard: make(chan int, 10),
		mined:   make(chan int, 10),
	}
	lightchain, err := NewLightChain(odr, config, ethash.NewFullFaker(), nil)
	if err != nil {
		t.Fatalf("create light chain: %v", err)
	}
	pool := NewTxPool(config, lightchain, relay)
	t.Cleanup(func() {
		pool.Stop()
		lightchain.Stop()
		blockchain.Stop()
	})
	return pool, lightchain, blockchain, genesis, relay, odr
}

func lightPoolTransaction(nonce uint64, gasLimit uint64) *types.Transaction {
	tx, _ := types.SignTx(
		types.NewTransaction(nonce, acc1Addr, big.NewInt(10000), gasLimit, big.NewInt(1), nil),
		types.HomesteadSigner{},
		testBankKey,
	)
	return tx
}

func TestLightTxPoolRejectsOversizedGasPostAmsterdam(t *testing.T) {
	cfg := *params.TestNoL1DataFeeChainConfig
	cfg.AmsterdamTime = params.NewUint64(0)

	pool, _, _, _, _, _ := setupLightTxPoolWithConfigAndGasLimit(t, &cfg, 30_000_000)
	tx := lightPoolTransaction(0, params.MaxTxGas+1)

	if err := pool.Add(context.Background(), tx); !errors.Is(err, core.ErrGasLimitTooHigh) {
		t.Fatalf("expected ErrGasLimitTooHigh, got %v", err)
	}
}

func TestLightTxPoolAllowsOversizedGasPreAmsterdam(t *testing.T) {
	cfg := *params.TestNoL1DataFeeChainConfig
	cfg.AmsterdamTime = params.NewUint64(11)

	pool, _, _, _, _, _ := setupLightTxPoolWithConfigAndGasLimit(t, &cfg, 30_000_000)
	tx := lightPoolTransaction(0, params.MaxTxGas+1)

	if err := pool.Add(context.Background(), tx); err != nil {
		t.Fatalf("pre-Amsterdam oversized tx must be accepted, got %v", err)
	}
	if pool.GetTransaction(tx.Hash()) == nil {
		t.Fatalf("expected oversized tx to remain pending before Amsterdam activation")
	}
}

func TestLightTxPoolDropsOversizedGasOnAmsterdamActivation(t *testing.T) {
	cfg := *params.TestNoL1DataFeeChainConfig
	cfg.AmsterdamTime = params.NewUint64(10)

	pool, lightchain, blockchain, genesis, relay, odr := setupLightTxPoolWithConfigAndGasLimit(t, &cfg, 30_000_000)
	tx := lightPoolTransaction(0, params.MaxTxGas+1)
	if err := pool.Add(context.Background(), tx); err != nil {
		t.Fatalf("failed adding oversized pre-Amsterdam tx: %v", err)
	}
	if pool.GetTransaction(tx.Hash()) == nil {
		t.Fatalf("expected tx to be present before Amsterdam activation")
	}

	chain, _ := core.GenerateChain(&cfg, genesis, ethash.NewFaker(), odr.sdb, 1, nil)
	if _, err := blockchain.InsertChain(chain); err != nil {
		t.Fatalf("insert chain: %v", err)
	}
	if _, err := lightchain.InsertHeaderChain([]*types.Header{chain[0].Header()}, 1); err != nil {
		t.Fatalf("insert header chain: %v", err)
	}

	select {
	case got := <-relay.discard:
		if got != 1 {
			t.Fatalf("expected exactly one oversized tx discard, got %d", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for Amsterdam activation discard")
	}
	if !pool.amsterdam {
		t.Fatalf("expected pool to switch to Amsterdam mode")
	}
	if pool.GetTransaction(tx.Hash()) != nil {
		t.Fatalf("expected oversized tx to be purged on Amsterdam activation")
	}
}
