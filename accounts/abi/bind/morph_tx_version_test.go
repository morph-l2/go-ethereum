package bind

import (
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
)

func refTestPtr(r common.Reference) *common.Reference {
	return &r
}

func memoTestPtr(b []byte) *[]byte {
	return &b
}

// TestMorphTxVersion_HeuristicDefault tests version derivation:
// MorphTx defaults to v1 and a non-empty authorization list selects v2.
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
		{
			name:        "FeeTokenID > 0, no optional fields → V1",
			opts:        &TransactOpts{FeeTokenID: 1},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "FeeTokenID > 0, with Reference → V1",
			opts:        &TransactOpts{FeeTokenID: 1, Reference: refTestPtr(ref)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "FeeTokenID > 0, with Memo → V1",
			opts:        &TransactOpts{FeeTokenID: 1, Memo: memoTestPtr(memo)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "FeeTokenID > 0, with Reference + Memo → V1",
			opts:        &TransactOpts{FeeTokenID: 1, Reference: refTestPtr(ref), Memo: memoTestPtr(memo)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "FeeTokenID = 0, with Reference → V1",
			opts:        &TransactOpts{FeeTokenID: 0, Reference: refTestPtr(ref)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "FeeTokenID = 0, with Memo → V1",
			opts:        &TransactOpts{FeeTokenID: 0, Memo: memoTestPtr(memo)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "FeeTokenID = 0, no optional fields → V1",
			opts:        &TransactOpts{FeeTokenID: 0},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "empty AuthorizationList → V1",
			opts:        &TransactOpts{FeeTokenID: 1, AuthorizationList: []types.SetCodeAuthorization{}},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "non-empty AuthorizationList → V2",
			opts:        &TransactOpts{FeeTokenID: 1, AuthorizationList: []types.SetCodeAuthorization{{}}},
			wantVersion: types.MorphTxVersion2,
		},
		{
			name:        "empty Reference → V1",
			opts:        &TransactOpts{FeeTokenID: 1, Reference: refTestPtr(emptyRef)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "empty Memo → V1",
			opts:        &TransactOpts{FeeTokenID: 1, Memo: memoTestPtr(emptyMemo)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "nil Reference and Memo → V1",
			opts:        &TransactOpts{FeeTokenID: 1, Reference: nil, Memo: nil},
			wantVersion: types.MorphTxVersion1,
		},

		{
			name:    "Reference + FeeTokenID=0 + FeeLimit > 0 → error",
			opts:    &TransactOpts{FeeTokenID: 0, FeeLimit: big.NewInt(100), Reference: refTestPtr(ref)},
			wantErr: types.ErrMorphTxV1IllegalExtraParams,
		},
		{
			name:        "Reference + FeeTokenID=0 + FeeLimit=0 → V1",
			opts:        &TransactOpts{FeeTokenID: 0, FeeLimit: big.NewInt(0), Reference: refTestPtr(ref)},
			wantVersion: types.MorphTxVersion1,
		},
		{
			name:        "Reference + FeeTokenID=0 + nil FeeLimit → V1",
			opts:        &TransactOpts{FeeTokenID: 0, FeeLimit: nil, Reference: refTestPtr(ref)},
			wantVersion: types.MorphTxVersion1,
		},

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
