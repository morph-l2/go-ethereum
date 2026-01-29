// Copyright 2014 The go-ethereum Authors
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
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/rlp"
)

// The values in those tests are from the Transaction Tests
// at github.com/ethereum/tests.
var (
	testAddr = common.HexToAddress("b94f5374fce5edbc8e2a8697c15331677e6ebf0b")

	emptyTx = NewTransaction(
		0,
		common.HexToAddress("095e7baea6a6c7c4c2dfeb977efac326af552d87"),
		big.NewInt(0), 0, big.NewInt(0),
		nil,
	)

	rightvrsTx, _ = NewTransaction(
		3,
		testAddr,
		big.NewInt(10),
		2000,
		big.NewInt(1),
		common.FromHex("5544"),
	).WithSignature(
		HomesteadSigner{},
		common.Hex2Bytes("98ff921201554726367d2be8c804a7ff89ccf285ebc57dff8ae4c44b9c19ac4a8887321be575c8095f789dd4c743dfe42c1820f9231f98a962b210e3ac2452a301"),
	)

	emptyEip2718Tx = NewTx(&AccessListTx{
		ChainID:  big.NewInt(1),
		Nonce:    3,
		To:       &testAddr,
		Value:    big.NewInt(10),
		Gas:      25000,
		GasPrice: big.NewInt(1),
		Data:     common.FromHex("5544"),
	})

	signedEip2718Tx, _ = emptyEip2718Tx.WithSignature(
		NewEIP2930Signer(big.NewInt(1)),
		common.Hex2Bytes("c9519f4f2b30335884581971573fadf60c6204f59a911df35ee8a540456b266032f1e8e2c5dd761f9e4f88f41c8310aeaba26a8bfcdacfedfa12ec3862d3752101"),
	)

	// MorphTx test fixtures
	testMorphTxReference = common.HexToReference("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	testMorphTxMemo      = []byte("test memo data for morph tx")

	// MorphTx Version 0 (legacy format with alt fee)
	emptyMorphTxV0 = NewTx(&MorphTx{
		ChainID:    big.NewInt(1),
		Nonce:      3,
		GasTipCap:  big.NewInt(1),
		GasFeeCap:  big.NewInt(10),
		Gas:        25000,
		To:         &testAddr,
		Value:      big.NewInt(10),
		Data:       common.FromHex("5544"),
		AccessList: nil,
		FeeTokenID: 1, // V0 requires FeeTokenID > 0
		FeeLimit:   big.NewInt(1000000),
		Version:    MorphTxVersion0,
	})

	// MorphTx Version 1 (with Reference and Memo)
	emptyMorphTxV1 = NewTx(&MorphTx{
		ChainID:    big.NewInt(1),
		Nonce:      3,
		GasTipCap:  big.NewInt(1),
		GasFeeCap:  big.NewInt(10),
		Gas:        25000,
		To:         &testAddr,
		Value:      big.NewInt(10),
		Data:       common.FromHex("5544"),
		AccessList: nil,
		FeeTokenID: 0, // V1 allows FeeTokenID = 0
		FeeLimit:   big.NewInt(0),
		Version:    MorphTxVersion1,
		Reference:  &testMorphTxReference,
		Memo:       &testMorphTxMemo,
	})

	// MorphTx V1 with only Reference (no Memo)
	morphTxV1RefOnly = NewTx(&MorphTx{
		ChainID:    big.NewInt(1),
		Nonce:      4,
		GasTipCap:  big.NewInt(2),
		GasFeeCap:  big.NewInt(20),
		Gas:        30000,
		To:         &testAddr,
		Value:      big.NewInt(20),
		Version:    MorphTxVersion1,
		Reference:  &testMorphTxReference,
	})

	// MorphTx V1 with only Memo (no Reference)
	morphTxV1MemoOnly = NewTx(&MorphTx{
		ChainID:   big.NewInt(1),
		Nonce:     5,
		GasTipCap: big.NewInt(3),
		GasFeeCap: big.NewInt(30),
		Gas:       35000,
		To:        &testAddr,
		Value:     big.NewInt(30),
		Version:   MorphTxVersion1,
		Memo:      &testMorphTxMemo,
	})
)

func TestDecodeEmptyTypedTx(t *testing.T) {
	input := []byte{0x80}
	var tx Transaction
	err := rlp.DecodeBytes(input, &tx)
	// The error should be either errEmptyTypedTx or errShortTypedTx
	if err != errEmptyTypedTx && err != errShortTypedTx {
		t.Fatal("wrong error:", err)
	}
}

func TestTransactionSigHash(t *testing.T) {
	var homestead HomesteadSigner
	if homestead.Hash(emptyTx) != common.HexToHash("c775b99e7ad12f50d819fcd602390467e28141316969f4b57f0626f74fe3b386") {
		t.Errorf("empty transaction hash mismatch, got %x", emptyTx.Hash())
	}
	if homestead.Hash(rightvrsTx) != common.HexToHash("fe7a79529ed5f7c3375d06b26b186a8644e0e16c373d7a12be41c62d6042b77a") {
		t.Errorf("RightVRS transaction hash mismatch, got %x", rightvrsTx.Hash())
	}
}

func TestTransactionEncode(t *testing.T) {
	txb, err := rlp.EncodeToBytes(rightvrsTx)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	should := common.FromHex("f86103018207d094b94f5374fce5edbc8e2a8697c15331677e6ebf0b0a8255441ca098ff921201554726367d2be8c804a7ff89ccf285ebc57dff8ae4c44b9c19ac4aa08887321be575c8095f789dd4c743dfe42c1820f9231f98a962b210e3ac2452a3")
	if !bytes.Equal(txb, should) {
		t.Errorf("encoded RLP mismatch, got %x", txb)
	}
}

func TestEIP2718TransactionSigHash(t *testing.T) {
	s := NewEIP2930Signer(big.NewInt(1))
	if s.Hash(emptyEip2718Tx) != common.HexToHash("49b486f0ec0a60dfbbca2d30cb07c9e8ffb2a2ff41f29a1ab6737475f6ff69f3") {
		t.Errorf("empty EIP-2718 transaction hash mismatch, got %x", s.Hash(emptyEip2718Tx))
	}
	if s.Hash(signedEip2718Tx) != common.HexToHash("49b486f0ec0a60dfbbca2d30cb07c9e8ffb2a2ff41f29a1ab6737475f6ff69f3") {
		t.Errorf("signed EIP-2718 transaction hash mismatch, got %x", s.Hash(signedEip2718Tx))
	}
}

// This test checks signature operations on access list transactions.
func TestEIP2930Signer(t *testing.T) {

	var (
		key, _  = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		keyAddr = crypto.PubkeyToAddress(key.PublicKey)
		signer1 = NewEIP2930Signer(big.NewInt(1))
		signer2 = NewEIP2930Signer(big.NewInt(2))
		tx0     = NewTx(&AccessListTx{Nonce: 1})
		tx1     = NewTx(&AccessListTx{ChainID: big.NewInt(1), Nonce: 1})
		tx2, _  = SignNewTx(key, signer2, &AccessListTx{ChainID: big.NewInt(2), Nonce: 1})
	)

	tests := []struct {
		tx             *Transaction
		signer         Signer
		wantSignerHash common.Hash
		wantSenderErr  error
		wantSignErr    error
		wantHash       common.Hash // after signing
	}{
		{
			tx:             tx0,
			signer:         signer1,
			wantSignerHash: common.HexToHash("846ad7672f2a3a40c1f959cd4a8ad21786d620077084d84c8d7c077714caa139"),
			wantSenderErr:  ErrInvalidChainId,
			wantHash:       common.HexToHash("1ccd12d8bbdb96ea391af49a35ab641e219b2dd638dea375f2bc94dd290f2549"),
		},
		{
			tx:             tx1,
			signer:         signer1,
			wantSenderErr:  ErrInvalidSig,
			wantSignerHash: common.HexToHash("846ad7672f2a3a40c1f959cd4a8ad21786d620077084d84c8d7c077714caa139"),
			wantHash:       common.HexToHash("1ccd12d8bbdb96ea391af49a35ab641e219b2dd638dea375f2bc94dd290f2549"),
		},
		{
			// This checks what happens when trying to sign an unsigned tx for the wrong chain.
			tx:             tx1,
			signer:         signer2,
			wantSenderErr:  ErrInvalidChainId,
			wantSignerHash: common.HexToHash("367967247499343401261d718ed5aa4c9486583e4d89251afce47f4a33c33362"),
			wantSignErr:    ErrInvalidChainId,
		},
		{
			// This checks what happens when trying to re-sign a signed tx for the wrong chain.
			tx:             tx2,
			signer:         signer1,
			wantSenderErr:  ErrInvalidChainId,
			wantSignerHash: common.HexToHash("846ad7672f2a3a40c1f959cd4a8ad21786d620077084d84c8d7c077714caa139"),
			wantSignErr:    ErrInvalidChainId,
		},
	}

	for i, test := range tests {
		sigHash := test.signer.Hash(test.tx)
		if sigHash != test.wantSignerHash {
			t.Errorf("test %d: wrong sig hash: got %x, want %x", i, sigHash, test.wantSignerHash)
		}
		sender, err := Sender(test.signer, test.tx)
		// Use errors.Is for wrapped errors
		if !errors.Is(err, test.wantSenderErr) {
			t.Errorf("test %d: wrong Sender error %q, want %q", i, err, test.wantSenderErr)
		}
		if err == nil && sender != keyAddr {
			t.Errorf("test %d: wrong sender address %x", i, sender)
		}
		signedTx, err := SignTx(test.tx, test.signer, key)
		// Use errors.Is for wrapped errors
		if !errors.Is(err, test.wantSignErr) {
			t.Fatalf("test %d: wrong SignTx error %q, want %q", i, err, test.wantSignErr)
		}
		if signedTx != nil {
			if signedTx.Hash() != test.wantHash {
				t.Errorf("test %d: wrong tx hash after signing: got %x, want %x", i, signedTx.Hash(), test.wantHash)
			}
		}
	}
}

func TestEIP2718TransactionEncode(t *testing.T) {
	// RLP representation
	{
		have, err := rlp.EncodeToBytes(signedEip2718Tx)
		if err != nil {
			t.Fatalf("encode error: %v", err)
		}
		want := common.FromHex("b86601f8630103018261a894b94f5374fce5edbc8e2a8697c15331677e6ebf0b0a825544c001a0c9519f4f2b30335884581971573fadf60c6204f59a911df35ee8a540456b2660a032f1e8e2c5dd761f9e4f88f41c8310aeaba26a8bfcdacfedfa12ec3862d37521")
		if !bytes.Equal(have, want) {
			t.Errorf("encoded RLP mismatch, got %x", have)
		}
	}
	// Binary representation
	{
		have, err := signedEip2718Tx.MarshalBinary()
		if err != nil {
			t.Fatalf("encode error: %v", err)
		}
		want := common.FromHex("01f8630103018261a894b94f5374fce5edbc8e2a8697c15331677e6ebf0b0a825544c001a0c9519f4f2b30335884581971573fadf60c6204f59a911df35ee8a540456b2660a032f1e8e2c5dd761f9e4f88f41c8310aeaba26a8bfcdacfedfa12ec3862d37521")
		if !bytes.Equal(have, want) {
			t.Errorf("encoded RLP mismatch, got %x", have)
		}
	}
}

func decodeTx(data []byte) (*Transaction, error) {
	var tx Transaction
	t, err := &tx, rlp.Decode(bytes.NewReader(data), &tx)
	return t, err
}

func defaultTestKey() (*ecdsa.PrivateKey, common.Address) {
	key, _ := crypto.HexToECDSA("45a915e4d060149eb4365960e6a7a45f334393093061116b197e3240065ff2d8")
	addr := crypto.PubkeyToAddress(key.PublicKey)
	return key, addr
}

func TestRecipientEmpty(t *testing.T) {
	_, addr := defaultTestKey()
	tx, err := decodeTx(common.Hex2Bytes("f8498080808080011ca09b16de9d5bdee2cf56c28d16275a4da68cd30273e2525f3959f5d62557489921a0372ebd8fb3345f7db7b5a86d42e24d36e983e259b0664ceb8c227ec9af572f3d"))
	if err != nil {
		t.Fatal(err)
	}

	from, err := Sender(HomesteadSigner{}, tx)
	if err != nil {
		t.Fatal(err)
	}
	if addr != from {
		t.Fatal("derived address doesn't match")
	}
}

func TestRecipientNormal(t *testing.T) {
	_, addr := defaultTestKey()

	tx, err := decodeTx(common.Hex2Bytes("f85d80808094000000000000000000000000000000000000000080011ca0527c0d8f5c63f7b9f41324a7c8a563ee1190bcbf0dac8ab446291bdbf32f5c79a0552c4ef0a09a04395074dab9ed34d3fbfb843c2f2546cc30fe89ec143ca94ca6"))
	if err != nil {
		t.Fatal(err)
	}

	from, err := Sender(HomesteadSigner{}, tx)
	if err != nil {
		t.Fatal(err)
	}
	if addr != from {
		t.Fatal("derived address doesn't match")
	}
}

func TestTransactionPriceNonceSortLegacy(t *testing.T) {
	testTransactionPriceNonceSort(t, nil)
}

func TestTransactionPriceNonceSort1559(t *testing.T) {
	testTransactionPriceNonceSort(t, big.NewInt(0))
	testTransactionPriceNonceSort(t, big.NewInt(5))
	testTransactionPriceNonceSort(t, big.NewInt(50))
}

// Tests that transactions can be correctly sorted according to their price in
// decreasing order, but at the same time with increasing nonces when issued by
// the same account.
func testTransactionPriceNonceSort(t *testing.T, baseFee *big.Int) {
	// Generate a batch of accounts to start with
	keys := make([]*ecdsa.PrivateKey, 25)
	for i := 0; i < len(keys); i++ {
		keys[i], _ = crypto.GenerateKey()
	}
	signer := LatestSignerForChainID(common.Big1)

	// Generate a batch of transactions with overlapping values, but shifted nonces
	groups := map[common.Address]Transactions{}
	expectedCount := 0
	for start, key := range keys {
		addr := crypto.PubkeyToAddress(key.PublicKey)
		count := 25
		for i := 0; i < 25; i++ {
			var tx *Transaction
			gasFeeCap := rand.Intn(50)
			if baseFee == nil {
				tx = NewTx(&LegacyTx{
					Nonce:    uint64(start + i),
					To:       &common.Address{},
					Value:    big.NewInt(100),
					Gas:      100,
					GasPrice: big.NewInt(int64(gasFeeCap)),
					Data:     nil,
				})
			} else {
				tx = NewTx(&DynamicFeeTx{
					Nonce:     uint64(start + i),
					To:        &common.Address{},
					Value:     big.NewInt(100),
					Gas:       100,
					GasFeeCap: big.NewInt(int64(gasFeeCap)),
					GasTipCap: big.NewInt(int64(rand.Intn(gasFeeCap + 1))),
					Data:      nil,
				})
				if count == 25 && int64(gasFeeCap) < baseFee.Int64() {
					count = i
				}
			}
			tx, err := SignTx(tx, signer, key)
			if err != nil {
				t.Fatalf("failed to sign tx: %s", err)
			}
			groups[addr] = append(groups[addr], tx)
		}
		expectedCount += count
	}
	// Sort the transactions and cross check the nonce ordering
	txset := NewTransactionsByPriceAndNonce(signer, groups, baseFee)

	txs := Transactions{}
	for tx := txset.Peek(); tx != nil; tx = txset.Peek() {
		txs = append(txs, tx)
		txset.Shift()
	}
	if len(txs) != expectedCount {
		t.Errorf("expected %d transactions, found %d", expectedCount, len(txs))
	}
	for i, txi := range txs {
		fromi, _ := Sender(signer, txi)

		// Make sure the nonce order is valid
		for j, txj := range txs[i+1:] {
			fromj, _ := Sender(signer, txj)
			if fromi == fromj && txi.Nonce() > txj.Nonce() {
				t.Errorf("invalid nonce ordering: tx #%d (A=%x N=%v) < tx #%d (A=%x N=%v)", i, fromi[:4], txi.Nonce(), i+j, fromj[:4], txj.Nonce())
			}
		}
		// If the next tx has different from account, the price must be lower than the current one
		if i+1 < len(txs) {
			next := txs[i+1]
			fromNext, _ := Sender(signer, next)
			tip, err := txi.EffectiveGasTip(baseFee)
			nextTip, nextErr := next.EffectiveGasTip(baseFee)
			if err != nil || nextErr != nil {
				t.Errorf("error calculating effective tip")
			}
			if fromi != fromNext && tip.Cmp(nextTip) < 0 {
				t.Errorf("invalid gasprice ordering: tx #%d (A=%x P=%v) < tx #%d (A=%x P=%v)", i, fromi[:4], txi.GasPrice(), i+1, fromNext[:4], next.GasPrice())
			}
		}
	}
}

// Tests that if multiple transactions have the same price, the ones seen earlier
// are prioritized to avoid network spam attacks aiming for a specific ordering.
func TestTransactionTimeSort(t *testing.T) {
	// Generate a batch of accounts to start with
	keys := make([]*ecdsa.PrivateKey, 5)
	for i := 0; i < len(keys); i++ {
		keys[i], _ = crypto.GenerateKey()
	}
	signer := HomesteadSigner{}

	// Generate a batch of transactions with overlapping prices, but different creation times
	groups := map[common.Address]Transactions{}
	for start, key := range keys {
		addr := crypto.PubkeyToAddress(key.PublicKey)

		tx, _ := SignTx(NewTransaction(0, common.Address{}, big.NewInt(100), 100, big.NewInt(1), nil), signer, key)
		tx.time = time.Unix(0, int64(len(keys)-start))

		groups[addr] = append(groups[addr], tx)
	}
	// Sort the transactions and cross check the nonce ordering
	txset := NewTransactionsByPriceAndNonce(signer, groups, nil)

	txs := Transactions{}
	for tx := txset.Peek(); tx != nil; tx = txset.Peek() {
		txs = append(txs, tx)
		txset.Shift()
	}
	if len(txs) != len(keys) {
		t.Errorf("expected %d transactions, found %d", len(keys), len(txs))
	}
	for i, txi := range txs {
		fromi, _ := Sender(signer, txi)
		if i+1 < len(txs) {
			next := txs[i+1]
			fromNext, _ := Sender(signer, next)

			if txi.GasPrice().Cmp(next.GasPrice()) < 0 {
				t.Errorf("invalid gasprice ordering: tx #%d (A=%x P=%v) < tx #%d (A=%x P=%v)", i, fromi[:4], txi.GasPrice(), i+1, fromNext[:4], next.GasPrice())
			}
			// Make sure time order is ascending if the txs have the same gas price
			if txi.GasPrice().Cmp(next.GasPrice()) == 0 && txi.time.After(next.time) {
				t.Errorf("invalid received time ordering: tx #%d (A=%x T=%v) > tx #%d (A=%x T=%v)", i, fromi[:4], txi.time, i+1, fromNext[:4], next.time)
			}
		}
	}
}

// TestTransactionCoding tests serializing/de-serializing to/from rlp and JSON.
func TestTransactionCoding(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("could not generate key: %v", err)
	}
	var (
		signer       = NewEIP2930Signer(common.Big1)
		morphSigner  = NewEmeraldSigner(common.Big1) // Signer for MorphTx
		addr         = common.HexToAddress("0x0000000000000000000000000000000000000001")
		recipient    = common.HexToAddress("095e7baea6a6c7c4c2dfeb977efac326af552d87")
		accesses     = AccessList{{Address: addr, StorageKeys: []common.Hash{{0}}}}
	)
	morphTxRef := common.HexToReference("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	morphTxMemo := []byte("test memo")
	for i := uint64(0); i < 800; i++ {
		var txdata TxData
		var isL1MessageTx bool
		switch i % 8 {
		case 0:
			// Legacy tx.
			txdata = &LegacyTx{
				Nonce:    i,
				To:       &recipient,
				Gas:      1,
				GasPrice: big.NewInt(2),
				Data:     []byte("abcdef"),
			}
		case 1:
			// Legacy tx contract creation.
			txdata = &LegacyTx{
				Nonce:    i,
				Gas:      1,
				GasPrice: big.NewInt(2),
				Data:     []byte("abcdef"),
			}
		case 2:
			// Tx with non-zero access list.
			txdata = &AccessListTx{
				ChainID:    big.NewInt(1),
				Nonce:      i,
				To:         &recipient,
				Gas:        123457,
				GasPrice:   big.NewInt(10),
				AccessList: accesses,
				Data:       []byte("abcdef"),
			}
		case 3:
			// Tx with empty access list.
			txdata = &AccessListTx{
				ChainID:  big.NewInt(1),
				Nonce:    i,
				To:       &recipient,
				Gas:      123457,
				GasPrice: big.NewInt(10),
				Data:     []byte("abcdef"),
			}
		case 4:
			// Contract creation with access list.
			txdata = &AccessListTx{
				ChainID:    big.NewInt(1),
				Nonce:      i,
				Gas:        123457,
				GasPrice:   big.NewInt(10),
				AccessList: accesses,
			}
		case 5:
			// L1MessageTx
			isL1MessageTx = true
			txdata = &L1MessageTx{
				QueueIndex: i,
				Gas:        123457,
				To:         &recipient,
				Value:      big.NewInt(10),
				Data:       []byte("abcdef"),
				Sender:     addr,
			}
		case 6:
			// MorphTx V0 (legacy format with alt fee)
			txdata = &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      i,
				GasTipCap:  big.NewInt(1),
				GasFeeCap:  big.NewInt(10),
				Gas:        123457,
				To:         &recipient,
				Value:      big.NewInt(10),
				Data:       []byte("abcdef"),
				AccessList: accesses,
				FeeTokenID: 1, // V0 requires FeeTokenID > 0
				FeeLimit:   big.NewInt(1000000),
				Version:    MorphTxVersion0,
			}
		case 7:
			// MorphTx V1 (with Reference and Memo)
			txdata = &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      i,
				GasTipCap:  big.NewInt(2),
				GasFeeCap:  big.NewInt(20),
				Gas:        123457,
				To:         &recipient,
				Value:      big.NewInt(20),
				Data:       []byte("abcdef"),
				AccessList: accesses,
				FeeTokenID: 0,
				FeeLimit:   big.NewInt(0),
				Version:    MorphTxVersion1,
				Reference:  &morphTxRef,
				Memo:       &morphTxMemo,
			}
		}
		var tx *Transaction
		//  dont sign L1MessageTx
		if isL1MessageTx {
			tx = NewTx(txdata)
		} else {
			// Use morphSigner for MorphTx types
			txSigner := signer
			if _, ok := txdata.(*MorphTx); ok {
				txSigner = morphSigner
			}
			tx, err = SignNewTx(key, txSigner, txdata)
			if err != nil {
				t.Fatalf("could not sign transaction: %v", err)
			}
		}
		// RLP
		parsedTx, err := encodeDecodeBinary(tx)
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(parsedTx, tx)

		// JSON
		parsedTx, err = encodeDecodeJSON(tx)
		if err != nil {
			t.Fatal(err)
		}
		assertEqual(parsedTx, tx)
	}
}

