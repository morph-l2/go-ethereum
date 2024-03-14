package main

import (
	"fmt"
	"math/big"
	"time"

	"net/http"
	_ "net/http/pprof" // Import for side-effect: registers handlers in http.DefaultServeMux

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/common/hexutil"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/params"
	"github.com/scroll-tech/go-ethereum/rlp"
	"github.com/scroll-tech/go-ethereum/rollup/circuitcapacitychecker"
)

type worker struct {
	circuitCapacityChecker *circuitcapacitychecker.CircuitCapacityChecker
}

func main() {
	// Start HTTP server for pprof
	go func() {
		fmt.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	for {
		worker := worker{
			circuitCapacityChecker: circuitcapacitychecker.NewCircuitCapacityChecker(true),
		}
		worker.applyBlock()
		worker.circuitCapacityChecker.Reset()

		time.Sleep(time.Second)
	}

}

func NewGwei(n int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(n), big.NewInt(params.GWei))
}

func (w *worker) applyBlock() {
	tx, block, err := newBlockAndTx()

	fmt.Println(err)

	storageTrace := &types.StorageTrace{
		RootBefore:     common.Hash{},
		RootAfter:      common.Hash{},
		Proofs:         map[string][]hexutil.Bytes{},
		StorageProofs:  map[string]map[string][]hexutil.Bytes{},
		DeletionProofs: []hexutil.Bytes{},
	}
	traces := &types.BlockTrace{
		ChainID:          tx.ChainId().Uint64(),
		Version:          "1.0",
		Coinbase:         &types.AccountWrapper{},
		Header:           block.Header(),
		Transactions:     []*types.TransactionData{},
		StorageTrace:     storageTrace,
		TxStorageTraces:  []*types.StorageTrace{storageTrace},
		ExecutionResults: []*types.ExecutionResult{},
		MPTWitness:       nil,
	}

	var accRows *types.RowConsumption

	accRows, err = w.circuitCapacityChecker.ApplyBlock(traces)
	fmt.Println(accRows)
	fmt.Println(err)

}

// from core/types/block_test.go
func newBlockAndTx() (tx *types.Transaction, block types.Block, err error) {
	blockEnc := common.FromHex("f90260f901f9a083cafc574e1f51ba9dc0568fc617a08ea2429fb384059c972f13b19fa1c8dd55a01dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347948888f1f195afa192cfee860698584c030f4c9db1a0ef1552a40b7165c3cd773806b9e0c165b75356e0314bf0706f279c729f51e017a05fe50b260da6308036625b850b5d6ced6d0a9f814c0688bc91ffb7b7a3a54b67a0bc37d79753ad738a6dac4921e57392f145d8887476de3f783dfa7edae9283e52b90100000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000008302000001832fefd8825208845506eb0780a0bd4472abb6659ebe3ee06ee4d7b72a00a9f4d001caca51342001075469aff49888a13a5a8c8f2bb1c4f861f85f800a82c35094095e7baea6a6c7c4c2dfeb977efac326af552d870a801ba09bea4c4daac7c7c52e093e6a4c35dbbcf8856f1af7b059ba20253e70848d094fa08a8fae537ce25ed8cb5af9adac3f141af69bd515bd2ba031522df09b97dd72b1c0")

	err = rlp.DecodeBytes(blockEnc, &block)

	to := common.HexToAddress("095e7baea6a6c7c4c2dfeb977efac326af552d87")

	tx = types.NewTx(&types.LegacyTx{
		Nonce:    0,
		To:       &to,
		Value:    big.NewInt(10),
		Gas:      50000,
		GasPrice: big.NewInt(10),
	})

	sig := common.Hex2Bytes("9bea4c4daac7c7c52e093e6a4c35dbbcf8856f1af7b059ba20253e70848d094f8a8fae537ce25ed8cb5af9adac3f141af69bd515bd2ba031522df09b97dd72b100")
	tx, _ = tx.WithSignature(types.HomesteadSigner{}, sig)
	return
}
