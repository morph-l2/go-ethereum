// Copyright 2023 The go-ethereum Authors
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

// Package kzg4844 implements the KZG crypto for EIP-4844.
package kzg4844

import (
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"reflect"
	"sync/atomic"

	"github.com/morph-l2/go-ethereum/common/hexutil"
)

//go:embed trusted_setup.json
var content embed.FS

var (
	blobT       = reflect.TypeFor[Blob]()
	commitmentT = reflect.TypeFor[Commitment]()
	proofT      = reflect.TypeFor[Proof]()
)

const CellProofsPerBlob = 128

// Blob represents a 4844 data blob.
type Blob [131072]byte

// UnmarshalJSON parses a blob in hex syntax.
func (b *Blob) UnmarshalJSON(input []byte) error {
	return hexutil.UnmarshalFixedJSON(blobT, input, b[:])
}

// MarshalText returns the hex representation of b.
func (b *Blob) MarshalText() ([]byte, error) {
	return hexutil.Bytes(b[:]).MarshalText()
}

// Commitment is a serialized commitment to a polynomial.
type Commitment [48]byte

// UnmarshalJSON parses a commitment in hex syntax.
func (c *Commitment) UnmarshalJSON(input []byte) error {
	return hexutil.UnmarshalFixedJSON(commitmentT, input, c[:])
}

// MarshalText returns the hex representation of c.
func (c Commitment) MarshalText() ([]byte, error) {
	return hexutil.Bytes(c[:]).MarshalText()
}

// Proof is a serialized commitment to the quotient polynomial.
type Proof [48]byte

// UnmarshalJSON parses a proof in hex syntax.
func (p *Proof) UnmarshalJSON(input []byte) error {
	return hexutil.UnmarshalFixedJSON(proofT, input, p[:])
}

// MarshalText returns the hex representation of p.
func (p Proof) MarshalText() ([]byte, error) {
	return hexutil.Bytes(p[:]).MarshalText()
}

// Point is a BLS field element.
type Point [32]byte

// Claim is a claimed evaluation value in a specific point.
type Claim [32]byte

// useCKZG controls whether the cryptography should use the Go or C backend.
var useCKZG atomic.Bool

// UseCKZG can be called to switch the default Go implementation of KZG to the C
// library if fo some reason the user wishes to do so (e.g. consensus bug in one
// or the other).
func UseCKZG(use bool) error {
	if use && !ckzgAvailable {
		return errors.New("CKZG unavailable on your platform")
	}
	useCKZG.Store(use)

	// Initializing the library can take 2-4 seconds - and can potentially crash
	// on CKZG and non-ADX CPUs - so might as well do it now and don't wait until
	// a crypto operation is actually needed live.
	if use {
		ckzgIniter.Do(ckzgInit)
	} else {
		gokzgIniter.Do(gokzgInit)
	}
	return nil
}

// BlobToCommitment creates a small commitment out of a data blob.
func BlobToCommitment(blob *Blob) (Commitment, error) {
	if useCKZG.Load() {
		return ckzgBlobToCommitment(blob)
	}
	return gokzgBlobToCommitment(blob)
}

// ComputeProof computes the KZG proof at the given point for the polynomial
// represented by the blob.
func ComputeProof(blob *Blob, point Point) (Proof, Claim, error) {
	if useCKZG.Load() {
		return ckzgComputeProof(blob, point)
	}
	return gokzgComputeProof(blob, point)
}

// VerifyProof verifies the KZG proof that the polynomial represented by the blob
// evaluated at the given point is the claimed value.
func VerifyProof(commitment Commitment, point Point, claim Claim, proof Proof) error {
	if useCKZG.Load() {
		return ckzgVerifyProof(commitment, point, claim, proof)
	}
	return gokzgVerifyProof(commitment, point, claim, proof)
}

// ComputeBlobProof returns the KZG proof that is used to verify the blob against
// the commitment.
//
// This method does not verify that the commitment is correct with respect to blob.
func ComputeBlobProof(blob *Blob, commitment Commitment) (Proof, error) {
	if useCKZG.Load() {
		return ckzgComputeBlobProof(blob, commitment)
	}
	return gokzgComputeBlobProof(blob, commitment)
}

