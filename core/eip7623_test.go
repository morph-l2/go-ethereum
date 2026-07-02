package core

import (
	"bytes"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/params"
)

func TestFloorDataGas(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want uint64
	}{
		{
			name: "empty calldata",
			data: nil,
			want: params.TxGas,
		},
		{
			name: "x18 zero calldata",
			data: make([]byte, 5000),
			want: 71000,
		},
		{
			name: "mixed calldata",
			data: append(bytes.Repeat([]byte{1}, 1000), bytes.Repeat([]byte{0}, 1000)...),
			want: 71000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FloorDataGas(tt.data)
			if err != nil {
				t.Fatalf("FloorDataGas returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("FloorDataGas mismatch: got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTxPoolNextForkFloorDataGas(t *testing.T) {
	makeConfig := func(nextFork bool) *params.ChainConfig {
		cfg := params.TestNoL1DataFeeChainConfig.Clone()
		cfg.BerlinBlock = common.Big0
		cfg.LondonBlock = common.Big0
		cfg.JadeForkTime = new(uint64)
		if nextFork {
			cfg.NextForkTime = new(uint64)
		}
		return cfg
	}
	makeDataTx := func(key *ecdsa.PrivateKey, gas uint64) *types.Transaction {
		tx, err := types.SignTx(types.NewTransaction(0, common.Address{}, big.NewInt(0), gas, big.NewInt(1), make([]byte, 5000)), types.HomesteadSigner{}, key)
		if err != nil {
			t.Fatalf("failed to sign tx: %v", err)
		}
		return tx
	}

	for _, tt := range []struct {
		name     string
		nextFork bool
		gas      uint64
		wantErr  error
	}{
		{name: "pre next fork accepts legacy intrinsic", gas: 41000},
		{name: "next fork rejects below floor", nextFork: true, gas: 41000, wantErr: ErrIntrinsicGas},
		{name: "next fork rejects one below floor", nextFork: true, gas: 70999, wantErr: ErrIntrinsicGas},
		{name: "next fork accepts exact floor", nextFork: true, gas: 71000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pool, key := setupTxPoolWithConfig(makeConfig(tt.nextFork))
			defer pool.Stop()

			testAddBalance(pool, crypto.PubkeyToAddress(key.PublicKey), big.NewInt(1_000_000_000))
			err := pool.AddRemote(makeDataTx(key, tt.gas))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AddRemote error mismatch: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTransitionDbNextForkFloorDataGasUsed(t *testing.T) {
	data := make([]byte, 5000)
	legacyGas, err := IntrinsicGas(data, nil, nil, false, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	floorGas, err := FloorDataGas(data)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name     string
		nextFork bool
		wantGas  uint64
	}{
		{name: "pre next fork keeps legacy gas used", wantGas: legacyGas},
		{name: "next fork clamps gas used to floor", nextFork: true, wantGas: floorGas},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := params.TestNoL1DataFeeChainConfig.Clone()
			cfg.HomesteadBlock = common.Big0
			cfg.IstanbulBlock = common.Big0
			cfg.ShanghaiBlock = common.Big0
			cfg.JadeForkTime = new(uint64)
			if tt.nextFork {
				cfg.NextForkTime = new(uint64)
			}

			statedb, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
			if err != nil {
				t.Fatal(err)
			}
			from := common.HexToAddress("0x1000000000000000000000000000000000000000")
			to := common.HexToAddress("0x2000000000000000000000000000000000000000")
			gasLimit := uint64(100000)
			gasPrice := big.NewInt(1)
			statedb.AddBalance(from, new(big.Int).SetUint64(gasLimit), tracing.BalanceChangeUnspecified)

			evm := vm.NewEVM(vm.BlockContext{
				CanTransfer: CanTransfer,
				Transfer:    Transfer,
				GetHash:     func(uint64) common.Hash { return common.Hash{} },
				Coinbase:    common.Address{},
				GasLimit:    gasLimit,
				BlockNumber: common.Big0,
				Time:        common.Big0,
				BaseFee:     common.Big0,
			}, vm.TxContext{
				Origin:   from,
				To:       &to,
				GasPrice: gasPrice,
			}, statedb, cfg, vm.Config{})
			msg := types.NewMessage(from, &to, 0, big.NewInt(0), gasLimit, gasPrice, gasPrice, gasPrice, 0, nil, 0, nil, nil, data, nil, nil, false)
			gp := new(GasPool).AddGas(gasLimit)
			result, err := NewStateTransition(evm, msg, gp, big.NewInt(0)).TransitionDb()
			if err != nil {
				t.Fatalf("TransitionDb returned error: %v", err)
			}
			if result.UsedGas != tt.wantGas {
				t.Fatalf("UsedGas mismatch: got %d, want %d", result.UsedGas, tt.wantGas)
			}
		})
	}
}
