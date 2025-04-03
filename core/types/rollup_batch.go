package types

import (
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/crypto/kzg4844"
	"github.com/morph-l2/go-ethereum/rlp"
)

//go:generate go run github.com/fjl/gencodec -type RollupBatch -field-override rollupBatchMarshaling -out gen_batch.go

type RollupBatch struct {
	Index                    uint64
	Hash                     common.Hash
	Version                  uint
	ParentBatchHeader        []byte
	BlockContexts            []byte      `rlp:"optional"` // legacy field
	SkippedL1MessageBitmap   []byte      `rlp:"optional"` // legacy field
	CurrentSequencerSetBytes []byte      `rlp:"optional"`
	PrevStateRoot            common.Hash `rlp:"optional"`
	PostStateRoot            common.Hash `rlp:"optional"`
	WithdrawRoot             common.Hash `rlp:"optional"`
	LastBlockNumber          uint64      `rlp:"optional"`
	NumL1Messages            uint16      `rlp:"optional"`

	Sidecar *BlobTxSidecar `rlp:"-"`
}

type rollupBatchMarshaling struct {
	Version                  hexutil.Uint
	Index                    hexutil.Uint64
	LastBlockNumber          hexutil.Uint64
	NumL1Messages            hexutil.Uint
	ParentBatchHeader        hexutil.Bytes
	BlockContexts            hexutil.Bytes
	SkippedL1MessageBitmap   hexutil.Bytes
	CurrentSequencerSetBytes hexutil.Bytes
}

// blobTxWithBlobs is used for encoding of transactions when blobs are present.
type rollupBatchWithBlobs struct {
	Batch       *RollupBatch
	Blobs       []kzg4844.Blob
	Commitments []kzg4844.Commitment
	Proofs      []kzg4844.Proof
}

func (r *RollupBatch) Encode() ([]byte, error) {
	if r.Sidecar == nil {
		return rlp.EncodeToBytes(r)
	}
	inner := &rollupBatchWithBlobs{
		Batch:       r,
		Blobs:       r.Sidecar.Blobs,
		Commitments: r.Sidecar.Commitments,
		Proofs:      r.Sidecar.Proofs,
	}
	return rlp.EncodeToBytes(inner)
}

func (r *RollupBatch) Decode(input []byte) error {
	outerList, _, err := rlp.SplitList(input)
	if err != nil {
		return err
	}
	firstElemKind, _, _, err := rlp.Split(outerList)
	if err != nil {
		return err
	}

	if firstElemKind != rlp.List {
		return rlp.DecodeBytes(input, r)
	}
	// It's a batch with blobs.
	var inner rollupBatchWithBlobs
	if err := rlp.DecodeBytes(input, &inner); err != nil {
		return err
	}
	*r = *inner.Batch
	r.Sidecar = &BlobTxSidecar{
		Blobs:       inner.Blobs,
		Commitments: inner.Commitments,
		Proofs:      inner.Proofs,
	}
	return nil
}

//go:generate go run github.com/fjl/gencodec -type BatchSignature -field-override batchSignatureMarshaling -out gen_batch_sig.go

type BatchSignature struct {
	Signer       common.Address `json:"signer"`
	SignerPubKey []byte         `json:"signerPubKey"`
	Signature    []byte         `json:"signature"`
}

type batchSignatureMarshaling struct {
	Signer       common.Address
	SignerPubKey hexutil.Bytes
	Signature    hexutil.Bytes
}
