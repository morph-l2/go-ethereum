// Copyright 2020 The go-ethereum Authors
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

// Package miner implements Ethereum block creation and mining.
package miner

import (
	"math"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/consensus/l2"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/ethdb/memorydb"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/morph-l2/go-ethereum/rollup/circuitcapacitychecker"
	"github.com/morph-l2/go-ethereum/trie"
	"github.com/stretchr/testify/require"
)

type chainConfigFunc func(config *params.ChainConfig)
type minerConfigFunc func(config *Config)

var (
	testNonce uint64
	// testKey is a private key to use for funding a tester account.
	testKey, _  = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	testKey1, _ = crypto.HexToECDSA("b71c71a67e1176ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	testKey2, _ = crypto.HexToECDSA("b71c71a67e1175ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")

	// testAddr is the Ethereum address of the tester account.
	testAddr  = crypto.PubkeyToAddress(testKey.PublicKey)
	testAddr1 = crypto.PubkeyToAddress(testKey1.PublicKey)
	testAddr2 = crypto.PubkeyToAddress(testKey2.PublicKey)

	testBalance = big.NewInt(9e18)

	destAddr = common.HexToAddress("0x9a9070028361F7AAbeB3f2F2Dc07F82C4a98A02a")
)

func l2ChainConfig(configOpt chainConfigFunc) params.ChainConfig {
	config := *params.AllEthashProtocolChanges
	config.Morph.UseZktrie = true
	config.TerminalTotalDifficulty = common.Big0
	addr := common.BigToAddress(big.NewInt(123))
	config.Morph.FeeVaultAddress = &addr
	if configOpt != nil {
		configOpt(&config)
	}
	return config
}

func l2Genesis(configOpt chainConfigFunc) *core.Genesis {
	config := l2ChainConfig(configOpt)
	timestamp := time.Now().Second()
	return &core.Genesis{
		Config: &config,
		Alloc: core.GenesisAlloc{
			testAddr:  {Balance: testBalance},
			testAddr1: {Balance: testBalance},
			testAddr2: {Balance: testBalance},
		},
		ExtraData:  []byte{},
		Timestamp:  uint64(timestamp),
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Difficulty: big.NewInt(0),
		GasLimit:   8000000,
	}
}

type mockBackend struct {
	bc      *core.BlockChain
	txPool  *core.TxPool
	chainDb ethdb.Database
}

func NewMockBackend(bc *core.BlockChain, txPool *core.TxPool, chainDb ethdb.Database) *mockBackend {
	return &mockBackend{
		bc:      bc,
		txPool:  txPool,
		chainDb: chainDb,
	}
}

func (m *mockBackend) BlockChain() *core.BlockChain {
	return m.bc
}

func (m *mockBackend) TxPool() *core.TxPool {
	return m.txPool
}

func (m *mockBackend) ChainDb() ethdb.Database {
	return m.chainDb
}

func (m *mockBackend) SetSynced() {}

func TestPending(t *testing.T) {
	miner := createMiner(t, nil, nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		block, _, _ := miner.Pending()
		if block == nil {
			t.Error("Pending failed")
		}
	}()
	wg.Wait()

	time.Sleep(3 * time.Second)

	stateDB, err := miner.chain.StateAt(miner.chain.CurrentHeader().Root)
	require.NoError(t, err)
	balance := stateDB.GetBalance(destAddr)
	require.EqualValues(t, 0, balance.Int64())

	tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey)
	err = miner.txpool.AddLocal(tx)
	require.NoError(t, err)
	block, _, stateDB := miner.Pending()
	if block == nil {
		t.Error("Pending failed")
	}

	balance = stateDB.GetBalance(destAddr)
	require.EqualValues(t, 1, balance.Int64())
}

