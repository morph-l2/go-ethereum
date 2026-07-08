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

package vm

import (
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/params"
)

func TestGetVMContextNilTime(t *testing.T) {
	evm := NewEVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})

	ctx := evm.GetVMContext()
	if ctx.Time != 0 {
		t.Fatalf("unexpected block time: got %d want 0", ctx.Time)
	}
}

func TestGetVMContextNilEVM(t *testing.T) {
	var evm *EVM

	ctx := evm.GetVMContext()
	if ctx == nil {
		t.Fatal("expected empty context")
	}
}

func TestGetVMContextPrecompilesOnlyWhenCustom(t *testing.T) {
	evm := NewEVM(BlockContext{BlockNumber: big.NewInt(0)}, TxContext{}, nil, params.TestChainConfig, Config{})
	if ctx := evm.GetVMContext(); ctx.Precompiles != nil {
		t.Fatalf("default EVM should not expose custom precompile set, got %v", ctx.Precompiles)
	}

	addr := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	precompiles := PrecompiledContracts{addr: PrecompiledContractsHomestead[common.BytesToAddress([]byte{0x01})]}
	evm.SetPrecompiles(precompiles)

	ctx := evm.GetVMContext()
	if len(ctx.Precompiles) != 1 || ctx.Precompiles[0] != addr {
		t.Fatalf("unexpected custom precompiles: got %v want [%s]", ctx.Precompiles, addr)
	}
}

func TestCopyPrecompilesIsolatesInstanceMap(t *testing.T) {
	evm := NewEVM(BlockContext{BlockNumber: big.NewInt(0)}, TxContext{}, nil, params.TestChainConfig, Config{})

	src := common.BytesToAddress([]byte{0x01})
	dst := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	evm.SetPrecompiles(PrecompiledContracts{dst: PrecompiledContractsHomestead[src]})

	clone := evm.CopyPrecompiles()
	if _, ok := clone[dst]; !ok {
		t.Fatalf("clone missing precompile %s", dst)
	}

	// Mutating the clone must not leak back into the EVM instance map; this is
	// what lets nested probe EVMs reuse the override safely.
	extra := common.HexToAddress("0x00000000000000000000000000000000000000bb")
	clone[extra] = PrecompiledContractsHomestead[src]
	delete(clone, dst)
	if _, ok := evm.precompiles[dst]; !ok {
		t.Fatalf("instance precompile %s was dropped through clone mutation", dst)
	}
	if _, ok := evm.precompiles[extra]; ok {
		t.Fatalf("instance precompile map gained entry %s through clone mutation", extra)
	}
}

func TestCopyPrecompilesNilEVM(t *testing.T) {
	var evm *EVM
	if got := evm.CopyPrecompiles(); got != nil {
		t.Fatalf("nil EVM should return nil precompiles, got %v", got)
	}
}
