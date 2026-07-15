// Copyright 2021 The go-ethereum Authors
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

package types_test

import (
	"bytes"
	"fmt"
	"io"
	"math/big"
	mrand "math/rand"
	"testing"

	"github.com/holiman/uint256"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/rlp"
	"github.com/morph-l2/go-ethereum/trie"
)

func TestDeriveSha(t *testing.T) {
	txs, err := genTxs(0)
	if err != nil {
		t.Fatal(err)
	}
	for len(txs) < 1000 {
		exp := types.DeriveSha(txs, trie.NewListHasher())
		got := types.DeriveSha(txs, trie.NewStackTrie(nil))
		if !bytes.Equal(got[:], exp[:]) {
			t.Fatalf("%d txs: got %x exp %x", len(txs), got, exp)
		}
		newTxs, err := genTxs(uint64(len(txs) + 1))
		if err != nil {
			t.Fatal(err)
		}
		txs = append(txs, newTxs...)
	}
}

func TestDeriveShaStackTrieMatchesListHasher(t *testing.T) {
	txs, err := genTxs(16)
	if err != nil {
		t.Fatal(err)
	}
	assertDeriveShaMatches(t, txs)
	assertDeriveShaMatches(t, typedDeriveShaTransactions())
	assertDeriveShaMatches(t, typedDeriveShaReceipts())
}

func TestDeriveShaBufferReuseDoesNotAlias(t *testing.T) {
	list := flatList{
		"0x010203040506070809",
		"0x101112131415161718191a1b1c1d1e1f",
		"0x202122232425262728292a2b2c2d2e2f30313233",
	}
	assertDeriveShaMatches(t, list)
}

func TestBlobTxDeriveSha(t *testing.T) {
	assertDeriveShaMatches(t, types.Transactions{typedDeriveShaTransactions()[2]})
	assertDeriveShaMatches(t, types.Receipts{typedReceipt(types.BlobTxType)})
}

func TestSetCodeTxDeriveSha(t *testing.T) {
	assertDeriveShaMatches(t, types.Transactions{typedDeriveShaTransactions()[3]})
	assertDeriveShaMatches(t, types.Receipts{typedReceipt(types.SetCodeTxType)})
}

func TestMorphTxDeriveSha(t *testing.T) {
	assertDeriveShaMatches(t, types.Transactions{typedDeriveShaTransactions()[5]})
	assertDeriveShaMatches(t, types.Receipts{typedReceipt(types.MorphTxType)})
}

func TestL1MessageReceiptDeriveSha(t *testing.T) {
	assertDeriveShaMatches(t, types.Transactions{typedDeriveShaTransactions()[4]})
	assertDeriveShaMatches(t, types.Receipts{typedReceipt(types.L1MessageTxType)})
}

// TestEIP2718DeriveSha tests that the input to the DeriveSha function is correct.
func TestEIP2718DeriveSha(t *testing.T) {
	for _, tc := range []struct {
		rlpData string
		exp     string
	}{
		{
			rlpData: "0xb8a701f8a486796f6c6f763380843b9aca008262d4948a8eafb1cf62bfbeb1741769dae1a9dd479961928080f838f7940000000000000000000000000000000000001337e1a0000000000000000000000000000000000000000000000000000000000000000080a0775101f92dcca278a56bfe4d613428624a1ebfc3cd9e0bcc1de80c41455b9021a06c9deac205afe7b124907d4ba54a9f46161498bd3990b90d175aac12c9a40ee9",
			exp:     "01 01f8a486796f6c6f763380843b9aca008262d4948a8eafb1cf62bfbeb1741769dae1a9dd479961928080f838f7940000000000000000000000000000000000001337e1a0000000000000000000000000000000000000000000000000000000000000000080a0775101f92dcca278a56bfe4d613428624a1ebfc3cd9e0bcc1de80c41455b9021a06c9deac205afe7b124907d4ba54a9f46161498bd3990b90d175aac12c9a40ee9\n80 01f8a486796f6c6f763380843b9aca008262d4948a8eafb1cf62bfbeb1741769dae1a9dd479961928080f838f7940000000000000000000000000000000000001337e1a0000000000000000000000000000000000000000000000000000000000000000080a0775101f92dcca278a56bfe4d613428624a1ebfc3cd9e0bcc1de80c41455b9021a06c9deac205afe7b124907d4ba54a9f46161498bd3990b90d175aac12c9a40ee9\n",
		},
	} {
		d := &hashToHumanReadable{}
		var t1, t2 types.Transaction
		rlp.DecodeBytes(common.FromHex(tc.rlpData), &t1)
		rlp.DecodeBytes(common.FromHex(tc.rlpData), &t2)
		txs := types.Transactions{&t1, &t2}
		types.DeriveSha(txs, d)
		if tc.exp != string(d.data) {
			t.Fatalf("Want\n%v\nhave:\n%v", tc.exp, string(d.data))
		}
	}
}

