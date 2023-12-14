package catalyst

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/core/state"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/eth"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/node"
	"github.com/scroll-tech/go-ethereum/rollup/rcfg"
	"github.com/scroll-tech/go-ethereum/rollup/withdrawtrie"
	"github.com/scroll-tech/go-ethereum/rpc"
	"github.com/scroll-tech/go-ethereum/trie"
)

func RegisterL2Engine(stack *node.Node, backend *eth.Ethereum) error {
	chainconfig := backend.BlockChain().Config()
	if chainconfig.TerminalTotalDifficulty == nil {
		return errors.New("catalyst started without valid total difficulty")
	}

	stack.RegisterAPIs([]rpc.API{
		{
			Namespace:     "engine",
			Version:       "1.0",
			Service:       newL2ConsensusAPI(backend),
			Public:        true,
			Authenticated: true,
		},
	})
	return nil
}

type l2ConsensusAPI struct {
	eth               *eth.Ethereum
	verified          map[common.Hash]executionResult // stored execution result of the next block that to be committed
	verifiedMapLock   *sync.RWMutex
	validateBlockLock *sync.Mutex
	newBlockLock      *sync.Mutex
}

func newL2ConsensusAPI(eth *eth.Ethereum) *l2ConsensusAPI {
	return &l2ConsensusAPI{
		eth:               eth,
		verified:          make(map[common.Hash]executionResult),
		verifiedMapLock:   &sync.RWMutex{},
		validateBlockLock: &sync.Mutex{},
		newBlockLock:      &sync.Mutex{},
	}
}

type executionResult struct {
	block            *types.Block
	state            *state.StateDB
	receipts         types.Receipts
	skippedTxs       []*types.SkippedTransaction
	procTime         time.Duration
	withdrawTrieRoot common.Hash
}

func (api *l2ConsensusAPI) AssembleL2Block(params AssembleL2BlockParams) (*ExecutableL2Data, error) {
	log.Info("Assembling block", "block number", params.Number)
	parent := api.eth.BlockChain().CurrentHeader()
	expectedBlockNumber := parent.Number.Uint64() + 1
	if params.Number != expectedBlockNumber {
		log.Warn("Cannot assemble block with discontinuous block number", "expected number", expectedBlockNumber, "actual number", params.Number)
		return nil, fmt.Errorf("cannot assemble block with discontinuous block number %d, expected number is %d", params.Number, expectedBlockNumber)
	}
	transactions := make(types.Transactions, 0, len(params.Transactions))
	for i, otx := range params.Transactions {
		var tx types.Transaction
		if err := tx.UnmarshalBinary(otx); err != nil {
			return nil, fmt.Errorf("transaction %d is not valid: %v", i, err)
		}
		transactions = append(transactions, &tx)
	}

	start := time.Now()
	block, state, receipts, rc, skippedTxs, err := api.eth.Miner().BuildBlock(parent.Hash(), time.Now(), transactions)
	if err != nil {
		return nil, err
	}

	// Do not produce new block if no transaction is involved
	// if block.TxHash() == types.EmptyRootHash {
	//	 return nil, nil
	// }
	procTime := time.Since(start)
	withdrawTrieRoot := api.writeVerified(state, block, receipts, skippedTxs, procTime)
	var resRc types.RowConsumption
	if rc != nil {
		resRc = *rc
	}
	return &ExecutableL2Data{
		ParentHash:   block.ParentHash(),
		Number:       block.NumberU64(),
		Miner:        block.Coinbase(),
		Timestamp:    block.Time(),
		GasLimit:     block.GasLimit(),
		BaseFee:      block.BaseFee(),
		Transactions: encodeTransactions(block.Transactions()),

		StateRoot:          block.Root(),
		GasUsed:            block.GasUsed(),
		ReceiptRoot:        block.ReceiptHash(),
		LogsBloom:          block.Bloom().Bytes(),
		NextL1MessageIndex: block.Header().NextL1MsgIndex,
		WithdrawTrieRoot:   withdrawTrieRoot,
		RowUsages:          resRc,

		Hash: block.Hash(),
	}, nil
}

