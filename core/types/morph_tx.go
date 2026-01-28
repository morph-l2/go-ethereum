package types

import (
	"bytes"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/rlp"
)

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

type MorphTx struct {
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int
	GasFeeCap  *big.Int
	Gas        uint64
	To         *common.Address `rlp:"nil"` // nil means contract creation
	Value      *big.Int
	Data       []byte
	AccessList AccessList

	Version    uint8             // version of morph tx
	FeeTokenID uint16            // ERC20 token ID for fee payment (0 = ETH)
	FeeLimit   *big.Int          // maximum fee in token units (optional)
	Reference  *common.Reference // reference key for the transaction (optional)
	Memo       *[]byte           // memo for the transaction (optional)

	// Signature values
	V *big.Int `json:"v" gencodec:"required"`
	R *big.Int `json:"r" gencodec:"required"`
	S *big.Int `json:"s" gencodec:"required"`
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *MorphTx) copy() TxData {
	cpy := &MorphTx{
		Nonce:      tx.Nonce,
		Gas:        tx.Gas,
		To:         copyAddressPtr(tx.To),
		Data:       common.CopyBytes(tx.Data),
		Reference:  copyReferencePtr(tx.Reference),
		Memo:       tx.Memo,
		Version:    tx.Version,
		FeeTokenID: tx.FeeTokenID,
		// These are copied below.
		AccessList: make(AccessList, len(tx.AccessList)),
		FeeLimit:   new(big.Int),
		Value:      new(big.Int),
		ChainID:    new(big.Int),
		GasTipCap:  new(big.Int),
		GasFeeCap:  new(big.Int),
		V:          new(big.Int),
		R:          new(big.Int),
		S:          new(big.Int),
	}
	copy(cpy.AccessList, tx.AccessList)
	if tx.Value != nil {
		cpy.Value.Set(tx.Value)
	}
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
	}
	if tx.GasTipCap != nil {
		cpy.GasTipCap.Set(tx.GasTipCap)
	}
	if tx.GasFeeCap != nil {
		cpy.GasFeeCap.Set(tx.GasFeeCap)
	}
	if tx.FeeLimit != nil {
		cpy.FeeLimit.Set(tx.FeeLimit)
	}
	if tx.V != nil {
		cpy.V.Set(tx.V)
	}
	if tx.R != nil {
		cpy.R.Set(tx.R)
	}
	if tx.S != nil {
		cpy.S.Set(tx.S)
	}
	return cpy
}

// accessors for innerTx.
func (tx *MorphTx) txType() byte           { return MorphTxType }
func (tx *MorphTx) chainID() *big.Int      { return tx.ChainID }
func (tx *MorphTx) accessList() AccessList { return tx.AccessList }
func (tx *MorphTx) data() []byte           { return tx.Data }
func (tx *MorphTx) gas() uint64            { return tx.Gas }
func (tx *MorphTx) gasFeeCap() *big.Int    { return tx.GasFeeCap }
func (tx *MorphTx) gasTipCap() *big.Int    { return tx.GasTipCap }
func (tx *MorphTx) gasPrice() *big.Int     { return tx.GasFeeCap }
func (tx *MorphTx) value() *big.Int        { return tx.Value }
func (tx *MorphTx) nonce() uint64          { return tx.Nonce }
func (tx *MorphTx) to() *common.Address    { return tx.To }

func (tx *MorphTx) effectiveGasPrice(dst *big.Int, baseFee *big.Int) *big.Int {
	if baseFee == nil {
		return dst.Set(tx.GasFeeCap)
	}
	tip := dst.Sub(tx.GasFeeCap, baseFee)
	if tip.Cmp(tx.GasTipCap) > 0 {
		tip.Set(tx.GasTipCap)
	}
	return tip.Add(tip, baseFee)
}

func (tx *MorphTx) rawSignatureValues() (v, r, s *big.Int) {
	return tx.V, tx.R, tx.S
}

func (tx *MorphTx) setSignatureValues(chainID, v, r, s *big.Int) {
	tx.ChainID, tx.V, tx.R, tx.S = chainID, v, r, s
}

func (tx *MorphTx) encode(b *bytes.Buffer) error {
	return rlp.Encode(b, tx)
}

func (tx *MorphTx) decode(input []byte) error {
	return rlp.DecodeBytes(input, tx)
}

func (tx *MorphTx) sigHash(chainID *big.Int) common.Hash {
	return prefixedRlpHash(
		MorphTxType,
		[]any{
			chainID,
			tx.Nonce,
			tx.GasTipCap,
			tx.GasFeeCap,
			tx.Gas,
			tx.To,
			tx.Value,
			tx.Data,
			tx.AccessList,
			tx.Version,
			tx.FeeTokenID,
			tx.FeeLimit,
			tx.Reference,
			tx.Memo,
		})
}
