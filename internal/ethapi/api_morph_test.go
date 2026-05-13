package ethapi

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/consensus"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/event"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/morph-l2/go-ethereum/rpc"
	"github.com/morph-l2/go-ethereum/trie"
)

// mockMorphBackend is a minimal Backend mock that only implements ChainDb().
// All other Backend methods are left unimplemented via embedding.
// This is sufficient because GetTransactionHashesByReference only uses ChainDb().
type mockMorphBackend struct {
	Backend // embed interface — calling unimplemented methods will panic
	db      ethdb.Database
}

func (m *mockMorphBackend) ChainDb() ethdb.Database { return m.db }

func makeTestRef(b byte) common.Reference {
	var ref common.Reference
	ref[0] = b
	return ref
}

func uint64Ptr(v uint64) *hexutil.Uint64 {
	h := hexutil.Uint64(v)
	return &h
}

// TestGetTransactionHashesByReference_OffsetExceedsMax verifies that offset > 10000 is rejected.
func TestGetTransactionHashesByReference_OffsetExceedsMax(t *testing.T) {
	// Validation errors are returned before ChainDb() is called, so nil backend is safe.
	api := &PublicMorphAPI{b: nil}
	ref := makeTestRef(0x01)

	tests := []struct {
		name   string
		offset uint64
		want   string
	}{
		{"offset 10001", 10001, "offset exceeds maximum value of 10000"},
		{"offset max uint64", ^uint64(0), "offset exceeds maximum value of 10000"},
		{"offset 50000", 50000, "offset exceeds maximum value of 10000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := rpc.ReferenceQueryArgs{
				Reference: ref,
				Offset:    uint64Ptr(tt.offset),
			}
			_, err := api.GetTransactionHashesByReference(context.Background(), args)
			if err == nil {
				t.Fatalf("expected error for offset=%d, got nil", tt.offset)
			}
			if err.Error() != tt.want {
				t.Fatalf("expected error %q, got %q", tt.want, err.Error())
			}
		})
	}
}

// TestGetTransactionHashesByReference_LimitExceedsMax verifies that limit > 100 is rejected.
func TestGetTransactionHashesByReference_LimitExceedsMax(t *testing.T) {
	api := &PublicMorphAPI{b: nil}
	ref := makeTestRef(0x02)

	args := rpc.ReferenceQueryArgs{
		Reference: ref,
		Limit:     uint64Ptr(101),
	}
	_, err := api.GetTransactionHashesByReference(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for limit=101, got nil")
	}
	if err.Error() != "limit exceeds maximum value of 100" {
		t.Fatalf("unexpected error: %s", err.Error())
	}
}

// TestGetTransactionHashesByReference_OffsetBoundary verifies boundary values.
func TestGetTransactionHashesByReference_OffsetBoundary(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	api := NewPublicMorphAPI(&mockMorphBackend{db: db})
	ref := makeTestRef(0x03)

	tests := []struct {
		name      string
		offset    *hexutil.Uint64
		limit     *hexutil.Uint64
		expectErr bool
	}{
		{"offset 0 (default)", nil, nil, false},
		{"offset 10000 (max allowed)", uint64Ptr(10000), uint64Ptr(1), false},
		{"offset 10001 (over max)", uint64Ptr(10001), uint64Ptr(1), true},
		{"offset 9999 limit 100", uint64Ptr(9999), uint64Ptr(100), false},
		{"limit 0", nil, uint64Ptr(0), false},
		{"limit 100 (max allowed)", nil, uint64Ptr(100), false},
		{"limit 101 (over max)", nil, uint64Ptr(101), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := rpc.ReferenceQueryArgs{
				Reference: ref,
				Offset:    tt.offset,
				Limit:     tt.limit,
			}
			_, err := api.GetTransactionHashesByReference(context.Background(), args)
			if tt.expectErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Fatalf("expected no error, got: %s", err.Error())
			}
		})
	}
}

// TestGetTransactionHashesByReference_EmptyDB verifies that valid params on an empty DB return nil.
func TestGetTransactionHashesByReference_EmptyDB(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	api := NewPublicMorphAPI(&mockMorphBackend{db: db})
	ref := makeTestRef(0x04)

	args := rpc.ReferenceQueryArgs{
		Reference: ref,
		Offset:    uint64Ptr(0),
		Limit:     uint64Ptr(100),
	}
	result, err := api.GetTransactionHashesByReference(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %s", err.Error())
	}
	if result != nil {
		t.Fatalf("expected nil result for empty db, got %d entries", len(result))
	}
}

// --- setDefaults version-heuristic tests ---

// mockSetDefaultsBackend implements the Backend methods required by setDefaults
// when Gas, Nonce, MaxFeePerGas, and MaxPriorityFeePerGas are pre-filled.
type mockSetDefaultsBackend struct {
	Backend     // embed — unimplemented methods panic if called
	chainConfig *params.ChainConfig
	header      *types.Header
}

func (m *mockSetDefaultsBackend) CurrentHeader() *types.Header     { return m.header }
func (m *mockSetDefaultsBackend) ChainConfig() *params.ChainConfig { return m.chainConfig }

