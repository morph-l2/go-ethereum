package types

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/rlp"
)

// ---------------------------------------------------------------------------
// Encoding / Decoding compatibility tests (from morph_tx_compat_test.go)
// ---------------------------------------------------------------------------

// TestMorphTxV0BackwardCompatibility tests that old AltFeeTx encoded data
// can be correctly decoded by the new MorphTx decoder.
// These hex values were generated from the original AltFeeTx implementation.
func TestMorphTxV0BackwardCompatibility(t *testing.T) {
	// Expected values from the original encoding
	expectedTo := common.HexToAddress("0x1234567890123456789012345678901234567890")
	expectedChainID := big.NewInt(2818)
	expectedNonce := uint64(1)
	expectedGasTipCap := big.NewInt(1000000000)
	expectedGasFeeCap := big.NewInt(2000000000)
	expectedGas := uint64(21000)
	expectedValue := big.NewInt(1000000000000000000) // 1 ETH
	expectedR, _ := new(big.Int).SetString("abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", 16)
	expectedS, _ := new(big.Int).SetString("1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", 16)

	testCases := []struct {
		name       string
		fullHex    string // Full hex including 0x7F prefix
		feeTokenID uint16
		feeLimit   *big.Int
	}{
		{
			// Case 1: FeeLimit has value (0.5 ETH = 500000000000000000)
			name:       "V0 with FeeLimit value",
			fullHex:    "7ff87e820b0201843b9aca008477359400825208941234567890123456789012345678901234567890880de0b6b3a764000080c0018806f05b59d3b2000001a0abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890a01234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			feeTokenID: 1,
			feeLimit:   big.NewInt(500000000000000000),
		},
		{
			// Case 2: FeeLimit is nil (encoded as 0x80)
			name:       "V0 with nil FeeLimit",
			fullHex:    "7ff876820b0201843b9aca008477359400825208941234567890123456789012345678901234567890880de0b6b3a764000080c0018001a0abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890a01234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			feeTokenID: 1,
			feeLimit:   nil,
		},
		{
			// Case 3: FeeLimit is 0 (also encoded as 0x80)
			name:       "V0 with zero FeeLimit",
			fullHex:    "7ff876820b0201843b9aca008477359400825208941234567890123456789012345678901234567890880de0b6b3a764000080c0018001a0abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890a01234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			feeTokenID: 1,
			feeLimit:   nil, // 0 is encoded as empty, decoded as nil
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := hex.DecodeString(tc.fullHex)
			if err != nil {
				t.Fatalf("failed to decode hex: %v", err)
			}

			// Verify first byte is MorphTxType (0x7F)
			if data[0] != MorphTxType {
				t.Fatalf("expected first byte 0x7F, got 0x%x", data[0])
			}

			// Skip txType byte, decode the rest
			innerData := data[1:]
			t.Logf("First inner byte: 0x%x (should be RLP list prefix >= 0xC0)", innerData[0])

			// Verify it's RLP list prefix (V0 format)
			if innerData[0] < 0xC0 {
				t.Errorf("V0 data should start with RLP list prefix, got 0x%x", innerData[0])
			}

			// Decode using MorphTx.decode
			var decoded MorphTx
			if err := decoded.decode(innerData); err != nil {
				t.Fatalf("failed to decode MorphTx: %v", err)
			}

			// Verify version is 0 (V0 format)
			if decoded.Version != MorphTxVersion0 {
				t.Errorf("expected Version 0, got %d", decoded.Version)
			}

			// Verify FeeTokenID
			if decoded.FeeTokenID != tc.feeTokenID {
				t.Errorf("expected FeeTokenID %d, got %d", tc.feeTokenID, decoded.FeeTokenID)
			}

			// Verify FeeLimit
			if tc.feeLimit == nil {
				if decoded.FeeLimit != nil && decoded.FeeLimit.Sign() != 0 {
					t.Errorf("expected nil/zero FeeLimit, got %v", decoded.FeeLimit)
				}
			} else {
				if decoded.FeeLimit == nil || decoded.FeeLimit.Cmp(tc.feeLimit) != 0 {
					t.Errorf("expected FeeLimit %v, got %v", tc.feeLimit, decoded.FeeLimit)
				}
			}

			// Verify other common fields
			if decoded.ChainID.Cmp(expectedChainID) != 0 {
				t.Errorf("ChainID mismatch: expected %v, got %v", expectedChainID, decoded.ChainID)
			}
			if decoded.Nonce != expectedNonce {
				t.Errorf("Nonce mismatch: expected %d, got %d", expectedNonce, decoded.Nonce)
			}
			if decoded.GasTipCap.Cmp(expectedGasTipCap) != 0 {
				t.Errorf("GasTipCap mismatch: expected %v, got %v", expectedGasTipCap, decoded.GasTipCap)
			}
			if decoded.GasFeeCap.Cmp(expectedGasFeeCap) != 0 {
				t.Errorf("GasFeeCap mismatch: expected %v, got %v", expectedGasFeeCap, decoded.GasFeeCap)
			}
			if decoded.Gas != expectedGas {
				t.Errorf("Gas mismatch: expected %d, got %d", expectedGas, decoded.Gas)
			}
			if decoded.To == nil || *decoded.To != expectedTo {
				t.Errorf("To mismatch: expected %v, got %v", expectedTo, decoded.To)
			}
			if decoded.Value.Cmp(expectedValue) != 0 {
				t.Errorf("Value mismatch: expected %v, got %v", expectedValue, decoded.Value)
			}
			if decoded.R.Cmp(expectedR) != 0 {
				t.Errorf("R mismatch: expected %v, got %v", expectedR, decoded.R)
			}
			if decoded.S.Cmp(expectedS) != 0 {
				t.Errorf("S mismatch: expected %v, got %v", expectedS, decoded.S)
			}

			t.Logf("Successfully decoded V0 MorphTx: ChainID=%v, Nonce=%d, FeeTokenID=%d, FeeLimit=%v, Version=%d",
				decoded.ChainID, decoded.Nonce, decoded.FeeTokenID, decoded.FeeLimit, decoded.Version)
		})
	}
}

