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

package filters

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/consensus/ethash"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/bloombits"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/eth/ethconfig"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/event"
	"github.com/morph-l2/go-ethereum/internal/ethapi"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/morph-l2/go-ethereum/rpc"
)

type testBackend struct {
	mux             *event.TypeMux
	db              ethdb.Database
	sections        uint64
	txFeed          event.Feed
	logsFeed        event.Feed
	rmLogsFeed      event.Feed
	chainFeed       event.Feed
	pendingBlock    *types.Block
	pendingReceipts types.Receipts
}

func (b *testBackend) ChainConfig() *params.ChainConfig {
	return params.TestChainConfig
}

func (b *testBackend) CurrentHeader() *types.Header {
	hdr, _ := b.HeaderByNumber(context.TODO(), rpc.LatestBlockNumber)
	return hdr
}

func (b *testBackend) ChainDb() ethdb.Database {
	return b.db
}

func (b *testBackend) HeaderByNumber(ctx context.Context, blockNr rpc.BlockNumber) (*types.Header, error) {
	var (
		hash common.Hash
		num  uint64
	)
	if blockNr == rpc.LatestBlockNumber {
		hash = rawdb.ReadHeadBlockHash(b.db)
		number := rawdb.ReadHeaderNumber(b.db, hash)
		if number == nil {
			return nil, nil
		}
		num = *number
	} else {
		num = uint64(blockNr)
		hash = rawdb.ReadCanonicalHash(b.db, num)
	}
	return rawdb.ReadHeader(b.db, hash, num), nil
}

func (b *testBackend) HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error) {
	number := rawdb.ReadHeaderNumber(b.db, hash)
	if number == nil {
		return nil, nil
	}
	return rawdb.ReadHeader(b.db, hash, *number), nil
}

func (b *testBackend) GetReceipts(ctx context.Context, hash common.Hash) (types.Receipts, error) {
	if number := rawdb.ReadHeaderNumber(b.db, hash); number != nil {
		header := rawdb.ReadHeader(b.db, hash, 0)
		if header == nil {
			return nil, nil
		}
		return rawdb.ReadReceipts(b.db, hash, *number, header.Time, params.TestChainConfig), nil
	}
	return nil, nil
}

func (b *testBackend) GetLogs(ctx context.Context, hash common.Hash, number uint64) ([][]*types.Log, error) {
	logs := rawdb.ReadLogs(b.db, hash, number, params.TestChainConfig)
	return logs, nil
}

func (b *testBackend) Pending() (*types.Block, types.Receipts, *state.StateDB) {
	return b.pendingBlock, b.pendingReceipts, nil
}

func (b *testBackend) SubscribeNewTxsEvent(ch chan<- core.NewTxsEvent) event.Subscription {
	return b.txFeed.Subscribe(ch)
}

func (b *testBackend) SubscribeRemovedLogsEvent(ch chan<- core.RemovedLogsEvent) event.Subscription {
	return b.rmLogsFeed.Subscribe(ch)
}

func (b *testBackend) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	return b.logsFeed.Subscribe(ch)
}

func (b *testBackend) SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription {
	return b.chainFeed.Subscribe(ch)
}

func (b *testBackend) BloomStatus() (uint64, uint64) {
	return params.BloomBitsBlocks, b.sections
}

func (b *testBackend) StateAt(root common.Hash) (*state.StateDB, error) {
	return nil, nil
}

func (b *testBackend) ServiceFilter(ctx context.Context, session *bloombits.MatcherSession) {
	requests := make(chan chan *bloombits.Retrieval)

	go session.Multiplex(16, 0, requests)
	go func() {
		for {
			// Wait for a service request or a shutdown
			select {
			case <-ctx.Done():
				return

			case request := <-requests:
				task := <-request

				task.Bitsets = make([][]byte, len(task.Sections))
				for i, section := range task.Sections {
					if rand.Int()%4 != 0 { // Handle occasional missing deliveries
						head := rawdb.ReadCanonicalHash(b.db, (section+1)*params.BloomBitsBlocks-1)
						task.Bitsets[i], _ = rawdb.ReadBloomBits(b.db, task.Bit, section, head)
					}
				}
				request <- task
			}
		}
	}()
}

func (b *testBackend) setPending(block *types.Block, receipts types.Receipts) {
	b.pendingBlock = block
	b.pendingReceipts = receipts
}

func (b *testBackend) notifyPending(logs []*types.Log) {
	genesis := &core.Genesis{
		Config: params.TestChainConfig,
	}
	_, blocks, _ := core.GenerateChainWithGenesis(genesis, ethash.NewFaker(), rawdb.NewMemoryDatabase(), 2, func(i int, b *core.BlockGen) {})
	b.setPending(blocks[1], []*types.Receipt{{Logs: logs}})
	b.chainFeed.Send(core.ChainEvent{Block: blocks[0]})
}

