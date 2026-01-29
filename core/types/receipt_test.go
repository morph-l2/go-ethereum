// Copyright 2019 The go-ethereum Authors
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

package types

import (
	"bytes"
	"math"
	"math/big"
	"reflect"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/morph-l2/go-ethereum/rlp"
)

var (
	legacyReceipt = &Receipt{
		Status:            ReceiptStatusFailed,
		CumulativeGasUsed: 1,
		Logs: []*Log{
			{
				Address: common.BytesToAddress([]byte{0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
			{
				Address: common.BytesToAddress([]byte{0x01, 0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
		},
	}
	accessListReceipt = &Receipt{
		Status:            ReceiptStatusFailed,
		CumulativeGasUsed: 1,
		Logs: []*Log{
			{
				Address: common.BytesToAddress([]byte{0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
			{
				Address: common.BytesToAddress([]byte{0x01, 0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
		},
		Type: AccessListTxType,
	}
	eip1559Receipt = &Receipt{
		Status:            ReceiptStatusFailed,
		CumulativeGasUsed: 1,
		Logs: []*Log{
			{
				Address: common.BytesToAddress([]byte{0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
			{
				Address: common.BytesToAddress([]byte{0x01, 0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
		},
		Type: DynamicFeeTxType,
	}
	morphTxReceipt = &Receipt{
		Status:            ReceiptStatusFailed,
		CumulativeGasUsed: 1,
		Logs: []*Log{
			{
				Address: common.BytesToAddress([]byte{0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
			{
				Address: common.BytesToAddress([]byte{0x01, 0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
		},
		Type: MorphTxType,
	}

	// MorphTx receipt with Version 1 and all new fields
	testReference     = common.HexToReference("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	testMemo          = []byte("test memo data")
	testFeeTokenID    = uint16(1)
	morphTxV1Receipt  = &Receipt{
		Status:            ReceiptStatusSuccessful,
		CumulativeGasUsed: 100,
		Logs: []*Log{
			{
				Address: common.BytesToAddress([]byte{0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
		},
		Type:       MorphTxType,
		Version:    MorphTxVersion1,
		Reference:  &testReference,
		Memo:       &testMemo,
		FeeTokenID: &testFeeTokenID,
		FeeRate:    big.NewInt(1000),
		TokenScale: big.NewInt(18),
		FeeLimit:   big.NewInt(1000000),
		L1Fee:      big.NewInt(500),
	}

	// MorphTx receipt with Version 0 (legacy format)
	// Note: V0 format doesn't use Reference/Memo, so we set them to nil equivalent zero values for RLP compatibility
	zeroReference    = common.Reference{}
	nilMemo          = []byte{}
	morphTxV0Receipt = &Receipt{
		Status:            ReceiptStatusSuccessful,
		CumulativeGasUsed: 200,
		Logs: []*Log{
			{
				Address: common.BytesToAddress([]byte{0x22}),
				Topics:  []common.Hash{common.HexToHash("cafe"), common.HexToHash("babe")},
				Data:    []byte{0x02, 0x00, 0xfe},
			},
		},
		Type:       MorphTxType,
		Version:    MorphTxVersion0,
		Reference:  &zeroReference, // V0 has no Reference (use zero value for RLP)
		Memo:       &nilMemo,       // V0 has no Memo (use empty for RLP)
		FeeTokenID: &testFeeTokenID,
		FeeRate:    big.NewInt(2000),
		TokenScale: big.NewInt(18),
		FeeLimit:   big.NewInt(2000000),
		L1Fee:      big.NewInt(600),
	}

	// MorphTx receipt with only Reference (no Memo)
	zeroFeeTokenID        = uint16(0)
	morphTxRefOnlyReceipt = &Receipt{
		Status:            ReceiptStatusSuccessful,
		CumulativeGasUsed: 300,
		Logs:              []*Log{},
		Type:              MorphTxType,
		Version:           MorphTxVersion1,
		Reference:         &testReference,
		Memo:              &nilMemo, // Use empty memo for RLP compatibility
		FeeTokenID:        &zeroFeeTokenID,
		FeeRate:           big.NewInt(0),
		TokenScale:        big.NewInt(0),
		FeeLimit:          big.NewInt(0),
		L1Fee:             big.NewInt(0),
	}

	// MorphTx receipt with only Memo (no Reference)
	testMemoOnly           = []byte("memo only test")
	morphTxMemoOnlyReceipt = &Receipt{
		Status:            ReceiptStatusSuccessful,
		CumulativeGasUsed: 400,
		Logs:              []*Log{},
		Type:              MorphTxType,
		Version:           MorphTxVersion1,
		Reference:         &zeroReference, // Use zero reference for RLP compatibility
		Memo:              &testMemoOnly,
		FeeTokenID:        &zeroFeeTokenID,
		FeeRate:           big.NewInt(0),
		TokenScale:        big.NewInt(0),
		FeeLimit:          big.NewInt(0),
		L1Fee:             big.NewInt(0),
	}

	// MorphTx receipt with empty Memo
	emptyMemo               = []byte{}
	morphTxEmptyMemoReceipt = &Receipt{
		Status:            ReceiptStatusSuccessful,
		CumulativeGasUsed: 500,
		Logs:              []*Log{},
		Type:              MorphTxType,
		Version:           MorphTxVersion1,
		Reference:         &testReference,
		Memo:              &emptyMemo,
		FeeTokenID:        &zeroFeeTokenID,
		FeeRate:           big.NewInt(0),
		TokenScale:        big.NewInt(0),
		FeeLimit:          big.NewInt(0),
		L1Fee:             big.NewInt(0),
	}
)

func TestDecodeEmptyTypedReceipt(t *testing.T) {
	input := []byte{0x80}
	var r Receipt
	err := rlp.DecodeBytes(input, &r)
	if err != errShortTypedReceipt {
		t.Fatal("wrong error:", err)
	}
}

func TestLegacyReceiptDecoding(t *testing.T) {
	tests := []struct {
		name   string
		encode func(*Receipt) ([]byte, error)
	}{
		{
			"StoredReceiptRLP",
			encodeAsStoredReceiptRLP,
		},
		{
			"V8StoredReceiptRLP",
			encodeAsV8StoredReceiptRLP,
		},
		{
			"V7StoredReceiptRLP",
			encodeAsV7StoredReceiptRLP,
		},
		{
			"V6StoredReceiptRLP",
			encodeAsV6StoredReceiptRLP,
		},
		{
			"V5StoredReceiptRLP",
			encodeAsV5StoredReceiptRLP,
		},
		{
			"V4StoredReceiptRLP",
			encodeAsV4StoredReceiptRLP,
		},
		{
			"V3StoredReceiptRLP",
			encodeAsV3StoredReceiptRLP,
		},
	}

	tx := NewTransaction(1, common.HexToAddress("0x1"), big.NewInt(1), 1, big.NewInt(1), nil)
	legacyZeroRef := common.Reference{}
	legacyNilMemo := []byte{}
	legacyFeeTokenID := uint16(0)
	receipt := &Receipt{
		Status:            ReceiptStatusFailed,
		CumulativeGasUsed: 1,
		Logs: []*Log{
			{
				Address: common.BytesToAddress([]byte{0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
			{
				Address: common.BytesToAddress([]byte{0x01, 0x11}),
				Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
				Data:    []byte{0x01, 0x00, 0xff},
			},
		},
		TxHash:          tx.Hash(),
		ContractAddress: common.BytesToAddress([]byte{0x01, 0x11, 0x11}),
		GasUsed:         111111,
		// Set new fields to zero/empty values for RLP compatibility
		Version:    0,
		Reference:  &legacyZeroRef,
		Memo:       &legacyNilMemo,
		FeeTokenID: &legacyFeeTokenID,
		FeeRate:    big.NewInt(0),
		TokenScale: big.NewInt(0),
		FeeLimit:   big.NewInt(0),
		L1Fee:      big.NewInt(0),
	}
	receipt.Bloom = CreateBloom(Receipts{receipt})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := tc.encode(receipt)
			if err != nil {
				t.Fatalf("Error encoding receipt: %v", err)
			}
			var dec ReceiptForStorage
			if err := rlp.DecodeBytes(enc, &dec); err != nil {
				t.Fatalf("Error decoding RLP receipt: %v", err)
			}
			// Check whether all consensus fields are correct.
			if dec.Status != receipt.Status {
				t.Fatalf("Receipt status mismatch, want %v, have %v", receipt.Status, dec.Status)
			}
			if dec.CumulativeGasUsed != receipt.CumulativeGasUsed {
				t.Fatalf("Receipt CumulativeGasUsed mismatch, want %v, have %v", receipt.CumulativeGasUsed, dec.CumulativeGasUsed)
			}
			if dec.Bloom != receipt.Bloom {
				t.Fatalf("Bloom data mismatch, want %v, have %v", receipt.Bloom, dec.Bloom)
			}
			if len(dec.Logs) != len(receipt.Logs) {
				t.Fatalf("Receipt log number mismatch, want %v, have %v", len(receipt.Logs), len(dec.Logs))
			}
			for i := 0; i < len(dec.Logs); i++ {
				if dec.Logs[i].Address != receipt.Logs[i].Address {
					t.Fatalf("Receipt log %d address mismatch, want %v, have %v", i, receipt.Logs[i].Address, dec.Logs[i].Address)
				}
				if !reflect.DeepEqual(dec.Logs[i].Topics, receipt.Logs[i].Topics) {
					t.Fatalf("Receipt log %d topics mismatch, want %v, have %v", i, receipt.Logs[i].Topics, dec.Logs[i].Topics)
				}
				if !bytes.Equal(dec.Logs[i].Data, receipt.Logs[i].Data) {
					t.Fatalf("Receipt log %d data mismatch, want %v, have %v", i, receipt.Logs[i].Data, dec.Logs[i].Data)
				}
			}
		})
	}
}

func encodeAsStoredReceiptRLP(want *Receipt) ([]byte, error) {
	stored := &storedReceiptRLP{
		PostStateOrStatus: want.statusEncoding(),
		CumulativeGasUsed: want.CumulativeGasUsed,
		Logs:              make([]*LogForStorage, len(want.Logs)),
		L1Fee:             want.L1Fee,
		FeeTokenID:        want.FeeTokenID,
		FeeRate:           want.FeeRate,
		TokenScale:        want.TokenScale,
		FeeLimit:          want.FeeLimit,
		Version:           want.Version,
		Reference:         want.Reference,
		Memo:              want.Memo,
	}
	for i, log := range want.Logs {
		stored.Logs[i] = (*LogForStorage)(log)
	}
	return rlp.EncodeToBytes(stored)
}

func encodeAsV8StoredReceiptRLP(want *Receipt) ([]byte, error) {
	stored := &v8StoredReceiptRLP{
		PostStateOrStatus: want.statusEncoding(),
		CumulativeGasUsed: want.CumulativeGasUsed,
		Logs:              make([]*LogForStorage, len(want.Logs)),
		L1Fee:             want.L1Fee,
		FeeTokenID:        want.FeeTokenID,
		FeeRate:           want.FeeRate,
		TokenScale:        want.TokenScale,
		FeeLimit:          want.FeeLimit,
		Version:           want.Version,
		Reference:         want.Reference,
		Memo:              want.Memo,
	}
	for i, log := range want.Logs {
		stored.Logs[i] = (*LogForStorage)(log)
	}
	return rlp.EncodeToBytes(stored)
}

func encodeAsV7StoredReceiptRLP(want *Receipt) ([]byte, error) {
	stored := &v7StoredReceiptRLP{
		PostStateOrStatus: want.statusEncoding(),
		CumulativeGasUsed: want.CumulativeGasUsed,
		Logs:              make([]*LogForStorage, len(want.Logs)),
		L1Fee:             want.L1Fee,
		FeeTokenID:        want.FeeTokenID,
		FeeRate:           want.FeeRate,
		TokenScale:        want.TokenScale,
		FeeLimit:          want.FeeLimit,
	}
	for i, log := range want.Logs {
		stored.Logs[i] = (*LogForStorage)(log)
	}
	return rlp.EncodeToBytes(stored)
}

func encodeAsV6StoredReceiptRLP(want *Receipt) ([]byte, error) {
	stored := &v6StoredReceiptRLP{
		PostStateOrStatus: want.statusEncoding(),
		CumulativeGasUsed: want.CumulativeGasUsed,
		Logs:              make([]*LogForStorage, len(want.Logs)),
		L1Fee:             want.L1Fee,
	}
	for i, log := range want.Logs {
		stored.Logs[i] = (*LogForStorage)(log)
	}
	return rlp.EncodeToBytes(stored)
}

func encodeAsV5StoredReceiptRLP(want *Receipt) ([]byte, error) {
	stored := &v5StoredReceiptRLP{
		PostStateOrStatus: want.statusEncoding(),
		CumulativeGasUsed: want.CumulativeGasUsed,
		Logs:              make([]*LogForStorage, len(want.Logs)),
	}
	for i, log := range want.Logs {
		stored.Logs[i] = (*LogForStorage)(log)
	}
	return rlp.EncodeToBytes(stored)
}

func encodeAsV4StoredReceiptRLP(want *Receipt) ([]byte, error) {
	stored := &v4StoredReceiptRLP{
		PostStateOrStatus: want.statusEncoding(),
		CumulativeGasUsed: want.CumulativeGasUsed,
		TxHash:            want.TxHash,
		ContractAddress:   want.ContractAddress,
		Logs:              make([]*LogForStorage, len(want.Logs)),
		GasUsed:           want.GasUsed,
	}
	for i, log := range want.Logs {
		stored.Logs[i] = (*LogForStorage)(log)
	}
	return rlp.EncodeToBytes(stored)
}

func encodeAsV3StoredReceiptRLP(want *Receipt) ([]byte, error) {
	stored := &v3StoredReceiptRLP{
		PostStateOrStatus: want.statusEncoding(),
		CumulativeGasUsed: want.CumulativeGasUsed,
		Bloom:             want.Bloom,
		TxHash:            want.TxHash,
		ContractAddress:   want.ContractAddress,
		Logs:              make([]*LogForStorage, len(want.Logs)),
		GasUsed:           want.GasUsed,
	}
	for i, log := range want.Logs {
		stored.Logs[i] = (*LogForStorage)(log)
	}
	return rlp.EncodeToBytes(stored)
}

// Tests that receipt data can be correctly derived from the contextual infos
func TestDeriveFields(t *testing.T) {
	// Create a few transactions to have receipts for
	to2 := common.HexToAddress("0x2")
	to3 := common.HexToAddress("0x3")
	txs := Transactions{
		NewTx(&LegacyTx{
			Nonce:    1,
			Value:    big.NewInt(1),
			Gas:      1,
			GasPrice: big.NewInt(1),
		}),
		NewTx(&LegacyTx{
			To:       &to2,
			Nonce:    2,
			Value:    big.NewInt(2),
			Gas:      2,
			GasPrice: big.NewInt(2),
		}),
		NewTx(&AccessListTx{
			To:       &to3,
			Nonce:    3,
			Value:    big.NewInt(3),
			Gas:      3,
			GasPrice: big.NewInt(3),
		}),
	}
	// Create the corresponding receipts
	receipts := Receipts{
		&Receipt{
			Status:            ReceiptStatusFailed,
			CumulativeGasUsed: 1,
			Logs: []*Log{
				{Address: common.BytesToAddress([]byte{0x11})},
				{Address: common.BytesToAddress([]byte{0x01, 0x11})},
			},
			TxHash:          txs[0].Hash(),
			ContractAddress: common.BytesToAddress([]byte{0x01, 0x11, 0x11}),
			GasUsed:         1,
		},
		&Receipt{
			PostState:         common.Hash{2}.Bytes(),
			CumulativeGasUsed: 3,
			Logs: []*Log{
				{Address: common.BytesToAddress([]byte{0x22})},
				{Address: common.BytesToAddress([]byte{0x02, 0x22})},
			},
			TxHash:          txs[1].Hash(),
			ContractAddress: common.BytesToAddress([]byte{0x02, 0x22, 0x22}),
			GasUsed:         2,
		},
		&Receipt{
			Type:              AccessListTxType,
			PostState:         common.Hash{3}.Bytes(),
			CumulativeGasUsed: 6,
			Logs: []*Log{
				{Address: common.BytesToAddress([]byte{0x33})},
				{Address: common.BytesToAddress([]byte{0x03, 0x33})},
			},
			TxHash:          txs[2].Hash(),
			ContractAddress: common.BytesToAddress([]byte{0x03, 0x33, 0x33}),
			GasUsed:         3,
		},
	}
	// Clear all the computed fields and re-derive them
	number := big.NewInt(1)
	blockTime := uint64(2)
	hash := common.BytesToHash([]byte{0x03, 0x14})

	clearComputedFieldsOnReceipts(t, receipts)
	if err := receipts.DeriveFields(params.TestChainConfig, hash, number.Uint64(), blockTime, nil, txs); err != nil {
		t.Fatalf("DeriveFields(...) = %v, want <nil>", err)
	}
	// Iterate over all the computed fields and check that they're correct
	signer := MakeSigner(params.TestChainConfig, number, blockTime)

	logIndex := uint(0)
	for i := range receipts {
		if receipts[i].Type != txs[i].Type() {
			t.Errorf("receipts[%d].Type = %d, want %d", i, receipts[i].Type, txs[i].Type())
		}
		if receipts[i].TxHash != txs[i].Hash() {
			t.Errorf("receipts[%d].TxHash = %s, want %s", i, receipts[i].TxHash.String(), txs[i].Hash().String())
		}
		if receipts[i].BlockHash != hash {
			t.Errorf("receipts[%d].BlockHash = %s, want %s", i, receipts[i].BlockHash.String(), hash.String())
		}
		if receipts[i].BlockNumber.Cmp(number) != 0 {
			t.Errorf("receipts[%c].BlockNumber = %s, want %s", i, receipts[i].BlockNumber.String(), number.String())
		}
		if receipts[i].TransactionIndex != uint(i) {
			t.Errorf("receipts[%d].TransactionIndex = %d, want %d", i, receipts[i].TransactionIndex, i)
		}
		if receipts[i].GasUsed != txs[i].Gas() {
			t.Errorf("receipts[%d].GasUsed = %d, want %d", i, receipts[i].GasUsed, txs[i].Gas())
		}
		if txs[i].To() != nil && receipts[i].ContractAddress != (common.Address{}) {
			t.Errorf("receipts[%d].ContractAddress = %s, want %s", i, receipts[i].ContractAddress.String(), (common.Address{}).String())
		}
		from, _ := Sender(signer, txs[i])
		contractAddress := crypto.CreateAddress(from, txs[i].Nonce())
		if txs[i].To() == nil && receipts[i].ContractAddress != contractAddress {
			t.Errorf("receipts[%d].ContractAddress = %s, want %s", i, receipts[i].ContractAddress.String(), contractAddress.String())
		}
		for j := range receipts[i].Logs {
			if receipts[i].Logs[j].BlockNumber != number.Uint64() {
				t.Errorf("receipts[%d].Logs[%d].BlockNumber = %d, want %d", i, j, receipts[i].Logs[j].BlockNumber, number.Uint64())
			}
			if receipts[i].Logs[j].BlockHash != hash {
				t.Errorf("receipts[%d].Logs[%d].BlockHash = %s, want %s", i, j, receipts[i].Logs[j].BlockHash.String(), hash.String())
			}
			if receipts[i].Logs[j].TxHash != txs[i].Hash() {
				t.Errorf("receipts[%d].Logs[%d].TxHash = %s, want %s", i, j, receipts[i].Logs[j].TxHash.String(), txs[i].Hash().String())
			}
			if receipts[i].Logs[j].TxIndex != uint(i) {
				t.Errorf("receipts[%d].Logs[%d].TransactionIndex = %d, want %d", i, j, receipts[i].Logs[j].TxIndex, i)
			}
			if receipts[i].Logs[j].Index != logIndex {
				t.Errorf("receipts[%d].Logs[%d].Index = %d, want %d", i, j, receipts[i].Logs[j].Index, logIndex)
			}
			logIndex++
		}
	}
}

// TestTypedReceiptEncodingDecoding reproduces a flaw that existed in the receipt
// rlp decoder, which failed due to a shadowing error.
func TestTypedReceiptEncodingDecoding(t *testing.T) {
	var payload = common.FromHex("f9043eb9010c01f90108018262d4b9010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000c0b9010c01f901080182cd14b9010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000c0b9010d01f901090183013754b9010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000c0b9010d01f90109018301a194b9010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000c0")
	check := func(bundle []*Receipt) {
		t.Helper()
		for i, receipt := range bundle {
			if got, want := receipt.Type, uint8(1); got != want {
				t.Fatalf("bundle %d: got %x, want %x", i, got, want)
			}
		}
	}
	{
		var bundle []*Receipt
		rlp.DecodeBytes(payload, &bundle)
		check(bundle)
	}
	{
		var bundle []*Receipt
		r := bytes.NewReader(payload)
		s := rlp.NewStream(r, uint64(len(payload)))
		if err := s.Decode(&bundle); err != nil {
			t.Fatal(err)
		}
		check(bundle)
	}
}

func TestReceiptMarshalBinary(t *testing.T) {
	// Legacy Receipt
	legacyReceipt.Bloom = CreateBloom(Receipts{legacyReceipt})
	have, err := legacyReceipt.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal binary error: %v", err)
	}
	legacyReceipts := Receipts{legacyReceipt}
	buf := new(bytes.Buffer)
	legacyReceipts.EncodeIndex(0, buf)
	haveEncodeIndex := buf.Bytes()
	if !bytes.Equal(have, haveEncodeIndex) {
		t.Errorf("BinaryMarshal and EncodeIndex mismatch, got %x want %x", have, haveEncodeIndex)
	}
	buf.Reset()
	if err := legacyReceipt.EncodeRLP(buf); err != nil {
		t.Fatalf("encode rlp error: %v", err)
	}
	haveRLPEncode := buf.Bytes()
	if !bytes.Equal(have, haveRLPEncode) {
		t.Errorf("BinaryMarshal and EncodeRLP mismatch for legacy tx, got %x want %x", have, haveRLPEncode)
	}
	legacyWant := common.FromHex("f901c58001b9010000000000000010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000500000000000000000000000000000000000014000000000000000000000000000000000000000000000000000000000000000000000000000010000080000000000000000000004000000000000000000000000000040000000000000000000000000000800000000000000000000000000000000000000000000000000000400000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000f8bef85d940000000000000000000000000000000000000011f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100fff85d940000000000000000000000000000000000000111f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100ff")
	if !bytes.Equal(have, legacyWant) {
		t.Errorf("encoded RLP mismatch, got %x want %x", have, legacyWant)
	}

	// 2930 Receipt
	buf.Reset()
	accessListReceipt.Bloom = CreateBloom(Receipts{accessListReceipt})
	have, err = accessListReceipt.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal binary error: %v", err)
	}
	accessListReceipts := Receipts{accessListReceipt}
	accessListReceipts.EncodeIndex(0, buf)
	haveEncodeIndex = buf.Bytes()
	if !bytes.Equal(have, haveEncodeIndex) {
		t.Errorf("BinaryMarshal and EncodeIndex mismatch, got %x want %x", have, haveEncodeIndex)
	}
	accessListWant := common.FromHex("01f901c58001b9010000000000000010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000500000000000000000000000000000000000014000000000000000000000000000000000000000000000000000000000000000000000000000010000080000000000000000000004000000000000000000000000000040000000000000000000000000000800000000000000000000000000000000000000000000000000000400000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000f8bef85d940000000000000000000000000000000000000011f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100fff85d940000000000000000000000000000000000000111f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100ff")
	if !bytes.Equal(have, accessListWant) {
		t.Errorf("encoded RLP mismatch, got %x want %x", have, accessListWant)
	}

	// 1559 Receipt
	buf.Reset()
	eip1559Receipt.Bloom = CreateBloom(Receipts{eip1559Receipt})
	have, err = eip1559Receipt.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal binary error: %v", err)
	}
	eip1559Receipts := Receipts{eip1559Receipt}
	eip1559Receipts.EncodeIndex(0, buf)
	haveEncodeIndex = buf.Bytes()
	if !bytes.Equal(have, haveEncodeIndex) {
		t.Errorf("BinaryMarshal and EncodeIndex mismatch, got %x want %x", have, haveEncodeIndex)
	}
	eip1559Want := common.FromHex("02f901c58001b9010000000000000010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000500000000000000000000000000000000000014000000000000000000000000000000000000000000000000000000000000000000000000000010000080000000000000000000004000000000000000000000000000040000000000000000000000000000800000000000000000000000000000000000000000000000000000400000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000f8bef85d940000000000000000000000000000000000000011f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100fff85d940000000000000000000000000000000000000111f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100ff")
	if !bytes.Equal(have, eip1559Want) {
		t.Errorf("encoded RLP mismatch, got %x want %x", have, eip1559Want)
	}

	// MorphTx Receipt
	buf.Reset()
	morphTxReceipt.Bloom = CreateBloom(Receipts{morphTxReceipt})
	have, err = morphTxReceipt.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal binary error: %v", err)
	}
	morphTxReceipts := Receipts{morphTxReceipt}
	morphTxReceipts.EncodeIndex(0, buf)
	haveEncodeIndex = buf.Bytes()
	if !bytes.Equal(have, haveEncodeIndex) {
		t.Errorf("BinaryMarshal and EncodeIndex mismatch, got %x want %x", have, haveEncodeIndex)
	}
	morphTxWant := common.FromHex("7ff901c58001b9010000000000000010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000500000000000000000000000000000000000014000000000000000000000000000000000000000000000000000000000000000000000000000010000080000000000000000000004000000000000000000000000000040000000000000000000000000000800000000000000000000000000000000000000000000000000000400000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000f8bef85d940000000000000000000000000000000000000011f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100fff85d940000000000000000000000000000000000000111f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100ff")
	if !bytes.Equal(have, morphTxWant) {
		t.Errorf("encoded RLP mismatch, got %x want %x", have, morphTxWant)
	}
}

func TestReceiptUnmarshalBinary(t *testing.T) {
	// Legacy Receipt
	legacyBinary := common.FromHex("f901c58001b9010000000000000010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000500000000000000000000000000000000000014000000000000000000000000000000000000000000000000000000000000000000000000000010000080000000000000000000004000000000000000000000000000040000000000000000000000000000800000000000000000000000000000000000000000000000000000400000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000f8bef85d940000000000000000000000000000000000000011f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100fff85d940000000000000000000000000000000000000111f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100ff")
	gotLegacyReceipt := new(Receipt)
	if err := gotLegacyReceipt.UnmarshalBinary(legacyBinary); err != nil {
		t.Fatalf("unmarshal binary error: %v", err)
	}
	legacyReceipt.Bloom = CreateBloom(Receipts{legacyReceipt})
	if !reflect.DeepEqual(gotLegacyReceipt, legacyReceipt) {
		t.Errorf("receipt unmarshalled from binary mismatch, got %v want %v", gotLegacyReceipt, legacyReceipt)
	}

	// 2930 Receipt
	accessListBinary := common.FromHex("01f901c58001b9010000000000000010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000500000000000000000000000000000000000014000000000000000000000000000000000000000000000000000000000000000000000000000010000080000000000000000000004000000000000000000000000000040000000000000000000000000000800000000000000000000000000000000000000000000000000000400000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000f8bef85d940000000000000000000000000000000000000011f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100fff85d940000000000000000000000000000000000000111f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100ff")
	gotAccessListReceipt := new(Receipt)
	if err := gotAccessListReceipt.UnmarshalBinary(accessListBinary); err != nil {
		t.Fatalf("unmarshal binary error: %v", err)
	}
	accessListReceipt.Bloom = CreateBloom(Receipts{accessListReceipt})
	if !reflect.DeepEqual(gotAccessListReceipt, accessListReceipt) {
		t.Errorf("receipt unmarshalled from binary mismatch, got %v want %v", gotAccessListReceipt, accessListReceipt)
	}

	// 1559 Receipt
	eip1559RctBinary := common.FromHex("02f901c58001b9010000000000000010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000500000000000000000000000000000000000014000000000000000000000000000000000000000000000000000000000000000000000000000010000080000000000000000000004000000000000000000000000000040000000000000000000000000000800000000000000000000000000000000000000000000000000000400000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000f8bef85d940000000000000000000000000000000000000011f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100fff85d940000000000000000000000000000000000000111f842a0000000000000000000000000000000000000000000000000000000000000deada0000000000000000000000000000000000000000000000000000000000000beef830100ff")
	got1559Receipt := new(Receipt)
	if err := got1559Receipt.UnmarshalBinary(eip1559RctBinary); err != nil {
		t.Fatalf("unmarshal binary error: %v", err)
	}
	eip1559Receipt.Bloom = CreateBloom(Receipts{eip1559Receipt})
	if !reflect.DeepEqual(got1559Receipt, eip1559Receipt) {
		t.Errorf("receipt unmarshalled from binary mismatch, got %v want %v", got1559Receipt, eip1559Receipt)
	}
}

func clearComputedFieldsOnReceipts(t *testing.T, receipts Receipts) {
	t.Helper()

	for _, receipt := range receipts {
		clearComputedFieldsOnReceipt(t, receipt)
	}
}

func clearComputedFieldsOnReceipt(t *testing.T, receipt *Receipt) {
	t.Helper()

	receipt.TxHash = common.Hash{}
	receipt.BlockHash = common.Hash{}
	receipt.BlockNumber = big.NewInt(math.MaxUint32)
	receipt.TransactionIndex = math.MaxUint32
	receipt.ContractAddress = common.Address{}
	receipt.GasUsed = 0

	clearComputedFieldsOnLogs(t, receipt.Logs)
}

func clearComputedFieldsOnLogs(t *testing.T, logs []*Log) {
	t.Helper()

	for _, log := range logs {
		clearComputedFieldsOnLog(t, log)
	}
}

func clearComputedFieldsOnLog(t *testing.T, log *Log) {
	t.Helper()

	log.BlockNumber = math.MaxUint32
	log.BlockHash = common.Hash{}
	log.TxHash = common.Hash{}
	log.TxIndex = math.MaxUint32
	log.Index = math.MaxUint32
}

// TestMorphTxReceiptStorageEncoding tests the storage encoding/decoding of MorphTx receipts
// with Version, Reference, and Memo fields.
func TestMorphTxReceiptStorageEncoding(t *testing.T) {
	tests := []struct {
		name    string
		receipt *Receipt
	}{
		{
			name:    "MorphTx V1 with all fields",
			receipt: morphTxV1Receipt,
		},
		{
			name:    "MorphTx V0 (legacy format)",
			receipt: morphTxV0Receipt,
		},
		{
			name:    "MorphTx V1 with Reference only",
			receipt: morphTxRefOnlyReceipt,
		},
		{
			name:    "MorphTx V1 with Memo only",
			receipt: morphTxMemoOnlyReceipt,
		},
		{
			name:    "MorphTx V1 with empty Memo",
			receipt: morphTxEmptyMemoReceipt,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Encode the receipt
			enc, err := encodeAsStoredReceiptRLP(tc.receipt)
			if err != nil {
				t.Fatalf("Error encoding receipt: %v", err)
			}

			// Decode the receipt
			var dec ReceiptForStorage
			if err := rlp.DecodeBytes(enc, &dec); err != nil {
				t.Fatalf("Error decoding RLP receipt: %v", err)
			}

			// Check consensus fields
			if dec.Status != tc.receipt.Status {
				t.Errorf("Status mismatch, want %v, have %v", tc.receipt.Status, dec.Status)
			}
			if dec.CumulativeGasUsed != tc.receipt.CumulativeGasUsed {
				t.Errorf("CumulativeGasUsed mismatch, want %v, have %v", tc.receipt.CumulativeGasUsed, dec.CumulativeGasUsed)
			}
			if len(dec.Logs) != len(tc.receipt.Logs) {
				t.Errorf("Logs count mismatch, want %v, have %v", len(tc.receipt.Logs), len(dec.Logs))
			}

			// Check MorphTx fields
			if dec.Version != tc.receipt.Version {
				t.Errorf("Version mismatch, want %v, have %v", tc.receipt.Version, dec.Version)
			}
			if !compareReference(dec.Reference, tc.receipt.Reference) {
				t.Errorf("Reference mismatch, want %v, have %v", tc.receipt.Reference, dec.Reference)
			}
			if !compareMemo(dec.Memo, tc.receipt.Memo) {
				t.Errorf("Memo mismatch, want %v, have %v", tc.receipt.Memo, dec.Memo)
			}

			// Check other MorphTx fields
			if !compareFeeTokenID(dec.FeeTokenID, tc.receipt.FeeTokenID) {
				t.Errorf("FeeTokenID mismatch, want %v, have %v", tc.receipt.FeeTokenID, dec.FeeTokenID)
			}
			if !compareBigInt(dec.L1Fee, tc.receipt.L1Fee) {
				t.Errorf("L1Fee mismatch, want %v, have %v", tc.receipt.L1Fee, dec.L1Fee)
			}
		})
	}
}

// Helper functions for comparing receipt fields that may have different nil/zero representations after RLP encoding
func compareReference(a, b *common.Reference) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		// After RLP decode, nil becomes zero Reference
		if a == nil {
			return *b == common.Reference{}
		}
		return *a == common.Reference{}
	}
	return *a == *b
}

func compareMemo(a, b *[]byte) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		// After RLP decode, nil becomes empty slice
		if a == nil {
			return len(*b) == 0
		}
		return len(*a) == 0
	}
	return bytes.Equal(*a, *b)
}

func compareFeeTokenID(a, b *uint16) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		// After RLP decode, nil becomes 0
		if a == nil {
			return *b == 0
		}
		return *a == 0
	}
	return *a == *b
}

func compareBigInt(a, b *big.Int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		// After RLP decode, nil becomes 0
		if a == nil {
			return b.Sign() == 0
		}
		return a.Sign() == 0
	}
	return a.Cmp(b) == 0
}

// TestMorphTxReceiptV8StorageEncoding tests V8 storage format specifically
func TestMorphTxReceiptV8StorageEncoding(t *testing.T) {
	tests := []struct {
		name    string
		receipt *Receipt
	}{
		{
			name:    "MorphTx V1 with all fields",
			receipt: morphTxV1Receipt,
		},
		{
			name:    "MorphTx V0 (legacy format)",
			receipt: morphTxV0Receipt,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Encode using V8 format
			enc, err := encodeAsV8StoredReceiptRLP(tc.receipt)
			if err != nil {
				t.Fatalf("Error encoding receipt: %v", err)
			}

			// Decode the receipt
			var dec ReceiptForStorage
			if err := rlp.DecodeBytes(enc, &dec); err != nil {
				t.Fatalf("Error decoding RLP receipt: %v", err)
			}

			// Check new fields
			if dec.Version != tc.receipt.Version {
				t.Errorf("Version mismatch, want %v, have %v", tc.receipt.Version, dec.Version)
			}
			if !reflect.DeepEqual(dec.Reference, tc.receipt.Reference) {
				t.Errorf("Reference mismatch, want %v, have %v", tc.receipt.Reference, dec.Reference)
			}
			if !reflect.DeepEqual(dec.Memo, tc.receipt.Memo) {
				t.Errorf("Memo mismatch, want %v, have %v", tc.receipt.Memo, dec.Memo)
			}
		})
	}
}

// TestMorphTxReceiptBackwardCompatibility tests that old format receipts (V3-V7)
// decode correctly with new fields having default values
func TestMorphTxReceiptBackwardCompatibility(t *testing.T) {
	// Create a basic receipt without new fields
	receipt := &Receipt{
		Status:            ReceiptStatusSuccessful,
		CumulativeGasUsed: 1000,
		Logs: []*Log{
			{
				Address: common.BytesToAddress([]byte{0x11}),
				Topics:  []common.Hash{common.HexToHash("dead")},
				Data:    []byte{0x01},
			},
		},
		L1Fee: big.NewInt(100),
	}

	tests := []struct {
		name   string
		encode func(*Receipt) ([]byte, error)
	}{
		{"V7StoredReceiptRLP", encodeAsV7StoredReceiptRLP},
		{"V6StoredReceiptRLP", encodeAsV6StoredReceiptRLP},
		{"V5StoredReceiptRLP", encodeAsV5StoredReceiptRLP},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := tc.encode(receipt)
			if err != nil {
				t.Fatalf("Error encoding receipt: %v", err)
			}

			var dec ReceiptForStorage
			if err := rlp.DecodeBytes(enc, &dec); err != nil {
				t.Fatalf("Error decoding RLP receipt: %v", err)
			}

			// New fields should have default/zero values
			if dec.Version != 0 {
				t.Errorf("Version should be 0 for old format, got %v", dec.Version)
			}
			if dec.Reference != nil {
				t.Errorf("Reference should be nil for old format, got %v", dec.Reference)
			}
			if dec.Memo != nil {
				t.Errorf("Memo should be nil for old format, got %v", dec.Memo)
			}

			// Original fields should be preserved
			if dec.Status != receipt.Status {
				t.Errorf("Status mismatch, want %v, have %v", receipt.Status, dec.Status)
			}
			if dec.CumulativeGasUsed != receipt.CumulativeGasUsed {
				t.Errorf("CumulativeGasUsed mismatch, want %v, have %v", receipt.CumulativeGasUsed, dec.CumulativeGasUsed)
			}
		})
	}
}

// TestMorphTxReceiptJSONMarshal tests JSON marshaling/unmarshaling of MorphTx receipts
func TestMorphTxReceiptJSONMarshal(t *testing.T) {
	tests := []struct {
		name    string
		receipt *Receipt
	}{
		{
			name:    "MorphTx V1 with all fields",
			receipt: morphTxV1Receipt,
		},
		{
			name:    "MorphTx V0 (legacy format)",
			receipt: morphTxV0Receipt,
		},
		{
			name:    "MorphTx V1 with Reference only",
			receipt: morphTxRefOnlyReceipt,
		},
		{
			name:    "MorphTx V1 with Memo only",
			receipt: morphTxMemoOnlyReceipt,
		},
		{
			name:    "MorphTx V1 with empty Memo",
			receipt: morphTxEmptyMemoReceipt,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Marshal to JSON
			data, err := tc.receipt.MarshalJSON()
			if err != nil {
				t.Fatalf("Error marshaling receipt to JSON: %v", err)
			}

			// Unmarshal from JSON
			var dec Receipt
			if err := dec.UnmarshalJSON(data); err != nil {
				t.Fatalf("Error unmarshaling receipt from JSON: %v", err)
			}

			// Check new fields
			if dec.Version != tc.receipt.Version {
				t.Errorf("Version mismatch, want %v, have %v", tc.receipt.Version, dec.Version)
			}
			if !reflect.DeepEqual(dec.Reference, tc.receipt.Reference) {
				t.Errorf("Reference mismatch, want %v, have %v", tc.receipt.Reference, dec.Reference)
			}
			if !reflect.DeepEqual(dec.Memo, tc.receipt.Memo) {
				t.Errorf("Memo mismatch, want %v, have %v", tc.receipt.Memo, dec.Memo)
			}

			// Check core fields
			if dec.Status != tc.receipt.Status {
				t.Errorf("Status mismatch, want %v, have %v", tc.receipt.Status, dec.Status)
			}
			if dec.CumulativeGasUsed != tc.receipt.CumulativeGasUsed {
				t.Errorf("CumulativeGasUsed mismatch, want %v, have %v", tc.receipt.CumulativeGasUsed, dec.CumulativeGasUsed)
			}
			if dec.Type != tc.receipt.Type {
				t.Errorf("Type mismatch, want %v, have %v", tc.receipt.Type, dec.Type)
			}
		})
	}
}

// TestDeriveFieldsWithMorphTx tests DeriveFields with MorphTx transactions
func TestDeriveFieldsWithMorphTx(t *testing.T) {
	to := common.HexToAddress("0x1")
	ref := common.HexToReference("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	memo := []byte("derive fields test memo")

	// Create transactions including MorphTx
	txs := Transactions{
		NewTx(&LegacyTx{
			Nonce:    1,
			Value:    big.NewInt(1),
			Gas:      1,
			GasPrice: big.NewInt(1),
		}),
		NewTx(&MorphTx{
			ChainID:   big.NewInt(1),
			Nonce:     2,
			GasTipCap: big.NewInt(1),
			GasFeeCap: big.NewInt(1),
			Gas:       2,
			To:        &to,
			Value:     big.NewInt(2),
			Version:   MorphTxVersion1,
			Reference: &ref,
			Memo:      &memo,
		}),
		NewTx(&MorphTx{
			ChainID:    big.NewInt(1),
			Nonce:      3,
			GasTipCap:  big.NewInt(1),
			GasFeeCap:  big.NewInt(1),
			Gas:        3,
			To:         &to,
			Value:      big.NewInt(3),
			Version:    MorphTxVersion0,
			FeeTokenID: 1,
		}),
	}

	// Create corresponding receipts
	receipts := Receipts{
		&Receipt{
			Status:            ReceiptStatusSuccessful,
			CumulativeGasUsed: 1,
			Logs:              []*Log{{Address: common.BytesToAddress([]byte{0x11})}},
			TxHash:            txs[0].Hash(),
			GasUsed:           1,
		},
		&Receipt{
			Status:            ReceiptStatusSuccessful,
			CumulativeGasUsed: 3,
			Logs:              []*Log{{Address: common.BytesToAddress([]byte{0x22})}},
			TxHash:            txs[1].Hash(),
			GasUsed:           2,
			Version:           MorphTxVersion1,
			Reference:         &ref,
			Memo:              &memo,
		},
		&Receipt{
			Status:            ReceiptStatusSuccessful,
			CumulativeGasUsed: 6,
			Logs:              []*Log{{Address: common.BytesToAddress([]byte{0x33})}},
			TxHash:            txs[2].Hash(),
			GasUsed:           3,
			Version:           MorphTxVersion0,
		},
	}

	// Test DeriveFields
	number := big.NewInt(1)
	blockTime := uint64(2)
	hash := common.BytesToHash([]byte{0x03, 0x14})

	clearComputedFieldsOnReceipts(t, receipts)
	if err := receipts.DeriveFields(params.TestChainConfig, hash, number.Uint64(), blockTime, nil, txs); err != nil {
		t.Fatalf("DeriveFields(...) = %v, want <nil>", err)
	}

	// Verify MorphTx receipts
	for i := range receipts {
		if receipts[i].Type != txs[i].Type() {
			t.Errorf("receipts[%d].Type = %d, want %d", i, receipts[i].Type, txs[i].Type())
		}
		if receipts[i].TxHash != txs[i].Hash() {
			t.Errorf("receipts[%d].TxHash = %s, want %s", i, receipts[i].TxHash.String(), txs[i].Hash().String())
		}
		if receipts[i].BlockHash != hash {
			t.Errorf("receipts[%d].BlockHash = %s, want %s", i, receipts[i].BlockHash.String(), hash.String())
		}
		if receipts[i].TransactionIndex != uint(i) {
			t.Errorf("receipts[%d].TransactionIndex = %d, want %d", i, receipts[i].TransactionIndex, i)
		}
	}

	// Verify MorphTx V1 receipt preserves Version/Reference/Memo
	if receipts[1].Version != MorphTxVersion1 {
		t.Errorf("receipts[1].Version = %d, want %d", receipts[1].Version, MorphTxVersion1)
	}
	if !reflect.DeepEqual(receipts[1].Reference, &ref) {
		t.Errorf("receipts[1].Reference = %v, want %v", receipts[1].Reference, &ref)
	}
	if !reflect.DeepEqual(receipts[1].Memo, &memo) {
		t.Errorf("receipts[1].Memo = %v, want %v", receipts[1].Memo, &memo)
	}

	// Verify MorphTx V0 receipt
	if receipts[2].Version != MorphTxVersion0 {
		t.Errorf("receipts[2].Version = %d, want %d", receipts[2].Version, MorphTxVersion0)
	}
}

// TestMorphTxReceiptVersionValues tests specific version values
func TestMorphTxReceiptVersionValues(t *testing.T) {
	feeTokenID := uint16(1)
	zeroRef := common.Reference{}
	emptyMemo := []byte{}
	tests := []struct {
		name          string
		version       uint8
		expectVersion uint8
	}{
		{"Version 0", MorphTxVersion0, 0},
		{"Version 1", MorphTxVersion1, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			receipt := &Receipt{
				Status:            ReceiptStatusSuccessful,
				CumulativeGasUsed: 100,
				Logs:              []*Log{},
				Type:              MorphTxType,
				Version:           tc.version,
				FeeTokenID:        &feeTokenID,
				FeeRate:           big.NewInt(100),
				TokenScale:        big.NewInt(18),
				FeeLimit:          big.NewInt(1000),
				L1Fee:             big.NewInt(50),
				Reference:         &zeroRef,   // Use zero value for RLP compatibility
				Memo:              &emptyMemo, // Use empty for RLP compatibility
			}

			// Encode
			enc, err := encodeAsStoredReceiptRLP(receipt)
			if err != nil {
				t.Fatalf("Error encoding receipt: %v", err)
			}

			// Decode
			var dec ReceiptForStorage
			if err := rlp.DecodeBytes(enc, &dec); err != nil {
				t.Fatalf("Error decoding RLP receipt: %v", err)
			}

			if dec.Version != tc.expectVersion {
				t.Errorf("Version mismatch, want %v, have %v", tc.expectVersion, dec.Version)
			}
		})
	}
}

// TestMorphTxReceiptMemoEdgeCases tests edge cases for Memo field
func TestMorphTxReceiptMemoEdgeCases(t *testing.T) {
	feeTokenID := uint16(1)
	zeroRef := common.Reference{}
	tests := []struct {
		name string
		memo *[]byte
	}{
		// Note: nil memo cannot be tested with storedReceiptRLP as RLP encoding changes the element count
		{"empty memo", func() *[]byte { m := []byte{}; return &m }()},
		{"single byte memo", func() *[]byte { m := []byte{0x01}; return &m }()},
		{"max length memo", func() *[]byte { m := make([]byte, common.MaxMemoLength); return &m }()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			receipt := &Receipt{
				Status:            ReceiptStatusSuccessful,
				CumulativeGasUsed: 100,
				Logs:              []*Log{},
				Type:              MorphTxType,
				Version:           MorphTxVersion1,
				Memo:              tc.memo,
				FeeTokenID:        &feeTokenID,
				FeeRate:           big.NewInt(100),
				TokenScale:        big.NewInt(18),
				FeeLimit:          big.NewInt(1000),
				L1Fee:             big.NewInt(50),
				Reference:         &zeroRef, // Required for RLP compatibility
			}

			// Encode
			enc, err := encodeAsStoredReceiptRLP(receipt)
			if err != nil {
				t.Fatalf("Error encoding receipt: %v", err)
			}

			// Decode
			var dec ReceiptForStorage
			if err := rlp.DecodeBytes(enc, &dec); err != nil {
				t.Fatalf("Error decoding RLP receipt: %v", err)
			}

			if !compareMemo(dec.Memo, tc.memo) {
				t.Errorf("Memo mismatch, want %v, have %v", tc.memo, dec.Memo)
			}
		})
	}
}

// TestMorphTxReceiptReferenceEdgeCases tests edge cases for Reference field
func TestMorphTxReceiptReferenceEdgeCases(t *testing.T) {
	feeTokenID := uint16(1)
	emptyMemo := []byte{}
	zeroRef := common.Reference{}
	fullRef := common.HexToReference("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	partialRef := common.HexToReference("0x1234567890abcdef1234567890abcdef00000000000000000000000000000000")

	tests := []struct {
		name      string
		reference *common.Reference
	}{
		// Note: nil reference cannot be tested with storedReceiptRLP as RLP encoding changes the element count
		{"zero reference", &zeroRef},
		{"full reference", &fullRef},
		{"partial reference", &partialRef},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			receipt := &Receipt{
				Status:            ReceiptStatusSuccessful,
				CumulativeGasUsed: 100,
				Logs:              []*Log{},
				Type:              MorphTxType,
				Version:           MorphTxVersion1,
				Reference:         tc.reference,
				FeeTokenID:        &feeTokenID,
				FeeRate:           big.NewInt(100),
				TokenScale:        big.NewInt(18),
				FeeLimit:          big.NewInt(1000),
				L1Fee:             big.NewInt(50),
				Memo:              &emptyMemo, // Required for RLP compatibility
			}

			// Encode
			enc, err := encodeAsStoredReceiptRLP(receipt)
			if err != nil {
				t.Fatalf("Error encoding receipt: %v", err)
			}

			// Decode
			var dec ReceiptForStorage
			if err := rlp.DecodeBytes(enc, &dec); err != nil {
				t.Fatalf("Error decoding RLP receipt: %v", err)
			}

			if !compareReference(dec.Reference, tc.reference) {
				t.Errorf("Reference mismatch, want %v, have %v", tc.reference, dec.Reference)
			}
		})
	}
}