func (api *l2ConsensusAPI) ValidateL2Block(params ExecutableL2Data, l1Messages []types.L1MessageTx) (*GenericResponse, error) {
	parent := api.eth.BlockChain().CurrentBlock()
	expectedBlockNumber := parent.NumberU64() + 1
	if params.Number != expectedBlockNumber {
		log.Warn("Cannot validate block with discontinuous block number", "expected number", expectedBlockNumber, "actual number", params.Number)
		return nil, fmt.Errorf("cannot validate block with discontinuous block number %d, expected number is %d", params.Number, expectedBlockNumber)
	}
	if params.ParentHash != parent.Hash() {
		log.Warn("Wrong parent hash", "expected block hash", parent.Hash().Hex(), "actual block hash", params.ParentHash.Hex())
		return nil, fmt.Errorf("wrong parent hash: %s, expected parent hash is %s", params.ParentHash, parent.Hash())
	}

	block, err := api.executableDataToBlock(params, nil)
	if err != nil {
		return nil, err
	}

	api.validateBlockLock.Lock()
	defer api.validateBlockLock.Unlock()

	er, verified := api.isVerified(block.Hash())
	if verified {
		if (params.WithdrawTrieRoot != common.Hash{} && params.WithdrawTrieRoot != er.withdrawTrieRoot) {
			log.Error("ValidateL2Block failed, wrong withdrawTrieRoot", "expected", er.withdrawTrieRoot.Hex(), "actual", params.WithdrawTrieRoot.Hex())
			return &GenericResponse{
				false,
			}, nil
		}
		return &GenericResponse{
			true,
		}, nil
	}

	if err := api.verifyBlock(block); err != nil {
		return &GenericResponse{
			false,
		}, nil
	}

	// get the skipped L1Message queue indexes, and check whether the nextL1MessageIndex is valid.
	skippedL1Indexes, err := extractSkippedQueueIndexes(block, parent.Header().NextL1MsgIndex)
	if err != nil {
		log.Error("failed to acquire skipped L1 messages", "error", err)
		return &GenericResponse{
			false,
		}, nil
	}

	// check whether the skipped transactions should be skipped.
	var skippedTransactions []*types.SkippedTransaction
	if len(skippedL1Indexes) > 0 {
		l1MsgMap := make(map[uint64]types.L1MessageTx)
		for _, l1Message := range l1Messages {
			l1MsgMap[l1Message.QueueIndex] = l1Message
		}

		skippedL1MessageTxs := make(types.Transactions, len(skippedL1Indexes))
		for i, skippedL1Index := range skippedL1Indexes {
			l1Message, ok := l1MsgMap[skippedL1Index]
			if !ok {
				log.Error("no l1message found from parameter", "queue index", skippedL1Index)
				return &GenericResponse{
					false,
				}, nil
			}
			skippedL1MessageTxs[i] = types.NewTx(&l1Message)
		}
		involvedTxs, realSkipped, err := api.eth.Miner().SimulateL1Messages(params.ParentHash, skippedL1MessageTxs)
		if err != nil {
			log.Error("failed to simulate L1Messages", "error", err)
			return &GenericResponse{
				false,
			}, nil
		}
		if len(involvedTxs) > 0 {
			log.Error("found the skipped transactions that should not be skipped", "involved tx count", len(involvedTxs))
			return &GenericResponse{
				false,
			}, nil
		}
		skippedTransactions = realSkipped
	}

	stateDB, receipts, _, procTime, err := api.eth.BlockChain().ProcessBlock(block, parent.Header(), false)
	if err != nil {
		log.Error("error processing block", "error", err)
		return &GenericResponse{
			false,
		}, nil
	}

	if (params.WithdrawTrieRoot != common.Hash{}) && params.WithdrawTrieRoot != withdrawtrie.ReadWTRSlot(rcfg.L2MessageQueueAddress, stateDB) {
		log.Error("ValidateL2Block failed, wrong withdraw trie root")
		return &GenericResponse{
			false,
		}, nil
	}
	api.writeVerified(stateDB, block, receipts, skippedTransactions, procTime)
	return &GenericResponse{
		true,
	}, nil
}