func newTestFilterSystem(t testing.TB, db ethdb.Database, cfg Config) (*testBackend, *FilterSystem) {
	backend := &testBackend{db: db}
	sys := NewFilterSystem(backend, cfg)
	return backend, sys
}

// TestBlockSubscription tests if a block subscription returns block hashes for posted chain events.
// It creates multiple subscriptions:
// - one at the start and should receive all posted chain events and a second (blockHashes)
// - one that is created after a cutoff moment and uninstalled after a second cutoff moment (blockHashes[cutoff1:cutoff2])
// - one that is created after the second cutoff moment (blockHashes[cutoff2:])
func TestBlockSubscription(t *testing.T) {
	t.Parallel()

	var (
		db           = rawdb.NewMemoryDatabase()
		backend, sys = newTestFilterSystem(t, db, Config{})
		api          = NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)
		genesis      = (&core.Genesis{BaseFee: big.NewInt(params.InitialBaseFee)}).MustCommit(db)
		chain, _     = core.GenerateChain(params.TestChainConfig, genesis, ethash.NewFaker(), db, 10, func(i int, gen *core.BlockGen) {})
		chainEvents  []core.ChainEvent
	)

	for _, blk := range chain {
		chainEvents = append(chainEvents, core.ChainEvent{Hash: blk.Hash(), Block: blk})
	}

	chan0 := make(chan *types.Header)
	sub0 := api.events.SubscribeNewHeads(chan0)
	chan1 := make(chan *types.Header)
	sub1 := api.events.SubscribeNewHeads(chan1)

	go func() { // simulate client
		i1, i2 := 0, 0
		for i1 != len(chainEvents) || i2 != len(chainEvents) {
			select {
			case header := <-chan0:
				if chainEvents[i1].Hash != header.Hash() {
					t.Errorf("sub0 received invalid hash on index %d, want %x, got %x", i1, chainEvents[i1].Hash, header.Hash())
				}
				i1++
			case header := <-chan1:
				if chainEvents[i2].Hash != header.Hash() {
					t.Errorf("sub1 received invalid hash on index %d, want %x, got %x", i2, chainEvents[i2].Hash, header.Hash())
				}
				i2++
			}
		}

		sub0.Unsubscribe()
		sub1.Unsubscribe()
	}()

	time.Sleep(1 * time.Second)
	for _, e := range chainEvents {
		backend.chainFeed.Send(e)
	}

	<-sub0.Err()
	<-sub1.Err()
}

