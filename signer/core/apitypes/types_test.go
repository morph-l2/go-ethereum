package apitypes

import (
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/core/types"
)

func uint16Ptr(v uint16) *hexutil.Uint16 {
	h := hexutil.Uint16(v)
	return &h
}

func uint64VersionPtr(v uint64) *hexutil.Uint64 {
	h := hexutil.Uint64(v)
	return &h
}

func refPtr(r common.Reference) *common.Reference {
	return &r
}

func memoPtr(b []byte) *hexutil.Bytes {
	h := hexutil.Bytes(b)
	return &h
}

// TestSendTxArgs_ToTransaction_VersionHeuristic tests the heuristic version defaulting logic
// in SendTxArgs.ToTransaction():
//   - Explicit Version → use as-is
//   - No Version + Reference or Memo present → V1
//   - No Version + no V1 fields → V0 (backward compatible)
func TestSendTxArgs_ToTransaction_VersionHeuristic(t *testing.T) {
	from := common.NewMixedcaseAddress(common.HexToAddress("0x1234"))
	to := common.NewMixedcaseAddress(common.HexToAddress("0x5678"))
	ref := common.HexToReference("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	emptyRef := common.Reference{}
	maxFee := (*hexutil.Big)(common.Big1)
	tip := (*hexutil.Big)(common.Big1)

	tests := []struct {
		name            string
		args            SendTxArgs
		expectType      uint8
		expectVersion   uint8
		expectIsMorphTx bool
	}{
		{
			name: "FeeTokenID only, no Version → V0",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				FeeTokenID:           uint16Ptr(1),
			},
			expectType:      types.MorphTxType,
			expectVersion:   types.MorphTxVersion0,
			expectIsMorphTx: true,
		},
		{
			name: "FeeTokenID + Reference → V1",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				FeeTokenID:           uint16Ptr(1),
				Reference:            refPtr(ref),
			},
			expectType:      types.MorphTxType,
			expectVersion:   types.MorphTxVersion1,
			expectIsMorphTx: true,
		},
		{
			name: "FeeTokenID + Memo → V1",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				FeeTokenID:           uint16Ptr(1),
				Memo:                 memoPtr([]byte("hello")),
			},
			expectType:      types.MorphTxType,
			expectVersion:   types.MorphTxVersion1,
			expectIsMorphTx: true,
		},
		{
			name: "Reference only (no FeeTokenID) → V1",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				Reference:            refPtr(ref),
			},
			expectType:      types.MorphTxType,
			expectVersion:   types.MorphTxVersion1,
			expectIsMorphTx: true,
		},
		{
			name: "Memo only (no FeeTokenID) → V1",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				Memo:                 memoPtr([]byte("test")),
			},
			expectType:      types.MorphTxType,
			expectVersion:   types.MorphTxVersion1,
			expectIsMorphTx: true,
		},
		{
			name: "Empty Reference (all zeros) + FeeTokenID → V0",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				FeeTokenID:           uint16Ptr(1),
				Reference:            refPtr(emptyRef),
			},
			expectType:      types.MorphTxType,
			expectVersion:   types.MorphTxVersion0,
			expectIsMorphTx: true,
		},
		{
			name: "Empty Memo (len=0) + FeeTokenID → V0",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				FeeTokenID:           uint16Ptr(1),
				Memo:                 memoPtr([]byte{}),
			},
			expectType:      types.MorphTxType,
			expectVersion:   types.MorphTxVersion0,
			expectIsMorphTx: true,
		},
		{
			name: "Explicit Version=0 with Reference → V0 (explicit wins)",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				FeeTokenID:           uint16Ptr(1),
				Version:              uint64VersionPtr(uint64(types.MorphTxVersion0)),
				Reference:            refPtr(ref),
			},
			expectType:      types.MorphTxType,
			expectVersion:   types.MorphTxVersion0,
			expectIsMorphTx: true,
		},
		{
			name: "Explicit Version=1 without V1 fields → V1 (explicit wins)",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				Version:              uint64VersionPtr(uint64(types.MorphTxVersion1)),
			},
			expectType:      types.MorphTxType,
			expectVersion:   types.MorphTxVersion1,
			expectIsMorphTx: true,
		},
		{
			name: "Reference + Memo → V1",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				Reference:            refPtr(ref),
				Memo:                 memoPtr([]byte("memo")),
			},
			expectType:      types.MorphTxType,
			expectVersion:   types.MorphTxVersion1,
			expectIsMorphTx: true,
		},
		{
			name: "MaxFeePerGas only → DynamicFeeTx (not MorphTx)",
			args: SendTxArgs{
				From:                 from,
				To:                   &to,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
			},
			expectType:      types.DynamicFeeTxType,
			expectVersion:   0,
			expectIsMorphTx: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := tt.args.ToTransaction()

			if tx.Type() != tt.expectType {
				t.Errorf("tx type: got %d, want %d", tx.Type(), tt.expectType)
			}

			if tt.expectIsMorphTx {
				if !tx.IsMorphTx() {
					t.Fatal("expected IsMorphTx() = true")
				}
				if tx.Version() != tt.expectVersion {
					t.Errorf("version: got %d, want %d", tx.Version(), tt.expectVersion)
				}
			}
		})
	}
}