func TestSimulateL1Messages(t *testing.T) {
	miner := createMiner(t, nil, nil)
	parentHeader := miner.chain.CurrentHeader()
	l1tx := constructSimpleL1Tx(2)
	l1tx1 := constructSimpleL1Tx(3)
	involved, skipped, err := miner.SimulateL1Messages(parentHeader.Hash(), types.Transactions{l1tx, l1tx1})
	require.NoError(t, err)
	require.EqualValues(t, 2, len(involved))
	require.EqualValues(t, 0, len(skipped))

	miner.circuitCapacityChecker.Skip(l1tx.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow)
	involved, skipped, err = miner.SimulateL1Messages(parentHeader.Hash(), types.Transactions{l1tx, l1tx1})
	require.NoError(t, err)
	require.EqualValues(t, 0, len(involved))
	require.EqualValues(t, 1, len(skipped))
	require.EqualValues(t, l1tx.Hash(), skipped[0].Tx.Hash())

	miner.circuitCapacityChecker.Skip(l1tx1.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow)
	involved, skipped, err = miner.SimulateL1Messages(parentHeader.Hash(), types.Transactions{l1tx, l1tx1})
	require.NoError(t, err)
	require.EqualValues(t, 1, len(involved))
	require.EqualValues(t, l1tx.Hash(), involved[0].Hash())
	require.EqualValues(t, 0, len(skipped))

	l1tx = constructSimpleL1Tx(0, func(tx *types.L1MessageTx) {
		tx.Gas = 8_000_001
	})
	miner.circuitCapacityChecker.Skip(l1tx1.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow)
	involved, skipped, err = miner.SimulateL1Messages(parentHeader.Hash(), types.Transactions{l1tx, l1tx1})
	require.NoError(t, err)
	require.EqualValues(t, 0, len(involved))
	require.EqualValues(t, 2, len(skipped))
	require.EqualValues(t, "gas limit exceeded", skipped[0].Reason)
	require.EqualValues(t, "row consumption overflow", skipped[1].Reason)
}

func TestBuildBlockRegular(t *testing.T) {
	t.Run("build empty block", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		parentHeader := miner.chain.CurrentHeader()
		timestamp := time.Now().Add(3 * time.Second)
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, timestamp, nil)
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.Root, newBlockResult.Block.Root())
		require.EqualValues(t, timestamp.Unix(), newBlockResult.Block.Time())
		require.EqualValues(t, parentHeader.NextL1MsgIndex, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, newBlockResult.State.IntermediateRoot(false), newBlockResult.Block.Root())
		require.EqualValues(t, types.EmptyRootHash, newBlockResult.Block.ReceiptHash())
		require.EqualValues(t, types.EmptyRootHash, newBlockResult.Block.TxHash())
		require.EqualValues(t, 0, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 0, newBlockResult.Receipts.Len())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})

	t.Run("build block with l2 transactions", func(t *testing.T) {
		miner := createMiner(t, nil, nil)

		destAddr := common.HexToAddress("0x9a9070028361F7AAbeB3f2F2Dc07F82C4a98A02a")
		stateDB, err := miner.chain.StateAt(miner.chain.CurrentHeader().Root)
		require.NoError(t, err)
		balance := stateDB.GetBalance(destAddr)
		require.EqualValues(t, 0, balance.Int64())
		tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey)
		require.NoError(t, miner.txpool.AddLocal(tx))

		tx2, _ := types.SignTx(types.NewTransaction(testNonce+1, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey)
		require.NoError(t, miner.txpool.AddLocal(tx2))

		parentHeader := miner.chain.CurrentHeader()
		timestamp := time.Now().Add(3 * time.Second)
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, timestamp, nil)
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, timestamp.Unix(), newBlockResult.Block.Time())
		require.EqualValues(t, parentHeader.NextL1MsgIndex, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, newBlockResult.State.IntermediateRoot(false), newBlockResult.Block.Root())
		require.EqualValues(t, types.DeriveSha(types.Receipts(newBlockResult.Receipts), trie.NewStackTrie(nil)), newBlockResult.Block.ReceiptHash())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{tx, tx2}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 2, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 2, newBlockResult.Receipts.Len())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
		balance = newBlockResult.State.GetBalance(destAddr)
		require.EqualValues(t, 2, balance.Int64())
	})

	t.Run("build block with l1 transactions", func(t *testing.T) {
		miner := createMiner(t, nil, nil)

		l1tx := constructSimpleL1Tx(0)
		l1tx2 := constructSimpleL1Tx(1)

		parentHeader := miner.chain.CurrentHeader()
		timestamp := time.Now().Add(3 * time.Second)
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, timestamp, types.Transactions{l1tx, l1tx2})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, timestamp.Unix(), newBlockResult.Block.Time())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+2, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, newBlockResult.State.IntermediateRoot(false), newBlockResult.Block.Root())
		require.EqualValues(t, types.DeriveSha(types.Receipts(newBlockResult.Receipts), trie.NewStackTrie(nil)), newBlockResult.Block.ReceiptHash())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{l1tx, l1tx2}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 2, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 2, newBlockResult.Receipts.Len())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
		balance := newBlockResult.State.GetBalance(*l1tx.To())
		require.EqualValues(t, 2, balance.Int64())
	})

	t.Run("build block with l1 and l2 transactions", func(t *testing.T) {
		miner := createMiner(t, nil, nil)

		l1tx := constructSimpleL1Tx(0)
		l1tx1 := constructSimpleL1Tx(1)
		tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(tx))
		tx1, _ := types.SignTx(types.NewTransaction(testNonce+1, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(tx1))
		tx2, _ := types.SignTx(types.NewTransaction(testNonce+2, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(tx2))

		miner.circuitCapacityChecker.SkipWithLatency(tx1.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow, 100*time.Millisecond)

		parentHeader := miner.chain.CurrentHeader()
		timestamp := time.Now().Add(3 * time.Second)
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, timestamp, types.Transactions{l1tx, l1tx1})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, timestamp.Unix(), newBlockResult.Block.Time())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+2, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, newBlockResult.State.IntermediateRoot(false), newBlockResult.Block.Root())
		require.EqualValues(t, types.DeriveSha(types.Receipts(newBlockResult.Receipts), trie.NewStackTrie(nil)), newBlockResult.Block.ReceiptHash())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{l1tx, l1tx1, tx}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 3, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotNil(t, miner.txpool.Get(tx1.Hash()))
	})
}