func uint16VersionPtr(v uint8) *hexutil.Uint16 {
	h := hexutil.Uint16(v)
	return &h
}

type estimateGasChainContext struct{}

func (estimateGasChainContext) Engine() consensus.Engine { return nil }

func (estimateGasChainContext) GetHeader(common.Hash, uint64) *types.Header { return nil }

// estimateGasBackend is a minimal Backend that can run setDefaults -> DoEstimateGas
// against an in-memory state without spinning up a full blockchain.
type estimateGasBackend struct {
	Backend
	chainConfig *params.ChainConfig
	header      *types.Header
	block       *types.Block
	state       *state.StateDB
	gasCap      uint64
}

func newEstimateGasBackend(t *testing.T, sender common.Address) *estimateGasBackend {
	t.Helper()

	db := rawdb.NewMemoryDatabase()
	statedb, err := state.New(common.Hash{}, state.NewDatabase(db), nil)
	if err != nil {
		t.Fatalf("failed to create state db: %v", err)
	}
	statedb.SetBalance(sender, new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil), tracing.BalanceChangeUnspecified)

	header := &types.Header{
		Number:     big.NewInt(1),
		Time:       0,
		GasLimit:   30_000_000,
		BaseFee:    big.NewInt(1),
		Difficulty: big.NewInt(0),
	}
	return &estimateGasBackend{
		chainConfig: params.TestNoL1DataFeeChainConfig,
		header:      header,
		block:       types.NewBlockWithHeader(header),
		state:       statedb,
		gasCap:      header.GasLimit,
	}
}

func (m *estimateGasBackend) CurrentHeader() *types.Header { return types.CopyHeader(m.header) }

func (m *estimateGasBackend) ChainConfig() *params.ChainConfig { return m.chainConfig }

func (m *estimateGasBackend) RPCGasCap() uint64 { return m.gasCap }

func (m *estimateGasBackend) BlockByNumberOrHash(context.Context, rpc.BlockNumberOrHash) (*types.Block, error) {
	return m.block, nil
}

func (m *estimateGasBackend) StateAndHeaderByNumberOrHash(context.Context, rpc.BlockNumberOrHash) (*state.StateDB, *types.Header, error) {
	return m.state.Copy(), types.CopyHeader(m.header), nil
}

func (m *estimateGasBackend) GetEVM(ctx context.Context, msg core.Message, statedb *state.StateDB, header *types.Header, vmConfig *vm.Config) (*vm.EVM, func() error, error) {
	author := common.Address{}
	blockCtx := core.NewEVMBlockContext(header, estimateGasChainContext{}, m.chainConfig, &author)
	txCtx := core.NewEVMTxContext(msg)
	return vm.NewEVM(blockCtx, txCtx, statedb, m.chainConfig, *vmConfig), func() error { return nil }, nil
}

func makeAuthorizationList(count int) []types.SetCodeAuthorization {
	auths := make([]types.SetCodeAuthorization, count)
	for i := range auths {
		auths[i] = types.SetCodeAuthorization{
			Address: common.Address{byte(i + 1)},
			Nonce:   uint64(i),
		}
	}
	return auths
}

func TestSetDefaultsEstimateGasIncludesAuthorizationList(t *testing.T) {
	sender := common.HexToAddress("0x1000000000000000000000000000000000000001")
	to := common.HexToAddress("0x2000000000000000000000000000000000000002")
	backend := newEstimateGasBackend(t, sender)

	nonce := hexutil.Uint64(0)
	maxFee := (*hexutil.Big)(big.NewInt(10))
	tip := (*hexutil.Big)(big.NewInt(1))

	tests := []struct {
		name  string
		auths []types.SetCodeAuthorization
		want  uint64
	}{
		{
			name: "legacy call without authorization list",
			want: params.TxGas,
		},
		{
			name:  "set code with one authorization",
			auths: makeAuthorizationList(1),
			want:  params.TxGas + params.CallNewAccountGas,
		},
		{
			name:  "set code with two authorizations",
			auths: makeAuthorizationList(2),
			want:  params.TxGas + 2*params.CallNewAccountGas,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := TransactionArgs{
				From:                 &sender,
				To:                   &to,
				Nonce:                &nonce,
				MaxFeePerGas:         maxFee,
				MaxPriorityFeePerGas: tip,
				AuthorizationList:    tt.auths,
			}
			if err := args.setDefaults(context.Background(), backend); err != nil {
				t.Fatalf("setDefaults failed: %v", err)
			}
			if args.Gas == nil {
				t.Fatal("expected gas to be populated")
			}
			if have := uint64(*args.Gas); have != tt.want {
				t.Fatalf("estimated gas mismatch: have %d want %d", have, tt.want)
			}
		})
	}
}

