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
	"testing"

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
