package codehash

import (
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/crypto/poseidon"
)

var EmptyPoseidonCodeHash common.Hash
var EmptyKeccakCodeHash common.Hash

func PoseidonCodeHash(code []byte) (h common.Hash) {
	return poseidon.CodeHash(code)
}

func KeccakCodeHash(code []byte) (h common.Hash) {
	return crypto.Keccak256Hash(code)
}

func init() {
	EmptyPoseidonCodeHash = poseidon.CodeHash(nil)
	EmptyKeccakCodeHash = crypto.Keccak256Hash(nil)
}