// TestSetDefaults_MorphTxVersionHeuristic tests the heuristic version defaulting logic
// in TransactionArgs.setDefaults():
//   - Version == nil + no V1 fields → V0
//   - Version == nil + Reference or Memo present → V1
//   - Explicit Version → use as-is
//   - Before jade fork: V1-specific fields rejected; default is V0
//   - After jade fork: V1 fields allowed; heuristic picks V1 when present
func TestSetDefaults_MorphTxVersionHeuristic(t *testing.T) {
	to := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	ref := common.HexToReference("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	emptyRef := common.Reference{}
	memo := hexutil.Bytes([]byte("test memo"))
	emptyMemo := hexutil.Bytes([]byte{})

	jadeForkTime := uint64(1000)

	// Helper to build a backend with jade fork at time 1000
	makeBackend := func(headTime uint64) *mockSetDefaultsBackend {
		return &mockSetDefaultsBackend{
			chainConfig: &params.ChainConfig{
				ChainID:      big.NewInt(1),
				CurieBlock:   big.NewInt(0), // IsCurie = true so EIP-1559 path is used
				JadeForkTime: &jadeForkTime,
			},
			header: &types.Header{
				Number:  big.NewInt(1),
				Time:    headTime,
				BaseFee: big.NewInt(100),
			},
		}
	}

	// Common base args that avoid deep mocking (Gas, Nonce, gas fees pre-filled)
	gas := hexutil.Uint64(21000)
	nonce := hexutil.Uint64(0)
	maxFee := (*hexutil.Big)(big.NewInt(100))
	tip := (*hexutil.Big)(big.NewInt(1))

	baseArgs := func() TransactionArgs {
		return TransactionArgs{
			To:                   &to,
			Gas:                  &gas,
			Nonce:                &nonce,
			MaxFeePerGas:         maxFee,
			MaxPriorityFeePerGas: tip,
		}
	}

	tests := []struct {
		name        string
		headTime    uint64 // head.Time (jade fork at 1000)
		modify      func(args *TransactionArgs)
		wantVersion *uint16 // nil means version field should not be set (non-MorphTx)
		wantErr     bool
	}{
		// === After jade fork (headTime >= 1000) ===
		{
			name:     "jade fork: FeeTokenID only → V0",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
			},
			wantVersion: uint16Ref(types.MorphTxVersion0),
		},
		{
			name:     "jade fork: FeeTokenID + Reference → V1",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
				args.Reference = &ref
			},
			wantVersion: uint16Ref(types.MorphTxVersion1),
		},
		{
			name:     "jade fork: FeeTokenID + Memo → V1",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
				args.Memo = &memo
			},
			wantVersion: uint16Ref(types.MorphTxVersion1),
		},
		{
			name:     "jade fork: Reference only → V1",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				args.Reference = &ref
			},
			wantVersion: uint16Ref(types.MorphTxVersion1),
		},
		{
			name:     "jade fork: Memo only → V1",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				args.Memo = &memo
			},
			wantVersion: uint16Ref(types.MorphTxVersion1),
		},
		{
			name:     "jade fork: empty Reference + FeeTokenID → V0",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
				args.Reference = &emptyRef
			},
			wantVersion: uint16Ref(types.MorphTxVersion0),
		},
		{
			name:     "jade fork: empty Memo + FeeTokenID → V0",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
				args.Memo = &emptyMemo
			},
			wantVersion: uint16Ref(types.MorphTxVersion0),
		},
		{
			name:     "jade fork: explicit V0 → V0",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
				args.Version = uint16VersionPtr(types.MorphTxVersion0)
			},
			wantVersion: uint16Ref(types.MorphTxVersion0),
		},
		{
			name:     "jade fork: explicit V1 → V1",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				args.Version = uint16VersionPtr(types.MorphTxVersion1)
			},
			wantVersion: uint16Ref(types.MorphTxVersion1),
		},
		{
			name:        "jade fork: no MorphTx fields → not MorphTx (version nil)",
			headTime:    1000,
			modify:      func(args *TransactionArgs) {},
			wantVersion: nil,
		},

		// === Before jade fork (headTime < 1000) ===
		{
			name:     "pre-jade: FeeTokenID only → V0",
			headTime: 500,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
			},
			wantVersion: uint16Ref(types.MorphTxVersion0),
		},
		{
			name:     "pre-jade: explicit V1 → rejected",
			headTime: 500,
			modify: func(args *TransactionArgs) {
				args.Version = uint16VersionPtr(types.MorphTxVersion1)
			},
			wantErr: true,
		},
		{
			name:     "pre-jade: Reference → rejected (V1-only field before fork)",
			headTime: 500,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
				args.Reference = &ref
			},
			wantErr: true,
		},
		{
			name:     "pre-jade: Memo → rejected (V1-only field before fork)",
			headTime: 500,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
				args.Memo = &memo
			},
			wantErr: true,
		},
		{
			name:     "pre-jade: explicit V0 + FeeTokenID → V0 (ok)",
			headTime: 500,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
				args.Version = uint16VersionPtr(types.MorphTxVersion0)
			},
			wantVersion: uint16Ref(types.MorphTxVersion0),
		},

		// === validateMorphTxVersion rejection cases ===
		{
			name:     "jade fork: explicit V0 + FeeTokenID=0 → rejected",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(0)
				args.FeeTokenID = &fid
				args.Version = uint16VersionPtr(types.MorphTxVersion0)
			},
			wantErr: true,
		},
		{
			name:     "jade fork: explicit V1 + FeeTokenID=0 + FeeLimit>0 → rejected",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(0)
				args.FeeTokenID = &fid
				args.FeeLimit = (*hexutil.Big)(big.NewInt(1000))
				args.Version = uint16VersionPtr(types.MorphTxVersion1)
			},
			wantErr: true,
		},
		{
			name:     "jade fork: auto V1 (Reference) + FeeTokenID=0 + FeeLimit>0 → rejected",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(0)
				args.FeeTokenID = &fid
				args.FeeLimit = (*hexutil.Big)(big.NewInt(1000))
				args.Reference = &ref
			},
			wantErr: true,
		},
		{
			name:     "jade fork: explicit V1 + FeeTokenID=0 + FeeLimit=nil → ok",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				args.Version = uint16VersionPtr(types.MorphTxVersion1)
			},
			wantVersion: uint16Ref(types.MorphTxVersion1),
		},
		{
			name:     "jade fork: explicit V1 + FeeTokenID=0 + FeeLimit=0 → ok",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(0)
				args.FeeTokenID = &fid
				args.FeeLimit = (*hexutil.Big)(big.NewInt(0))
				args.Version = uint16VersionPtr(types.MorphTxVersion1)
			},
			wantVersion: uint16Ref(types.MorphTxVersion1),
		},
		{
			name:     "jade fork: auto V0 (FeeTokenID=1) + FeeLimit>0 → ok (V0 ignores FeeLimit)",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
				args.FeeLimit = (*hexutil.Big)(big.NewInt(1000))
			},
			wantVersion: uint16Ref(types.MorphTxVersion0),
		},
		{
			name:     "jade fork: explicit V1 + FeeTokenID>0 + FeeLimit>0 → ok",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				fid := hexutil.Uint16(1)
				args.FeeTokenID = &fid
				args.FeeLimit = (*hexutil.Big)(big.NewInt(1000))
				args.Version = uint16VersionPtr(types.MorphTxVersion1)
			},
			wantVersion: uint16Ref(types.MorphTxVersion1),
		},
		{
			name:     "jade fork: unsupported version 99 → rejected",
			headTime: 1000,
			modify: func(args *TransactionArgs) {
				args.Version = uint16VersionPtr(99)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := makeBackend(tt.headTime)
			args := baseArgs()
			tt.modify(&args)

			err := args.setDefaults(context.Background(), backend)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantVersion == nil {
				// Not a MorphTx — Version should remain nil
				if args.Version != nil {
					t.Errorf("expected Version to be nil (non-MorphTx), got %d", *args.Version)
				}
			} else {
				if args.Version == nil {
					t.Fatalf("expected Version = %d, got nil", *tt.wantVersion)
				}
				if uint16(*args.Version) != *tt.wantVersion {
					t.Errorf("Version: got %d, want %d", *args.Version, *tt.wantVersion)
				}
			}
		})
	}
}

