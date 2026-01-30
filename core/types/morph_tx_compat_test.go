package types

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
)

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

// encodeMorphTx encodes a MorphTx using its encode method
func encodeMorphTx(tx *MorphTx) ([]byte, error) {
	buf := new(bytes.Buffer)
	buf.WriteByte(MorphTxType) // Write txType prefix
	if err := tx.encode(buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