func BenchmarkDeriveSha200(b *testing.B) {
	txs, err := genTxs(200)
	if err != nil {
		b.Fatal(err)
	}
	var exp common.Hash
	var got common.Hash
	b.Run("std_trie", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			exp = types.DeriveSha(txs, trie.NewListHasher())
		}
	})

	b.Run("stack_trie", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			got = types.DeriveSha(txs, trie.NewStackTrie(nil))
		}
	})
	if got != exp {
		b.Errorf("got %x exp %x", got, exp)
	}
}

func TestFuzzDeriveSha(t *testing.T) {
	// increase this for longer runs -- it's set to quite low for travis
	rndSeed := mrand.Int()
	for i := 0; i < 10; i++ {
		seed := rndSeed + i
		exp := types.DeriveSha(newDummy(i), trie.NewListHasher())
		got := types.DeriveSha(newDummy(i), trie.NewStackTrie(nil))
		if !bytes.Equal(got[:], exp[:]) {
			printList(newDummy(seed))
			t.Fatalf("seed %d: got %x exp %x", seed, got, exp)
		}
	}
}

// TestDerivableList contains testcases found via fuzzing
func TestDerivableList(t *testing.T) {
	type tcase []string
	tcs := []tcase{
		{
			"0xc041",
		},
		{
			"0xf04cf757812428b0763112efb33b6f4fad7deb445e",
			"0xf04cf757812428b0763112efb33b6f4fad7deb445e",
		},
		{
			"0xca410605310cdc3bb8d4977ae4f0143df54a724ed873457e2272f39d66e0460e971d9d",
			"0x6cd850eca0a7ac46bb1748d7b9cb88aa3bd21c57d852c28198ad8fa422c4595032e88a4494b4778b36b944fe47a52b8c5cd312910139dfcb4147ab8e972cc456bcb063f25dd78f54c4d34679e03142c42c662af52947d45bdb6e555751334ace76a5080ab5a0256a1d259855dfc5c0b8023b25befbb13fd3684f9f755cbd3d63544c78ee2001452dd54633a7593ade0b183891a0a4e9c7844e1254005fbe592b1b89149a502c24b6e1dca44c158aebedf01beae9c30cabe16a",
			"0x14abd5c47c0be87b0454596baad2",
			"0xca410605310cdc3bb8d4977ae4f0143df54a724ed873457e2272f39d66e0460e971d9d",
		},
	}
	for i, tc := range tcs[1:] {
		exp := types.DeriveSha(flatList(tc), trie.NewListHasher())
		got := types.DeriveSha(flatList(tc), trie.NewStackTrie(nil))
		if !bytes.Equal(got[:], exp[:]) {
			t.Fatalf("case %d: got %x exp %x", i, got, exp)
		}
	}
}

func genTxs(num uint64) (types.Transactions, error) {
	key, err := crypto.HexToECDSA("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		return nil, err
	}
	var addr = crypto.PubkeyToAddress(key.PublicKey)
	newTx := func(i uint64) (*types.Transaction, error) {
		signer := types.NewEIP155Signer(big.NewInt(18))
		utx := types.NewTransaction(i, addr, new(big.Int), 0, new(big.Int).SetUint64(10000000), nil)
		tx, err := types.SignTx(utx, signer, key)
		return tx, err
	}
	var txs types.Transactions
	for i := uint64(0); i < num; i++ {
		tx, err := newTx(i)
		if err != nil {
			return nil, err
		}
		txs = append(txs, tx)
	}
	return txs, nil
}

func assertDeriveShaMatches(t *testing.T, list types.DerivableList) {
	t.Helper()
	exp := types.DeriveSha(list, trie.NewListHasher())
	got := types.DeriveSha(list, trie.NewStackTrie(nil))
	if got != exp {
		t.Fatalf("%T root mismatch: got %x exp %x", list, got, exp)
	}
}