func TestTransactionReceiptsSubscription(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	backend, sys := newTestFilterSystem(t, db, Config{})
	api := NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)

	tx1 := types.NewTransaction(0, common.HexToAddress("0x1000000000000000000000000000000000000001"), big.NewInt(1), 21000, big.NewInt(1), nil)
	tx2 := types.NewTransaction(1, common.HexToAddress("0x2000000000000000000000000000000000000002"), big.NewInt(2), 21000, big.NewInt(1), nil)
	header := &types.Header{Number: big.NewInt(1), Time: 1}
	ev := core.ChainEvent{
		Hash:         header.Hash(),
		Header:       header,
		Receipts:     types.Receipts{{TxHash: tx1.Hash(), Status: types.ReceiptStatusSuccessful}, {TxHash: tx2.Hash(), Status: types.ReceiptStatusSuccessful}},
		Transactions: types.Transactions{tx1, tx2},
	}

	allCh := make(chan []*ReceiptWithTx, 1)
	allSub := api.events.SubscribeTransactionReceipts(nil, allCh)
	defer allSub.Unsubscribe()
	filteredCh := make(chan []*ReceiptWithTx, 1)
	filteredSub := api.events.SubscribeTransactionReceipts([]common.Hash{tx2.Hash()}, filteredCh)
	defer filteredSub.Unsubscribe()
	multiCh := make(chan []*ReceiptWithTx, 1)
	multiSub := api.events.SubscribeTransactionReceipts([]common.Hash{tx1.Hash(), tx2.Hash()}, multiCh)
	defer multiSub.Unsubscribe()
	unmatchedCh := make(chan []*ReceiptWithTx, 1)
	unmatchedSub := api.events.SubscribeTransactionReceipts([]common.Hash{common.HexToHash("0xff")}, unmatchedCh)
	defer unmatchedSub.Unsubscribe()

	backend.chainFeed.Send(ev)
	select {
	case receipts := <-allCh:
		if len(receipts) != 2 {
			t.Fatalf("expected 2 receipts, got %d", len(receipts))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for all receipts")
	}
	select {
	case receipts := <-filteredCh:
		if len(receipts) != 1 || receipts[0].Transaction.Hash() != tx2.Hash() {
			t.Fatalf("unexpected filtered receipts: %#v", receipts)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for filtered receipt")
	}
	select {
	case receipts := <-multiCh:
		if len(receipts) != 2 {
			t.Fatalf("expected 2 multi-filtered receipts, got %d", len(receipts))
		}
		got := map[common.Hash]bool{
			receipts[0].Transaction.Hash(): true,
			receipts[1].Transaction.Hash(): true,
		}
		if !got[tx1.Hash()] || !got[tx2.Hash()] {
			t.Fatalf("unexpected multi-filtered receipts: %#v", receipts)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for multi-filtered receipts")
	}
	select {
	case receipts := <-unmatchedCh:
		t.Fatalf("unexpected empty/non-matching notification: %#v", receipts)
	case <-time.After(50 * time.Millisecond):
	}

	missingMetaCh := make(chan []*ReceiptWithTx, 1)
	missingMetaSub := api.events.SubscribeTransactionReceipts(nil, missingMetaCh)
	defer missingMetaSub.Unsubscribe()
	backend.chainFeed.Send(core.ChainEvent{Header: header, Transactions: types.Transactions{tx1}})
	select {
	case receipts := <-missingMetaCh:
		t.Fatalf("unexpected notification for event without receipts: %#v", receipts)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTransactionReceiptsSubscriptionGeneratedChain(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	backend, sys := newTestFilterSystem(t, db, Config{})
	api := NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	from := crypto.PubkeyToAddress(key.PublicKey)
	to := common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268")
	signer := types.NewLondonSigner(big.NewInt(1))
	genesis := &core.Genesis{
		Config:  params.TestChainConfig,
		BaseFee: big.NewInt(params.InitialBaseFee),
		Alloc:   core.GenesisAlloc{from: {Balance: big.NewInt(1_000_000_000_000_000_000)}},
	}
	_, chain, receipts := core.GenerateChainWithGenesis(genesis, ethash.NewFaker(), db, 1, func(i int, gen *core.BlockGen) {
		for nonce := uint64(0); nonce < 4; nonce++ {
			tx, err := types.SignTx(types.NewTx(&types.LegacyTx{
				Nonce:    nonce,
				GasPrice: gen.BaseFee(),
				Gas:      21000,
				To:       &to,
				Value:    big.NewInt(1),
			}), signer, key)
			if err != nil {
				t.Fatalf("failed to sign tx: %v", err)
			}
			gen.AddTx(tx)
		}
	})
	if len(chain) != 1 || len(receipts) != 1 || len(receipts[0]) != 4 {
		t.Fatalf("unexpected generated chain receipts: blocks=%d receipt batches=%d receipts=%d", len(chain), len(receipts), len(receipts[0]))
	}

	txHashes := []common.Hash{chain[0].Transactions()[1].Hash(), chain[0].Transactions()[3].Hash()}
	receiptCh := make(chan []*ReceiptWithTx, 1)
	sub := api.events.SubscribeTransactionReceipts(txHashes, receiptCh)
	defer sub.Unsubscribe()

	backend.chainFeed.Send(core.ChainEvent{
		Hash:         chain[0].Hash(),
		Header:       chain[0].Header(),
		Receipts:     receipts[0],
		Transactions: chain[0].Transactions(),
	})
	select {
	case got := <-receiptCh:
		if len(got) != len(txHashes) {
			t.Fatalf("expected %d receipts, got %d", len(txHashes), len(got))
		}
		matches := make(map[common.Hash]bool, len(got))
		for _, receipt := range got {
			matches[receipt.Receipt.TxHash] = true
		}
		for _, hash := range txHashes {
			if !matches[hash] {
				t.Fatalf("missing receipt for tx %s in %#v", hash, got)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for generated-chain receipts")
	}
}

func TestTransactionReceiptsRejectsTooManyHashes(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	_, sys := newTestFilterSystem(t, db, Config{})
	api := NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)

	hashes := make([]common.Hash, maxTxHashes+1)
	_, err := api.TransactionReceipts(context.Background(), &TransactionReceiptsQuery{TransactionHashes: hashes})
	if !errors.Is(err, errExceedMaxTxHashes) {
		t.Fatalf("unexpected error: got %v want %v", err, errExceedMaxTxHashes)
	}
}

// TestPendingTxFilter tests whether pending tx filters retrieve all pending transactions that are posted to the event mux.
func TestPendingTxFilter(t *testing.T) {
	t.Parallel()

	var (
		db           = rawdb.NewMemoryDatabase()
		backend, sys = newTestFilterSystem(t, db, Config{})
		api          = NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)

		transactions = []*types.Transaction{
			types.NewTransaction(0, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil),
			types.NewTransaction(1, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil),
			types.NewTransaction(2, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil),
			types.NewTransaction(3, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil),
			types.NewTransaction(4, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil),
		}

		hashes []common.Hash
	)

	fid0 := api.NewPendingTransactionFilter(nil)

	time.Sleep(1 * time.Second)
	backend.txFeed.Send(core.NewTxsEvent{Txs: transactions})

	timeout := time.Now().Add(1 * time.Second)
	for {
		results, err := api.GetFilterChanges(fid0)
		if err != nil {
			t.Fatalf("Unable to retrieve logs: %v", err)
		}

		h := results.([]common.Hash)
		hashes = append(hashes, h...)
		if len(hashes) >= len(transactions) {
			break
		}
		// check timeout
		if time.Now().After(timeout) {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if len(hashes) != len(transactions) {
		t.Errorf("invalid number of transactions, want %d transactions(s), got %d", len(transactions), len(hashes))
		return
	}
	for i := range hashes {
		if hashes[i] != transactions[i].Hash() {
			t.Errorf("hashes[%d] invalid, want %x, got %x", i, transactions[i].Hash(), hashes[i])
		}
	}
}

// TestPendingTxFilterFullTx tests whether pending tx filters retrieve all pending transactions that are posted to the event mux.
func TestPendingTxFilterFullTx(t *testing.T) {
	t.Parallel()

	var (
		db           = rawdb.NewMemoryDatabase()
		backend, sys = newTestFilterSystem(t, db, Config{})
		api          = NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)

		transactions = []*types.Transaction{
			types.NewTransaction(0, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil),
			types.NewTransaction(1, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil),
			types.NewTransaction(2, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil),
			types.NewTransaction(3, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil),
			types.NewTransaction(4, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil),
		}

		txs []*ethapi.RPCTransaction
	)

	fullTx := true
	fid0 := api.NewPendingTransactionFilter(&fullTx)

	time.Sleep(1 * time.Second)
	backend.txFeed.Send(core.NewTxsEvent{Txs: transactions})

	timeout := time.Now().Add(1 * time.Second)
	for {
		results, err := api.GetFilterChanges(fid0)
		if err != nil {
			t.Fatalf("Unable to retrieve logs: %v", err)
		}

		tx := results.([]*ethapi.RPCTransaction)
		txs = append(txs, tx...)
		if len(txs) >= len(transactions) {
			break
		}
		// check timeout
		if time.Now().After(timeout) {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if len(txs) != len(transactions) {
		t.Errorf("invalid number of transactions, want %d transactions(s), got %d", len(transactions), len(txs))
		return
	}
	for i := range txs {
		if txs[i].Hash != transactions[i].Hash() {
			t.Errorf("hashes[%d] invalid, want %x, got %x", i, transactions[i].Hash(), txs[i].Hash)
		}
	}
}

// TestLogFilterCreation test whether a given filter criteria makes sense.
// If not it must return an error.
func TestLogFilterCreation(t *testing.T) {
	var (
		db     = rawdb.NewMemoryDatabase()
		_, sys = newTestFilterSystem(t, db, Config{})
		api    = NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)

		testCases = []struct {
			crit    FilterCriteria
			success bool
		}{
			// defaults
			{FilterCriteria{}, true},
			// valid block number range
			{FilterCriteria{FromBlock: big.NewInt(1), ToBlock: big.NewInt(2)}, true},
			// "mined" block range to pending
			{FilterCriteria{FromBlock: big.NewInt(1), ToBlock: big.NewInt(rpc.LatestBlockNumber.Int64())}, true},
			// new mined and pending blocks
			{FilterCriteria{FromBlock: big.NewInt(rpc.LatestBlockNumber.Int64()), ToBlock: big.NewInt(rpc.PendingBlockNumber.Int64())}, true},
			// from block "higher" than to block
			{FilterCriteria{FromBlock: big.NewInt(2), ToBlock: big.NewInt(1)}, false},
			// from block "higher" than to block
			{FilterCriteria{FromBlock: big.NewInt(rpc.LatestBlockNumber.Int64()), ToBlock: big.NewInt(100)}, false},
			// from block "higher" than to block
			{FilterCriteria{FromBlock: big.NewInt(rpc.PendingBlockNumber.Int64()), ToBlock: big.NewInt(100)}, false},
			// from block "higher" than to block
			{FilterCriteria{FromBlock: big.NewInt(rpc.PendingBlockNumber.Int64()), ToBlock: big.NewInt(rpc.LatestBlockNumber.Int64())}, false},
		}
	)

	for i, test := range testCases {
		_, err := api.NewFilter(test.crit)
		if test.success && err != nil {
			t.Errorf("expected filter creation for case %d to success, got %v", i, err)
		}
		if !test.success && err == nil {
			t.Errorf("expected testcase %d to fail with an error", i)
		}
	}
}

// TestInvalidLogFilterCreation tests whether invalid filter log criteria results in an error
// when the filter is created.
func TestInvalidLogFilterCreation(t *testing.T) {
	t.Parallel()

	var (
		db     = rawdb.NewMemoryDatabase()
		_, sys = newTestFilterSystem(t, db, Config{})
		api    = NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)
	)

	// different situations where log filter creation should fail.
	// Reason: fromBlock > toBlock
	testCases := []FilterCriteria{
		0: {FromBlock: big.NewInt(rpc.PendingBlockNumber.Int64()), ToBlock: big.NewInt(rpc.LatestBlockNumber.Int64())},
		1: {FromBlock: big.NewInt(rpc.PendingBlockNumber.Int64()), ToBlock: big.NewInt(100)},
		2: {FromBlock: big.NewInt(rpc.LatestBlockNumber.Int64()), ToBlock: big.NewInt(100)},
	}

	for i, test := range testCases {
		if _, err := api.NewFilter(test); err == nil {
			t.Errorf("Expected NewFilter for case #%d to fail", i)
		}
	}
}

func TestInvalidGetLogsRequest(t *testing.T) {
	var (
		db        = rawdb.NewMemoryDatabase()
		_, sys    = newTestFilterSystem(t, db, Config{})
		api       = NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)
		blockHash = common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	)

	// Reason: Cannot specify both BlockHash and FromBlock/ToBlock)
	testCases := []FilterCriteria{
		0: {BlockHash: &blockHash, FromBlock: big.NewInt(100)},
		1: {BlockHash: &blockHash, ToBlock: big.NewInt(500)},
		2: {BlockHash: &blockHash, FromBlock: big.NewInt(rpc.LatestBlockNumber.Int64())},
	}

	for i, test := range testCases {
		if _, err := api.GetLogs(context.Background(), test); err == nil {
			t.Errorf("Expected Logs for case #%d to fail", i)
		}
	}
}

func TestGetLogsRange(t *testing.T) {
	var (
		db     = rawdb.NewMemoryDatabase()
		_, sys = newTestFilterSystem(t, db, Config{})
		api    = NewFilterAPI(sys, false, 2)
	)
	(&core.Genesis{
		Config: params.TestChainConfig,
	}).MustCommit(db)
	chain, _ := core.NewBlockChain(db, nil, params.TestChainConfig, ethash.NewFaker(), vm.Config{}, nil, nil)
	bs, _ := core.GenerateChain(params.TestChainConfig, chain.Genesis(), ethash.NewFaker(), db, 10, nil)
	if _, err := chain.InsertChain(bs); err != nil {
		panic(err)
	}
	// those test cases should fail because block range is greater then limit
	failTestCases := []FilterCriteria{
		// from 0 to 2 block
		0: {FromBlock: big.NewInt(0), ToBlock: big.NewInt(2)},
		// from 8 to latest block (10)
		1: {FromBlock: big.NewInt(8)},
		// from 0 to latest block (10)
		2: {FromBlock: big.NewInt(0)},
	}
	for i, test := range failTestCases {
		if _, err := api.GetLogs(context.Background(), test); err == nil {
			t.Errorf("Expected Logs for failing case #%d to fail", i)
		}
	}

	okTestCases := []FilterCriteria{
		// from latest to latest block
		0: {},
		// from 9 to last block (10)
		1: {FromBlock: big.NewInt(9)},
		// from 3 to 4 block
		2: {FromBlock: big.NewInt(3), ToBlock: big.NewInt(4)},
	}
	for i, test := range okTestCases {
		if _, err := api.GetLogs(context.Background(), test); err != nil {
			t.Errorf("Expected Logs for ok case #%d not to fail", i)
		}
	}
}

// TestLogFilter tests whether log filters match the correct logs that are posted to the event feed.
func TestLogFilter(t *testing.T) {
	t.Parallel()

	var (
		db           = rawdb.NewMemoryDatabase()
		backend, sys = newTestFilterSystem(t, db, Config{})
		api          = NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)

		firstAddr      = common.HexToAddress("0x1111111111111111111111111111111111111111")
		secondAddr     = common.HexToAddress("0x2222222222222222222222222222222222222222")
		thirdAddress   = common.HexToAddress("0x3333333333333333333333333333333333333333")
		notUsedAddress = common.HexToAddress("0x9999999999999999999999999999999999999999")
		firstTopic     = common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
		secondTopic    = common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
		notUsedTopic   = common.HexToHash("0x9999999999999999999999999999999999999999999999999999999999999999")

		// posted twice, once as regular logs and once as pending logs.
		allLogs = []*types.Log{
			{Address: firstAddr},
			{Address: firstAddr, Topics: []common.Hash{firstTopic}, BlockNumber: 1},
			{Address: secondAddr, Topics: []common.Hash{firstTopic}, BlockNumber: 1},
			{Address: thirdAddress, Topics: []common.Hash{secondTopic}, BlockNumber: 2},
			{Address: thirdAddress, Topics: []common.Hash{secondTopic}, BlockNumber: 3},
		}

		expectedCase7  = []*types.Log{allLogs[3], allLogs[4], allLogs[0], allLogs[1], allLogs[2], allLogs[3], allLogs[4]}
		expectedCase11 = []*types.Log{allLogs[1], allLogs[2], allLogs[1], allLogs[2]}

		testCases = []struct {
			crit     FilterCriteria
			expected []*types.Log
			id       rpc.ID
		}{
			// match all
			0: {FilterCriteria{}, allLogs, ""},
			// match none due to no matching addresses
			1: {FilterCriteria{Addresses: []common.Address{{}, notUsedAddress}, Topics: [][]common.Hash{nil}}, []*types.Log{}, ""},
			// match logs based on addresses, ignore topics
			2: {FilterCriteria{Addresses: []common.Address{firstAddr}}, allLogs[:2], ""},
			// match none due to no matching topics (match with address)
			3: {FilterCriteria{Addresses: []common.Address{secondAddr}, Topics: [][]common.Hash{{notUsedTopic}}}, []*types.Log{}, ""},
			// match logs based on addresses and topics
			4: {FilterCriteria{Addresses: []common.Address{thirdAddress}, Topics: [][]common.Hash{{firstTopic, secondTopic}}}, allLogs[3:5], ""},
			// match logs based on multiple addresses and "or" topics
			5: {FilterCriteria{Addresses: []common.Address{secondAddr, thirdAddress}, Topics: [][]common.Hash{{firstTopic, secondTopic}}}, allLogs[2:5], ""},
			// logs in the pending block
			6: {FilterCriteria{Addresses: []common.Address{firstAddr}, FromBlock: big.NewInt(rpc.PendingBlockNumber.Int64()), ToBlock: big.NewInt(rpc.PendingBlockNumber.Int64())}, allLogs[:2], ""},
			// mined logs with block num >= 2 or pending logs
			7: {FilterCriteria{FromBlock: big.NewInt(2), ToBlock: big.NewInt(rpc.PendingBlockNumber.Int64())}, expectedCase7, ""},
			// all "mined" logs with block num >= 2
			8: {FilterCriteria{FromBlock: big.NewInt(2), ToBlock: big.NewInt(rpc.LatestBlockNumber.Int64())}, allLogs[3:], ""},
			// all "mined" logs
			9: {FilterCriteria{ToBlock: big.NewInt(rpc.LatestBlockNumber.Int64())}, allLogs, ""},
			// all "mined" logs with 1>= block num <=2 and topic secondTopic
			10: {FilterCriteria{FromBlock: big.NewInt(1), ToBlock: big.NewInt(2), Topics: [][]common.Hash{{secondTopic}}}, allLogs[3:4], ""},
			// all "mined" and pending logs with topic firstTopic
			11: {FilterCriteria{FromBlock: big.NewInt(rpc.LatestBlockNumber.Int64()), ToBlock: big.NewInt(rpc.PendingBlockNumber.Int64()), Topics: [][]common.Hash{{firstTopic}}}, expectedCase11, ""},
			// match all logs due to wildcard topic
			12: {FilterCriteria{Topics: [][]common.Hash{nil}}, allLogs[1:], ""},
		}
	)

	// create all filters
	for i := range testCases {
		testCases[i].id, _ = api.NewFilter(testCases[i].crit)
	}

	// raise events
	time.Sleep(1 * time.Second)
	if nsend := backend.logsFeed.Send(allLogs); nsend == 0 {
		t.Fatal("Logs event not delivered")
	}

	// set pending logs
	backend.notifyPending(allLogs)

	for i, tt := range testCases {
		var fetched []*types.Log
		timeout := time.Now().Add(1 * time.Second)
		for { // fetch all expected logs
			results, err := api.GetFilterChanges(tt.id)
			if err != nil {
				t.Fatalf("Unable to fetch logs: %v", err)
			}

			fetched = append(fetched, results.([]*types.Log)...)
			if len(fetched) >= len(tt.expected) {
				break
			}
			// check timeout
			if time.Now().After(timeout) {
				break
			}

			time.Sleep(100 * time.Millisecond)
		}

		if len(fetched) != len(tt.expected) {
			t.Errorf("invalid number of logs for case %d, want %d log(s), got %d", i, len(tt.expected), len(fetched))
			return
		}

		for l := range fetched {
			if fetched[l].Removed {
				t.Errorf("expected log not to be removed for log %d in case %d", l, i)
			}
			if !reflect.DeepEqual(fetched[l], tt.expected[l]) {
				t.Errorf("invalid log on index %d for case %d", l, i)
			}
		}
	}
}

// TestPendingLogsSubscription tests if a subscription receives the correct pending logs that are posted to the event feed.
func TestPendingLogsSubscription(t *testing.T) {
	t.Parallel()

	var (
		db           = rawdb.NewMemoryDatabase()
		backend, sys = newTestFilterSystem(t, db, Config{})
		api          = NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)

		firstAddr      = common.HexToAddress("0x1111111111111111111111111111111111111111")
		secondAddr     = common.HexToAddress("0x2222222222222222222222222222222222222222")
		thirdAddress   = common.HexToAddress("0x3333333333333333333333333333333333333333")
		notUsedAddress = common.HexToAddress("0x9999999999999999999999999999999999999999")
		firstTopic     = common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
		secondTopic    = common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
		thirdTopic     = common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333")
		fourthTopic    = common.HexToHash("0x4444444444444444444444444444444444444444444444444444444444444444")
		notUsedTopic   = common.HexToHash("0x9999999999999999999999999999999999999999999999999999999999999999")

		allLogs = [][]*types.Log{
			{{Address: firstAddr, Topics: []common.Hash{}, BlockNumber: 0}},
			{{Address: firstAddr, Topics: []common.Hash{firstTopic}, BlockNumber: 1}},
			{{Address: secondAddr, Topics: []common.Hash{firstTopic}, BlockNumber: 2}},
			{{Address: thirdAddress, Topics: []common.Hash{secondTopic}, BlockNumber: 3}},
			{{Address: thirdAddress, Topics: []common.Hash{secondTopic}, BlockNumber: 4}},
			{
				{Address: thirdAddress, Topics: []common.Hash{firstTopic}, BlockNumber: 5},
				{Address: thirdAddress, Topics: []common.Hash{thirdTopic}, BlockNumber: 5},
				{Address: thirdAddress, Topics: []common.Hash{fourthTopic}, BlockNumber: 5},
				{Address: firstAddr, Topics: []common.Hash{firstTopic}, BlockNumber: 5},
			},
		}

		pendingBlockNumber = big.NewInt(rpc.PendingBlockNumber.Int64())

		testCases = []struct {
			crit     ethereum.FilterQuery
			expected []*types.Log
			c        chan []*types.Log
			sub      *Subscription
			err      chan error
		}{
			// match all
			{
				ethereum.FilterQuery{FromBlock: pendingBlockNumber, ToBlock: pendingBlockNumber},
				flattenLogs(allLogs),
				nil, nil, nil,
			},
			// match none due to no matching addresses
			{
				ethereum.FilterQuery{Addresses: []common.Address{{}, notUsedAddress}, Topics: [][]common.Hash{nil}, FromBlock: pendingBlockNumber, ToBlock: pendingBlockNumber},
				nil,
				nil, nil, nil,
			},
			// match logs based on addresses, ignore topics
			{
				ethereum.FilterQuery{Addresses: []common.Address{firstAddr}, FromBlock: pendingBlockNumber, ToBlock: pendingBlockNumber},
				append(flattenLogs(allLogs[:2]), allLogs[5][3]),
				nil, nil, nil,
			},
			// match none due to no matching topics (match with address)
			{
				ethereum.FilterQuery{Addresses: []common.Address{secondAddr}, Topics: [][]common.Hash{{notUsedTopic}}, FromBlock: pendingBlockNumber, ToBlock: pendingBlockNumber},
				nil,
				nil, nil, nil,
			},
			// match logs based on addresses and topics
			{
				ethereum.FilterQuery{Addresses: []common.Address{thirdAddress}, Topics: [][]common.Hash{{firstTopic, secondTopic}}, FromBlock: pendingBlockNumber, ToBlock: pendingBlockNumber},
				append(flattenLogs(allLogs[3:5]), allLogs[5][0]),
				nil, nil, nil,
			},
			// match logs based on multiple addresses and "or" topics
			{
				ethereum.FilterQuery{Addresses: []common.Address{secondAddr, thirdAddress}, Topics: [][]common.Hash{{firstTopic, secondTopic}}, FromBlock: pendingBlockNumber, ToBlock: pendingBlockNumber},
				append(flattenLogs(allLogs[2:5]), allLogs[5][0]),
				nil, nil, nil,
			},
			// multiple pending logs, should match only 2 topics from the logs in block 5
			{
				ethereum.FilterQuery{Addresses: []common.Address{thirdAddress}, Topics: [][]common.Hash{{firstTopic, fourthTopic}}, FromBlock: pendingBlockNumber, ToBlock: pendingBlockNumber},
				[]*types.Log{allLogs[5][0], allLogs[5][2]},
				nil, nil, nil,
			},
			// match none due to only matching new mined logs
			{
				ethereum.FilterQuery{},
				nil,
				nil, nil, nil,
			},
			// match none due to only matching mined logs within a specific block range
			{
				ethereum.FilterQuery{FromBlock: big.NewInt(1), ToBlock: big.NewInt(2)},
				nil,
				nil, nil, nil,
			},
			// match all due to matching mined and pending logs
			{
				ethereum.FilterQuery{FromBlock: big.NewInt(rpc.LatestBlockNumber.Int64()), ToBlock: big.NewInt(rpc.PendingBlockNumber.Int64())},
				flattenLogs(allLogs),
				nil, nil, nil,
			},
			// match none due to matching logs from a specific block number to new mined blocks
			{
				ethereum.FilterQuery{FromBlock: big.NewInt(1), ToBlock: big.NewInt(rpc.LatestBlockNumber.Int64())},
				nil,
				nil, nil, nil,
			},
		}
	)

	// create all subscriptions, this ensures all subscriptions are created before the events are posted.
	// on slow machines this could otherwise lead to missing events when the subscription is created after
	// (some) events are posted.
	for i := range testCases {
		testCases[i].c = make(chan []*types.Log)
		testCases[i].err = make(chan error)

		var err error
		testCases[i].sub, err = api.events.SubscribeLogs(testCases[i].crit, testCases[i].c)
		if err != nil {
			t.Fatalf("SubscribeLogs %d failed: %v\n", i, err)
		}
	}

	for n, test := range testCases {
		i := n
		tt := test
		go func() {
			defer tt.sub.Unsubscribe()

			var fetched []*types.Log

			timeout := time.After(1 * time.Second)
		fetchLoop:
			for {
				select {
				case logs := <-tt.c:
					// Do not break early if we've fetched greater, or equal,
					// to the number of logs expected. This ensures we do not
					// deadlock the filter system because it will do a blocking
					// send on this channel if another log arrives.
					fetched = append(fetched, logs...)
				case <-timeout:
					break fetchLoop
				}
			}

			if len(fetched) != len(tt.expected) {
				tt.err <- fmt.Errorf("invalid number of logs for case %d, want %d log(s), got %d", i, len(tt.expected), len(fetched))
				return
			}

			for l := range fetched {
				if fetched[l].Removed {
					tt.err <- fmt.Errorf("expected log not to be removed for log %d in case %d", l, i)
					return
				}
				if !reflect.DeepEqual(fetched[l], tt.expected[l]) {
					tt.err <- fmt.Errorf("invalid log on index %d for case %d\n", l, i)
					return
				}
			}
			tt.err <- nil
		}()
	}

	// set pending logs
	var flattenLogs []*types.Log
	for _, logs := range allLogs {
		flattenLogs = append(flattenLogs, logs...)
	}
	backend.notifyPending(flattenLogs)

	for i := range testCases {
		err := <-testCases[i].err
		if err != nil {
			t.Fatalf("test %d failed: %v", i, err)
		}
		<-testCases[i].sub.Err()
	}
}

// TestPendingTxFilterDeadlock tests if the event loop hangs when pending
// txes arrive at the same time that one of multiple filters is timing out.
// Please refer to #22131 for more details.
func TestPendingTxFilterDeadlock(t *testing.T) {
	t.Parallel()
	timeout := 100 * time.Millisecond

	var (
		db           = rawdb.NewMemoryDatabase()
		backend, sys = newTestFilterSystem(t, db, Config{Timeout: timeout})
		api          = NewFilterAPI(sys, false, ethconfig.Defaults.MaxBlockRange)
		done         = make(chan struct{})
	)

	go func() {
		// Bombard feed with txes until signal was received to stop
		i := uint64(0)
		for {
			select {
			case <-done:
				return
			default:
			}

			tx := types.NewTransaction(i, common.HexToAddress("0xb794f5ea0ba39494ce83a213fffba74279579268"), new(big.Int), 0, new(big.Int), nil)
			backend.txFeed.Send(core.NewTxsEvent{Txs: []*types.Transaction{tx}})
			i++
		}
	}()

	// Create a bunch of filters that will
	// timeout either in 100ms or 200ms
	fids := make([]rpc.ID, 20)
	for i := 0; i < len(fids); i++ {
		fid := api.NewPendingTransactionFilter(nil)
		fids[i] = fid
		// Wait for at least one tx to arrive in filter
		for {
			hashes, err := api.GetFilterChanges(fid)
			if err != nil {
				t.Fatalf("Filter should exist: %v\n", err)
			}
			if len(hashes.([]common.Hash)) > 0 {
				break
			}
			runtime.Gosched()
		}
	}

	// Wait until filters have timed out
	time.Sleep(3 * timeout)

	// If tx loop doesn't consume `done` after a second
	// it's hanging.
	select {
	case done <- struct{}{}:
		// Check that all filters have been uninstalled
		for _, fid := range fids {
			if _, err := api.GetFilterChanges(fid); err == nil {
				t.Errorf("Filter %s should have been uninstalled\n", fid)
			}
		}
	case <-time.After(1 * time.Second):
		t.Error("Tx sending loop hangs")
	}
}

func flattenLogs(pl [][]*types.Log) []*types.Log {
	var logs []*types.Log
	for _, l := range pl {
		logs = append(logs, l...)
	}
	return logs
}
