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
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/params"
)

// signTestLegacyTx produces a signed legacy transaction using a freshly
// generated key. Only the signature shape matters for the fields exercised
// by these tests (Sender recovery, etc.); the concrete sender address is
// irrelevant.
func signTestLegacyTx(t *testing.T) *types.Transaction {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	inner := &types.LegacyTx{
		Nonce:    0,
		To:       &common.Address{0xaa},
		Value:    big.NewInt(1),
		Gas:      21000,
		GasPrice: big.NewInt(1000000000),
	}
	signer := types.MakeSigner(params.TestChainConfig, big.NewInt(1), 0)
	tx, err := types.SignNewTx(key, signer, inner)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tx
}

// TestRPCTransactionBlockTimestamp verifies that NewRPCTransaction populates
// the BlockTimestamp field with the supplied block timestamp when the
// transaction has been mined (blockHash is non-zero), matching upstream
// go-ethereum PR #33709. Pending transactions (zero blockHash) should expose
// the field as null so eth_getTransactionByHash returns a stable shape and
// clients can distinguish pending from confirmed txs.
func TestRPCTransactionBlockTimestamp(t *testing.T) {
	tx := signTestLegacyTx(t)

	t.Run("mined", func(t *testing.T) {
		const (
			blockNumber = uint64(10)
			blockTime   = uint64(1234567890)
			index       = uint64(2)
		)
		blockHash := common.HexToHash("0xabc")

		got := NewRPCTransaction(tx, blockHash, blockNumber, blockTime, index, nil, params.TestChainConfig)

		if got.BlockTimestamp == nil {
			t.Fatalf("BlockTimestamp is nil for mined tx; want %d", blockTime)
		}
		if uint64(*got.BlockTimestamp) != blockTime {
			t.Fatalf("BlockTimestamp = %d, want %d", uint64(*got.BlockTimestamp), blockTime)
		}
		if got.BlockNumber == nil || got.BlockNumber.ToInt().Uint64() != blockNumber {
			t.Fatalf("BlockNumber not populated for mined tx: %v", got.BlockNumber)
		}

		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"blockTimestamp":"0x499602d2"`) {
			t.Fatalf("blockTimestamp not in JSON: %s", string(raw))
		}
	})

	t.Run("pending", func(t *testing.T) {
		got := NewRPCTransaction(tx, common.Hash{}, 0, 0, 0, nil, params.TestChainConfig)

		if got.BlockTimestamp != nil {
			t.Fatalf("BlockTimestamp should be nil for pending tx; got %d", uint64(*got.BlockTimestamp))
		}
		if got.BlockHash != nil {
			t.Fatalf("BlockHash should be nil for pending tx; got %v", got.BlockHash)
		}
		if got.BlockNumber != nil {
			t.Fatalf("BlockNumber should be nil for pending tx; got %v", got.BlockNumber)
		}

		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"blockTimestamp":null`) {
			t.Fatalf("expected blockTimestamp:null for pending tx: %s", string(raw))
		}
	})
}