// TestMorphTxV1Encoding tests the new V1 encoding format
// where version is a prefix byte before RLP data.
func TestMorphTxV1Encoding(t *testing.T) {
	reference := common.HexToReference("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	memo := []byte("test memo")
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")

	tx := &MorphTx{
		ChainID:    big.NewInt(2818),
		Nonce:      1,
		GasTipCap:  big.NewInt(1000000000),
		GasFeeCap:  big.NewInt(2000000000),
		Gas:        21000,
		To:         &to,
		Value:      big.NewInt(0),
		Data:       []byte{},
		AccessList: AccessList{},
		FeeTokenID: 0, // ETH
		FeeLimit:   nil,
		Version:    MorphTxVersion1,
		Reference:  &reference,
		Memo:       &memo,
		V:          big.NewInt(0),
		R:          big.NewInt(0),
		S:          big.NewInt(0),
	}

	// Encode
	encoded, err := encodeMorphTx(tx)
	if err != nil {
		t.Fatalf("failed to encode: %v", err)
	}

	t.Logf("V1 encoded hex: %s", hex.EncodeToString(encoded))
	t.Logf("First byte (type): 0x%x", encoded[0])
	t.Logf("Second byte (version): 0x%x", encoded[1])

	// Verify first byte is MorphTxType
	if encoded[0] != MorphTxType {
		t.Errorf("expected first byte 0x%x, got 0x%x", MorphTxType, encoded[0])
	}

	// Verify second byte is version
	if encoded[1] != MorphTxVersion1 {
		t.Errorf("expected second byte 0x%x (version 1), got 0x%x", MorphTxVersion1, encoded[1])
	}

	// Decode back
	var decoded MorphTx
	if err := decoded.decode(encoded[1:]); err != nil { // Skip txType byte
		t.Fatalf("failed to decode: %v", err)
	}

	// Verify fields
	if decoded.Version != MorphTxVersion1 {
		t.Errorf("expected Version 1, got %d", decoded.Version)
	}
	if decoded.Reference == nil || *decoded.Reference != reference {
		t.Errorf("Reference mismatch")
	}
	if decoded.Memo == nil || string(*decoded.Memo) != string(memo) {
		t.Errorf("Memo mismatch")
	}

	t.Logf("Successfully encoded and decoded V1 MorphTx")
}

// TestMorphTxV0V1RoundTrip tests encoding/decoding round trip for both versions
func TestMorphTxV0V1RoundTrip(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	reference := common.HexToReference("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	memo := []byte("hello")

	testCases := []struct {
		name string
		tx   *MorphTx
	}{
		{
			name: "V0 with FeeTokenID",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      1,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(1000000000000000000),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 1, // Non-zero required for V0
				FeeLimit:   big.NewInt(100000000000000000),
				Version:    MorphTxVersion0,
				V:          big.NewInt(1),
				R:          big.NewInt(123456),
				S:          big.NewInt(654321),
			},
		},
		{
			name: "V1 with Reference and Memo",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      2,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{0x01, 0x02, 0x03},
				AccessList: AccessList{},
				FeeTokenID: 0,
				FeeLimit:   nil,
				Version:    MorphTxVersion1,
				Reference:  &reference,
				Memo:       &memo,
				V:          big.NewInt(0),
				R:          big.NewInt(111),
				S:          big.NewInt(222),
			},
		},
		{
			name: "V1 with FeeTokenID and Reference",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      3,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        50000,
				To:         &to,
				Value:      big.NewInt(500000000000000000),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 2,
				FeeLimit:   big.NewInt(200000000000000000),
				Version:    MorphTxVersion1,
				Reference:  &reference,
				Memo:       nil,
				V:          big.NewInt(1),
				R:          big.NewInt(333),
				S:          big.NewInt(444),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode
			encoded, err := encodeMorphTx(tc.tx)
			if err != nil {
				t.Fatalf("failed to encode: %v", err)
			}

			t.Logf("Encoded hex: %s", hex.EncodeToString(encoded))
			t.Logf("Length: %d bytes", len(encoded))

			// Decode
			var decoded MorphTx
			if err := decoded.decode(encoded[1:]); err != nil { // Skip txType byte
				t.Fatalf("failed to decode: %v", err)
			}

			// Verify key fields
			if decoded.Version != tc.tx.Version {
				t.Errorf("Version mismatch: expected %d, got %d", tc.tx.Version, decoded.Version)
			}
			if decoded.FeeTokenID != tc.tx.FeeTokenID {
				t.Errorf("FeeTokenID mismatch: expected %d, got %d", tc.tx.FeeTokenID, decoded.FeeTokenID)
			}
			if decoded.Nonce != tc.tx.Nonce {
				t.Errorf("Nonce mismatch: expected %d, got %d", tc.tx.Nonce, decoded.Nonce)
			}
			if decoded.Gas != tc.tx.Gas {
				t.Errorf("Gas mismatch: expected %d, got %d", tc.tx.Gas, decoded.Gas)
			}

			t.Logf("Round-trip successful for %s", tc.name)
		})
	}
}

