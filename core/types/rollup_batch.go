package types

import (
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/common/hexutil"
)

//go:generate go run github.com/fjl/gencodec -type RollupBatch -field-override rollupBatchMarshaling -out gen_batch.go

type RollupBatch struct {
	Index                  uint64
	Hash                   common.Hash
	Version                uint
	ParentBatchHeader      []byte
	Chunks                 [][]byte
	SkippedL1MessageBitmap []byte
	PrevStateRoot          common.Hash
	PostStateRoot          common.Hash
	WithdrawRoot           common.Hash
}

type rollupBatchMarshaling struct {
	Version                hexutil.Uint
	Index                  hexutil.Uint64
	ParentBatchHeader      hexutil.Bytes
	Chunks                 []hexutil.Bytes
	SkippedL1MessageBitmap hexutil.Bytes
}

//go:generate go run github.com/fjl/gencodec -type BatchSignature -field-override batchSignatureMarshaling -out gen_batch_sig.go

type BatchSignature struct {
	Version      uint64 `json:"version"`
	Signer       uint64 `json:"signer"`
	SignerPubKey []byte `json:"signerPubKey"`
	Signature    []byte `json:"signature"`
}

type batchSignatureMarshaling struct {
	Version      hexutil.Uint64
	Signer       hexutil.Uint64
	SignerPubKey hexutil.Bytes
	Signature    hexutil.Bytes
}