// make sure that the transaction hash is same as bridge contract
// go test -v -run TestBridgeTxHash
func TestBridgeTxHash(t *testing.T) {
	sender := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	to := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	tx := NewTx(
		&L1MessageTx{
			Sender:     sender,
			QueueIndex: 1,
			Value:      big.NewInt(2),
			Gas:        3,
			To:         &to,
			Data:       []byte{1, 2, 3, 4},
		},
	)
	// assert equal
	if tx.Hash() != common.HexToHash("0x1cebed6d90ef618f60eec1b7edc0df36b298a237c219f0950081acfb72eac6be") {
		t.Errorf("hash does not match bridge contract")
	}
}

func encodeDecodeJSON(tx *Transaction) (*Transaction, error) {
	data, err := json.Marshal(tx)
	if err != nil {
		return nil, fmt.Errorf("json encoding failed: %v", err)
	}
	var parsedTx = &Transaction{}
	if err := json.Unmarshal(data, &parsedTx); err != nil {
		return nil, fmt.Errorf("json decoding failed: %v", err)
	}
	return parsedTx, nil
}

func encodeDecodeBinary(tx *Transaction) (*Transaction, error) {
	data, err := tx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("rlp encoding failed: %v", err)
	}
	var parsedTx = &Transaction{}
	if err := parsedTx.UnmarshalBinary(data); err != nil {
		return nil, fmt.Errorf("rlp decoding failed: %v", err)
	}
	return parsedTx, nil
}