func TestBuildBlockTimeout(t *testing.T) {
	miner := createMiner(t, func(config *Config) {
		config.NewBlockTimeout = 300 * time.Millisecond
	}, nil)

	l1tx := constructSimpleL1Tx(0)
	l1tx1 := constructSimpleL1Tx(1)
	tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
	require.NoError(t, miner.txpool.AddLocal(tx))
	tx1, _ := types.SignTx(types.NewTransaction(testNonce+1, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
	require.NoError(t, miner.txpool.AddLocal(tx1))
	miner.circuitCapacityChecker.SetApplyLatency(tx1.Hash(), 400*time.Millisecond)
	tx2, _ := types.SignTx(types.NewTransaction(testNonce+2, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
	require.NoError(t, miner.txpool.AddLocal(tx2))

	parentHeader := miner.chain.CurrentHeader()
	timestamp := time.Now().Add(3 * time.Second)
	newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, timestamp, types.Transactions{l1tx, l1tx1})
	require.NoError(t, err)
	require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
	require.EqualValues(t, timestamp.Unix(), newBlockResult.Block.Time())
	require.EqualValues(t, parentHeader.NextL1MsgIndex+2, newBlockResult.Block.Header().NextL1MsgIndex)
	require.EqualValues(t, newBlockResult.State.IntermediateRoot(false), newBlockResult.Block.Root())
	require.EqualValues(t, types.DeriveSha(types.Receipts(newBlockResult.Receipts), trie.NewStackTrie(nil)), newBlockResult.Block.ReceiptHash())
	require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{l1tx, l1tx1, tx, tx1}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
	require.EqualValues(t, 4, newBlockResult.Block.Transactions().Len())
	require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
}

func TestBuildBlockErrorOnApplyStage(t *testing.T) {
	t.Run("wrong l1 index", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		l1tx := constructSimpleL1Tx(0)
		l1tx2 := constructSimpleL1Tx(2)
		l2tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(l2tx))

		parentHeader := miner.chain.CurrentHeader()
		timestamp := time.Now().Add(3 * time.Second)
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, timestamp, types.Transactions{l1tx, l1tx2})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, timestamp.Unix(), newBlockResult.Block.Time())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+1, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 2, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{l1tx, l2tx}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})
	t.Run("first l1 transaction gas limit reached", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		l1tx := constructSimpleL1Tx(0, func(tx *types.L1MessageTx) {
			tx.Gas = 8_000_001
		})
		parentHeader := miner.chain.CurrentHeader()
		timestamp := time.Now().Add(3 * time.Second)
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, timestamp, types.Transactions{l1tx})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, timestamp.Unix(), newBlockResult.Block.Time())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+1, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, parentHeader.Root, newBlockResult.Block.Root())
		require.EqualValues(t, 0, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 1, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})
	t.Run("second l1 transaction gas limit reached", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		l1tx := constructSimpleL1Tx(0, func(tx *types.L1MessageTx) {
			tx.Gas = 5_000_000
		})
		l1tx1 := constructSimpleL1Tx(1, func(tx *types.L1MessageTx) {
			tx.Gas = 3_000_001
		})
		parentHeader := miner.chain.CurrentHeader()
		timestamp := time.Now().Add(3 * time.Second)
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, timestamp, types.Transactions{l1tx, l1tx1})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, timestamp.Unix(), newBlockResult.Block.Time())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+1, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 1, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{l1tx}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})
	t.Run("l2 transaction gas limit reached", func(t *testing.T) {
		miner := createMiner(t, func(config *Config) {
			config.GasCeil = 100_000
		}, nil)
		// fill txpool with txs
		// new header gas limit should be 7992189
		reachLimitTx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), 7_992_190, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey)
		require.NoError(t, miner.txpool.AddLocal(reachLimitTx))
		tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(99), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(tx))

		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), nil)
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 1, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{tx}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
		balance := newBlockResult.State.GetBalance(destAddr)
		require.EqualValues(t, 99, balance.Int64())
	})
}