// TestMorphTxVersionDetection tests the version detection logic in decode
func TestMorphTxVersionDetection(t *testing.T) {
	// Create a V0 transaction (legacy format)
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	v0Tx := &MorphTx{
		ChainID:    big.NewInt(1),
		Nonce:      1,
		GasTipCap:  big.NewInt(1000000000),
		GasFeeCap:  big.NewInt(2000000000),
		Gas:        21000,
		To:         &to,
		Value:      big.NewInt(0),
		FeeTokenID: 1,
		FeeLimit:   big.NewInt(100),
		Version:    MorphTxVersion0,
		V:          big.NewInt(0),
		R:          big.NewInt(0),
		S:          big.NewInt(0),
	}

	encoded, err := encodeMorphTx(v0Tx)
	if err != nil {
		t.Fatalf("failed to encode V0: %v", err)
	}
	innerData := encoded[1:] // Skip txType

	// V0 should start with RLP list prefix (0xC0-0xFF)
	if innerData[0] < 0xC0 {
		t.Errorf("V0 encoded data should start with RLP list prefix, got 0x%x", innerData[0])
	}
	t.Logf("V0 first inner byte: 0x%x (RLP list prefix)", innerData[0])

	// Create a V1 transaction
	reference := common.HexToReference("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	v1Tx := &MorphTx{
		ChainID:    big.NewInt(1),
		Nonce:      1,
		GasTipCap:  big.NewInt(1000000000),
		GasFeeCap:  big.NewInt(2000000000),
		Gas:        21000,
		To:         &to,
		Value:      big.NewInt(0),
		FeeTokenID: 0,
		Version:    MorphTxVersion1,
		Reference:  &reference,
		V:          big.NewInt(0),
		R:          big.NewInt(0),
		S:          big.NewInt(0),
	}

	encoded, err = encodeMorphTx(v1Tx)
	if err != nil {
		t.Fatalf("failed to encode V1: %v", err)
	}
	innerData = encoded[1:] // Skip txType

	// V1 should start with version byte (0x01)
	if innerData[0] != MorphTxVersion1 {
		t.Errorf("V1 encoded data should start with version byte 0x01, got 0x%x", innerData[0])
	}
	t.Logf("V1 first inner byte: 0x%x (version prefix)", innerData[0])

	// Second byte should be RLP list prefix
	if innerData[1] < 0xC0 {
		t.Errorf("V1 second byte should be RLP list prefix, got 0x%x", innerData[1])
	}
	t.Logf("V1 second inner byte: 0x%x (RLP list prefix)", innerData[1])
}

// TestMorphTxEncodeRLPConsistency verifies that rlp.Encode(morphTx) produces
// the same output as the custom encode() method. This ensures Hash() (which
// uses rlp.Encode internally) is consistent with the wire format.
func TestMorphTxEncodeRLPConsistency(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	reference := common.HexToReference("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	memo := []byte("hello")

	testCases := []struct {
		name string
		tx   *MorphTx
	}{
		{
			name: "V0",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      1,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(1000000000000000000),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 1,
				FeeLimit:   big.NewInt(100000000000000000),
				Version:    MorphTxVersion0,
				V:          big.NewInt(1),
				R:          big.NewInt(123456),
				S:          big.NewInt(654321),
			},
		},
		{
			name: "V1 with Reference and Memo",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      2,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{0x01, 0x02, 0x03},
				AccessList: AccessList{},
				FeeTokenID: 0,
				FeeLimit:   nil,
				Version:    MorphTxVersion1,
				Reference:  &reference,
				Memo:       &memo,
				V:          big.NewInt(0),
				R:          big.NewInt(111),
				S:          big.NewInt(222),
			},
		},
		{
			name: "V1 minimal",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      3,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        50000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 0,
				FeeLimit:   nil,
				Version:    MorphTxVersion1,
				Reference:  nil,
				Memo:       nil,
				V:          big.NewInt(0),
				R:          big.NewInt(0),
				S:          big.NewInt(0),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Path 1: rlp.Encode (used by Hash via prefixedRlpHash)
			var rlpBuf bytes.Buffer
			if err := rlp.Encode(&rlpBuf, tc.tx); err != nil {
				t.Fatalf("rlp.Encode failed: %v", err)
			}

			// Path 2: custom encode() (used by wire format via encodeTyped)
			var encodeBuf bytes.Buffer
			if err := tc.tx.encode(&encodeBuf); err != nil {
				t.Fatalf("encode() failed: %v", err)
			}

			if !bytes.Equal(rlpBuf.Bytes(), encodeBuf.Bytes()) {
				t.Errorf("rlp.Encode and encode() produce different output:\n  rlp.Encode = %s\n  encode()   = %s",
					hex.EncodeToString(rlpBuf.Bytes()), hex.EncodeToString(encodeBuf.Bytes()))
			}
		})
	}
}