func uint16Ref(v uint8) *uint16 {
	u := uint16(v)
	return &u
}

// --- marshalReceipt field-presence tests ---

// mockReceiptBackend implements just ChainConfig() for marshalReceipt tests.
// IsCurie returns false so marshalReceipt takes the simpler gasPrice path
// and does not call HeaderByHash.
type mockReceiptBackend struct {
	Backend // embed — unimplemented methods panic if called
}

func (m *mockReceiptBackend) ChainConfig() *params.ChainConfig {
	return &params.ChainConfig{
		ChainID: big.NewInt(1),
		// CurieBlock is nil → IsCurie returns false
	}
}

// signTx is a helper that signs a transaction with the given key and signer.
func signTx(t *testing.T, key *ecdsa.PrivateKey, signer types.Signer, inner types.TxData) *types.Transaction {
	t.Helper()
	tx, err := types.SignNewTx(key, signer, inner)
	if err != nil {
		t.Fatalf("failed to sign tx: %v", err)
	}
	return tx
}

// TestMarshalReceipt_FieldPresence verifies that all MorphTx-specific fields
// are always present in marshalled receipts regardless of tx type:
//   - l1Fee, feeRate, tokenScale, feeTokenID, feeLimit, version, reference, memo
//     are unconditionally included (values may be nil/zero for non-MorphTx).
func TestMarshalReceipt_FieldPresence(t *testing.T) {
	key, _ := crypto.GenerateKey()
	testAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	morphSigner := types.NewEmeraldSigner(big.NewInt(1))
	londonSigner := types.NewLondonSigner(big.NewInt(1))

	ref := common.HexToReference("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	memo := []byte("test memo")
	feeTokenID := uint16(1)
	feeRate := big.NewInt(500)
	tokenScale := big.NewInt(1000)

	tests := []struct {
		name string
		tx   *types.Transaction
		// receipt fields to populate
		receiptFeeTokenID *uint16
		receiptFeeRate    *big.Int
		receiptTokenScale *big.Int
		receiptFeeLimit   *big.Int
		receiptVersion    uint8
		receiptReference  *common.Reference
		receiptMemo       *[]byte
	}{
		{
			name: "DynamicFeeTx (non-MorphTx)",
			tx: signTx(t, key, londonSigner, &types.DynamicFeeTx{
				ChainID:   big.NewInt(1),
				Nonce:     0,
				GasTipCap: big.NewInt(1),
				GasFeeCap: big.NewInt(10),
				Gas:       21000,
				To:        &testAddr,
				Value:     big.NewInt(1),
			}),
		},
		{
			name: "MorphTx V0",
			tx: signTx(t, key, morphSigner, &types.MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      1,
				GasTipCap:  big.NewInt(1),
				GasFeeCap:  big.NewInt(10),
				Gas:        21000,
				To:         &testAddr,
				Value:      big.NewInt(1),
				FeeTokenID: 1,
				FeeLimit:   big.NewInt(1000),
				Version:    types.MorphTxVersion0,
			}),
			receiptFeeTokenID: &feeTokenID,
			receiptFeeRate:    feeRate,
			receiptTokenScale: tokenScale,
			receiptFeeLimit:   big.NewInt(1000),
			receiptVersion:    types.MorphTxVersion0,
		},
		{
			name: "MorphTx V1",
			tx: signTx(t, key, morphSigner, &types.MorphTx{
				ChainID:    big.NewInt(1),
				Nonce:      2,
				GasTipCap:  big.NewInt(1),
				GasFeeCap:  big.NewInt(10),
				Gas:        21000,
				To:         &testAddr,
				Value:      big.NewInt(1),
				FeeTokenID: 0,
				FeeLimit:   big.NewInt(0),
				Version:    types.MorphTxVersion1,
				Reference:  &ref,
				Memo:       &memo,
			}),
			receiptFeeRate:    feeRate,
			receiptTokenScale: tokenScale,
			receiptFeeLimit:   big.NewInt(0),
			receiptVersion:    types.MorphTxVersion1,
			receiptReference:  &ref,
			receiptMemo:       &memo,
		},
	}

	backend := &mockReceiptBackend{}
	blockHash := common.HexToHash("0xdeadbeef")
	bigblock := big.NewInt(1)

	// All MorphTx-related fields should always be present in the result map
	allMorphFields := []string{"l1Fee", "feeRate", "tokenScale", "feeTokenID", "feeLimit", "version", "reference", "memo"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := &types.Receipt{
				Status:     types.ReceiptStatusSuccessful,
				GasUsed:    21000,
				FeeTokenID: tt.receiptFeeTokenID,
				FeeRate:    tt.receiptFeeRate,
				TokenScale: tt.receiptTokenScale,
				FeeLimit:   tt.receiptFeeLimit,
				Version:    tt.receiptVersion,
				Reference:  tt.receiptReference,
				Memo:       tt.receiptMemo,
			}

			signer := types.NewEmeraldSigner(big.NewInt(1))
			fields, err := marshalReceipt(context.Background(), backend, receipt, bigblock, blockHash, 1, signer, tt.tx, 0)
			if err != nil {
				t.Fatalf("marshalReceipt error: %v", err)
			}

			// All MorphTx fields should always be present regardless of tx type
			for _, field := range allMorphFields {
				if _, exists := fields[field]; !exists {
					t.Errorf("expected field %q to always be present for %s, but it was absent", field, tt.name)
				}
			}
		})
	}
}