func TestBuildBlockErrorOnCCCStage(t *testing.T) {
	t.Run("first l1 transaction rows overflow", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		l1Tx := constructSimpleL1Tx(0)
		l1Tx1 := constructSimpleL1Tx(1)

		miner.circuitCapacityChecker.Skip(l1Tx.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow)

		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1Tx, l1Tx1})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+1, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 0, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 1, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})

	t.Run("second l1 transaction rows overflow", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		l1Tx := constructSimpleL1Tx(0)
		l1Tx1 := constructSimpleL1Tx(1)

		miner.circuitCapacityChecker.Skip(l1Tx1.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow)

		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1Tx, l1Tx1})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+1, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 1, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})

	t.Run("fist l2 transaction rows overflow", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		overflowTx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey)
		require.NoError(t, miner.txpool.AddLocal(overflowTx))
		tx2, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(tx2))

		miner.circuitCapacityChecker.Skip(overflowTx.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow)
		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), nil)
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 0, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})

	t.Run("second l2 transaction rows overflow", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey)
		require.NoError(t, miner.txpool.AddLocal(tx))
		overflowTx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(overflowTx))

		miner.circuitCapacityChecker.Skip(overflowTx.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow)
		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), nil)
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 1, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{tx}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})

	t.Run("l1 transaction ccc unKnown error", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		l1Tx := constructSimpleL1Tx(0)
		l1Tx1 := constructSimpleL1Tx(1)

		miner.circuitCapacityChecker.Skip(l1Tx1.Hash(), circuitcapacitychecker.ErrUnknown)

		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1Tx, l1Tx1})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+2, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 1, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{l1Tx}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 1, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))

		l2tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey)
		require.NoError(t, miner.txpool.AddLocal(l2tx))
		l2tx1, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(l2tx1))
		miner.circuitCapacityChecker.Skip(l2tx1.Hash(), circuitcapacitychecker.ErrUnknown)
		newBlockResult, err = miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), nil)
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 1, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{l2tx}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})

	t.Run("l2 transaction unKnown err", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey)
		require.NoError(t, miner.txpool.AddLocal(tx))
		tx1, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(tx1))

		miner.circuitCapacityChecker.Skip(tx.Hash(), circuitcapacitychecker.ErrUnknown)
		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), nil)
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, 0, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.Nil(t, miner.txpool.Get(tx.Hash()))
		require.NotNil(t, miner.txpool.Get(tx1.Hash()))

		miner.circuitCapacityChecker.Skip(tx1.Hash(), circuitcapacitychecker.ErrUnknown)
		newBlockResult, err = miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), nil)
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, 0, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.Nil(t, miner.txpool.Get(tx1.Hash()))
	})
}

