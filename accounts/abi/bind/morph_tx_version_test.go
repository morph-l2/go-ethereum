package bind

import (
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
)

func versionPtr(v uint8) *uint8 {
	return &v
}

func refTestPtr(r common.Reference) *common.Reference {
	return &r
}

func memoTestPtr(b []byte) *[]byte {
	return &b
}

// TestMorphTxVersion_HeuristicDefault tests the heuristic version defaulting logic
// in morphTxVersion():
//   - Version == nil + no V1 fields → V0
//   - Version == nil + Reference or Memo → V1
//   - Explicit Version → use as-is (with validation)
func TestMorphTxVersion_HeuristicDefault(t *testing.T) {
	ref := common.HexToReference("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	emptyRef := common.Reference{}
	memo := []byte("test memo")
	emptyMemo := []byte{}

	bc := &BoundContract{} // morphTxVersion doesn't use BoundContract fields

	tests := []struct {
		name        string
		opts        *TransactOpts
		wantVersion uint8
		wantErr     error
	}{
		// === Heuristic defaults (Version == nil) ===
		{
			name:        "nil Version, FeeTokenID > 0, no V1 fields → V0",
			opts:        &TransactOpts{FeeTokenID: 1},
			wantVersion: types.MorphTxVersion0,
		},
		{
			name:        "nil Version, FeeTokenID > 0, with Reference → V1",
			opts:        &TransactOpts{FeeTokenID: 1, Reference: refTestPtr(ref)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "nil Version, FeeTokenID > 0, with Memo → V1",
			opts:        &TransactOpts{FeeTokenID: 1, Memo: memoTestPtr(memo)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "nil Version, FeeTokenID > 0, with Reference + Memo → V1",
			opts:        &TransactOpts{FeeTokenID: 1, Reference: refTestPtr(ref), Memo: memoTestPtr(memo)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "nil Version, FeeTokenID = 0, with Reference → V1",
			opts:        &TransactOpts{FeeTokenID: 0, Reference: refTestPtr(ref)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "nil Version, FeeTokenID = 0, with Memo → V1",
			opts:        &TransactOpts{FeeTokenID: 0, Memo: memoTestPtr(memo)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "nil Version, FeeTokenID = 0, no V1 fields → V0",
			opts:        &TransactOpts{FeeTokenID: 0},
			wantVersion: types.MorphTxVersion0,
		},
		{
			name:        "nil Version, empty Reference → V0",
			opts:        &TransactOpts{FeeTokenID: 1, Reference: refTestPtr(emptyRef)},
			wantVersion: types.MorphTxVersion0,
		},
		{
			name:        "nil Version, empty Memo → V0",
			opts:        &TransactOpts{FeeTokenID: 1, Memo: memoTestPtr(emptyMemo)},
			wantVersion: types.MorphTxVersion0,
		},
		{
			name:        "nil Version, nil Reference, nil Memo → V0",
			opts:        &TransactOpts{FeeTokenID: 1, Reference: nil, Memo: nil},
			wantVersion: types.MorphTxVersion0,
		},

		// === V1 heuristic with illegal params ===
		{
			name:    "nil Version, Reference + FeeTokenID=0 + FeeLimit > 0 → error",
			opts:    &TransactOpts{FeeTokenID: 0, FeeLimit: big.NewInt(100), Reference: refTestPtr(ref)},
			wantErr: types.ErrMorphTxV1IllegalExtraParams,
		},
		{
			name:        "nil Version, Reference + FeeTokenID=0 + FeeLimit=0 → V1 (ok)",
			opts:        &TransactOpts{FeeTokenID: 0, FeeLimit: big.NewInt(0), Reference: refTestPtr(ref)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "nil Version, Reference + FeeTokenID=0 + nil FeeLimit → V1 (ok)",
			opts:        &TransactOpts{FeeTokenID: 0, FeeLimit: nil, Reference: refTestPtr(ref)},
			wantVersion: types.MorphTxVersion1,
		},

		// === Explicit Version ===
		{
			name:        "explicit V0, FeeTokenID > 0 → V0",
			opts:        &TransactOpts{Version: versionPtr(types.MorphTxVersion0), FeeTokenID: 1},
			wantVersion: types.MorphTxVersion0,
		},
		{
			name:        "explicit V1, no special fields → V1",
			opts:        &TransactOpts{Version: versionPtr(types.MorphTxVersion1)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "explicit V1, with Reference + Memo → V1",
			opts:        &TransactOpts{Version: versionPtr(types.MorphTxVersion1), Reference: refTestPtr(ref), Memo: memoTestPtr(memo)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:    "explicit V0, FeeTokenID = 0 → error (V0 requires FeeTokenID > 0)",
			opts:    &TransactOpts{Version: versionPtr(types.MorphTxVersion0), FeeTokenID: 0},
			wantErr: types.ErrMorphTxV0IllegalExtraParams,
		},
		{
			name:    "explicit V0, with Reference → error",
			opts:    &TransactOpts{Version: versionPtr(types.MorphTxVersion0), FeeTokenID: 1, Reference: refTestPtr(ref)},
			wantErr: types.ErrMorphTxV0IllegalExtraParams,
		},
		{
			name:    "explicit V0, with Memo → error",
			opts:    &TransactOpts{Version: versionPtr(types.MorphTxVersion0), FeeTokenID: 1, Memo: memoTestPtr(memo)},
			wantErr: types.ErrMorphTxV0IllegalExtraParams,
		},
		{
			name:    "explicit V1, FeeTokenID=0 + FeeLimit>0 → error",
			opts:    &TransactOpts{Version: versionPtr(types.MorphTxVersion1), FeeTokenID: 0, FeeLimit: big.NewInt(100)},
			wantErr: types.ErrMorphTxV1IllegalExtraParams,
		},
		{
			name:    "unsupported version 255 → error",
			opts:    &TransactOpts{Version: versionPtr(255)},
			wantErr: types.ErrMorphTxUnsupportedVersion,
		},

		// === Memo length validation ===
		{
			name:    "memo too long → error",
			opts:    &TransactOpts{FeeTokenID: 1, Memo: memoTestPtr(make([]byte, common.MaxMemoLength+1))},
			wantErr: types.ErrMemoTooLong,
		},
		{
			name:        "memo at max length → ok",
			opts:        &TransactOpts{FeeTokenID: 1, Memo: memoTestPtr(make([]byte, common.MaxMemoLength))},
			wantVersion: types.MorphTxVersion1, // non-empty memo → V1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, err := bc.morphTxVersion(tt.opts)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("error: got %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if version != tt.wantVersion {
				t.Errorf("version: got %d, want %d", version, tt.wantVersion)
			}
		})
	}
}