type txSyncTestBackend struct {
	Backend
	feed             event.Feed
	chainConfig      *params.ChainConfig
	syncEnabled      bool
	defaultTimeout   time.Duration
	maxTimeout       time.Duration
	sendEvent        bool
	mineOnSend       bool
	failHeaderByHash bool
	minedBlock       *types.Block
	minedReceipts    types.Receipts
	minedTx          *types.Transaction
}

func newTxSyncTestBackend(sendEvent bool) *txSyncTestBackend {
	return &txSyncTestBackend{
		chainConfig:    &params.ChainConfig{ChainID: big.NewInt(1), EIP155Block: big.NewInt(0)},
		syncEnabled:    true,
		defaultTimeout: time.Second,
		maxTimeout:     time.Second,
		sendEvent:      sendEvent,
	}
}

func (b *txSyncTestBackend) ChainConfig() *params.ChainConfig { return b.chainConfig }
func (b *txSyncTestBackend) RPCTxFeeCap() float64             { return 0 }
func (b *txSyncTestBackend) UnprotectedAllowed() bool         { return true }
func (b *txSyncTestBackend) RPCTxSyncDefaultTimeout() time.Duration {
	return b.defaultTimeout
}
func (b *txSyncTestBackend) RPCTxSyncMaxTimeout() time.Duration { return b.maxTimeout }
func (b *txSyncTestBackend) RPCTxSyncEnabled() bool             { return b.syncEnabled }
func (b *txSyncTestBackend) CurrentBlock() *types.Block {
	return types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1), Time: 1})
}
func (b *txSyncTestBackend) SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription {
	return b.feed.Subscribe(ch)
}
func (b *txSyncTestBackend) SendTx(ctx context.Context, tx *types.Transaction) error {
	header := &types.Header{Number: big.NewInt(1), Time: 1}
	receipt := &types.Receipt{
		Status:            types.ReceiptStatusSuccessful,
		TxHash:            tx.Hash(),
		TransactionIndex:  0,
		GasUsed:           21000,
		CumulativeGasUsed: 21000,
	}
	receipts := types.Receipts{receipt}
	block := types.NewBlock(header, types.Transactions{tx}, nil, receipts, trie.NewStackTrie(nil))
	receipt.BlockHash = block.Hash()
	receipt.BlockNumber = block.Number()
	if b.sendEvent {
		b.feed.Send(core.ChainEvent{
			Hash:         block.Hash(),
			Header:       block.Header(),
			Receipts:     receipts,
			Transactions: types.Transactions{tx},
		})
	}
	if b.mineOnSend {
		b.minedBlock = block
		b.minedReceipts = receipts
		b.minedTx = tx
	}
	return nil
}
func (b *txSyncTestBackend) GetTransaction(ctx context.Context, txHash common.Hash) (*types.Transaction, common.Hash, uint64, uint64, error) {
	if b.minedTx != nil && b.minedTx.Hash() == txHash {
		return b.minedTx, b.minedBlock.Hash(), b.minedBlock.NumberU64(), 0, nil
	}
	return nil, common.Hash{}, 0, 0, errors.New("not found")
}
func (b *txSyncTestBackend) GetReceipts(ctx context.Context, hash common.Hash) (types.Receipts, error) {
	if b.minedBlock != nil && b.minedBlock.Hash() == hash {
		return b.minedReceipts, nil
	}
	return nil, nil
}
func (b *txSyncTestBackend) HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error) {
	if b.failHeaderByHash {
		return nil, errors.New("header temporarily unavailable")
	}
	if b.minedBlock != nil && b.minedBlock.Hash() == hash {
		return b.minedBlock.Header(), nil
	}
	return nil, nil
}

