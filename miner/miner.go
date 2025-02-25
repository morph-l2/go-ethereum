package miner

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/consensus"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/params"
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
	PendingFeeRecipient common.Address `toml:"-"`          // Address for pending block rewards.
	ExtraData           hexutil.Bytes  `toml:",omitempty"` // Block extra data set by the miner
	GasFloor            uint64         // Target gas floor for mined blocks.
	GasCeil             uint64         // Target gas ceiling for mined blocks.
	GasPrice            *big.Int       // Minimum gas price for mining a transaction

	NewBlockTimeout time.Duration // The maximum time allowance for creating a new block
	MaxAccountsNum  int           // Maximum number of accounts that miner will fetch the pending transactions of when building a new block
}

// DefaultConfig contains default settings for miner.
var DefaultConfig = Config{
	GasCeil:  30_000_000,
	GasPrice: big.NewInt(params.GWei / 1000),
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
		config:      &config,
		chainConfig: eth.BlockChain().Config(),
		chainDB:     eth.ChainDb(),
		engine:      engine,
		txpool:      eth.TxPool(),
		chain:       eth.BlockChain(),
		pending:     &pending{},

		newBlockTimeout: newBlockTimeout,
		getWorkCh:       make(chan *getWorkReq),
		exitCh:          make(chan struct{}),
	}

	// Sanitize account fetch limit.
	if miner.config.MaxAccountsNum == 0 {
		log.Warn("Sanitizing miner account fetch limit", "provided", miner.config.MaxAccountsNum, "updated", math.MaxInt)
		miner.config.MaxAccountsNum = math.MaxInt
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

func (miner *Miner) BuildBlock(parentHash common.Hash, timestamp time.Time, coinbase common.Address, transactions types.Transactions) (*NewBlockResult, error) {
	return miner.getSealingBlockAndState(&generateParams{
		timestamp:    uint64(timestamp.Unix()),
		parentHash:   parentHash,
		coinbase:     coinbase,
		transactions: transactions,
		timeout:      miner.newBlockTimeout,
	})
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

	// It may cause the `generateWork` fall into concurrent case
	ret, err := miner.generateWork(&generateParams{
		timestamp:  uint64(time.Now().Unix()),
		parentHash: header.Hash(),
		coinbase:   miner.config.PendingFeeRecipient,
	}, interrupt)

	if err != nil {
		return nil
	}
	miner.pending.update(header.Hash(), ret)
	return ret
}