func assertEqual(orig *Transaction, cpy *Transaction) error {
	// compare nonce, price, gaslimit, recipient, amount, payload, V, R, S
	if want, got := orig.Hash(), cpy.Hash(); want != got {
		return fmt.Errorf("parsed tx differs from original tx, want %v, got %v", want, got)
	}
	if want, got := orig.ChainId(), cpy.ChainId(); want.Cmp(got) != 0 {
		return fmt.Errorf("invalid chain id, want %d, got %d", want, got)
	}
	if orig.AccessList() != nil {
		if !reflect.DeepEqual(orig.AccessList(), cpy.AccessList()) {
			return fmt.Errorf("access list wrong!")
		}
	}
	return nil
}

// ==================== MorphTx Tests ====================

// TestMorphTxSigHash tests the signature hash calculation for MorphTx V0 and V1.
// These hashes are stable and should not change - any change indicates a breaking change.
func TestMorphTxSigHash(t *testing.T) {
	signer := NewEmeraldSigner(big.NewInt(1))

	// Test V0 sigHash (legacy format)
	v0SigHash := signer.Hash(emptyMorphTxV0)
	t.Logf("MorphTx V0 sigHash: %s", v0SigHash.Hex())
	expectedV0Hash := common.HexToHash("0x88cdb3aa657406af62d0c9d752d3f496829487516d70ef1c6172bd69c2ab9c4a")
	if v0SigHash != expectedV0Hash {
		t.Errorf("MorphTx V0 sigHash mismatch, got %s, want %s", v0SigHash.Hex(), expectedV0Hash.Hex())
	}

	// Test V1 sigHash (with Reference/Memo)
	v1SigHash := signer.Hash(emptyMorphTxV1)
	t.Logf("MorphTx V1 sigHash: %s", v1SigHash.Hex())
	expectedV1Hash := common.HexToHash("0xe80adb944285a68b17dfcf50e95e90f19a7ce23352563786f3732bf07be8f8cf")
	if v1SigHash != expectedV1Hash {
		t.Errorf("MorphTx V1 sigHash mismatch, got %s, want %s", v1SigHash.Hex(), expectedV1Hash.Hex())
	}

	// Ensure V0 and V1 sigHashes are different
	if v0SigHash == v1SigHash {
		t.Error("MorphTx V0 and V1 sigHashes should be different")
	}
}