func makeTxSyncRaw(t *testing.T) (hexutil.Bytes, *types.Transaction) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	to := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	tx := signTx(t, key, types.NewEIP155Signer(big.NewInt(1)), &types.LegacyTx{
		Nonce:    0,
		GasPrice: big.NewInt(1),
		Gas:      21000,
		To:       &to,
		Value:    big.NewInt(1),
	})
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatalf("failed to marshal tx: %v", err)
	}
	return raw, tx
}

func TestSendRawTransactionSyncDisabled(t *testing.T) {
	raw, _ := makeTxSyncRaw(t)
	backend := newTxSyncTestBackend(false)
	backend.syncEnabled = false
	api := NewPublicTransactionPoolAPI(backend, nil)
	if _, err := api.SendRawTransactionSync(context.Background(), raw, nil); err == nil || err.Error() != "eth_sendRawTransactionSync is disabled" {
		t.Fatalf("unexpected disabled error: %v", err)
	}
}

func TestSendRawTransactionSyncEventSuccess(t *testing.T) {
	raw, tx := makeTxSyncRaw(t)
	api := NewPublicTransactionPoolAPI(newTxSyncTestBackend(true), nil)
	receipt, err := api.SendRawTransactionSync(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("SendRawTransactionSync failed: %v", err)
	}
	if got := receipt["transactionHash"]; got != tx.Hash() {
		t.Fatalf("unexpected transaction hash: got %v want %v", got, tx.Hash())
	}
	if _, ok := receipt["memo"]; !ok {
		t.Fatalf("expected Morph receipt field memo to be present")
	}
}

func TestSendRawTransactionSyncFastReceiptSuccess(t *testing.T) {
	raw, tx := makeTxSyncRaw(t)
	backend := newTxSyncTestBackend(false)
	backend.mineOnSend = true
	api := NewPublicTransactionPoolAPI(backend, nil)

	receipt, err := api.SendRawTransactionSync(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("SendRawTransactionSync failed: %v", err)
	}
	if got := receipt["transactionHash"]; got != tx.Hash() {
		t.Fatalf("unexpected transaction hash: got %v want %v", got, tx.Hash())
	}
	if got := receipt["blockNumber"]; got != hexutil.Uint64(1) {
		t.Fatalf("unexpected block number: got %v want %v", got, hexutil.Uint64(1))
	}
}

func TestSendRawTransactionSyncFastReceiptErrorFallsBackToEvent(t *testing.T) {
	raw, tx := makeTxSyncRaw(t)
	backend := newTxSyncTestBackend(true)
	backend.mineOnSend = true
	backend.failHeaderByHash = true
	api := NewPublicTransactionPoolAPI(backend, nil)

	receipt, err := api.SendRawTransactionSync(context.Background(), raw, nil)
	if err != nil {
		t.Fatalf("SendRawTransactionSync failed: %v", err)
	}
	if got := receipt["transactionHash"]; got != tx.Hash() {
		t.Fatalf("unexpected transaction hash: got %v want %v", got, tx.Hash())
	}
}