func (api *l2ConsensusAPI) NewL2Block(params ExecutableL2Data, l1Messages []types.L1MessageTx, batchHash *common.Hash) (err error) {
	api.newBlockLock.Lock()
	defer api.newBlockLock.Unlock()

	parent := api.eth.BlockChain().CurrentBlock()
	expectedBlockNumber := parent.NumberU64() + 1
	if params.Number != expectedBlockNumber {
		if params.Number < expectedBlockNumber {
			log.Warn("ignore the past block number", "block number", params.Number, "current block number", parent.NumberU64())
			return nil
		}
		log.Warn("Cannot new block with discontinuous block number", "expected number", expectedBlockNumber, "actual number", params.Number)
		return fmt.Errorf("cannot new block with discontinuous block number %d, expected number is %d", params.Number, expectedBlockNumber)
	}
	if params.ParentHash != parent.Hash() {
		log.Warn("Wrong parent hash", "expected block hash", parent.Hash().Hex(), "actual block hash", params.ParentHash.Hex())
		return fmt.Errorf("wrong parent hash: %s, expected parent hash is %s", params.ParentHash, parent.Hash())
	}

	block, err := api.executableDataToBlock(params, batchHash)
	if err != nil {
		return err
	}

	defer func() {
		if err == nil {
			api.verified = make(map[common.Hash]executionResult) // clear cached pending block
		}
	}()

	bas, verified := api.isVerified(block.Hash())
	if verified {
		api.eth.BlockChain().UpdateBlockProcessMetrics(bas.state, bas.procTime)
		for _, skipped := range bas.skippedTxs {
			bh := block.Hash()
			rawdb.WriteSkippedTransaction(api.eth.ChainDb(), &skipped.Tx, skipped.Trace, skipped.Reason, block.NumberU64(), &bh)
		}
		return api.eth.BlockChain().WriteStateAndSetHead(block, bas.receipts, bas.state, bas.procTime)
	}

	if err = api.verifyBlock(block); err != nil {
		log.Error("failed to verify block", "error", err)
		return err
	}

	stateDB, receipts, _, procTime, err := api.eth.BlockChain().ProcessBlock(block, parent.Header(), false)
	if err != nil {
		return err
	}

	skippedQueueIndexes, err := extractSkippedQueueIndexes(block, parent.Header().NextL1MsgIndex)
	if err != nil {
		return err
	}
	l1MsgMap := make(map[uint64]types.L1MessageTx)
	for _, l1Message := range l1Messages {
		l1MsgMap[l1Message.QueueIndex] = l1Message
	}
	for _, queueIndex := range skippedQueueIndexes {
		l1Message, ok := l1MsgMap[queueIndex]
		if !ok {
			log.Error("no l1message found from parameter", "queue index", queueIndex)
			return errors.New("unexpected l1Messages")
		}
		bh := block.Hash()
		rawdb.WriteSkippedTransaction(api.eth.ChainDb(), types.NewTx(&l1Message), nil, "", block.NumberU64(), &bh)
	}

	if len(params.RowUsages) > 0 {
		if rawdb.ReadBlockRowConsumption(api.eth.ChainDb(), block.Hash()) == nil {
			rawdb.WriteBlockRowConsumption(api.eth.ChainDb(), block.Hash(), &params.RowUsages)
		}
	}

	return api.eth.BlockChain().WriteStateAndSetHead(block, receipts, stateDB, procTime)
}

func (api *l2ConsensusAPI) NewSafeL2Block(params SafeL2Data) (header *types.Header, err error) {
	log.Info("receive NewSafeL2Block request", "number", params.Number)
	parent := api.eth.BlockChain().CurrentBlock()
	expectedBlockNumber := parent.NumberU64() + 1
	if params.Number != expectedBlockNumber {
		log.Warn("Cannot assemble block with discontinuous block number", "expected number", expectedBlockNumber, "actual number", params.Number)
		return nil, fmt.Errorf("cannot assemble block with discontinuous block number %d, expected number is %d", params.Number, expectedBlockNumber)
	}

	block, err := api.safeDataToBlock(params)
	if err != nil {
		return nil, err
	}
	log.Info("start to ProcessBlock", "number", params.Number)
	stateDB, receipts, usedGas, procTime, err := api.eth.BlockChain().ProcessBlock(block, parent.Header(), true)
	log.Info("finished ProcessBlock", "number", params.Number, "error", err)
	if err != nil {
		return nil, err
	}
	// reconstruct the block with the execution result
	header = block.Header()
	header.ParentHash = parent.Hash()
	header.GasUsed = usedGas
	header.Bloom = types.CreateBloom(receipts)
	header.ReceiptHash = types.DeriveSha(receipts, trie.NewStackTrie(nil))
	header.Root = stateDB.IntermediateRoot(true)
	block = types.NewBlockWithHeader(header).WithBody(block.Transactions(), nil)

	// refill the receipts with real block hash
	for _, receipt := range receipts {
		receipt.BlockHash = block.Hash()
		for _, txLog := range receipt.Logs {
			txLog.BlockHash = block.Hash()
		}
	}
	return header, api.eth.BlockChain().WriteStateAndSetHead(block, receipts, stateDB, procTime)
}

func (api *l2ConsensusAPI) CommitBatch(batch types.RollupBatch, signatures []types.BatchSignature) error {
	log.Info("commit batch", "batch index", batch.Index)
	dbBatch := api.eth.ChainDb().NewBatch()
	rawdb.WriteRollupBatch(dbBatch, batch)
	for _, signature := range signatures {
		rawdb.WriteBatchSignature(dbBatch, batch.Hash, signature)
	}
	return dbBatch.Write()
}