// VerifyBlobProof verifies that the blob data corresponds to the provided commitment.
func VerifyBlobProof(blob *Blob, commitment Commitment, proof Proof) error {
	if useCKZG.Load() {
		return ckzgVerifyBlobProof(blob, commitment, proof)
	}
	return gokzgVerifyBlobProof(blob, commitment, proof)
}

// VerifyCellProofs verifies a batch of proofs corresponding to the blobs and commitments.
// Expects length of blobs and commitments to be equal.
// Expects length of proofs be 128 * length of blobs.
func VerifyCellProofs(blobs []Blob, commitments []Commitment, proofs []Proof) error {
	if useCKZG.Load() {
		return ckzgVerifyCellProofBatch(blobs, commitments, proofs)
	}
	return gokzgVerifyCellProofBatch(blobs, commitments, proofs)
}

// ComputeCellProofs returns the KZG cell proofs that are used to verify the blob against
// the commitment.
//
// This method does not verify that the commitment is correct with respect to blob.
func ComputeCellProofs(blob *Blob) ([]Proof, error) {
	if useCKZG.Load() {
		return ckzgComputeCellProofs(blob)
	}
	return gokzgComputeCellProofs(blob)
}

// CalcBlobHashV1 calculates the 'versioned blob hash' of a commitment.
// The given hasher must be a sha256 hash instance, otherwise the result will be invalid!
func CalcBlobHashV1(hasher hash.Hash, commit *Commitment) (vh [32]byte) {
	if hasher.Size() != 32 {
		panic("wrong hash size")
	}
	hasher.Reset()
	hasher.Write(commit[:])
	hasher.Sum(vh[:0])
	vh[0] = 0x01 // version
	return vh
}

// IsValidVersionedHash checks that h is a structurally-valid versioned blob hash.
func IsValidVersionedHash(h []byte) bool {
	return len(h) == 32 && h[0] == 0x01
}

const MaxBlobDataSize = 4096*31 - 4

// FromData encodes the given input data into this blob. The encoding scheme is as follows:
//
// First, field elements are encoded as big-endian uint256 in BLS modulus range. To avoid modulus
// overflow, we can't use the full 32 bytes, so we write data only to the topmost 31 bytes of each.
// TODO: we can optimize this to get a bit more data from the blobs by using the top byte
// partially.
//
// The first field element encodes the length of input data as a little endian uint32 in its
// topmost 4 (out of 31) bytes, and the first 27 bytes of the input data in its remaining 27
// bytes.
//
// The remaining field elements each encode 31 bytes of the remaining input data, up until the end
// of the input.
func (b *Blob) FromData(data []byte) error {
	if len(data) > MaxBlobDataSize {
		return fmt.Errorf("data is too large for blob. len=%v", len(data))
	}
	b.Clear()
	// encode 4-byte little-endian length value into topmost 4 bytes (out of 31) of first field
	// element
	binary.LittleEndian.PutUint32(b[1:5], uint32(len(data)))
	// encode first 27 bytes of input data into remaining bytes of first field element
	offset := copy(b[5:32], data)
	// encode (up to) 31 bytes of remaining input data at a time into the subsequent field element
	for i := 1; i < 4096; i++ {
		offset += copy(b[i*32+1:i*32+32], data[offset:])
		if offset == len(data) {
			break
		}
	}
	if offset < len(data) {
		return fmt.Errorf("failed to fit all data into blob. bytes remaining: %v", len(data)-offset)
	}
	return nil
}

// ToData decodes the blob into raw byte data. See FromData above for details on the encoding
// format.
func (b *Blob) ToData() ([]byte, error) {
	data := make([]byte, 4096*32)
	for i := 0; i < 4096; i++ {
		if b[i*32] != 0 {
			return nil, fmt.Errorf("invalid blob, found non-zero high order byte %x of field element %d", b[i*32], i)
		}
		copy(data[i*31:i*31+31], b[i*32+1:i*32+32])
	}
	// extract the length prefix & trim the output accordingly
	dataLen := binary.LittleEndian.Uint32(data[:4])
	data = data[4:]
	if dataLen > uint32(len(data)) {
		return nil, fmt.Errorf("invalid blob, length prefix out of range: %d", dataLen)
	}
	data = data[:dataLen]
	return data, nil
}

func (b *Blob) Clear() {
	for i := 0; i < 131072; i++ {
		b[i] = 0
	}
}
