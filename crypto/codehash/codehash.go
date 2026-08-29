package codehash

import (
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/crypto"
)

var EmptyKeccakCodeHash common.Hash

func KeccakCodeHash(code []byte) (h common.Hash) {
	return crypto.Keccak256Hash(code)
}

func init() {
	EmptyKeccakCodeHash = crypto.Keccak256Hash(nil)
}
