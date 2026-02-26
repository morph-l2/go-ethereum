package catalyst

import (
	"time"

	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/eth"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/node"
)

// StandaloneProducerConfig holds the configuration for the standalone block producer.
type StandaloneProducerConfig struct {
	Enabled       bool          // Whether standalone block production is enabled
	BlockInterval time.Duration // Time between block production attempts
}

// DefaultStandaloneProducerConfig returns a default configuration for the standalone producer.
var DefaultStandaloneProducerConfig = StandaloneProducerConfig{
	Enabled:       false,
	BlockInterval: 1 * time.Second,
}

// standaloneProducer simulates the consensus client by periodically calling
// the L2 engine API to assemble, validate, and commit blocks.
// This is used for standalone performance testing of the execution layer.
type standaloneProducer struct {
	config StandaloneProducerConfig
	api    *l2ConsensusAPI
	eth    *eth.Ethereum
	stopCh chan struct{}
}

// RegisterStandaloneProducer creates and starts a standalone block producer if enabled.
// It directly calls the l2ConsensusAPI methods (AssembleL2Block → NewL2Block)
// to simulate the full block production pipeline without a real consensus client.
func RegisterStandaloneProducer(stack *node.Node, backend *eth.Ethereum, config StandaloneProducerConfig) {
	if !config.Enabled {
		return
	}
	api := newL2ConsensusAPI(backend)
	producer := &standaloneProducer{
		config: config,
		api:    api,
		eth:    backend,
		stopCh: make(chan struct{}),
	}
	stack.RegisterLifecycle(producer)
	log.Info("Standalone block producer registered",
		"interval", config.BlockInterval,
	)
}

// Start implements node.Lifecycle, beginning the block production loop.
func (p *standaloneProducer) Start() error {
	log.Info("Starting standalone block producer",
		"interval", p.config.BlockInterval,
	)
	go p.loop()
	return nil
}

// Stop implements node.Lifecycle, stopping the block production loop.
func (p *standaloneProducer) Stop() error {
	log.Info("Stopping standalone block producer")
	close(p.stopCh)
	return nil
}

// loop is the main production loop. It runs on a timer and attempts to produce
// a new block on each tick by:
//   1. Collecting pending transactions from the txpool
//   2. Calling AssembleL2Block to build the block
//   3. Calling NewL2Block to commit the block to the chain
func (p *standaloneProducer) loop() {
	ticker := time.NewTicker(p.config.BlockInterval)
	defer ticker.Stop()

	log.Info("Standalone block producer loop started")

	for {
		select {
		case <-ticker.C:
			p.produceBlock()
		case <-p.stopCh:
			log.Info("Standalone block producer loop stopped")
			return
		}
	}
}

// produceBlock executes one cycle of block production:
// collect txs → assemble → commit.
func (p *standaloneProducer) produceBlock() {
	start := time.Now()

	currentBlock := p.eth.BlockChain().CurrentBlock()
	nextBlockNumber := currentBlock.NumberU64() + 1

	// Collect pending transactions from the txpool
	txs := p.collectPendingTransactions()

	// Encode transactions for the AssembleL2Block call
	encodedTxs := make([][]byte, 0, len(txs))
	for _, tx := range txs {
		data, err := tx.MarshalBinary()
		if err != nil {
			log.Error("Failed to marshal transaction", "hash", tx.Hash(), "err", err)
			continue
		}
		encodedTxs = append(encodedTxs, data)
	}

	log.Info("Producing block",
		"number", nextBlockNumber,
		"txCount", len(encodedTxs),
	)

	// Step 1: Assemble the block
	assembleStart := time.Now()
	execData, err := p.api.AssembleL2Block(AssembleL2BlockParams{
		Number:       nextBlockNumber,
		Transactions: encodedTxs,
	})
	assembleDuration := time.Since(assembleStart)

	if err != nil {
		log.Error("Failed to assemble block",
			"number", nextBlockNumber,
			"err", err,
		)
		return
	}

	if execData == nil {
		log.Debug("No block produced (nil result)", "number", nextBlockNumber)
		return
	}

	log.Info("Block assembled",
		"number", execData.Number,
		"hash", execData.Hash,
		"txCount", len(execData.Transactions),
		"gasUsed", execData.GasUsed,
		"assembleTime", assembleDuration,
	)

	// Step 2: Commit the block via NewL2Block
	// Since we are the assembler, the block is already verified in cache,
	// so NewL2Block will find it in the verified map and skip re-execution.
	commitStart := time.Now()
	err = p.api.NewL2Block(*execData, nil)
	commitDuration := time.Since(commitStart)

	if err != nil {
		log.Error("Failed to commit block",
			"number", execData.Number,
			"hash", execData.Hash,
			"err", err,
		)
		return
	}

	totalDuration := time.Since(start)
	log.Info("Block produced and committed",
		"number", execData.Number,
		"hash", execData.Hash,
		"txCount", len(execData.Transactions),
		"gasUsed", execData.GasUsed,
		"stateRoot", execData.StateRoot,
		"assembleTime", assembleDuration,
		"commitTime", commitDuration,
		"totalTime", totalDuration,
	)
}

// collectPendingTransactions retrieves pending transactions from the txpool,
// flattened and sorted by nonce for each account.
func (p *standaloneProducer) collectPendingTransactions() types.Transactions {
	pending := p.eth.TxPool().Pending(nil, nil)

	var txs types.Transactions
	for _, accountTxs := range pending {
		txs = append(txs, accountTxs...)
	}
	return txs
}

