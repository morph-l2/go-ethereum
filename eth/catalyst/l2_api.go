package catalyst

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/consensus/l2"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/eth"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/node"
	"github.com/morph-l2/go-ethereum/rollup/rcfg"
	"github.com/morph-l2/go-ethereum/rollup/withdrawtrie"
	"github.com/morph-l2/go-ethereum/rpc"
	"github.com/morph-l2/go-ethereum/trie"
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
	newBlockResult, err := api.eth.Miner().BuildBlock(parent.Hash(), time.Now(), transactions)
	if err != nil {
		return nil, err
	}

	// Do not produce new block if no transaction is involved
	// if block.TxHash() == types.EmptyRootHash {
	//	 return nil, nil
	// }
	procTime := time.Since(start)
	withdrawTrieRoot := api.writeVerified(newBlockResult.State, newBlockResult.Block, newBlockResult.Receipts, procTime)
	return &ExecutableL2Data{
		ParentHash:   newBlockResult.Block.ParentHash(),
		Number:       newBlockResult.Block.NumberU64(),
		Miner:        newBlockResult.Block.Coinbase(),
		Timestamp:    newBlockResult.Block.Time(),
		GasLimit:     newBlockResult.Block.GasLimit(),
		BaseFee:      newBlockResult.Block.BaseFee(),
		Transactions: encodeTransactions(newBlockResult.Block.Transactions()),

		StateRoot:          newBlockResult.Block.Root(),
		GasUsed:            newBlockResult.Block.GasUsed(),
		ReceiptRoot:        newBlockResult.Block.ReceiptHash(),
		LogsBloom:          newBlockResult.Block.Bloom().Bytes(),
		NextL1MessageIndex: newBlockResult.Block.Header().NextL1MsgIndex,
		WithdrawTrieRoot:   withdrawTrieRoot,

		Hash: newBlockResult.Block.Hash(),
	}, nil
}

func (api *l2ConsensusAPI) ValidateL2Block(params ExecutableL2Data) (*GenericResponse, error) {
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
	api.writeVerified(stateDB, block, receipts, procTime)
	return &GenericResponse{
		true,
	}, nil
}

func (api *l2ConsensusAPI) NewL2Block(params ExecutableL2Data, batchHash *common.Hash) (err error) {
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

	return api.eth.BlockChain().WriteStateAndSetHead(block, receipts, stateDB, procTime)
}

func (api *l2ConsensusAPI) NewSafeL2Block(params SafeL2Data) (header *types.Header, err error) {
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
	stateDB, receipts, usedGas, procTime, err := api.eth.BlockChain().ProcessBlock(block, parent.Header(), true)
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
	return api.eth.BatchHandler().ImportBatch(&batch, signatures)
}

func (api *l2ConsensusAPI) AppendBatchSignature(batchHash common.Hash, signature types.BatchSignature) {
	log.Info("append batch signature", "batch hash", fmt.Sprintf("%x", batchHash))
	api.eth.BatchHandler().ImportBatchSig(batchHash, signature)
}

func (api *l2ConsensusAPI) safeDataToBlock(params SafeL2Data) (*types.Block, error) {
	var batchHash common.Hash
	if params.BatchHash != nil {
		batchHash = *params.BatchHash
	}
	header := &types.Header{
		Number:    big.NewInt(int64(params.Number)),
		GasLimit:  params.GasLimit,
		Time:      params.Timestamp,
		BaseFee:   params.BaseFee,
		BatchHash: batchHash,
	}
	header.Difficulty = l2.Difficulty
	header.Nonce = l2.Nonce
	header.UncleHash = types.EmptyUncleHash
	header.Extra = []byte{}
	if api.eth.BlockChain().Config().Morph.FeeVaultEnabled() {
		header.Coinbase = types.EmptyAddress
	}
	txs, err := decodeTransactions(params.Transactions)
	if err != nil {
		return nil, err
	}
	header.TxHash = types.DeriveSha(types.Transactions(txs), trie.NewStackTrie(nil))
	return types.NewBlockWithHeader(header).WithBody(txs, nil), nil
}

func (api *l2ConsensusAPI) executableDataToBlock(params ExecutableL2Data, batchHash *common.Hash) (*types.Block, error) {
	var bh common.Hash
	if batchHash != nil {
		bh = *batchHash
	}
	header := &types.Header{
		ParentHash:     params.ParentHash,
		Number:         big.NewInt(int64(params.Number)),
		GasUsed:        params.GasUsed,
		GasLimit:       params.GasLimit,
		Time:           params.Timestamp,
		Coinbase:       params.Miner,
		BaseFee:        params.BaseFee,
		NextL1MsgIndex: params.NextL1MessageIndex,
		BatchHash:      bh,
	}
	header.Difficulty = l2.Difficulty
	header.Nonce = l2.Nonce
	header.UncleHash = types.EmptyUncleHash
	header.Extra = []byte{}
	if api.eth.BlockChain().Config().Morph.FeeVaultEnabled() {
		header.Coinbase = types.EmptyAddress
	}

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

func (api *l2ConsensusAPI) writeVerified(state *state.StateDB, block *types.Block, receipts types.Receipts, procTime time.Duration) common.Hash {
	withdrawTrieRoot := withdrawtrie.ReadWTRSlot(rcfg.L2MessageQueueAddress, state)
	api.verifiedMapLock.Lock()
	defer api.verifiedMapLock.Unlock()
	api.verified[block.Hash()] = executionResult{
		block:            block,
		state:            state,
		receipts:         receipts,
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

// SetBlockTags sets the safe and finalized block by hash.
// This is called by the node layer when it determines the safe/finalized
// status based on L1 batch information.
func (api *l2ConsensusAPI) SetBlockTags(safeBlockHash common.Hash, finalizedBlockHash common.Hash) error {
	bc := api.eth.BlockChain()

	// Set finalized block
	if finalizedBlockHash != (common.Hash{}) {
		finalizedHeader := bc.GetHeaderByHash(finalizedBlockHash)
		if finalizedHeader == nil {
			return fmt.Errorf("finalized block %s not found", finalizedBlockHash.Hex())
		}
		bc.SetFinalized(finalizedHeader)
		log.Info("Set finalized block", "number", finalizedHeader.Number, "hash", finalizedBlockHash)
	}

	// Set safe block
	if safeBlockHash != (common.Hash{}) {
		safeHeader := bc.GetHeaderByHash(safeBlockHash)
		if safeHeader == nil {
			return fmt.Errorf("safe block %s not found", safeBlockHash.Hex())
		}
		bc.SetSafe(safeHeader)
		log.Info("Set safe block", "number", safeHeader.Number, "hash", safeBlockHash)
	}

	return nil
}