// TestMorphTxSigner tests signature operations on MorphTx.
func TestMorphTxSigner(t *testing.T) {
	key, _ := crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	keyAddr := crypto.PubkeyToAddress(key.PublicKey)
	signer := NewEmeraldSigner(big.NewInt(1))

	tests := []struct {
		name          string
		tx            *Transaction
		wantSenderErr error
	}{
		{
			name:          "MorphTx V0 unsigned",
			tx:            emptyMorphTxV0,
			wantSenderErr: ErrInvalidSig,
		},
		{
			name:          "MorphTx V1 unsigned",
			tx:            emptyMorphTxV1,
			wantSenderErr: ErrInvalidSig,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Test unsigned tx returns expected error
			_, err := Sender(signer, tc.tx)
			if err != tc.wantSenderErr {
				t.Errorf("Sender error mismatch, got %v, want %v", err, tc.wantSenderErr)
			}

			// Sign the tx
			signedTx, err := SignTx(tc.tx, signer, key)
			if err != nil {
				t.Fatalf("SignTx failed: %v", err)
			}

			// Verify sender
			sender, err := Sender(signer, signedTx)
			if err != nil {
				t.Fatalf("Sender failed after signing: %v", err)
			}
			if sender != keyAddr {
				t.Errorf("Sender mismatch, got %s, want %s", sender.Hex(), keyAddr.Hex())
			}

			// Verify hash is stable after signing
			hash1 := signedTx.Hash()
			hash2 := signedTx.Hash()
			if hash1 != hash2 {
				t.Error("Hash should be stable")
			}
		})
	}
}