func typedDeriveShaTransactions() types.Transactions {
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	return types.Transactions{
		types.NewTx(&types.AccessListTx{
			ChainID:  big.NewInt(1),
			Nonce:    1,
			GasPrice: big.NewInt(1),
			Gas:      21000,
			To:       &to,
			Value:    big.NewInt(1),
			Data:     []byte{0x01},
			V:        big.NewInt(0),
			R:        big.NewInt(1),
			S:        big.NewInt(1),
		}),
		types.NewTx(&types.DynamicFeeTx{
			ChainID:   big.NewInt(1),
			Nonce:     2,
			GasTipCap: big.NewInt(1),
			GasFeeCap: big.NewInt(2),
			Gas:       21000,
			To:        &to,
			Value:     big.NewInt(2),
			Data:      []byte{0x02},
			V:         big.NewInt(0),
			R:         big.NewInt(1),
			S:         big.NewInt(1),
		}),
		types.NewTx(&types.BlobTx{
			ChainID:    uint256.NewInt(1),
			Nonce:      3,
			GasTipCap:  uint256.NewInt(1),
			GasFeeCap:  uint256.NewInt(2),
			Gas:        21000,
			To:         to,
			Value:      uint256.NewInt(3),
			Data:       []byte{0x03},
			BlobFeeCap: uint256.NewInt(1),
			BlobHashes: []common.Hash{common.HexToHash("0x01")},
			V:          uint256.NewInt(0),
			R:          uint256.NewInt(1),
			S:          uint256.NewInt(1),
		}),
		types.NewTx(&types.SetCodeTx{
			ChainID:   uint256.NewInt(1),
			Nonce:     4,
			GasTipCap: uint256.NewInt(1),
			GasFeeCap: uint256.NewInt(2),
			Gas:       21000,
			To:        to,
			Value:     uint256.NewInt(4),
			Data:      []byte{0x04},
			V:         uint256.NewInt(0),
			R:         uint256.NewInt(1),
			S:         uint256.NewInt(1),
		}),
		types.NewTx(&types.L1MessageTx{
			QueueIndex: 5,
			Gas:        21000,
			To:         &to,
			Value:      big.NewInt(5),
			Data:       []byte{0x05},
			Sender:     common.HexToAddress("0x0000000000000000000000000000000000000002"),
		}),
		types.NewTx(&types.MorphTx{
			ChainID:   big.NewInt(1),
			Nonce:     6,
			GasTipCap: big.NewInt(1),
			GasFeeCap: big.NewInt(2),
			Gas:       21000,
			To:        &to,
			Value:     big.NewInt(6),
			Data:      []byte{0x06},
			FeeLimit:  big.NewInt(1000),
			V:         big.NewInt(0),
			R:         big.NewInt(1),
			S:         big.NewInt(1),
		}),
	}
}

func typedDeriveShaReceipts() types.Receipts {
	return types.Receipts{
		typedReceipt(types.AccessListTxType),
		typedReceipt(types.DynamicFeeTxType),
		typedReceipt(types.BlobTxType),
		typedReceipt(types.SetCodeTxType),
		typedReceipt(types.L1MessageTxType),
		typedReceipt(types.MorphTxType),
	}
}

func typedReceipt(txType uint8) *types.Receipt {
	return &types.Receipt{
		Type:              txType,
		Status:            types.ReceiptStatusSuccessful,
		CumulativeGasUsed: uint64(100 + txType),
		Logs: []*types.Log{
			{
				Address: common.BytesToAddress([]byte{txType}),
				Topics:  []common.Hash{common.BigToHash(big.NewInt(int64(txType)))},
				Data:    []byte{txType, 0x01, 0x02},
			},
		},
	}
}

type dummyDerivableList struct {
	len  int
	seed int
}

func newDummy(seed int) *dummyDerivableList {
	d := &dummyDerivableList{}
	src := mrand.NewSource(int64(seed))
	// don't use lists longer than 4K items
	d.len = int(src.Int63() & 0x0FFF)
	d.seed = seed
	return d
}

func (d *dummyDerivableList) Len() int {
	return d.len
}

func (d *dummyDerivableList) EncodeIndex(i int, w *bytes.Buffer) {
	src := mrand.NewSource(int64(d.seed + i))
	// max item size 256, at least 1 byte per item
	size := 1 + src.Int63()&0x00FF
	io.CopyN(w, mrand.New(src), size)
}

func printList(l types.DerivableList) {
	fmt.Printf("list length: %d\n", l.Len())
	fmt.Printf("{\n")
	for i := 0; i < l.Len(); i++ {
		var buf bytes.Buffer
		l.EncodeIndex(i, &buf)
		fmt.Printf("\"0x%x\",\n", buf.Bytes())
	}
	fmt.Printf("},\n")
}

type flatList []string

func (f flatList) Len() int {
	return len(f)
}
func (f flatList) EncodeIndex(i int, w *bytes.Buffer) {
	w.Write(hexutil.MustDecode(f[i]))
}

type hashToHumanReadable struct {
	data []byte
}

func (d *hashToHumanReadable) Reset() {
	d.data = make([]byte, 0)
}

func (d *hashToHumanReadable) Update(i []byte, i2 []byte) error {
	l := fmt.Sprintf("%x %x\n", i, i2)
	d.data = append(d.data, []byte(l)...)
	return nil
}

func (d *hashToHumanReadable) Hash() common.Hash {
	return common.Hash{}
}