func TestBuildBlockErrorOnEncode(t *testing.T) {
	miner := createMiner(t, nil, nil)
	l1tx := constructSimpleL1Tx(0)
	l1tx1 := constructSimpleL1Tx(1)
	circuitcapacitychecker.TestRustTraceErrorHash = l1tx1.Hash().String()

	parentHeader := miner.chain.CurrentHeader()
	newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1tx, l1tx1})
	require.NoError(t, err)
	require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
	require.EqualValues(t, 1, newBlockResult.Block.Transactions().Len())
	require.EqualValues(t, parentHeader.NextL1MsgIndex+2, newBlockResult.Block.Header().NextL1MsgIndex)
	require.EqualValues(t, 1, len(newBlockResult.SkippedTxs))
	require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{l1tx}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())

	circuitcapacitychecker.TestRustTraceErrorHash = l1tx.Hash().String()
	newBlockResult, err = miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1tx, l1tx1})
	require.NoError(t, err)
	require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
	require.EqualValues(t, 0, newBlockResult.Block.Transactions().Len())
	require.EqualValues(t, parentHeader.NextL1MsgIndex+1, newBlockResult.Block.Header().NextL1MsgIndex)
	require.EqualValues(t, 1, len(newBlockResult.SkippedTxs))

	tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
	require.NoError(t, miner.txpool.AddLocal(tx))
	tx1, _ := types.SignTx(types.NewTransaction(testNonce+1, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
	require.NoError(t, miner.txpool.AddLocal(tx1))
	circuitcapacitychecker.TestRustTraceErrorHash = tx.Hash().String()

	newBlockResult, err = miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1tx, l1tx1})
	require.NoError(t, err)
	require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
	require.EqualValues(t, 2, newBlockResult.Block.Transactions().Len())
	require.EqualValues(t, parentHeader.NextL1MsgIndex+2, newBlockResult.Block.Header().NextL1MsgIndex)
	require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
	require.Nil(t, miner.txpool.Get(tx.Hash()))

	require.NoError(t, miner.txpool.AddLocal(tx))
	circuitcapacitychecker.TestRustTraceErrorHash = tx1.Hash().String()
	newBlockResult, err = miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1tx, l1tx1})
	require.NoError(t, err)
	require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
	require.EqualValues(t, 3, newBlockResult.Block.Transactions().Len())
	require.EqualValues(t, parentHeader.NextL1MsgIndex+2, newBlockResult.Block.Header().NextL1MsgIndex)
	require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
	require.Nil(t, miner.txpool.Get(tx1.Hash()))

}