// TestMorphTxHashMatchesWireFormat verifies that tx.Hash() equals
// keccak256(wire_bytes) for both V0 and V1 MorphTx transactions.
func TestMorphTxHashMatchesWireFormat(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	reference := common.HexToReference("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	memo := []byte("hello")

	testCases := []struct {
		name string
		tx   *MorphTx
	}{
		{
			name: "V0",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      1,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(1000000000000000000),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 1,
				FeeLimit:   big.NewInt(100000000000000000),
				Version:    MorphTxVersion0,
				V:          big.NewInt(1),
				R:          big.NewInt(123456),
				S:          big.NewInt(654321),
			},
		},
		{
			name: "V1 with Reference and Memo",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      2,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{0x01, 0x02, 0x03},
				AccessList: AccessList{},
				FeeTokenID: 0,
				FeeLimit:   nil,
				Version:    MorphTxVersion1,
				Reference:  &reference,
				Memo:       &memo,
				V:          big.NewInt(0),
				R:          big.NewInt(111),
				S:          big.NewInt(222),
			},
		},
		{
			name: "V1 minimal",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      3,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        50000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 0,
				FeeLimit:   nil,
				Version:    MorphTxVersion1,
				Reference:  nil,
				Memo:       nil,
				V:          big.NewInt(0),
				R:          big.NewInt(0),
				S:          big.NewInt(0),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tx := NewTx(tc.tx)

			wireBytes, err := tx.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary failed: %v", err)
			}

			expectedHash := crypto.Keccak256Hash(wireBytes)

			if tx.Hash() != expectedHash {
				t.Errorf("Hash mismatch:\n  tx.Hash()        = %s\n  keccak256(wire)  = %s\n  wireBytes        = %s",
					tx.Hash().Hex(), expectedHash.Hex(), hex.EncodeToString(wireBytes))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DecodeRLP tests (from morph_tx_decode_rlp_test.go)
// ---------------------------------------------------------------------------

// TestDecodeRLP_V0RoundTrip tests that rlp.Encode → rlp.DecodeBytes round-trip
// works correctly for V0 MorphTx via DecodeRLP. This is the scenario that
// triggered the original bug where derivation module called rlp.DecodeBytes
// directly on MorphTx, causing reflection-based field misalignment.
func TestDecodeRLP_V0RoundTrip(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")

	testCases := []struct {
		name string
		tx   *MorphTx
	}{
		{
			name: "V0 basic",
			tx: &MorphTx{
				ChainID:    big.NewInt(2818),
				Nonce:      1,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(1000000000000000000),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 1,
				FeeLimit:   big.NewInt(500000000000000000),
				Version:    MorphTxVersion0,
				V:          big.NewInt(1),
				R:          big.NewInt(123456),
				S:          big.NewInt(654321),
			},
		},
		{
			name: "V0 large FeeLimit (>65535, the bug trigger)",
			tx: &MorphTx{
				ChainID:    big.NewInt(2818),
				Nonce:      42,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 1,
				FeeLimit:   big.NewInt(999999999999999999),
				Version:    MorphTxVersion0,
				V:          big.NewInt(0),
				R:          big.NewInt(111),
				S:          big.NewInt(222),
			},
		},
		{
			name: "V0 nil FeeLimit",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      0,
				GasTipCap:  big.NewInt(100),
				GasFeeCap:  big.NewInt(200),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 5,
				FeeLimit:   nil,
				Version:    MorphTxVersion0,
				V:          big.NewInt(0),
				R:          big.NewInt(0),
				S:          big.NewInt(0),
			},
		},
		{
			name: "V0 max FeeTokenID",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      1,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        100000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{0xde, 0xad, 0xbe, 0xef},
				AccessList: AccessList{},
				FeeTokenID: 65535,
				FeeLimit:   big.NewInt(1),
				Version:    MorphTxVersion0,
				V:          big.NewInt(1),
				R:          big.NewInt(999),
				S:          big.NewInt(888),
			},
		},
		{
			name: "V0 with AccessList",
			tx: &MorphTx{
				ChainID:   big.NewInt(1),
				Nonce:     7,
				GasTipCap: big.NewInt(1000000000),
				GasFeeCap: big.NewInt(2000000000),
				Gas:       50000,
				To:        &to,
				Value:     big.NewInt(0),
				Data:      []byte{},
				AccessList: AccessList{
					{Address: common.HexToAddress("0xaaaa"), StorageKeys: []common.Hash{common.HexToHash("0x01")}},
				},
				FeeTokenID: 3,
				FeeLimit:   big.NewInt(100),
				Version:    MorphTxVersion0,
				V:          big.NewInt(1),
				R:          big.NewInt(100),
				S:          big.NewInt(200),
			},
		},
		{
			name: "V0 contract creation (To=nil)",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      0,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        3000000,
				To:         nil,
				Value:      big.NewInt(0),
				Data:       []byte{0x60, 0x60, 0x60, 0x40},
				AccessList: AccessList{},
				FeeTokenID: 1,
				FeeLimit:   big.NewInt(500000),
				Version:    MorphTxVersion0,
				V:          big.NewInt(0),
				R:          big.NewInt(12345),
				S:          big.NewInt(67890),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var encoded bytes.Buffer
			if err := rlp.Encode(&encoded, tc.tx); err != nil {
				t.Fatalf("rlp.Encode failed: %v", err)
			}

			var decoded MorphTx
			if err := rlp.DecodeBytes(encoded.Bytes(), &decoded); err != nil {
				t.Fatalf("rlp.DecodeBytes failed: %v", err)
			}

			assertMorphTxEqual(t, tc.tx, &decoded)
		})
	}
}