// TestMorphTxEncoding tests RLP and JSON encoding/decoding for MorphTx.
func TestMorphTxEncoding(t *testing.T) {
	key, _ := crypto.GenerateKey()
	signer := NewEmeraldSigner(big.NewInt(1))
	ref := common.HexToReference("0x1111111111111111111111111111111111111111111111111111111111111111")
	memo := []byte("encoding test memo")

	zeroRef := common.Reference{}
	emptyMemo := []byte{}

	tests := []struct {
		name string
		tx   *MorphTx
	}{
		{
			name: "V0 basic",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      1,
				GasTipCap:  big.NewInt(1),
				GasFeeCap:  big.NewInt(10),
				Gas:        21000,
				To:         &testAddr,
				Value:      big.NewInt(100),
				FeeTokenID: 1,
				FeeLimit:   big.NewInt(1000),
				Version:    MorphTxVersion0,
			},
		},
		{
			name: "V1 with all fields",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      2,
				GasTipCap:  big.NewInt(2),
				GasFeeCap:  big.NewInt(20),
				Gas:        21000,
				To:         &testAddr,
				Value:      big.NewInt(200),
				FeeTokenID: 0,
				Version:    MorphTxVersion1,
				Reference:  &ref,
				Memo:       &memo,
			},
		},
		{
			name: "V1 with Reference only",
			tx: &MorphTx{
				ChainID:   big.NewInt(1),
				Nonce:     3,
				GasTipCap: big.NewInt(1),
				GasFeeCap: big.NewInt(10),
				Gas:       21000,
				To:        &testAddr,
				Value:     big.NewInt(0),
				Version:   MorphTxVersion1,
				Reference: &ref,
				Memo:      &emptyMemo, // V1 requires Memo field for RLP encoding
			},
		},
		{
			name: "V1 with Memo only",
			tx: &MorphTx{
				ChainID:   big.NewInt(1),
				Nonce:     4,
				GasTipCap: big.NewInt(1),
				GasFeeCap: big.NewInt(10),
				Gas:       21000,
				To:        &testAddr,
				Value:     big.NewInt(0),
				Version:   MorphTxVersion1,
				Reference: &zeroRef, // V1 requires Reference field for RLP encoding
				Memo:      &memo,
			},
		},
		{
			name: "V1 with empty Memo",
			tx: &MorphTx{
				ChainID:   big.NewInt(1),
				Nonce:     5,
				GasTipCap: big.NewInt(1),
				GasFeeCap: big.NewInt(10),
				Gas:       21000,
				To:        &testAddr,
				Value:     big.NewInt(0),
				Version:   MorphTxVersion1,
				Reference: &zeroRef, // V1 requires Reference field for RLP encoding
				Memo:      &emptyMemo,
			},
		},
		{
			name: "V1 with max length Memo",
			tx: &MorphTx{
				ChainID:   big.NewInt(1),
				Nonce:     6,
				GasTipCap: big.NewInt(1),
				GasFeeCap: big.NewInt(10),
				Gas:       21000,
				To:        &testAddr,
				Value:     big.NewInt(0),
				Version:   MorphTxVersion1,
				Reference: &zeroRef, // V1 requires Reference field for RLP encoding
				Memo:      func() *[]byte { m := make([]byte, common.MaxMemoLength); return &m }(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := NewTx(tc.tx)
			signedTx, err := SignTx(tx, signer, key)
			if err != nil {
				t.Fatalf("SignTx failed: %v", err)
			}

			// Test RLP encoding/decoding
			parsedTx, err := encodeDecodeBinary(signedTx)
			if err != nil {
				t.Fatalf("RLP encode/decode failed: %v", err)
			}
			if parsedTx.Hash() != signedTx.Hash() {
				t.Error("Hash mismatch after RLP encode/decode")
			}
			if parsedTx.Version() != signedTx.Version() {
				t.Errorf("Version mismatch, got %d, want %d", parsedTx.Version(), signedTx.Version())
			}
			if !reflect.DeepEqual(parsedTx.Reference(), signedTx.Reference()) {
				t.Error("Reference mismatch after RLP encode/decode")
			}
			if !compareMemoPtr(parsedTx.Memo(), signedTx.Memo()) {
				t.Error("Memo mismatch after RLP encode/decode")
			}

			// Test JSON encoding/decoding
			parsedTx, err = encodeDecodeJSON(signedTx)
			if err != nil {
				t.Fatalf("JSON encode/decode failed: %v", err)
			}
			if parsedTx.Hash() != signedTx.Hash() {
				t.Error("Hash mismatch after JSON encode/decode")
			}
			if parsedTx.Version() != signedTx.Version() {
				t.Errorf("Version mismatch after JSON, got %d, want %d", parsedTx.Version(), signedTx.Version())
			}
		})
	}
}

