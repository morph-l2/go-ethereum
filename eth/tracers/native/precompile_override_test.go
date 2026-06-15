// Copyright 2026 The go-ethereum Authors
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

package native

import (
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/params"
)

func TestFourByteTracerUsesVMContextPrecompiles(t *testing.T) {
	source := common.BytesToAddress([]byte{0x01})
	target := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	tracer := &fourByteTracer{chainConfig: params.TestChainConfig}
	tx := types.NewTx(&types.LegacyTx{})

	tracer.OnTxStart(&tracing.VMContext{
		BlockNumber: big.NewInt(0),
		Precompiles: []common.Address{target},
	}, tx, common.Address{})

	if tracer.isPrecompiled(source) {
		t.Fatalf("source precompile %s should not be active after move", source)
	}
	if !tracer.isPrecompiled(target) {
		t.Fatalf("moved target %s should be active precompile", target)
	}
}
