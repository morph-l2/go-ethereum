// Copyright 2024 The go-ethereum Authors
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

package native_test

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/eth/tracers"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

func TestCallFlatStop(t *testing.T) {
	tracer, err := tracers.DefaultDirectory.New("flatCallTracer", &tracers.Context{}, nil, params.MainnetChainConfig)
	require.NoError(t, err)

	// this error should be returned by GetResult
	stopError := errors.New("stop error")

	// simulate a transaction
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    0,
		To:       &common.Address{},
		Value:    big.NewInt(0),
		Gas:      0,
		GasPrice: big.NewInt(0),
		Data:     nil,
	})

	tracer.OnTxStart(&tracing.VMContext{}, tx, common.Address{})

	tracer.OnEnter(0, byte(vm.CALL), common.Address{}, common.Address{}, nil, 0, big.NewInt(0))

	// stop before the transaction is finished
	tracer.Stop(stopError)

	tracer.OnTxEnd(&types.Receipt{GasUsed: 0}, nil)

	// check that the error is returned by GetResult
	_, tracerError := tracer.GetResult()
	require.Equal(t, stopError, tracerError)
}

func TestCallFlatIgnoresHiddenSystemCallFrames(t *testing.T) {
	tracer, err := tracers.DefaultDirectory.New("flatCallTracer", &tracers.Context{}, nil, params.MainnetChainConfig)
	require.NoError(t, err)

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    0,
		To:       &common.Address{},
		Value:    big.NewInt(0),
		Gas:      21000,
		GasPrice: big.NewInt(0),
		Data:     nil,
	})

	tracer.OnTxStart(&tracing.VMContext{BlockNumber: big.NewInt(0)}, tx, common.Address{})
	tracer.OnEnter(0, byte(vm.CALL), common.Address{}, common.Address{}, nil, 21000, big.NewInt(0))

	tracer.OnSystemCallStartV2(&tracing.VMContext{})
	tracer.OnEnter(1, byte(vm.STATICCALL), common.Address{}, common.Address{}, nil, 1000, nil)
	tracer.OnExit(1, nil, 0, nil, false)
	tracer.OnSystemCallEnd()

	tracer.OnExit(0, nil, 21000, nil, false)
	tracer.OnTxEnd(&types.Receipt{GasUsed: 21000}, nil)

	res, err := tracer.GetResult()
	require.NoError(t, err)

	var frames []map[string]any
	require.NoError(t, json.Unmarshal(res, &frames))
	require.Len(t, frames, 1)
	require.Equal(t, float64(0), frames[0]["subtraces"])
}