func TestSendRawTransactionSyncTimeout(t *testing.T) {
	raw, tx := makeTxSyncRaw(t)
	backend := newTxSyncTestBackend(false)
	backend.defaultTimeout = 5 * time.Millisecond
	backend.maxTimeout = 5 * time.Millisecond
	api := NewPublicTransactionPoolAPI(backend, nil)
	_, err := api.SendRawTransactionSync(context.Background(), raw, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var dataErr interface {
		ErrorCode() int
		ErrorData() interface{}
	}
	if !errors.As(err, &dataErr) {
		t.Fatalf("expected structured timeout error, got %T %v", err, err)
	}
	if dataErr.ErrorCode() != 4 {
		t.Fatalf("unexpected timeout error code: %d", dataErr.ErrorCode())
	}
	if got, want := dataErr.ErrorData(), tx.Hash().Hex(); got != want {
		t.Fatalf("unexpected timeout data: got %v want %v", got, want)
	}
}

type blockReceiptsTestBackend struct {
	Backend
	chainConfig     *params.ChainConfig
	pendingBlock    *types.Block
	pendingReceipts types.Receipts
	blocksByHash    map[common.Hash]*types.Block
	blocksByNumber  map[uint64]*types.Block
	receiptsByHash  map[common.Hash]types.Receipts
	latestNumber    uint64
}

func (b *blockReceiptsTestBackend) ChainConfig() *params.ChainConfig {
	if b.chainConfig != nil {
		return b.chainConfig
	}
	return &params.ChainConfig{
		ChainID:        big.NewInt(1),
		HomesteadBlock: big.NewInt(0),
		EIP155Block:    big.NewInt(0),
		EIP158Block:    big.NewInt(0),
		ByzantiumBlock: big.NewInt(0),
		LondonBlock:    big.NewInt(0),
	}
}

func (b *blockReceiptsTestBackend) Pending() (*types.Block, types.Receipts, *state.StateDB) {
	return b.pendingBlock, b.pendingReceipts, nil
}

func (b *blockReceiptsTestBackend) BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Block, error) {
	if hash, ok := blockNrOrHash.Hash(); ok {
		return b.blocksByHash[hash], nil
	}
	if number, ok := blockNrOrHash.Number(); ok {
		switch number {
		case rpc.EarliestBlockNumber:
			return b.blocksByNumber[0], nil
		case rpc.LatestBlockNumber:
			return b.blocksByNumber[b.latestNumber], nil
		default:
			if number < 0 {
				return nil, nil
			}
			return b.blocksByNumber[uint64(number)], nil
		}
	}
	return nil, nil
}

func (b *blockReceiptsTestBackend) GetReceipts(ctx context.Context, hash common.Hash) (types.Receipts, error) {
	return b.receiptsByHash[hash], nil
}

func makeReceiptBlock(t *testing.T) (*types.Block, types.Receipts, *types.Transaction) {
	t.Helper()
	_, tx := makeTxSyncRaw(t)
	receipt := &types.Receipt{
		Status:            types.ReceiptStatusSuccessful,
		TxHash:            tx.Hash(),
		GasUsed:           21000,
		CumulativeGasUsed: 21000,
	}
	receipts := types.Receipts{receipt}
	block := types.NewBlock(&types.Header{Number: big.NewInt(7), Time: 1}, []*types.Transaction{tx}, nil, receipts, trie.NewStackTrie(nil))
	return block, receipts, tx
}

func newBlockReceiptsTestBackend() *blockReceiptsTestBackend {
	return &blockReceiptsTestBackend{
		blocksByHash:   make(map[common.Hash]*types.Block),
		blocksByNumber: make(map[uint64]*types.Block),
		receiptsByHash: make(map[common.Hash]types.Receipts),
	}
}

func (b *blockReceiptsTestBackend) addBlock(block *types.Block, receipts types.Receipts) {
	b.blocksByHash[block.Hash()] = block
	b.blocksByNumber[block.NumberU64()] = block
	b.receiptsByHash[block.Hash()] = receipts
	if block.NumberU64() >= b.latestNumber {
		b.latestNumber = block.NumberU64()
	}
}

func makeBlockReceiptTestBlock(number uint64, txs types.Transactions, receipts types.Receipts) *types.Block {
	header := &types.Header{
		Number:   new(big.Int).SetUint64(number),
		Time:     number + 1,
		GasLimit: 30_000_000,
		BaseFee:  big.NewInt(1),
	}
	block := types.NewBlock(header, txs, nil, receipts, trie.NewStackTrie(nil))
	for i, receipt := range receipts {
		receipt.TxHash = txs[i].Hash()
		receipt.BlockHash = block.Hash()
		receipt.BlockNumber = block.Number()
		receipt.TransactionIndex = uint(i)
	}
	return block
}

func TestPendingBlockReceipts(t *testing.T) {
	block, receipts, tx := makeReceiptBlock(t)
	api := &PublicBlockChainAPI{b: &blockReceiptsTestBackend{pendingBlock: block, pendingReceipts: receipts}}

	result, err := api.GetBlockReceipts(context.Background(), rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber))
	if err != nil {
		t.Fatalf("GetBlockReceipts pending failed: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 receipt, got %d", len(result))
	}
	if got := result[0]["transactionHash"]; got != tx.Hash() {
		t.Fatalf("unexpected transaction hash: got %v want %v", got, tx.Hash())
	}
}

