package catalyst

import (
	"math/big"
	"testing"
	"time"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/consensus/l2"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/eth"
	"github.com/scroll-tech/go-ethereum/params"
	"github.com/scroll-tech/go-ethereum/rollup/circuitcapacitychecker"
	"github.com/stretchr/testify/require"
)

var testNonce uint64

func l2ChainConfig() params.ChainConfig {
	config := *params.AllEthashProtocolChanges
	config.Scroll.UseZktrie = true
	config.TerminalTotalDifficulty = common.Big0
	addr := common.BigToAddress(big.NewInt(123))
	config.Scroll.FeeVaultAddress = &addr
	config.CurieBlock = nil
	return config
}

func generateTestL2Chain(n int) (*core.Genesis, []*types.Block) {
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

func TestValidateL1Message(t *testing.T) {
	genesis, blocks := generateTestL2Chain(0)
	n, ethService := startEthService(t, genesis, blocks)
	//require.NoError(t, ethService.StartMining(0))
	defer n.Close()

	for _, block := range blocks {
		rawdb.WriteFirstQueueIndexNotInL2Block(ethService.ChainDb(), block.Hash(), 0)
	}

	api := newL2ConsensusAPI(ethService)
	ccc := api.eth.Miner().GetCCC()

	l1Txs, l1Messages := makeL1Txs(0, 10)
	// case: include #0, #1, fail on #2, skip it and seal the block
	ccc.ScheduleError(3, circuitcapacitychecker.ErrUnknown)
	ret, err := api.eth.Miner().BuildBlock(ethService.BlockChain().CurrentHeader().Hash(), time.Now(), l1Txs)
	require.NoError(t, err)
	block := ret.Block
	require.EqualValues(t, 2, block.Transactions().Len())
	require.EqualValues(t, 3, block.Header().NextL1MsgIndex)
	l2Data := ExecutableL2Data{
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
		SkippedTxs:         ret.SkippedTxs,

		Hash: block.Hash(),
	}

	// should be false, the ccc during the `ValidateL2Block` will include all the l1 messages
	resp, err := api.ValidateL2Block(l2Data)
	require.NoError(t, err)
	require.False(t, resp.Success)
	// should be true, the ccc during the `ValidateL2Block` behaviors the same with the ccc during `BuildBlock`
	ccc.ScheduleError(1, circuitcapacitychecker.ErrUnknown)
	resp, err = api.ValidateL2Block(l2Data)
	require.NoError(t, err)
	require.True(t, resp.Success)
	// generated block 1
	require.NoError(t, api.NewL2Block(l2Data, nil))

	// case: #2 - #9, error nextL1MessageIndex
	// expected: Unexpected L1 message queue index, build none transaction
	restL1Txs := l1Txs[2:]
	restL1Messages := l1Messages[2:]
	ret, err = ethService.Miner().BuildBlock(ethService.BlockChain().CurrentHeader().Hash(), time.Now(), restL1Txs)
	require.NoError(t, err)
	block = ret.Block
	require.EqualValues(t, 0, block.Transactions().Len())

	// case: #3 - #9, skip #3, includes the rest
	restL1Txs = restL1Txs[1:]
	restL1Messages = restL1Messages[1:]
	ccc.ScheduleError(1, circuitcapacitychecker.ErrBlockRowConsumptionOverflow)
	ret, err = ethService.Miner().BuildBlock(ethService.BlockChain().CurrentHeader().Hash(), time.Now(), restL1Txs)
	require.NoError(t, err)
	block = ret.Block
	require.EqualValues(t, 6, block.Transactions().Len())
	l2Data = ExecutableL2Data{
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
		SkippedTxs:         ret.SkippedTxs,

		Hash: block.Hash(),
	}
	resp, err = api.ValidateL2Block(l2Data)
	require.NoError(t, err)
	require.False(t, resp.Success) // false, skipped tx that should not be skipped

	ccc.ScheduleError(1, circuitcapacitychecker.ErrBlockRowConsumptionOverflow)
	resp, err = api.ValidateL2Block(l2Data)
	require.NoError(t, err)
	require.True(t, resp.Success)

	require.NoError(t, api.NewL2Block(l2Data, nil))

	// case: includes all l1messages from #10
	l1Txs, l1Messages = makeL1Txs(10, 5)
	ret, err = api.eth.Miner().BuildBlock(ethService.BlockChain().CurrentHeader().Hash(), time.Now(), l1Txs)
	require.NoError(t, err)
	block = ret.Block
	l2Data = ExecutableL2Data{
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
		SkippedTxs:         ret.SkippedTxs,

		Hash: block.Hash(),
	}

	// mess up transactions
	wrongL2Data := l2Data
	wrongL2Data.Transactions = encodeTransactions(block.Transactions()[1:])
	resp, err = api.ValidateL2Block(wrongL2Data)
	require.NoError(t, err)
	require.False(t, resp.Success)

	resp, err = api.ValidateL2Block(l2Data)
	require.NoError(t, err)
	require.True(t, resp.Success)
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

func sendTransfer(config params.ChainConfig, ethService *eth.Ethereum) error {
	tx, err := types.SignTx(types.NewTransaction(testNonce, common.HexToAddress("0x9a9070028361F7AAbeB3f2F2Dc07F82C4a98A02a"), big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(&config), testKey)
	if err != nil {
		return err
	}
	testNonce++
	return ethService.TxPool().AddLocal(tx)
}
