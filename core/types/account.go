package types

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/common/math"
)

//go:generate go run github.com/fjl/gencodec -type Account -field-override accountMarshaling -out gen_account.go

// Account represents an Ethereum account and its attached data.
// This type is used to specify accounts in the genesis block state, and
// is also useful for JSON encoding/decoding of accounts.
type Account struct {
	Code    []byte                      `json:"code,omitempty"`
	Storage map[common.Hash]common.Hash `json:"storage,omitempty"`
	Balance *big.Int                    `json:"balance" gencodec:"required"`
	Nonce   uint64                      `json:"nonce,omitempty"`
}

type accountMarshaling struct {
	Code    hexutil.Bytes
	Balance *math.HexOrDecimal256
	Nonce   math.HexOrDecimal64
	Storage map[storageJSON]storageJSON
}

// storageJSON represents a 256 bit byte array, but allows less than 256 bits when
// unmarshalling from hex.
type storageJSON common.Hash

func (h *storageJSON) UnmarshalText(text []byte) error {
	text = bytes.TrimPrefix(text, []byte("0x"))
	if len(text) > 64 {
		return fmt.Errorf("too many hex characters in storage key/value %q", text)
	}
	offset := len(h) - len(text)/2 // pad on the left
	if _, err := hex.Decode(h[offset:], text); err != nil {
		return fmt.Errorf("invalid hex storage key/value %q", text)
	}
	return nil
}

func (h storageJSON) MarshalText() ([]byte, error) {
	return hexutil.Bytes(h[:]).MarshalText()
}

// GenesisAlloc specifies the initial state of a genesis block.
type GenesisAlloc map[common.Address]Account

func (ga *GenesisAlloc) UnmarshalJSON(data []byte) error {
	m := make(map[common.UnprefixedAddress]Account)
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	*ga = make(GenesisAlloc)
	for addr, a := range m {
		(*ga)[common.Address(addr)] = a
	}
	return nil
}