// TestDecodeRLP_V1RoundTrip tests that rlp.Encode → rlp.DecodeBytes round-trip
// works correctly for V1 MorphTx. V1 uses a version byte prefix before the RLP list.
func TestDecodeRLP_V1RoundTrip(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	ref := common.HexToReference("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	memo := []byte("test memo data")

	testCases := []struct {
		name string
		tx   *MorphTx
	}{
		{
			name: "V1 with Reference and Memo",
			tx: &MorphTx{
				ChainID:    big.NewInt(2818),
				Nonce:      1,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 1,
				FeeLimit:   big.NewInt(100000),
				Version:    MorphTxVersion1,
				Reference:  &ref,
				Memo:       &memo,
				V:          big.NewInt(0),
				R:          big.NewInt(111),
				S:          big.NewInt(222),
			},
		},
		{
			name: "V1 Reference only (no Memo, no FeeTokenID)",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      10,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 0,
				FeeLimit:   nil,
				Version:    MorphTxVersion1,
				Reference:  &ref,
				Memo:       nil,
				V:          big.NewInt(0),
				R:          big.NewInt(0),
				S:          big.NewInt(0),
			},
		},
		{
			name: "V1 Memo only (no Reference, no FeeTokenID)",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      20,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 0,
				FeeLimit:   nil,
				Version:    MorphTxVersion1,
				Reference:  nil,
				Memo:       &memo,
				V:          big.NewInt(0),
				R:          big.NewInt(333),
				S:          big.NewInt(444),
			},
		},
		{
			name: "V1 minimal (no Reference, no Memo, no FeeTokenID)",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      0,
				GasTipCap:  big.NewInt(100),
				GasFeeCap:  big.NewInt(200),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 0,
				FeeLimit:   nil,
				Version:    MorphTxVersion1,
				Reference:  nil,
				Memo:       nil,
				V:          big.NewInt(0),
				R:          big.NewInt(0),
				S:          big.NewInt(0),
			},
		},
		{
			name: "V1 with FeeTokenID + Reference + Memo + large FeeLimit",
			tx: &MorphTx{
				ChainID:    big.NewInt(2818),
				Nonce:      999,
				GasTipCap:  big.NewInt(5000000000),
				GasFeeCap:  big.NewInt(10000000000),
				Gas:        500000,
				To:         &to,
				Value:      big.NewInt(1000000000000000000),
				Data:       []byte{0x01, 0x02, 0x03, 0x04},
				AccessList: AccessList{},
				FeeTokenID: 2,
				FeeLimit:   big.NewInt(999999999999999999),
				Version:    MorphTxVersion1,
				Reference:  &ref,
				Memo:       &memo,
				V:          big.NewInt(1),
				R:          big.NewInt(12345),
				S:          big.NewInt(67890),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var encoded bytes.Buffer
			if err := rlp.Encode(&encoded, tc.tx); err != nil {
				t.Fatalf("rlp.Encode failed: %v", err)
			}

			var decoded MorphTx
			if err := rlp.DecodeBytes(encoded.Bytes(), &decoded); err != nil {
				t.Fatalf("rlp.DecodeBytes failed: %v", err)
			}

			assertMorphTxEqual(t, tc.tx, &decoded)
		})
	}
}

// TestDecodeRLP_V0LargeFeeLimit_BugRepro reproduces the exact bug from the error:
//
//	rlp: input string too long for uint16, decoding into (types.MorphTx).FeeTokenID
//
// Without DecodeRLP, rlp.DecodeBytes uses reflection which misaligns fields:
// V0 wire format has [.., AccessList, FeeTokenID(uint16), FeeLimit(*big.Int), ..]
// but MorphTx struct has [.., AccessList, Version(uint8), FeeTokenID(uint16), FeeLimit, ..]
// so FeeLimit's big.Int bytes are decoded into FeeTokenID (uint16), causing the error.
func TestDecodeRLP_V0LargeFeeLimit_BugRepro(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	feeLimits := []*big.Int{
		big.NewInt(65536),                                     // just above uint16 max
		big.NewInt(500000000000000000),                        // 0.5 ETH
		big.NewInt(999999999999999999),                        // ~1 ETH
		new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil), // 10^30
	}

	for _, feeLimit := range feeLimits {
		t.Run("FeeLimit="+feeLimit.String(), func(t *testing.T) {
			tx := &MorphTx{
				ChainID:    big.NewInt(2818),
				Nonce:      1,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 1,
				FeeLimit:   feeLimit,
				Version:    MorphTxVersion0,
				V:          big.NewInt(0),
				R:          big.NewInt(111),
				S:          big.NewInt(222),
			}

			// Encode via rlp.Encode (uses EncodeRLP → encode → v0MorphTxRLP)
			var encoded bytes.Buffer
			if err := rlp.Encode(&encoded, tx); err != nil {
				t.Fatalf("rlp.Encode failed: %v", err)
			}

			// Decode via rlp.DecodeBytes (uses DecodeRLP).
			// Without DecodeRLP, this would fail with:
			// "rlp: input string too long for uint16, decoding into (types.MorphTx).FeeTokenID"
			var decoded MorphTx
			if err := rlp.DecodeBytes(encoded.Bytes(), &decoded); err != nil {
				t.Fatalf("rlp.DecodeBytes failed (this is the bug!): %v", err)
			}

			if decoded.FeeTokenID != tx.FeeTokenID {
				t.Errorf("FeeTokenID mismatch: want %d, got %d", tx.FeeTokenID, decoded.FeeTokenID)
			}
			if decoded.FeeLimit == nil || decoded.FeeLimit.Cmp(feeLimit) != 0 {
				t.Errorf("FeeLimit mismatch: want %v, got %v", feeLimit, decoded.FeeLimit)
			}
			if decoded.Version != MorphTxVersion0 {
				t.Errorf("Version mismatch: want %d, got %d", MorphTxVersion0, decoded.Version)
			}
		})
	}
}