func TestBuildBlockErrorOnMultiStages(t *testing.T) {

	/*
	 * case1
	 * l1 tx: reached gaslimit
	 * l1 tx1: rows overflow
	 * l2 tx: normal
	 * expected: skip 2 l1txs, block includs none
	 */
	t.Run("case1", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		// expect gaslimit error
		l1Tx := constructSimpleL1Tx(0, func(tx *types.L1MessageTx) {
			tx.Gas = 8_000_001
		})

		// expect rows overflow error
		l1Tx1 := constructSimpleL1Tx(1)
		miner.circuitCapacityChecker.SkipWithLatency(l1Tx1.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow, 100*time.Millisecond)

		// normal l2 tx
		tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(tx))

		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1Tx, l1Tx1})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+2, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 0, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 2, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})

	/*
	 * case2
	 * l1 tx: reached gaslimit
	 * l2 tx: reached gaslimit
	 * l2 tx1: rows overflow
	 * expected: skip 1 l1tx, block includs none
	 */
	t.Run("case2", func(t *testing.T) {
		miner := createMiner(t, func(config *Config) {
			config.GasCeil = 100_000
		}, nil)

		// expect gaslimit error
		l1Tx := constructSimpleL1Tx(0, func(tx *types.L1MessageTx) {
			tx.Gas = 7_992_190
		})

		// fill txpool with txs
		// new header gas limit should be 7992189
		l2tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), 7_992_190, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey)
		require.NoError(t, miner.txpool.AddLocal(l2tx))
		// l2 tx1 rows overflow
		l2tx1, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(l2tx1))
		miner.circuitCapacityChecker.Skip(l2tx1.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow)

		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1Tx})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+1, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 0, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, 1, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})

	/*
	 * case3
	 * l1 tx: reached gaslimit
	 * l1 tx: normal
	 * l2 tx: reached gaslimit
	 * l2 tx1: normal
	 * l2 tx2: reached gaslimit
	 * l2 tx3: row overflow
	 * expected: skip 1 l1tx, block includs 2
	 */
	t.Run("case3", func(t *testing.T) {
		miner := createMiner(t, func(config *Config) {
			config.GasCeil = 100_000
		}, nil)

		// expect gaslimit error l1
		l1Tx := constructSimpleL1Tx(0, func(tx *types.L1MessageTx) {
			tx.Gas = 7_992_190
		})
		// normal l1
		l1Tx1 := constructSimpleL1Tx(1)
		// expect gaslimit error l2
		l2tx, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), 7_992_190, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey)
		require.NoError(t, miner.txpool.AddLocal(l2tx))
		// normal l2
		l2tx1, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(l2tx1))
		// expect gaslimit error l2
		l2tx2, _ := types.SignTx(types.NewTransaction(testNonce+1, destAddr, big.NewInt(1), 7_992_190, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey1)
		require.NoError(t, miner.txpool.AddLocal(l2tx2))
		// l2 tx1 rows overflow
		l2tx3, _ := types.SignTx(types.NewTransaction(testNonce, destAddr, big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(miner.chainConfig), testKey2)
		require.NoError(t, miner.txpool.AddLocal(l2tx3))
		miner.circuitCapacityChecker.Skip(l2tx3.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow)

		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1Tx, l1Tx1})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+2, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 2, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{l1Tx1, l2tx1}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 1, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})

	t.Run("consecutively l1 transactions", func(t *testing.T) {
		miner := createMiner(t, nil, nil)
		l1Tx := constructSimpleL1Tx(0)
		l1Tx1 := constructSimpleL1Tx(1)
		l1Tx2 := constructSimpleL1Tx(2)
		miner.circuitCapacityChecker.SkipWithLatency(l1Tx1.Hash(), circuitcapacitychecker.ErrBlockRowConsumptionOverflow, 100*time.Millisecond)
		parentHeader := miner.chain.CurrentHeader()
		newBlockResult, err := miner.BuildBlock(parentHeader.ParentHash, time.Now().Add(3*time.Second), types.Transactions{l1Tx, l1Tx1, l1Tx2})
		require.NoError(t, err)
		require.EqualValues(t, parentHeader.Number.Int64()+1, newBlockResult.Block.Number().Int64())
		require.EqualValues(t, parentHeader.NextL1MsgIndex+1, newBlockResult.Block.Header().NextL1MsgIndex)
		require.EqualValues(t, 1, newBlockResult.Block.Transactions().Len())
		require.EqualValues(t, types.DeriveSha(types.Transactions([]*types.Transaction{l1Tx}), trie.NewStackTrie(nil)), newBlockResult.Block.TxHash())
		require.EqualValues(t, 0, len(newBlockResult.SkippedTxs))
		require.NotEqualValues(t, 0, len(*newBlockResult.RowConsumption))
	})
}

func constructSimpleL1Tx(queueIndex uint64, setTx ...func(*types.L1MessageTx)) *types.Transaction {
	tx := &types.L1MessageTx{
		QueueIndex: queueIndex,
		Gas:        21000,
		To:         &destAddr,
		Value:      big.NewInt(1),
		Sender:     testAddr,
	}
	for _, f := range setTx {
		f(tx)
	}
	return types.NewTx(tx)
}

func createMiner(t *testing.T, minerConfig minerConfigFunc, chainConfigOpt chainConfigFunc) *Miner {
	// Create config
	config := DefaultConfig
	config.PendingFeeRecipient = common.HexToAddress("123456789")
	config.MaxAccountsNum = math.MaxInt
	if minerConfig != nil {
		minerConfig(&config)
	}
	// Create chainConfig
	memdb := memorydb.New()
	chainDB := rawdb.NewDatabase(memdb)
	genesis := l2Genesis(chainConfigOpt)
	chainConfig, _, err := core.SetupGenesisBlock(chainDB, genesis)
	if err != nil {
		t.Fatalf("can't create new chain config: %v", err)
	}
	// Create consensus engine
	engine := l2.New(nil, params.TestChainConfig)
	// Create Ethereum backend
	bc, err := core.NewBlockChain(chainDB, nil, chainConfig, engine, vm.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("can't create new chain %v", err)
	}

	poolConfig := core.DefaultTxPoolConfig
	poolConfig.Journal = ""
	pool := core.NewTxPool(poolConfig, chainConfig, bc)
	backend := NewMockBackend(bc, pool, chainDB)
	// Create Miner
	return New(backend, config, engine)
}
