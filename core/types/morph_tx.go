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

// MorphTx version constants
const (
	// MorphTxVersion0 is the original format without Version, Reference, Memo fields
	MorphTxVersion0 = byte(0)
	// MorphTxVersion1 includes Version, Reference, Memo fields
	MorphTxVersion1 = byte(1)
)

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

	Version    uint8             // version of morph tx (0 = legacy, 1 = with reference/memo)
	FeeTokenID uint16            // ERC20 token ID for fee payment (0 = ETH)
	FeeLimit   *big.Int          // maximum fee in token units (optional)
	Reference  *common.Reference // reference key for the transaction (optional, v1 only)
	Memo       *[]byte           // memo for the transaction (optional, v1 only)

	// Signature values
	V *big.Int `json:"v" gencodec:"required"`
	R *big.Int `json:"r" gencodec:"required"`
	S *big.Int `json:"s" gencodec:"required"`
}

// morphTxV0RLP is the RLP encoding structure for MorphTx version 0 (legacy format)
type morphTxV0RLP struct {
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int
	GasFeeCap  *big.Int
	Gas        uint64
	To         *common.Address `rlp:"nil"`
	Value      *big.Int
	Data       []byte
	AccessList AccessList
	FeeTokenID uint16
	FeeLimit   *big.Int
	V          *big.Int
	R          *big.Int
	S          *big.Int
}

// morphTxV1RLP is the RLP encoding structure for MorphTx version 1 (with Reference/Memo)
type morphTxV1RLP struct {
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int
	GasFeeCap  *big.Int
	Gas        uint64
	To         *common.Address `rlp:"nil"`
	Value      *big.Int
	Data       []byte
	AccessList AccessList
	Version    uint8
	FeeTokenID uint16
	FeeLimit   *big.Int
	Reference  *common.Reference
	Memo       *[]byte
	V          *big.Int
	R          *big.Int
	S          *big.Int
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *MorphTx) copy() TxData {
	cpy := &MorphTx{
		Nonce:      tx.Nonce,
		Gas:        tx.Gas,
		To:         copyAddressPtr(tx.To),
		Data:       common.CopyBytes(tx.Data),
		Reference:  copyReferencePtr(tx.Reference),
		Memo:       copyBytesPtr(tx.Memo),
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
	if tx.Version == MorphTxVersion0 {
		// Encode as v0 format (legacy)
		return rlp.Encode(b, &morphTxV0RLP{
			ChainID:    tx.ChainID,
			Nonce:      tx.Nonce,
			GasTipCap:  tx.GasTipCap,
			GasFeeCap:  tx.GasFeeCap,
			Gas:        tx.Gas,
			To:         tx.To,
			Value:      tx.Value,
			Data:       tx.Data,
			AccessList: tx.AccessList,
			FeeTokenID: tx.FeeTokenID,
			FeeLimit:   tx.FeeLimit,
			V:          tx.V,
			R:          tx.R,
			S:          tx.S,
		})
	}
	// Encode as v1 format (with Version, Reference, Memo)
	return rlp.Encode(b, &morphTxV1RLP{
		ChainID:    tx.ChainID,
		Nonce:      tx.Nonce,
		GasTipCap:  tx.GasTipCap,
		GasFeeCap:  tx.GasFeeCap,
		Gas:        tx.Gas,
		To:         tx.To,
		Value:      tx.Value,
		Data:       tx.Data,
		AccessList: tx.AccessList,
		Version:    tx.Version,
		FeeTokenID: tx.FeeTokenID,
		FeeLimit:   tx.FeeLimit,
		Reference:  tx.Reference,
		Memo:       tx.Memo,
		V:          tx.V,
		R:          tx.R,
		S:          tx.S,
	})
}

func (tx *MorphTx) decode(input []byte) error {
	// Try to decode as v1 format first
	var v1 morphTxV1RLP
	if err := rlp.DecodeBytes(input, &v1); err == nil && v1.Version > 0 {
		tx.ChainID = v1.ChainID
		tx.Nonce = v1.Nonce
		tx.GasTipCap = v1.GasTipCap
		tx.GasFeeCap = v1.GasFeeCap
		tx.Gas = v1.Gas
		tx.To = v1.To
		tx.Value = v1.Value
		tx.Data = v1.Data
		tx.AccessList = v1.AccessList
		tx.Version = v1.Version
		tx.FeeTokenID = v1.FeeTokenID
		tx.FeeLimit = v1.FeeLimit
		tx.Reference = v1.Reference
		tx.Memo = v1.Memo
		tx.V = v1.V
		tx.R = v1.R
		tx.S = v1.S
		return nil
	}

	// Fall back to v0 format (legacy)
	var v0 morphTxV0RLP
	if err := rlp.DecodeBytes(input, &v0); err != nil {
		return err
	}
	tx.ChainID = v0.ChainID
	tx.Nonce = v0.Nonce
	tx.GasTipCap = v0.GasTipCap
	tx.GasFeeCap = v0.GasFeeCap
	tx.Gas = v0.Gas
	tx.To = v0.To
	tx.Value = v0.Value
	tx.Data = v0.Data
	tx.AccessList = v0.AccessList
	tx.Version = MorphTxVersion0
	tx.FeeTokenID = v0.FeeTokenID
	tx.FeeLimit = v0.FeeLimit
	tx.Reference = nil
	tx.Memo = nil
	tx.V = v0.V
	tx.R = v0.R
	tx.S = v0.S
	return nil
}

func (tx *MorphTx) sigHash(chainID *big.Int) common.Hash {
	switch tx.Version {
	case MorphTxVersion0:
		// v0 sigHash (legacy format without Version, Reference, Memo)
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
				tx.FeeTokenID,
				tx.FeeLimit,
			})
	case MorphTxVersion1:
		// v1 sigHash (with Version, Reference, Memo)
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
	default:
		// v1 sigHash (with Version, Reference, Memo)
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
}