// TestDecodeRLP_MatchesDecode verifies that DecodeRLP (via rlp.DecodeBytes)
// produces the same result as the custom decode() method for both V0 and V1.
func TestDecodeRLP_MatchesDecode(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	ref := common.HexToReference("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	memo := []byte("hello world")

	testCases := []struct {
		name string
		tx   *MorphTx
	}{
		{
			name: "V0",
			tx: &MorphTx{
				ChainID:    big.NewInt(2818),
				Nonce:      1,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(1000000000000000000),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 1,
				FeeLimit:   big.NewInt(500000000000000000),
				Version:    MorphTxVersion0,
				V:          big.NewInt(1),
				R:          big.NewInt(123456),
				S:          big.NewInt(654321),
			},
		},
		{
			name: "V1",
			tx: &MorphTx{
				ChainID:    big.NewInt(2818),
				Nonce:      2,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{0x01},
				AccessList: AccessList{},
				FeeTokenID: 2,
				FeeLimit:   big.NewInt(100000),
				Version:    MorphTxVersion1,
				Reference:  &ref,
				Memo:       &memo,
				V:          big.NewInt(0),
				R:          big.NewInt(999),
				S:          big.NewInt(888),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode via encode() to get raw wire bytes
			var buf bytes.Buffer
			if err := tc.tx.encode(&buf); err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			wireBytes := buf.Bytes()

			// Path 1: decode via custom decode()
			var fromDecode MorphTx
			if err := fromDecode.decode(wireBytes); err != nil {
				t.Fatalf("decode() failed: %v", err)
			}

			// Path 2: decode via rlp.DecodeBytes → DecodeRLP
			var encoded bytes.Buffer
			if err := rlp.Encode(&encoded, tc.tx); err != nil {
				t.Fatalf("rlp.Encode failed: %v", err)
			}
			var fromDecodeRLP MorphTx
			if err := rlp.DecodeBytes(encoded.Bytes(), &fromDecodeRLP); err != nil {
				t.Fatalf("rlp.DecodeBytes failed: %v", err)
			}

			// Both paths should produce identical results
			assertMorphTxEqual(t, &fromDecode, &fromDecodeRLP)
		})
	}
}

// TestDecodeRLP_InRLPList tests DecodeRLP when MorphTx is embedded within an
// RLP list, simulating the batch parsing scenario in the derivation module.
func TestDecodeRLP_InRLPList(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	ref := common.HexToReference("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	memo := []byte("memo")

	txV0 := &MorphTx{
		ChainID:    big.NewInt(2818),
		Nonce:      1,
		GasTipCap:  big.NewInt(1000000000),
		GasFeeCap:  big.NewInt(2000000000),
		Gas:        21000,
		To:         &to,
		Value:      big.NewInt(0),
		Data:       []byte{},
		AccessList: AccessList{},
		FeeTokenID: 1,
		FeeLimit:   big.NewInt(500000000000000000),
		Version:    MorphTxVersion0,
		V:          big.NewInt(1),
		R:          big.NewInt(111),
		S:          big.NewInt(222),
	}

	txV1 := &MorphTx{
		ChainID:    big.NewInt(2818),
		Nonce:      2,
		GasTipCap:  big.NewInt(1000000000),
		GasFeeCap:  big.NewInt(2000000000),
		Gas:        21000,
		To:         &to,
		Value:      big.NewInt(0),
		Data:       []byte{},
		AccessList: AccessList{},
		FeeTokenID: 0,
		FeeLimit:   nil,
		Version:    MorphTxVersion1,
		Reference:  &ref,
		Memo:       &memo,
		V:          big.NewInt(0),
		R:          big.NewInt(333),
		S:          big.NewInt(444),
	}

	// Encode a list of MorphTx (simulating a batch)
	batch := []*MorphTx{txV0, txV1}
	var encoded bytes.Buffer
	if err := rlp.Encode(&encoded, batch); err != nil {
		t.Fatalf("rlp.Encode batch failed: %v", err)
	}

	// Decode back
	var decoded []*MorphTx
	if err := rlp.DecodeBytes(encoded.Bytes(), &decoded); err != nil {
		t.Fatalf("rlp.DecodeBytes batch failed: %v", err)
	}

	if len(decoded) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(decoded))
	}

	t.Run("batch[0] V0", func(t *testing.T) {
		assertMorphTxEqual(t, txV0, decoded[0])
	})
	t.Run("batch[1] V1", func(t *testing.T) {
		assertMorphTxEqual(t, txV1, decoded[1])
	})
}

// TestDecodeRLP_V0BackwardCompat_HardcodedHex tests that DecodeRLP correctly
// handles hardcoded V0 data from the original AltFeeTx encoding (the same test
// vectors from TestMorphTxV0BackwardCompatibility, but decoded via rlp.DecodeBytes).
func TestDecodeRLP_V0BackwardCompat_HardcodedHex(t *testing.T) {
	// This hex was generated from the original AltFeeTx implementation.
	// Inner data (after stripping MorphTxType 0x7F prefix) is a V0 RLP list.
	//
	// Fields: ChainID=2818, Nonce=1, GasTipCap=1e9, GasFeeCap=2e9, Gas=21000,
	// To=0x1234..., Value=1e18, Data=[], AccessList=[], FeeTokenID=1,
	// FeeLimit=5e17, V=1, R=..., S=...
	innerHex := "f87e820b0201843b9aca008477359400825208941234567890123456789012345678901234567890880de0b6b3a764000080c0018806f05b59d3b2000001a0abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890a01234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	data, err := hex.DecodeString(innerHex)
	if err != nil {
		t.Fatalf("hex decode failed: %v", err)
	}

	// Decode using rlp.DecodeBytes → DecodeRLP
	var decoded MorphTx
	if err := rlp.DecodeBytes(data, &decoded); err != nil {
		t.Fatalf("rlp.DecodeBytes failed: %v", err)
	}

	if decoded.Version != MorphTxVersion0 {
		t.Errorf("Version: want %d, got %d", MorphTxVersion0, decoded.Version)
	}
	if decoded.FeeTokenID != 1 {
		t.Errorf("FeeTokenID: want 1, got %d", decoded.FeeTokenID)
	}
	if decoded.ChainID.Cmp(big.NewInt(2818)) != 0 {
		t.Errorf("ChainID: want 2818, got %v", decoded.ChainID)
	}
	if decoded.Nonce != 1 {
		t.Errorf("Nonce: want 1, got %d", decoded.Nonce)
	}
	expectedFeeLimit := big.NewInt(500000000000000000)
	if decoded.FeeLimit == nil || decoded.FeeLimit.Cmp(expectedFeeLimit) != 0 {
		t.Errorf("FeeLimit: want %v, got %v", expectedFeeLimit, decoded.FeeLimit)
	}
}