func TestBlockReceiptsByHashNumberAndTransactionTypes(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	to := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	legacySigner := types.NewEIP155Signer(big.NewInt(1))
	londonSigner := types.NewLondonSigner(big.NewInt(1))

	emptyBlock := makeBlockReceiptTestBlock(0, nil, nil)
	legacyTx := signTx(t, key, legacySigner, &types.LegacyTx{
		Nonce:    0,
		GasPrice: big.NewInt(7),
		Gas:      21000,
		To:       &to,
		Value:    big.NewInt(1),
	})
	legacyReceipts := types.Receipts{{
		Status:            types.ReceiptStatusSuccessful,
		GasUsed:           21000,
		CumulativeGasUsed: 21000,
	}}
	legacyBlock := makeBlockReceiptTestBlock(1, types.Transactions{legacyTx}, legacyReceipts)
	createTx := signTx(t, key, legacySigner, &types.LegacyTx{
		Nonce:    1,
		GasPrice: big.NewInt(7),
		Gas:      53000,
		Value:    big.NewInt(0),
	})
	createAddress := common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")
	createReceipts := types.Receipts{{
		Status:            types.ReceiptStatusSuccessful,
		GasUsed:           53000,
		CumulativeGasUsed: 53000,
		ContractAddress:   createAddress,
	}}
	createBlock := makeBlockReceiptTestBlock(2, types.Transactions{createTx}, createReceipts)
	dynamicTx := signTx(t, key, londonSigner, &types.DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     2,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(10),
		Gas:       21000,
		To:        &to,
		Value:     big.NewInt(3),
	})
	dynamicReceipts := types.Receipts{{
		Status:            types.ReceiptStatusSuccessful,
		GasUsed:           21000,
		CumulativeGasUsed: 21000,
	}}
	dynamicBlock := makeBlockReceiptTestBlock(3, types.Transactions{dynamicTx}, dynamicReceipts)

	backend := newBlockReceiptsTestBackend()
	backend.addBlock(emptyBlock, nil)
	backend.addBlock(legacyBlock, legacyReceipts)
	backend.addBlock(createBlock, createReceipts)
	backend.addBlock(dynamicBlock, dynamicReceipts)
	api := &PublicBlockChainAPI{b: backend}
	tests := []struct {
		name     string
		query    rpc.BlockNumberOrHash
		wantLen  int
		wantHash common.Hash
		wantAddr interface{}
	}{
		{name: "empty block by hash", query: rpc.BlockNumberOrHashWithHash(emptyBlock.Hash(), false), wantLen: 0},
		{name: "legacy tx by number", query: rpc.BlockNumberOrHashWithNumber(1), wantLen: 1, wantHash: legacyTx.Hash()},
		{name: "contract create by hash", query: rpc.BlockNumberOrHashWithHash(createBlock.Hash(), false), wantLen: 1, wantHash: createTx.Hash(), wantAddr: createAddress},
		{name: "dynamic fee tx by latest", query: rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber), wantLen: 1, wantHash: dynamicTx.Hash()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := api.GetBlockReceipts(context.Background(), tt.query)
			if err != nil {
				t.Fatalf("GetBlockReceipts failed: %v", err)
			}
			if len(result) != tt.wantLen {
				t.Fatalf("expected %d receipts, got %d", tt.wantLen, len(result))
			}
			if tt.wantLen == 0 {
				return
			}
			if got := result[0]["transactionHash"]; got != tt.wantHash {
				t.Fatalf("unexpected transaction hash: got %v want %v", got, tt.wantHash)
			}
			if tt.wantAddr != nil && result[0]["contractAddress"] != tt.wantAddr {
				t.Fatalf("unexpected contract address: got %v want %v", result[0]["contractAddress"], tt.wantAddr)
			}
			if _, ok := result[0]["memo"]; !ok {
				t.Fatalf("expected Morph receipt field memo to be present")
			}
		})
	}
}

func TestPendingBlockReceiptsUnavailable(t *testing.T) {
	api := &PublicBlockChainAPI{b: &blockReceiptsTestBackend{}}
	_, err := api.GetBlockReceipts(context.Background(), rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber))
	if err == nil || err.Error() != "pending receipts is not available" {
		t.Fatalf("unexpected pending unavailable error: %v", err)
	}

	block, _, _ := makeReceiptBlock(t)
	api = &PublicBlockChainAPI{b: &blockReceiptsTestBackend{pendingBlock: block}}
	_, err = api.GetBlockReceipts(context.Background(), rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber))
	if err == nil || err.Error() != "pending receipts is not available" {
		t.Fatalf("unexpected pending receipts unavailable error: %v", err)
	}
}

func TestNonPendingBlockReceiptsNotFoundReturnsNull(t *testing.T) {
	api := &PublicBlockChainAPI{b: &blockReceiptsTestBackend{}}
	result, err := api.GetBlockReceipts(context.Background(), rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(123)))
	if err != nil {
		t.Fatalf("unexpected non-pending error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for missing non-pending block, got %#v", result)
	}
}
