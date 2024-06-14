// Copyright 2014 The go-ethereum Authors
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
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/common/hexutil"
	"github.com/scroll-tech/go-ethereum/consensus"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/state"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/params"
	"github.com/scroll-tech/go-ethereum/rollup/circuitcapacitychecker"
)

// Backend wraps all methods required for mining.
type Backend interface {
	BlockChain() *core.BlockChain
	TxPool() *core.TxPool
	ChainDb() ethdb.Database
	SetSynced()
}

// Config is the configuration parameters of mining.
type Config struct {
	Etherbase           common.Address `toml:"-"`          // Deprecated
	PendingFeeRecipient common.Address `toml:"-"`          // Address for pending block rewards.
	ExtraData           hexutil.Bytes  `toml:",omitempty"` // Block extra data set by the miner
	GasFloor            uint64         // Target gas floor for mined blocks.
	GasCeil             uint64         // Target gas ceiling for mined blocks.
	GasPrice            *big.Int       // Minimum gas price for mining a transaction
	Recommit            time.Duration  // The time interval for miner to re-create mining work.

	NewBlockTimeout      time.Duration // The maximum time allowance for creating a new block
	StoreSkippedTxTraces bool          // Whether store the wrapped traces when storing a skipped tx
	MaxAccountsNum       int           // Maximum number of accounts that miner will fetch the pending transactions of when building a new block
}

// DefaultConfig contains default settings for miner.
var DefaultConfig = Config{
	GasCeil:  8000000,
	GasPrice: big.NewInt(params.GWei / 1000),

	// The default recommit time is chosen as two seconds since
	// consensus-layer usually will wait a half slot of time(6s)
	// for payload generation. It should be enough for Geth to
	// run 3 rounds.
	Recommit: 2 * time.Second,
}

// Miner creates blocks and searches for proof-of-work values.
type Miner struct {
	confMu      sync.RWMutex // The lock used to protect the config fields: GasCeil, GasTip and Extradata
	config      *Config
	chainConfig *params.ChainConfig
	engine      consensus.Engine
	chainDB     ethdb.Database
	txpool      *core.TxPool
	chain       *core.BlockChain
	pending     *pending
	pendingMu   sync.Mutex // Lock protects the pending block

	// newBlockTimeout is the maximum timeout allowance for creating block.
	// The default value is 3 seconds but node operator can set it to arbitrary
	// large value. A large timeout allowance may cause Geth to fail creating
	// a non-empty block within the specified time and eventually miss the chance to be a proposer
	// in case there are some computation expensive transactions in txpool.
	newBlockTimeout time.Duration

	// Make sure the checker here is used by a single block one time, and must be reset for another block.
	circuitCapacityChecker *circuitcapacitychecker.CircuitCapacityChecker
	prioritizedTx          *prioritizedTransaction

	getWorkCh chan *getWorkReq
	exitCh    chan struct{}
	wg        sync.WaitGroup
}

func New(eth Backend, config Config, engine consensus.Engine) *Miner {
	// Sanitize the timeout config for creating block.
	newBlockTimeout := config.NewBlockTimeout
	if newBlockTimeout == 0 {
		log.Warn("Sanitizing new block timeout to default", "provided", newBlockTimeout, "updated", 3*time.Second)
		newBlockTimeout = 3 * time.Second
	}
	if newBlockTimeout < time.Millisecond*100 {
		log.Warn("Low block timeout may cause high amount of non-full blocks", "provided", newBlockTimeout, "default", 3*time.Second)
	}

	miner := &Miner{
		config:                 &config,
		chainConfig:            eth.BlockChain().Config(),
		chainDB:                eth.ChainDb(),
		engine:                 engine,
		txpool:                 eth.TxPool(),
		chain:                  eth.BlockChain(),
		pending:                &pending{},
		circuitCapacityChecker: circuitcapacitychecker.NewCircuitCapacityChecker(true),

		newBlockTimeout: newBlockTimeout,
		getWorkCh:       make(chan *getWorkReq),
		exitCh:          make(chan struct{}),
	}
	miner.wg.Add(1)
	go miner.generateWorkLoop()

	// fixme later
	// short-term fix: setSynced when consensus client notifies it
	// long-term fix: setSynced when snap sync completed
	eth.SetSynced()
	return miner
}

