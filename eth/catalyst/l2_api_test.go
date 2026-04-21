package catalyst

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/consensus/l2"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/eth"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

var testNonce uint64

func l2ChainConfig() params.ChainConfig {
	config := *params.AllEthashProtocolChanges
	config.Morph.UseZktrie = true
	config.TerminalTotalDifficulty = common.Big0
	addr := common.BigToAddress(big.NewInt(123))
	config.Morph.FeeVaultAddress = &addr
	config.CurieBlock = nil
	return config
}

func generateTestL2Chain(n int) (*core.Genesis, []*types.Block) {
	testNonce = 0
	db := rawdb.NewMemoryDatabase()
	config := l2ChainConfig()
	engine := l2.New(nil, params.TestChainConfig)
	genesis := &core.Genesis{
		Config:     &config,
		Alloc:      core.GenesisAlloc{testAddr: {Balance: testBalance}},
		ExtraData:  []byte{},
		Timestamp:  9000,
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Difficulty: big.NewInt(0),
	}
	generate := func(i int, g *core.BlockGen) {
		g.OffsetTime(5)
		g.SetExtra([]byte{})
		tx, _ := types.SignTx(types.NewTransaction(testNonce, common.HexToAddress("0x9a9070028361F7AAbeB3f2F2Dc07F82C4a98A02a"), big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(&config), testKey)
		g.AddTx(tx)
		testNonce++
	}
	_, blocks, _ := core.GenerateChainWithGenesis(genesis, engine, db, n, generate)
	return genesis, blocks
}

func TestL2AssembleBlock(t *testing.T) {
	num := 10
	genesis, blocks := generateTestL2Chain(num)
	n, ethService := startEthService(t, genesis, blocks)
	defer n.Close()

	for _, block := range blocks {
		rawdb.WriteFirstQueueIndexNotInL2Block(ethService.ChainDb(), block.Hash(), 0)
	}

	api := newL2ConsensusAPI(ethService)
	_, err := api.AssembleL2Block(AssembleL2BlockParams{
		Number: uint64(num + 2),
	})
	require.Error(t, err)

	resp, err := api.AssembleL2Block(AssembleL2BlockParams{
		Number: uint64(num + 1),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	config := l2ChainConfig()
	err = sendTransfer(config, ethService)
	require.NoError(t, err)

	resp, err = api.AssembleL2Block(AssembleL2BlockParams{
		Number: uint64(num + 1),
	})
	require.NoError(t, err)
	require.EqualValues(t, num+1, resp.Number)
	require.EqualValues(t, 1, len(resp.Transactions))
}

func TestValidateL2Block(t *testing.T) {
	genesis, blocks := generateTestL2Chain(0)
	n, ethService := startEthService(t, genesis, blocks)
	defer n.Close()
	for _, block := range blocks {
		rawdb.WriteFirstQueueIndexNotInL2Block(ethService.ChainDb(), block.Hash(), 0)
	}

	api := newL2ConsensusAPI(ethService)
	config := l2ChainConfig()

	// wrong block number
	_, err := api.ValidateL2Block(ExecutableL2Data{Number: 2})
	require.Error(t, err)
	require.Contains(t, err.Error(), "discontinuous block number")

	// wrong parent hash
	currentBlockHash := api.eth.BlockChain().CurrentHeader().Hash()
	currentBlockHash[0] = 0
	_, err = api.ValidateL2Block(ExecutableL2Data{Number: 1, ParentHash: currentBlockHash})
	require.Error(t, err)
	require.Contains(t, err.Error(), "wrong parent hash")

	// generic case
	err = sendTransfer(config, ethService)
	require.NoError(t, err)
	ret, err := ethService.Miner().BuildBlock(ethService.BlockChain().CurrentHeader().Hash(), time.Now(), nil)
	require.NoError(t, err)
	block := ret.Block
	l2Data := ExecutableL2Data{
		ParentHash:   block.ParentHash(),
		Number:       block.NumberU64(),
		Miner:        block.Coinbase(),
		Timestamp:    block.Time(),
		GasLimit:     block.GasLimit(),
		BaseFee:      block.BaseFee(),
		Transactions: encodeTransactions(block.Transactions()),

		StateRoot:   block.Root(),
		GasUsed:     block.GasUsed(),
		ReceiptRoot: block.ReceiptHash(),
		LogsBloom:   block.Bloom().Bytes(),
	}

	wrongL2Data := l2Data
	wrongL2Data.BaseFee = big.NewInt(333)

	validResp, err := api.ValidateL2Block(wrongL2Data)
	require.NoError(t, err)
	require.False(t, validResp.Success)

	wrongL2Data = l2Data
	wrongL2Data.StateRoot[0] = wrongL2Data.StateRoot[0] + 1
	validResp, err = api.ValidateL2Block(wrongL2Data)
	require.NoError(t, err)
	require.False(t, validResp.Success)

	validResp, err = api.ValidateL2Block(l2Data)
	require.NoError(t, err)
	require.True(t, validResp.Success)

	// new api instance
	api = newL2ConsensusAPI(ethService)
	resp, err := api.AssembleL2Block(AssembleL2BlockParams{Number: uint64(1)})
	require.NoError(t, err)
	require.EqualValues(t, 1, len(l2Data.Transactions))

	validResp, err = api.ValidateL2Block(*resp)
	require.NoError(t, err)
	require.True(t, validResp.Success)
}

func TestNewL2Block(t *testing.T) {
	genesis, blocks := generateTestL2Chain(0)
	n, ethService := startEthService(t, genesis, blocks)
	defer n.Close()
	for _, block := range blocks {
		rawdb.WriteFirstQueueIndexNotInL2Block(ethService.ChainDb(), block.Hash(), 0)
	}

	api := newL2ConsensusAPI(ethService)
	config := l2ChainConfig()

	err := sendTransfer(config, ethService)
	require.NoError(t, err)
	ret, err := ethService.Miner().BuildBlock(ethService.BlockChain().CurrentHeader().Hash(), time.Now(), nil)
	block := ret.Block
	require.NoError(t, err)
	l2Data := ExecutableL2Data{
		ParentHash:   block.ParentHash(),
		Number:       block.NumberU64(),
		Miner:        block.Coinbase(),
		Timestamp:    block.Time(),
		GasLimit:     block.GasLimit(),
		BaseFee:      block.BaseFee(),
		Transactions: encodeTransactions(block.Transactions()),

		StateRoot:   block.Root(),
		GasUsed:     block.GasUsed(),
		ReceiptRoot: block.ReceiptHash(),
		LogsBloom:   block.Bloom().Bytes(),
	}

	err = api.NewL2Block(l2Data, nil)
	require.NoError(t, err)

	currentState, err := ethService.BlockChain().State()
	require.NoError(t, err)
	to := common.HexToAddress("0x9a9070028361F7AAbeB3f2F2Dc07F82C4a98A02a")
	toBal := currentState.GetBalance(to)
	require.EqualValues(t, common.Big1.Uint64(), toBal.Uint64())

	err = sendTransfer(config, ethService)
	require.NoError(t, err)
	resp, err := api.AssembleL2Block(AssembleL2BlockParams{Number: 2})
	require.NoError(t, err)
	require.EqualValues(t, 1, len(resp.Transactions))
	validResp, err := api.ValidateL2Block(*resp)
	require.NoError(t, err)
	require.True(t, validResp.Success)
	err = api.NewL2Block(*resp, nil)
	require.NoError(t, err)
	currentState, err = ethService.BlockChain().State()
	require.NoError(t, err)
	toBal = currentState.GetBalance(to)
	require.EqualValues(t, common.Big2.Uint64(), toBal.Uint64())
}

func TestNewSafeL2Block(t *testing.T) {
	genesis, blocks := generateTestL2Chain(0)
	n, ethService := startEthService(t, genesis, blocks)
	defer n.Close()

	api := newL2ConsensusAPI(ethService)
	config := l2ChainConfig()

	err := sendTransfer(config, ethService)
	require.NoError(t, err)
	ret, err := ethService.Miner().BuildBlock(ethService.BlockChain().CurrentHeader().Hash(), time.Now(), nil)
	require.NoError(t, err)
	block := ret.Block
	l2Data := SafeL2Data{
		Number:       block.NumberU64(),
		Timestamp:    block.Time(),
		GasLimit:     block.GasLimit(),
		BaseFee:      block.BaseFee(),
		Transactions: encodeTransactions(block.Transactions()),
	}
	header, err := api.NewSafeL2Block(l2Data)
	require.NoError(t, err)

	require.EqualValues(t, block.Root().String(), header.Root.String())
}

func makeL1Txs(fromIndex, count int) (types.Transactions, []types.L1MessageTx) {
	receiver := common.BigToAddress(big.NewInt(1111))
	l1Txs := make([]*types.Transaction, count)
	l1Messages := make([]types.L1MessageTx, count)
	for i := 0; i < count; i++ {
		l1Message := types.L1MessageTx{
			QueueIndex: uint64(fromIndex + i),
			Gas:        uint64(100000 + i),
			To:         &receiver,
			Value:      big.NewInt(100),
			Data:       nil,
			Sender:     testAddr,
		}
		l1Txs[i] = types.NewTx(&l1Message)
		l1Messages[i] = l1Message
	}
	return l1Txs, l1Messages
}

func TestNewL2BlockV2(t *testing.T) {
	// Build a chain of 10 blocks for reorg testing
	num := 10
	genesis, blocks := generateTestL2Chain(num)
	n, ethService := startEthService(t, genesis, blocks)
	defer n.Close()

	// Write FirstQueueIndexNotInL2Block for all blocks (required by writeBlockStateWithoutHead)
	genesisBlock := ethService.BlockChain().GetBlockByNumber(0)
	rawdb.WriteFirstQueueIndexNotInL2Block(ethService.ChainDb(), genesisBlock.Hash(), 0)
	for _, block := range blocks {
		rawdb.WriteFirstQueueIndexNotInL2Block(ethService.ChainDb(), block.Hash(), 0)
	}

	api := newL2ConsensusAPI(ethService)
	config := l2ChainConfig()
	bc := ethService.BlockChain()

	// TC-01: Normal sequential block (parent == currentHead)
	t.Run("NormalSequentialBlock", func(t *testing.T) {
		err := sendTransfer(config, ethService)
		require.NoError(t, err)

		currentHash := bc.CurrentBlock().Hash()
		ret, err := ethService.Miner().BuildBlock(currentHash, time.Now(), nil)
		require.NoError(t, err)

		data := ExecutableL2Data{
			ParentHash:   ret.Block.ParentHash(),
			Number:       ret.Block.NumberU64(),
			Miner:        ret.Block.Coinbase(),
			Timestamp:    ret.Block.Time(),
			GasLimit:     ret.Block.GasLimit(),
			BaseFee:      ret.Block.BaseFee(),
			Transactions: encodeTransactions(ret.Block.Transactions()),
			StateRoot:    ret.Block.Root(),
			GasUsed:      ret.Block.GasUsed(),
			ReceiptRoot:  ret.Block.ReceiptHash(),
			LogsBloom:    ret.Block.Bloom().Bytes(),
		}

		err = api.NewL2BlockV2(data, false)
		require.NoError(t, err)
		require.EqualValues(t, num+1, bc.CurrentBlock().NumberU64())
	})

	// TC-03: Parent not found
	t.Run("ParentNotFound", func(t *testing.T) {
		data := ExecutableL2Data{
			ParentHash: common.HexToHash("0xdeadbeef"),
			Number:     999,
		}
		err := api.NewL2BlockV2(data, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parent block not found")
	})

	// TC-04: Wrong block number (not parent+1)
	t.Run("WrongBlockNumber", func(t *testing.T) {
		data := ExecutableL2Data{
			ParentHash: bc.CurrentBlock().Hash(),
			Number:     999, // wrong
		}
		err := api.NewL2BlockV2(data, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "wrong block number")
	})

	// TC-02 + TC-06: Reorg from height 11 to build on block 7
	t.Run("Reorg", func(t *testing.T) {
		currentHead := bc.CurrentBlock().NumberU64() // should be 11 after TC-01
		require.True(t, currentHead >= 10, "chain must be at least 10 blocks")

		// Get block 7 as reorg parent
		block7 := bc.GetBlockByNumber(7)
		require.NotNil(t, block7)

		// Build a new block on top of block 7
		ret, err := ethService.Miner().BuildBlock(block7.Hash(), time.Now(), nil)
		require.NoError(t, err)
		newBlock := ret.Block

		data := ExecutableL2Data{
			ParentHash:   newBlock.ParentHash(),
			Number:       newBlock.NumberU64(),
			Miner:        newBlock.Coinbase(),
			Timestamp:    newBlock.Time(),
			GasLimit:     newBlock.GasLimit(),
			BaseFee:      newBlock.BaseFee(),
			Transactions: encodeTransactions(newBlock.Transactions()),
			StateRoot:    newBlock.Root(),
			GasUsed:      newBlock.GasUsed(),
			ReceiptRoot:  newBlock.ReceiptHash(),
			LogsBloom:    newBlock.Bloom().Bytes(),
		}

		// Apply the reorg block via NewL2BlockV2
		err = api.NewL2BlockV2(data, false)
		require.NoError(t, err)

		// Verify chain head is now at height 8
		require.EqualValues(t, 8, bc.CurrentBlock().NumberU64())

		// TC-06: Verify stale canonical hashes are cleaned
		for h := uint64(9); h <= currentHead; h++ {
			hash := rawdb.ReadCanonicalHash(ethService.ChainDb(), h)
			require.Equal(t, common.Hash{}, hash, "canonical hash at height %d should be empty after reorg", h)
		}

		// Verify new block is canonical at height 8
		canonicalHash8 := rawdb.ReadCanonicalHash(ethService.ChainDb(), 8)
		require.NotEqual(t, common.Hash{}, canonicalHash8)
	})

	// TC-07: header.NextL1MsgIndex must match the value derived from the
	// canonical L1 message stream. This field is not covered by Header.Hash(),
	// so a signature-replay attacker could otherwise set it freely. The check
	// in writeBlockStateWithoutHead must reject the block before it is
	// persisted, leaving the chain head untouched.
	t.Run("TamperedNextL1MessageIndex", func(t *testing.T) {
		headBefore := bc.CurrentBlock().Hash()
		numberBefore := bc.CurrentBlock().NumberU64()

		ret, err := ethService.Miner().BuildBlock(headBefore, time.Now(), nil)
		require.NoError(t, err)

		// The honest miner produces a block with NextL1MsgIndex == 0 here
		// (no L1 messages in the test setup). Tampering it to a non-zero
		// value mimics an attacker who replays a legitimate signature while
		// flipping bits in fields not bound to the block hash.
		require.EqualValues(t, 0, ret.Block.Header().NextL1MsgIndex,
			"sanity: honest block should have NextL1MsgIndex = 0 in this test setup")

		data := ExecutableL2Data{
			ParentHash:         ret.Block.ParentHash(),
			Number:             ret.Block.NumberU64(),
			Miner:              ret.Block.Coinbase(),
			Timestamp:          ret.Block.Time(),
			GasLimit:           ret.Block.GasLimit(),
			BaseFee:            ret.Block.BaseFee(),
			Transactions:       encodeTransactions(ret.Block.Transactions()),
			StateRoot:          ret.Block.Root(),
			GasUsed:            ret.Block.GasUsed(),
			ReceiptRoot:        ret.Block.ReceiptHash(),
			LogsBloom:          ret.Block.Bloom().Bytes(),
			NextL1MessageIndex: 99, // tampered
		}

		err = api.NewL2BlockV2(data, false)
		require.Error(t, err)
		require.True(t, errors.Is(err, core.ErrInvalidNextL1MsgIndex),
			"expected ErrInvalidNextL1MsgIndex, got: %v", err)

		// Chain head must not have advanced.
		require.Equal(t, headBefore, bc.CurrentBlock().Hash(),
			"head must not move when block is rejected")
		require.Equal(t, numberBefore, bc.CurrentBlock().NumberU64())
	})

	// TC-08: Honest NextL1MsgIndex on the same parent should still apply
	// cleanly, proving the check does not over-reject after TC-07.
	t.Run("HonestNextL1MessageIndexAfterTampered", func(t *testing.T) {
		headBefore := bc.CurrentBlock().Hash()
		numberBefore := bc.CurrentBlock().NumberU64()

		ret, err := ethService.Miner().BuildBlock(headBefore, time.Now(), nil)
		require.NoError(t, err)

		data := ExecutableL2Data{
			ParentHash:         ret.Block.ParentHash(),
			Number:             ret.Block.NumberU64(),
			Miner:              ret.Block.Coinbase(),
			Timestamp:          ret.Block.Time(),
			GasLimit:           ret.Block.GasLimit(),
			BaseFee:            ret.Block.BaseFee(),
			Transactions:       encodeTransactions(ret.Block.Transactions()),
			StateRoot:          ret.Block.Root(),
			GasUsed:            ret.Block.GasUsed(),
			ReceiptRoot:        ret.Block.ReceiptHash(),
			LogsBloom:          ret.Block.Bloom().Bytes(),
			NextL1MessageIndex: ret.Block.Header().NextL1MsgIndex, // honest
		}

		err = api.NewL2BlockV2(data, false)
		require.NoError(t, err)
		require.Equal(t, numberBefore+1, bc.CurrentBlock().NumberU64())
	})

	// TC-07: Consecutive reorg - now at height 8, reorg to build on block 5
	t.Run("ConsecutiveReorg", func(t *testing.T) {
		block5 := bc.GetBlockByNumber(5)
		require.NotNil(t, block5)

		ret, err := ethService.Miner().BuildBlock(block5.Hash(), time.Now(), nil)
		require.NoError(t, err)
		newBlock := ret.Block

		data := ExecutableL2Data{
			ParentHash:   newBlock.ParentHash(),
			Number:       newBlock.NumberU64(),
			Miner:        newBlock.Coinbase(),
			Timestamp:    newBlock.Time(),
			GasLimit:     newBlock.GasLimit(),
			BaseFee:      newBlock.BaseFee(),
			Transactions: encodeTransactions(newBlock.Transactions()),
			StateRoot:    newBlock.Root(),
			GasUsed:      newBlock.GasUsed(),
			ReceiptRoot:  newBlock.ReceiptHash(),
			LogsBloom:    newBlock.Bloom().Bytes(),
		}

		err = api.NewL2BlockV2(data, false)
		require.NoError(t, err)
		require.EqualValues(t, 6, bc.CurrentBlock().NumberU64())

		// Stale canonical hashes at 7, 8 should be empty
		for h := uint64(7); h <= 8; h++ {
			hash := rawdb.ReadCanonicalHash(ethService.ChainDb(), h)
			require.Equal(t, common.Hash{}, hash, "height %d should be empty", h)
		}
	})

	// TC-05: isSafe=true path
	t.Run("SafeBlock", func(t *testing.T) {
		currentHash := bc.CurrentBlock().Hash()
		ret, err := ethService.Miner().BuildBlock(currentHash, time.Now(), nil)
		require.NoError(t, err)
		newBlock := ret.Block

		data := ExecutableL2Data{
			ParentHash:   newBlock.ParentHash(),
			Number:       newBlock.NumberU64(),
			Miner:        newBlock.Coinbase(),
			Timestamp:    newBlock.Time(),
			GasLimit:     newBlock.GasLimit(),
			BaseFee:      newBlock.BaseFee(),
			Transactions: encodeTransactions(newBlock.Transactions()),
			StateRoot:    newBlock.Root(),
			GasUsed:      newBlock.GasUsed(),
			ReceiptRoot:  newBlock.ReceiptHash(),
			LogsBloom:    newBlock.Bloom().Bytes(),
		}

		err = api.NewL2BlockV2(data, true)
		require.NoError(t, err)
	})
}

func sendTransfer(config params.ChainConfig, ethService *eth.Ethereum) error {
	tx, err := types.SignTx(types.NewTransaction(testNonce, common.HexToAddress("0x9a9070028361F7AAbeB3f2F2Dc07F82C4a98A02a"), big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(&config), testKey)
	if err != nil {
		return err
	}
	testNonce++
	return ethService.TxPool().AddLocal(tx)
}