// TestMorphTxValidation tests ValidateMemo and ValidateMorphTxVersion.
func TestMorphTxValidation(t *testing.T) {
	t.Run("ValidateMemo", func(t *testing.T) {
		tests := []struct {
			name    string
			memo    *[]byte
			wantErr error
		}{
			{"nil memo", nil, nil},
			{"empty memo", func() *[]byte { m := []byte{}; return &m }(), nil},
			{"valid memo", func() *[]byte { m := []byte("hello"); return &m }(), nil},
			{"max length memo", func() *[]byte { m := make([]byte, common.MaxMemoLength); return &m }(), nil},
			{"over max length memo", func() *[]byte { m := make([]byte, common.MaxMemoLength+1); return &m }(), ErrMemoTooLong},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				tx := NewTx(&MorphTx{
					ChainID:   big.NewInt(1),
					Nonce:     1,
					GasTipCap: big.NewInt(1),
					GasFeeCap: big.NewInt(10),
					Gas:       21000,
					To:        &testAddr,
					Version:   MorphTxVersion1,
					Memo:      tc.memo,
				})
				err := tx.ValidateMemo()
				if err != tc.wantErr {
					t.Errorf("ValidateMemo error mismatch, got %v, want %v", err, tc.wantErr)
				}
			})
		}
	})

	t.Run("ValidateMorphTxVersion", func(t *testing.T) {
		tests := []struct {
			name       string
			version    uint8
			feeTokenID uint16
			wantErr    error
		}{
			{"V0 with FeeTokenID > 0", MorphTxVersion0, 1, nil},
			{"V0 with FeeTokenID = 0", MorphTxVersion0, 0, ErrMorphTxV0RequiresFeeToken},
			{"V1 with FeeTokenID = 0", MorphTxVersion1, 0, nil},
			{"V1 with FeeTokenID > 0", MorphTxVersion1, 1, nil},
			{"Unsupported version 2", 2, 0, ErrMorphTxUnsupportedVersion},
			{"Unsupported version 255", 255, 1, ErrMorphTxUnsupportedVersion},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				tx := NewTx(&MorphTx{
					ChainID:    big.NewInt(1),
					Nonce:      1,
					GasTipCap:  big.NewInt(1),
					GasFeeCap:  big.NewInt(10),
					Gas:        21000,
					To:         &testAddr,
					Version:    tc.version,
					FeeTokenID: tc.feeTokenID,
				})
				err := tx.ValidateMorphTxVersion()
				if err != tc.wantErr {
					t.Errorf("ValidateMorphTxVersion error mismatch, got %v, want %v", err, tc.wantErr)
				}
			})
		}
	})

	t.Run("Non-MorphTx validation", func(t *testing.T) {
		// ValidateMemo on non-MorphTx should return nil
		legacyTx := NewTx(&LegacyTx{
			Nonce:    1,
			GasPrice: big.NewInt(1),
			Gas:      21000,
			To:       &testAddr,
		})
		if err := legacyTx.ValidateMemo(); err != nil {
			t.Errorf("ValidateMemo on LegacyTx should return nil, got %v", err)
		}
		if err := legacyTx.ValidateMorphTxVersion(); err != nil {
			t.Errorf("ValidateMorphTxVersion on LegacyTx should return nil, got %v", err)
		}
	})
}