// TestDecodeRLP_ErrorCases tests that DecodeRLP correctly rejects invalid data.
func TestDecodeRLP_ErrorCases(t *testing.T) {
	testCases := []struct {
		name  string
		input []byte
	}{
		{
			name:  "empty input",
			input: []byte{},
		},
		{
			name:  "unsupported version byte 0x02",
			input: []byte{0x02, 0xc0},
		},
		{
			name:  "unsupported version byte 0xFF handled as V0 but invalid RLP list content",
			input: []byte{0xc1, 0xff},
		},
		{
			name:  "truncated V0 RLP list",
			input: []byte{0xc5, 0x01, 0x02},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var decoded MorphTx
			err := rlp.DecodeBytes(tc.input, &decoded)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// TestDecodeRLP_V0FeeTokenIDValues tests various FeeTokenID values to ensure
// they survive the rlp.Encode → rlp.DecodeBytes round-trip without corruption.
func TestDecodeRLP_V0FeeTokenIDValues(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	feeTokenIDs := []uint16{1, 2, 127, 128, 255, 256, 1000, 65535}

	for _, fid := range feeTokenIDs {
		t.Run("FeeTokenID="+big.NewInt(int64(fid)).String(), func(t *testing.T) {
			tx := &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      1,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: fid,
				FeeLimit:   big.NewInt(100000),
				Version:    MorphTxVersion0,
				V:          big.NewInt(0),
				R:          big.NewInt(0),
				S:          big.NewInt(0),
			}

			var encoded bytes.Buffer
			if err := rlp.Encode(&encoded, tx); err != nil {
				t.Fatalf("rlp.Encode failed: %v", err)
			}

			var decoded MorphTx
			if err := rlp.DecodeBytes(encoded.Bytes(), &decoded); err != nil {
				t.Fatalf("rlp.DecodeBytes failed: %v", err)
			}

			if decoded.FeeTokenID != fid {
				t.Errorf("FeeTokenID: want %d, got %d", fid, decoded.FeeTokenID)
			}
		})
	}
}

// TestDecodeRLP_TransactionWrapperConsistency verifies that decoding via
// Transaction.UnmarshalBinary (the normal path) and decoding via direct
// rlp.DecodeBytes on MorphTx produce semantically equivalent results.
func TestDecodeRLP_TransactionWrapperConsistency(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	ref := common.HexToReference("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	memo := []byte("memo")

	testCases := []struct {
		name string
		tx   *MorphTx
	}{
		{
			name: "V0",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      1,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 1,
				FeeLimit:   big.NewInt(500000000000000000),
				Version:    MorphTxVersion0,
				V:          big.NewInt(1),
				R:          big.NewInt(123),
				S:          big.NewInt(456),
			},
		},
		{
			name: "V1",
			tx: &MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      2,
				GasTipCap:  big.NewInt(1000000000),
				GasFeeCap:  big.NewInt(2000000000),
				Gas:        21000,
				To:         &to,
				Value:      big.NewInt(0),
				Data:       []byte{},
				AccessList: AccessList{},
				FeeTokenID: 0,
				FeeLimit:   nil,
				Version:    MorphTxVersion1,
				Reference:  &ref,
				Memo:       &memo,
				V:          big.NewInt(0),
				R:          big.NewInt(789),
				S:          big.NewInt(101),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Path 1: Transaction.MarshalBinary → Transaction.UnmarshalBinary
			wrappedTx := NewTx(tc.tx)
			wireBytes, err := wrappedTx.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary failed: %v", err)
			}
			var parsedTx Transaction
			if err := parsedTx.UnmarshalBinary(wireBytes); err != nil {
				t.Fatalf("UnmarshalBinary failed: %v", err)
			}
			fromWrapper := parsedTx.inner.(*MorphTx)

			// Path 2: rlp.Encode → rlp.DecodeBytes (direct MorphTx)
			var encoded bytes.Buffer
			if err := rlp.Encode(&encoded, tc.tx); err != nil {
				t.Fatalf("rlp.Encode failed: %v", err)
			}
			var fromDirect MorphTx
			if err := rlp.DecodeBytes(encoded.Bytes(), &fromDirect); err != nil {
				t.Fatalf("rlp.DecodeBytes failed: %v", err)
			}

			assertMorphTxEqual(t, fromWrapper, &fromDirect)
		})
	}
}