func (api *l2ConsensusAPI) AppendBatchSignature(batchHash common.Hash, signature types.BatchSignature) {
	log.Info("append batch signature", "batch hash", fmt.Sprintf("%x", batchHash))
	rawdb.WriteBatchSignature(api.eth.ChainDb(), batchHash, signature)
}

func (api *l2ConsensusAPI) safeDataToBlock(params SafeL2Data) (*types.Block, error) {
	header := &types.Header{
		Number:    big.NewInt(int64(params.Number)),
		GasLimit:  params.GasLimit,
		Time:      params.Timestamp,
		BaseFee:   params.BaseFee,
		BatchHash: params.BatchHash,
	}
	api.eth.Engine().Prepare(api.eth.BlockChain(), header)
	txs, err := decodeTransactions(params.Transactions)
	if err != nil {
		return nil, err
	}
	header.TxHash = types.DeriveSha(types.Transactions(txs), trie.NewStackTrie(nil))
	return types.NewBlockWithHeader(header).WithBody(txs, nil), nil
}

func (api *l2ConsensusAPI) executableDataToBlock(params ExecutableL2Data, batchHash *common.Hash) (*types.Block, error) {
	header := &types.Header{
		ParentHash:     params.ParentHash,
		Number:         big.NewInt(int64(params.Number)),
		GasUsed:        params.GasUsed,
		GasLimit:       params.GasLimit,
		Time:           params.Timestamp,
		Coinbase:       params.Miner,
		BaseFee:        params.BaseFee,
		NextL1MsgIndex: params.NextL1MessageIndex,
		BatchHash:      batchHash,
	}
	api.eth.Engine().Prepare(api.eth.BlockChain(), header)

	txs, err := decodeTransactions(params.Transactions)
	if err != nil {
		return nil, err
	}
	header.TxHash = types.DeriveSha(types.Transactions(txs), trie.NewStackTrie(nil))
	header.ReceiptHash = params.ReceiptRoot
	header.Root = params.StateRoot
	header.Bloom = types.BytesToBloom(params.LogsBloom)
	return types.NewBlockWithHeader(header).WithBody(txs, nil), nil
}

func extractSkippedQueueIndexes(block *types.Block, parentNextIndex uint64) ([]uint64, error) {
	nextIndex := parentNextIndex
	skipped := make([]uint64, 0)
	for _, tx := range block.Transactions() {
		if tx.IsL1MessageTx() {
			txQueueIndex := tx.L1MessageQueueIndex()
			if txQueueIndex < nextIndex {
				return nil, errors.New("wrong L1Message order")
			}
			for queueIndex := nextIndex; queueIndex < txQueueIndex; queueIndex++ {
				skipped = append(skipped, queueIndex)
			}
			nextIndex = txQueueIndex + 1
		}
	}

	if block.Header().NextL1MsgIndex < nextIndex {
		return nil, errors.New("wrong next L1Message index in the block header")
	}
	for queueIndex := nextIndex; queueIndex < block.Header().NextL1MsgIndex; queueIndex++ {
		skipped = append(skipped, queueIndex)
	}
	return skipped, nil
}

func (api *l2ConsensusAPI) verifyBlock(block *types.Block) error {
	if err := api.eth.Engine().VerifyHeader(api.eth.BlockChain(), block.Header(), false); err != nil {
		log.Warn("failed to verify header", "error", err)
		return err
	}
	if err := api.eth.BlockChain().Validator().ValidateBody(block); err != nil {
		log.Error("error validating body", "error", err)
		return err
	}
	return nil
}

func (api *l2ConsensusAPI) writeVerified(state *state.StateDB, block *types.Block, receipts types.Receipts, skipped []*types.SkippedTransaction, procTime time.Duration) common.Hash {
	withdrawTrieRoot := withdrawtrie.ReadWTRSlot(rcfg.L2MessageQueueAddress, state)
	api.verifiedMapLock.Lock()
	defer api.verifiedMapLock.Unlock()
	api.verified[block.Hash()] = executionResult{
		block:            block,
		state:            state,
		receipts:         receipts,
		skippedTxs:       skipped,
		procTime:         procTime,
		withdrawTrieRoot: withdrawTrieRoot,
	}
	return withdrawTrieRoot
}

func (api *l2ConsensusAPI) isVerified(blockHash common.Hash) (executionResult, bool) {
	api.verifiedMapLock.RLock()
	defer api.verifiedMapLock.RUnlock()
	er, found := api.verified[blockHash]
	return er, found
}