// TestMorphTxAccessors tests accessor methods for MorphTx fields.
func TestMorphTxAccessors(t *testing.T) {
	ref := common.HexToReference("0x2222222222222222222222222222222222222222222222222222222222222222")
	memo := []byte("accessor test memo")
	feeLimit := big.NewInt(999999)

	t.Run("MorphTx V0 accessors", func(t *testing.T) {
		tx := NewTx(&MorphTx{
			ChainID:    big.NewInt(1),
			Nonce:      10,
			GasTipCap:  big.NewInt(5),
			GasFeeCap:  big.NewInt(50),
			Gas:        30000,
			To:         &testAddr,
			Value:      big.NewInt(1000),
			FeeTokenID: 42,
			FeeLimit:   feeLimit,
			Version:    MorphTxVersion0,
		})

		if !tx.IsMorphTx() {
			t.Error("IsMorphTx should return true")
		}
		if !tx.IsMorphTxWithAltFee() {
			t.Error("IsMorphTxWithAltFee should return true for V0 with FeeTokenID > 0")
		}
		if tx.Version() != MorphTxVersion0 {
			t.Errorf("Version mismatch, got %d, want %d", tx.Version(), MorphTxVersion0)
		}
		if tx.FeeTokenID() != 42 {
			t.Errorf("FeeTokenID mismatch, got %d, want 42", tx.FeeTokenID())
		}
		if tx.FeeLimit().Cmp(feeLimit) != 0 {
			t.Errorf("FeeLimit mismatch, got %v, want %v", tx.FeeLimit(), feeLimit)
		}
		if tx.Reference() != nil {
			t.Error("Reference should be nil for V0")
		}
		if tx.Memo() != nil {
			t.Error("Memo should be nil for V0")
		}
	})

	t.Run("MorphTx V1 accessors", func(t *testing.T) {
		tx := NewTx(&MorphTx{
			ChainID:    big.NewInt(1),
			Nonce:      20,
			GasTipCap:  big.NewInt(10),
			GasFeeCap:  big.NewInt(100),
			Gas:        40000,
			To:         &testAddr,
			Value:      big.NewInt(2000),
			FeeTokenID: 0,
			Version:    MorphTxVersion1,
			Reference:  &ref,
			Memo:       &memo,
		})

		if !tx.IsMorphTx() {
			t.Error("IsMorphTx should return true")
		}
		if tx.IsMorphTxWithAltFee() {
			t.Error("IsMorphTxWithAltFee should return false for FeeTokenID = 0")
		}
		if tx.Version() != MorphTxVersion1 {
			t.Errorf("Version mismatch, got %d, want %d", tx.Version(), MorphTxVersion1)
		}
		if tx.FeeTokenID() != 0 {
			t.Errorf("FeeTokenID mismatch, got %d, want 0", tx.FeeTokenID())
		}
		if tx.Reference() == nil || *tx.Reference() != ref {
			t.Error("Reference mismatch")
		}
		if tx.Memo() == nil || !bytes.Equal(*tx.Memo(), memo) {
			t.Error("Memo mismatch")
		}
	})

	t.Run("Non-MorphTx accessors", func(t *testing.T) {
		legacyTx := NewTx(&LegacyTx{
			Nonce:    1,
			GasPrice: big.NewInt(1),
			Gas:      21000,
			To:       &testAddr,
		})

		if legacyTx.IsMorphTx() {
			t.Error("IsMorphTx should return false for LegacyTx")
		}
		if legacyTx.IsMorphTxWithAltFee() {
			t.Error("IsMorphTxWithAltFee should return false for LegacyTx")
		}
		if legacyTx.Version() != 0 {
			t.Errorf("Version should return 0 for non-MorphTx, got %d", legacyTx.Version())
		}
		if legacyTx.FeeTokenID() != 0 {
			t.Errorf("FeeTokenID should return 0 for non-MorphTx, got %d", legacyTx.FeeTokenID())
		}
		if legacyTx.Reference() != nil {
			t.Error("Reference should return nil for non-MorphTx")
		}
		if legacyTx.Memo() != nil {
			t.Error("Memo should return nil for non-MorphTx")
		}
	})
}