// TestDecodeRLP_EncodeDecodeSymmetry verifies that rlp.Encode output can be
// fed back into rlp.DecodeBytes and produce an identical MorphTx for both versions.
func TestDecodeRLP_EncodeDecodeSymmetry(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	ref := common.HexToReference("0x1111111111111111111111111111111111111111111111111111111111111111")
	memo := []byte("symmetric test")

	txV0 := &MorphTx{
		ChainID: big.NewInt(1), Nonce: 1,
		GasTipCap: big.NewInt(1e9), GasFeeCap: big.NewInt(2e9), Gas: 21000,
		To: &to, Value: big.NewInt(1e18), Data: []byte{},
		AccessList: AccessList{}, FeeTokenID: 1, FeeLimit: big.NewInt(1e17),
		Version: MorphTxVersion0,
		V: big.NewInt(1), R: big.NewInt(100), S: big.NewInt(200),
	}
	txV1 := &MorphTx{
		ChainID: big.NewInt(1), Nonce: 2,
		GasTipCap: big.NewInt(1e9), GasFeeCap: big.NewInt(2e9), Gas: 21000,
		To: &to, Value: big.NewInt(0), Data: []byte{0xab},
		AccessList: AccessList{}, FeeTokenID: 3, FeeLimit: big.NewInt(5e17),
		Version: MorphTxVersion1, Reference: &ref, Memo: &memo,
		V: big.NewInt(0), R: big.NewInt(300), S: big.NewInt(400),
	}

	for _, tc := range []struct {
		name string
		tx   *MorphTx
	}{
		{"V0", txV0},
		{"V1", txV1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Encode → Decode → Re-encode and verify byte-for-byte equality
			var buf1 bytes.Buffer
			if err := rlp.Encode(&buf1, tc.tx); err != nil {
				t.Fatalf("first rlp.Encode failed: %v", err)
			}

			var decoded MorphTx
			if err := rlp.DecodeBytes(buf1.Bytes(), &decoded); err != nil {
				t.Fatalf("rlp.DecodeBytes failed: %v", err)
			}

			var buf2 bytes.Buffer
			if err := rlp.Encode(&buf2, &decoded); err != nil {
				t.Fatalf("second rlp.Encode failed: %v", err)
			}

			if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
				t.Errorf("encode→decode→encode not stable:\n  first:  %x\n  second: %x",
					buf1.Bytes(), buf2.Bytes())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// encodeMorphTx encodes a MorphTx using its encode method with txType prefix.
func encodeMorphTx(tx *MorphTx) ([]byte, error) {
	buf := new(bytes.Buffer)
	buf.WriteByte(MorphTxType)
	if err := tx.encode(buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// assertMorphTxEqual compares two MorphTx structs field by field.
func assertMorphTxEqual(t *testing.T, want, got *MorphTx) {
	t.Helper()

	if want.Version != got.Version {
		t.Errorf("Version: want %d, got %d", want.Version, got.Version)
	}
	if want.FeeTokenID != got.FeeTokenID {
		t.Errorf("FeeTokenID: want %d, got %d", want.FeeTokenID, got.FeeTokenID)
	}
	if want.Nonce != got.Nonce {
		t.Errorf("Nonce: want %d, got %d", want.Nonce, got.Nonce)
	}
	if want.Gas != got.Gas {
		t.Errorf("Gas: want %d, got %d", want.Gas, got.Gas)
	}
	assertBigIntEqual(t, "ChainID", want.ChainID, got.ChainID)
	assertBigIntEqual(t, "GasTipCap", want.GasTipCap, got.GasTipCap)
	assertBigIntEqual(t, "GasFeeCap", want.GasFeeCap, got.GasFeeCap)
	assertBigIntEqual(t, "Value", want.Value, got.Value)
	assertBigIntEqual(t, "V", want.V, got.V)
	assertBigIntEqual(t, "R", want.R, got.R)
	assertBigIntEqual(t, "S", want.S, got.S)

	// FeeLimit: nil and zero are treated as equivalent in RLP
	wantFeeLimit := want.FeeLimit
	gotFeeLimit := got.FeeLimit
	if wantFeeLimit == nil {
		wantFeeLimit = new(big.Int)
	}
	if gotFeeLimit == nil {
		gotFeeLimit = new(big.Int)
	}
	if wantFeeLimit.Cmp(gotFeeLimit) != 0 {
		t.Errorf("FeeLimit: want %v, got %v", want.FeeLimit, got.FeeLimit)
	}

	if !bytes.Equal(want.Data, got.Data) {
		t.Errorf("Data: want %x, got %x", want.Data, got.Data)
	}

	// To
	if want.To == nil && got.To != nil {
		t.Errorf("To: want nil, got %v", got.To)
	} else if want.To != nil && got.To == nil {
		t.Errorf("To: want %v, got nil", want.To)
	} else if want.To != nil && got.To != nil && *want.To != *got.To {
		t.Errorf("To: want %v, got %v", want.To, got.To)
	}

	// Reference
	if want.Reference == nil && got.Reference != nil {
		t.Errorf("Reference: want nil, got %v", got.Reference)
	} else if want.Reference != nil && got.Reference == nil {
		t.Errorf("Reference: want %v, got nil", want.Reference)
	} else if want.Reference != nil && got.Reference != nil && *want.Reference != *got.Reference {
		t.Errorf("Reference: want %v, got %v", want.Reference, got.Reference)
	}

	// Memo
	var wantMemo, gotMemo []byte
	if want.Memo != nil {
		wantMemo = *want.Memo
	}
	if got.Memo != nil {
		gotMemo = *got.Memo
	}
	if !bytes.Equal(wantMemo, gotMemo) {
		t.Errorf("Memo: want %x, got %x", wantMemo, gotMemo)
	}
}

func assertBigIntEqual(t *testing.T, name string, want, got *big.Int) {
	t.Helper()
	if want == nil && got == nil {
		return
	}
	if want == nil {
		want = new(big.Int)
	}
	if got == nil {
		got = new(big.Int)
	}
	if want.Cmp(got) != 0 {
		t.Errorf("%s: want %v, got %v", name, want, got)
	}
}
