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

package ethapi

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/rpc"
)

// storageValuesBackend is a minimal Backend that only implements
// StateAndHeaderByNumberOrHash so the GetStorageValues method can be
// exercised without spinning up a full eth backend. Every other method is
// delegated to an embedded nil interface and will panic if invoked.
type storageValuesBackend struct {
	Backend
	state *state.StateDB
}

func (b *storageValuesBackend) StateAndHeaderByNumberOrHash(ctx context.Context, _ rpc.BlockNumberOrHash) (*state.StateDB, *types.Header, error) {
	return b.state, &types.Header{Number: big.NewInt(0)}, nil
}

func newStorageValuesAPI(t *testing.T) (*PublicBlockChainAPI, *state.StateDB) {
	t.Helper()
	statedb, err := state.New(common.Hash{}, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatalf("new statedb: %v", err)
	}
	return &PublicBlockChainAPI{b: &storageValuesBackend{state: statedb}}, statedb
}

// TestGetStorageValuesHappyPath mirrors upstream PR #32591's test: a single
// RPC call reads multiple slots across multiple accounts, with the per-
// address slot order preserved in the response.
func TestGetStorageValuesHappyPath(t *testing.T) {
	api, statedb := newStorageValuesAPI(t)

	var (
		addr1 = common.HexToAddress("0x1111")
		addr2 = common.HexToAddress("0x2222")
		slot0 = common.Hash{}
		slot1 = common.BigToHash(big.NewInt(1))
		slot2 = common.BigToHash(big.NewInt(2))
		val0  = common.BigToHash(big.NewInt(42))
		val1  = common.BigToHash(big.NewInt(100))
		val2  = common.BigToHash(big.NewInt(200))
	)
	statedb.SetState(addr1, slot0, val0)
	statedb.SetState(addr1, slot1, val1)
	statedb.SetState(addr2, slot2, val2)

	blockRef := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	result, err := api.GetStorageValues(context.Background(), map[common.Address][]common.Hash{
		addr1: {slot0, slot1},
		addr2: {slot2},
	}, blockRef)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 addresses in result, got %d", len(result))
	}
	if got := common.BytesToHash(result[addr1][0]); got != val0 {
		t.Errorf("addr1 slot0: want %x, got %x", val0, got)
	}
	if got := common.BytesToHash(result[addr1][1]); got != val1 {
		t.Errorf("addr1 slot1: want %x, got %x", val1, got)
	}
	if got := common.BytesToHash(result[addr2][0]); got != val2 {
		t.Errorf("addr2 slot2: want %x, got %x", val2, got)
	}
}

// TestGetStorageValuesMissingSlotReturnsZero documents that unset slots
// resolve to the all-zero hash, matching eth_getStorageAt's existing
// behavior.
func TestGetStorageValuesMissingSlotReturnsZero(t *testing.T) {
	api, _ := newStorageValuesAPI(t)

	blockRef := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	result, err := api.GetStorageValues(context.Background(), map[common.Address][]common.Hash{
		common.HexToAddress("0xabc"): {common.HexToHash("0xff")},
	}, blockRef)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := common.BytesToHash(result[common.HexToAddress("0xabc")][0]); got != (common.Hash{}) {
		t.Errorf("unset slot: want zero, got %x", got)
	}
}

// TestGetStorageValuesEmptyRequestRejected ensures we never hit the state
// snapshot lookup when the caller supplies an empty request map, which is
// an obvious parameter error.
func TestGetStorageValuesEmptyRequestRejected(t *testing.T) {
	api, _ := newStorageValuesAPI(t)

	blockRef := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	_, err := api.GetStorageValues(context.Background(), map[common.Address][]common.Hash{}, blockRef)
	if err == nil {
		t.Fatal("expected error for empty request")
	}
	if !strings.Contains(err.Error(), "empty request") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestGetStorageValuesEmptyPerAddressRejected covers the subtle case where
// the top-level map is non-empty but every slot list is empty. The total
// slot count is zero, which is equivalent to an empty request.
func TestGetStorageValuesEmptyPerAddressRejected(t *testing.T) {
	api, _ := newStorageValuesAPI(t)

	blockRef := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	_, err := api.GetStorageValues(context.Background(), map[common.Address][]common.Hash{
		common.HexToAddress("0x1"): {},
		common.HexToAddress("0x2"): {},
	}, blockRef)
	if err == nil {
		t.Fatal("expected error for zero total slots")
	}
	if !strings.Contains(err.Error(), "empty request") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestGetStorageValuesExceedsLimit ensures that requests with more than
// maxGetStorageSlots keys are rejected before any state read occurs.
func TestGetStorageValuesExceedsLimit(t *testing.T) {
	api, _ := newStorageValuesAPI(t)

	tooMany := make([]common.Hash, maxGetStorageSlots+1)
	for i := range tooMany {
		tooMany[i] = common.BigToHash(big.NewInt(int64(i)))
	}
	blockRef := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	_, err := api.GetStorageValues(context.Background(), map[common.Address][]common.Hash{
		common.HexToAddress("0xdeadbeef"): tooMany,
	}, blockRef)
	if err == nil {
		t.Fatal("expected error for request exceeding slot limit")
	}
	if !strings.Contains(err.Error(), "too many slots") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestGetStorageValuesLimitBoundary ensures that the maximum allowed slot
// count is accepted, guarding against off-by-one regressions.
func TestGetStorageValuesLimitBoundary(t *testing.T) {
	api, _ := newStorageValuesAPI(t)

	maxKeys := make([]common.Hash, maxGetStorageSlots)
	for i := range maxKeys {
		maxKeys[i] = common.BigToHash(big.NewInt(int64(i)))
	}
	blockRef := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	result, err := api.GetStorageValues(context.Background(), map[common.Address][]common.Hash{
		common.HexToAddress("0xabc"): maxKeys,
	}, blockRef)
	if err != nil {
		t.Fatalf("expected success at slot limit, got: %v", err)
	}
	if len(result[common.HexToAddress("0xabc")]) != maxGetStorageSlots {
		t.Fatalf("expected %d slots, got %d", maxGetStorageSlots, len(result[common.HexToAddress("0xabc")]))
	}
}