// TestMorphTxVersionBackwardCompatibility tests that V0 and V1 transactions
// can be correctly encoded, decoded, and signed.
func TestMorphTxVersionBackwardCompatibility(t *testing.T) {
	key, _ := crypto.GenerateKey()
	signer := NewEmeraldSigner(big.NewInt(1))

	// Create V0 tx
	v0Tx := NewTx(&MorphTx{
		ChainID:    big.NewInt(1),
		Nonce:      1,
		GasTipCap:  big.NewInt(1),
		GasFeeCap:  big.NewInt(10),
		Gas:        21000,
		To:         &testAddr,
		Value:      big.NewInt(100),
		FeeTokenID: 1,
		FeeLimit:   big.NewInt(1000),
		Version:    MorphTxVersion0,
	})

	// Create V1 tx with same basic params but different version/fields
	ref := common.HexToReference("0x3333333333333333333333333333333333333333333333333333333333333333")
	memo := []byte("backward compat test")
	v1Tx := NewTx(&MorphTx{
		ChainID:    big.NewInt(1),
		Nonce:      1,
		GasTipCap:  big.NewInt(1),
		GasFeeCap:  big.NewInt(10),
		Gas:        21000,
		To:         &testAddr,
		Value:      big.NewInt(100),
		FeeTokenID: 1,
		FeeLimit:   big.NewInt(1000),
		Version:    MorphTxVersion1,
		Reference:  &ref,
		Memo:       &memo,
	})

	// Sign both transactions
	signedV0, err := SignTx(v0Tx, signer, key)
	if err != nil {
		t.Fatalf("Failed to sign V0 tx: %v", err)
	}
	signedV1, err := SignTx(v1Tx, signer, key)
	if err != nil {
		t.Fatalf("Failed to sign V1 tx: %v", err)
	}

	// Hashes should be different (different sigHash due to version)
	if signedV0.Hash() == signedV1.Hash() {
		t.Error("V0 and V1 transaction hashes should be different")
	}

	// Both should be recoverable
	sender0, err := Sender(signer, signedV0)
	if err != nil {
		t.Fatalf("Failed to recover V0 sender: %v", err)
	}
	sender1, err := Sender(signer, signedV1)
	if err != nil {
		t.Fatalf("Failed to recover V1 sender: %v", err)
	}
	if sender0 != sender1 {
		t.Error("Senders should be the same")
	}

	// Both should round-trip through RLP
	decoded0, err := encodeDecodeBinary(signedV0)
	if err != nil {
		t.Fatalf("V0 RLP round-trip failed: %v", err)
	}
	if decoded0.Version() != MorphTxVersion0 {
		t.Errorf("V0 version not preserved, got %d", decoded0.Version())
	}

	decoded1, err := encodeDecodeBinary(signedV1)
	if err != nil {
		t.Fatalf("V1 RLP round-trip failed: %v", err)
	}
	if decoded1.Version() != MorphTxVersion1 {
		t.Errorf("V1 version not preserved, got %d", decoded1.Version())
	}
	if decoded1.Reference() == nil || *decoded1.Reference() != ref {
		t.Error("V1 Reference not preserved")
	}
	if decoded1.Memo() == nil || !bytes.Equal(*decoded1.Memo(), memo) {
		t.Error("V1 Memo not preserved")
	}
}

// TestMorphTxReferenceEdgeCases tests edge cases for Reference field.
// Note: V1 MorphTx requires both Reference and Memo fields for RLP encoding.
func TestMorphTxReferenceEdgeCases(t *testing.T) {
	key, _ := crypto.GenerateKey()
	signer := NewEmeraldSigner(big.NewInt(1))

	zeroRef := common.Reference{}
	fullRef := common.HexToReference("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	partialRef := common.HexToReference("0x1234567800000000000000000000000000000000000000000000000000000000")
	emptyMemo := []byte{}

	tests := []struct {
		name string
		ref  *common.Reference
	}{
		{"zero reference", &zeroRef},
		{"full reference", &fullRef},
		{"partial reference", &partialRef},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := NewTx(&MorphTx{
				ChainID:   big.NewInt(1),
				Nonce:     1,
				GasTipCap: big.NewInt(1),
				GasFeeCap: big.NewInt(10),
				Gas:       21000,
				To:        &testAddr,
				Version:   MorphTxVersion1,
				Reference: tc.ref,
				Memo:      &emptyMemo, // V1 requires Memo field for RLP encoding
			})

			signedTx, err := SignTx(tx, signer, key)
			if err != nil {
				t.Fatalf("SignTx failed: %v", err)
			}

			decoded, err := encodeDecodeBinary(signedTx)
			if err != nil {
				t.Fatalf("RLP round-trip failed: %v", err)
			}

			if !reflect.DeepEqual(decoded.Reference(), tc.ref) {
				t.Errorf("Reference mismatch after round-trip, got %v, want %v", decoded.Reference(), tc.ref)
			}
		})
	}
}

// TestMorphTxMemoEdgeCases tests edge cases for Memo field.
// Note: V1 MorphTx requires both Reference and Memo fields for RLP encoding.
func TestMorphTxMemoEdgeCases(t *testing.T) {
	key, _ := crypto.GenerateKey()
	signer := NewEmeraldSigner(big.NewInt(1))

	zeroRef := common.Reference{}

	tests := []struct {
		name string
		memo *[]byte
	}{
		{"empty memo", func() *[]byte { m := []byte{}; return &m }()},
		{"single byte", func() *[]byte { m := []byte{0x42}; return &m }()},
		{"max length", func() *[]byte { m := make([]byte, common.MaxMemoLength); return &m }()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := NewTx(&MorphTx{
				ChainID:   big.NewInt(1),
				Nonce:     1,
				GasTipCap: big.NewInt(1),
				GasFeeCap: big.NewInt(10),
				Gas:       21000,
				To:        &testAddr,
				Version:   MorphTxVersion1,
				Reference: &zeroRef, // V1 requires Reference field for RLP encoding
				Memo:      tc.memo,
			})

			signedTx, err := SignTx(tx, signer, key)
			if err != nil {
				t.Fatalf("SignTx failed: %v", err)
			}

			decoded, err := encodeDecodeBinary(signedTx)
			if err != nil {
				t.Fatalf("RLP round-trip failed: %v", err)
			}

			if !compareMemoPtr(decoded.Memo(), tc.memo) {
				t.Errorf("Memo mismatch after round-trip, got %v, want %v", decoded.Memo(), tc.memo)
			}
		})
	}
}

// Helper function to compare memo pointers
func compareMemoPtr(a, b *[]byte) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return bytes.Equal(*a, *b)
}
