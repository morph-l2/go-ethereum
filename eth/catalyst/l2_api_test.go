package catalyst

import (
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
	ret, err := ethService.Miner().BuildBlock(ethService.BlockChain().CurrentHeader().Hash(), uint64(time.Now().Unix()), nil)
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
	ret, err := ethService.Miner().BuildBlock(ethService.BlockChain().CurrentHeader().Hash(), uint64(time.Now().Unix()), nil)
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
	ret, err := ethService.Miner().BuildBlock(ethService.BlockChain().CurrentHeader().Hash(), uint64(time.Now().Unix()), nil)
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

func sendTransfer(config params.ChainConfig, ethService *eth.Ethereum) error {
	tx, err := types.SignTx(types.NewTransaction(testNonce, common.HexToAddress("0x9a9070028361F7AAbeB3f2F2Dc07F82C4a98A02a"), big.NewInt(1), params.TxGas, big.NewInt(params.InitialBaseFee*2), nil), types.LatestSigner(&config), testKey)
	if err != nil {
		return err
	}
	testNonce++
	return ethService.TxPool().AddLocal(tx)
}