func (miner *Miner) Close() {
	close(miner.exitCh)
	miner.wg.Wait()
}

func (miner *Miner) SetExtra(extra []byte) error {
	if uint64(len(extra)) > params.MaximumExtraDataSize {
		return fmt.Errorf("extra exceeds max length. %d > %v", len(extra), params.MaximumExtraDataSize)
	}
	miner.confMu.Lock()
	miner.config.ExtraData = extra
	miner.confMu.Unlock()
	return nil
}

// Pending returns the currently pending block and associated receipts, logs
// and statedb. The returned values can be nil in case the pending block is
// not initialized.
func (miner *Miner) Pending() (*types.Block, types.Receipts, *state.StateDB) {
	pending := miner.getPending()
	if pending == nil {
		return nil, nil, nil
	}
	return pending.Block, pending.Receipts, pending.State.Copy()
}

// SetGasCeil sets the gaslimit to strive for when mining blocks post 1559.
// For pre-1559 blocks, it sets the ceiling.
func (miner *Miner) SetGasCeil(ceil uint64) {
	miner.confMu.Lock()
	miner.config.GasCeil = ceil
	miner.confMu.Unlock()
}

func (miner *Miner) GetCCC() *circuitcapacitychecker.CircuitCapacityChecker {
	return miner.circuitCapacityChecker
}

func (miner *Miner) getSealingBlockAndState(params *generateParams) (*NewBlockResult, error) {
	interrupt := new(int32)
	timer := time.AfterFunc(params.timeout, func() {
		atomic.StoreInt32(interrupt, commitInterruptTimeout)
	})
	defer timer.Stop()

	req := &getWorkReq{
		interrupt: interrupt,
		params:    params,
		result:    make(chan getWorkResp),
	}
	select {
	case miner.getWorkCh <- req:
		result := <-req.result
		close(req.result)
		return result.ret, result.err
	case <-miner.exitCh:
		return nil, errors.New("miner closed")
	}
}

func (miner *Miner) BuildBlock(parentHash common.Hash, timestamp time.Time, transactions types.Transactions) (*NewBlockResult, error) {
	return miner.getSealingBlockAndState(&generateParams{
		timestamp:    uint64(timestamp.Unix()),
		parentHash:   parentHash,
		transactions: transactions,
		timeout:      miner.newBlockTimeout,
	})
}

func (miner *Miner) SimulateL1Messages(parentHash common.Hash, transactions types.Transactions) ([]*types.Transaction, []*types.SkippedTransaction, error) {
	if transactions.Len() == 0 {
		return nil, nil, nil
	}

	ret, err := miner.getSealingBlockAndState(&generateParams{
		timestamp:    uint64(time.Now().Unix()),
		parentHash:   parentHash,
		coinbase:     miner.config.PendingFeeRecipient,
		transactions: transactions,
		simulate:     true,
		timeout:      miner.newBlockTimeout * 2, // double the timeout, in case it is blocked due to the previous work
	})
	if err != nil {
		return nil, nil, err
	}

	return ret.Block.Transactions(), ret.SkippedTxs, nil
}

// getPending retrieves the pending block based on the current head block.
// The result might be nil if pending generation is failed.
func (miner *Miner) getPending() *NewBlockResult {
	header := miner.chain.CurrentHeader()
	miner.pendingMu.Lock()
	defer miner.pendingMu.Unlock()
	if cached := miner.pending.resolve(header.Hash()); cached != nil {
		return cached
	}

	interrupt := new(int32)
	timer := time.AfterFunc(miner.newBlockTimeout, func() {
		atomic.StoreInt32(interrupt, commitInterruptTimeout)
	})
	defer timer.Stop()

	// It may cause the `generateWork` fall into concurrent case,
	// but it is ok here, as it skips CCC so that will not reset ccc unexpectedly.
	ret, err := miner.generateWork(&generateParams{
		timestamp:  uint64(time.Now().Unix()),
		parentHash: header.Hash(),
		coinbase:   miner.config.PendingFeeRecipient,
		skipCCC:    true,
	}, interrupt)

	if err != nil {
		return nil
	}
	miner.pending.update(header.Hash(), ret)
	return ret
}
