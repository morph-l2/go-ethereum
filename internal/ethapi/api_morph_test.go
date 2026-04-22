package ethapi

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/morph-l2/go-ethereum/rpc"
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

func (m *mockSetDefaultsBackend) CurrentHeader() *types.Header    { return m.header }
func (m *mockSetDefaultsBackend) ChainConfig() *params.ChainConfig { return m.chainConfig }

func uint16VersionPtr(v uint8) *hexutil.Uint16 {
	h := hexutil.Uint16(v)
	return &h
}

type mockEstimateGasBackend struct {
	Backend
	chainConfig *params.ChainConfig
	header      *types.Header
	block       *types.Block
	state       *state.StateDB
}

func newMockEstimateGasBackend(t *testing.T, config *params.ChainConfig, headerTime uint64, gasLimit uint64) *mockEstimateGasBackend {
	t.Helper()

	db := rawdb.NewMemoryDatabase()
	sdb, err := state.New(common.Hash{}, state.NewDatabase(db), nil)
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	header := &types.Header{
		Number:   big.NewInt(1),
		Time:     headerTime,
		GasLimit: gasLimit,
		BaseFee:  big.NewInt(0),
	}
	return &mockEstimateGasBackend{
		chainConfig: config,
		header:      header,
		block:       types.NewBlockWithHeader(header),
		state:       sdb,
	}
}

func (m *mockEstimateGasBackend) ChainConfig() *params.ChainConfig { return m.chainConfig }
func (m *mockEstimateGasBackend) CurrentHeader() *types.Header     { return m.header }
func (m *mockEstimateGasBackend) HeaderByNumberOrHash(context.Context, rpc.BlockNumberOrHash) (*types.Header, error) {
	return m.header, nil
}
func (m *mockEstimateGasBackend) BlockByNumberOrHash(context.Context, rpc.BlockNumberOrHash) (*types.Block, error) {
	return m.block, nil
}
func (m *mockEstimateGasBackend) StateAndHeaderByNumberOrHash(context.Context, rpc.BlockNumberOrHash) (*state.StateDB, *types.Header, error) {
	return m.state.Copy(), m.header, nil
}
func (m *mockEstimateGasBackend) GetEVM(ctx context.Context, msg core.Message, state *state.StateDB, header *types.Header, vmConfig *vm.Config) (*vm.EVM, func() error, error) {
	if vmConfig == nil {
		vmConfig = new(vm.Config)
	}
	blockContext := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash:     func(uint64) common.Hash { return common.Hash{} },
		Coinbase:    common.Address{},
		GasLimit:    header.GasLimit,
		BlockNumber: header.Number,
		Time:        new(big.Int).SetUint64(header.Time),
		Difficulty:  big.NewInt(0),
		BaseFee:     header.BaseFee,
	}
	return vm.NewEVM(blockContext, core.NewEVMTxContext(msg), state, m.chainConfig, *vmConfig), state.Error, nil
}

func oversizedEstimateGasPayload() hexutil.Bytes {
	size := int((params.MaxTxGas-params.TxGas)/params.TxDataNonZeroGasEIP2028) + 1
	data := make([]byte, size)
	for i := range data {
		data[i] = 0x1
	}
	return hexutil.Bytes(data)
}

func TestDoEstimateGasCapsAtMaxTxGasPostAmsterdam(t *testing.T) {
	cfg := *params.TestNoL1DataFeeChainConfig
	cfg.AmsterdamTime = params.NewUint64(0)

	backend := newMockEstimateGasBackend(t, &cfg, 0, 30_000_000)
	to := common.HexToAddress("0x1")
	data := oversizedEstimateGasPayload()

	_, err := DoEstimateGas(context.Background(), backend, TransactionArgs{
		To:   &to,
		Data: &data,
	}, rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber), 0)
	if err == nil {
		t.Fatalf("expected estimateGas to fail once capped at MaxTxGas")
	}
	want := fmt.Sprintf("gas required exceeds allowance (%d)", params.MaxTxGas)
	if err.Error() != want {
		t.Fatalf("unexpected error: got %q want %q", err.Error(), want)
	}
}

func TestDoEstimateGasDoesNotCapPreAmsterdam(t *testing.T) {
	cfg := *params.TestNoL1DataFeeChainConfig
	cfg.AmsterdamTime = params.NewUint64(20)

	backend := newMockEstimateGasBackend(t, &cfg, 10, 30_000_000)
	to := common.HexToAddress("0x1")
	data := oversizedEstimateGasPayload()

	got, err := DoEstimateGas(context.Background(), backend, TransactionArgs{
		To:   &to,
		Data: &data,
	}, rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uint64(got) <= params.MaxTxGas {
		t.Fatalf("expected pre-Amsterdam estimate to exceed MaxTxGas, got %d", got)
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
			name:     "jade fork: no MorphTx fields → not MorphTx (version nil)",
			headTime: 1000,
			modify:   func(args *TransactionArgs) {},
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
			name:        "jade fork: explicit V1 + FeeTokenID=0 + FeeLimit=nil → ok",
			headTime:    1000,
			modify: func(args *TransactionArgs) {
				args.Version = uint16VersionPtr(types.MorphTxVersion1)
			},
			wantVersion: uint16Ref(types.MorphTxVersion1),
		},
		{
			name:        "jade fork: explicit V1 + FeeTokenID=0 + FeeLimit=0 → ok",
			headTime:    1000,
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
