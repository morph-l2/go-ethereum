// Copyright 2026 The go-ethereum Authors
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

package core

import (
	"crypto/ecdsa"
	"errors"
	"math/big"
	"testing"

	"github.com/holiman/uint256"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/params"
)

// TestIntrinsicGas exercises the pure IntrinsicGas function across the
// authorization-list pricing switch introduced by the Amsterdam fork (P1-3).
//
// The scenarios mirror the concrete comparison table from
// .vscode/Eezo_Shunt_Final.md § P1-3:
//   - Pre-Amsterdam: each authorization is charged CallNewAccountGas (25000).
//   - Post-Amsterdam: each authorization is charged TxAuthTupleGas (12500).
func TestIntrinsicGas(t *testing.T) {
	// A stable non-nil but empty auth list factory keeps the branches concise.
	authN := func(n int) []types.SetCodeAuthorization {
		return make([]types.SetCodeAuthorization, n)
	}
	tests := []struct {
		name                                                      string
		data                                                      []byte
		accessList                                                types.AccessList
		authList                                                  []types.SetCodeAuthorization
		isContractCreation, isHomestead, isEIP2028, isEIP3860     bool
		isAmsterdam                                               bool
		want                                                      uint64
	}{
		{
			name: "no auth, plain transfer",
			want: params.TxGas,
		},
		{
			name:        "no auth, contract creation (homestead)",
			isContractCreation: true, isHomestead: true,
			want: params.TxGasContractCreation,
		},
		{
			name:     "pre-fork: 1 authorization charged 25000",
			authList: authN(1),
			want:     params.TxGas + params.CallNewAccountGas,
		},
		{
			name:     "pre-fork: 10 authorizations charged 10 * 25000",
			authList: authN(10),
			want:     params.TxGas + 10*params.CallNewAccountGas,
		},
		{
			name:        "post-fork: 1 authorization charged 12500",
			authList:    authN(1),
			isAmsterdam: true,
			want:        params.TxGas + params.TxAuthTupleGas,
		},
		{
			name:        "post-fork: 10 authorizations charged 10 * 12500",
			authList:    authN(10),
			isAmsterdam: true,
			want:        params.TxGas + 10*params.TxAuthTupleGas,
		},
		{
			name:        "empty auth list treated as nil regardless of fork",
			authList:    nil,
			isAmsterdam: true,
			want:        params.TxGas,
		},
		{
			name:       "accessList + auth list are priced independently (pre-fork)",
			accessList: types.AccessList{{Address: common.Address{1}, StorageKeys: []common.Hash{{}}}},
			authList:   authN(2),
			want: params.TxGas +
				params.TxAccessListAddressGas +
				params.TxAccessListStorageKeyGas +
				2*params.CallNewAccountGas,
		},
		{
			name:        "accessList + auth list are priced independently (post-fork)",
			accessList:  types.AccessList{{Address: common.Address{1}, StorageKeys: []common.Hash{{}}}},
			authList:    authN(2),
			isAmsterdam: true,
			want: params.TxGas +
				params.TxAccessListAddressGas +
				params.TxAccessListStorageKeyGas +
				2*params.TxAuthTupleGas,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := IntrinsicGas(tc.data, tc.accessList, tc.authList, tc.isContractCreation, tc.isHomestead, tc.isEIP2028, tc.isEIP3860, tc.isAmsterdam)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("gas mismatch: got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestIntrinsicGasAuthListScalingPreVsPost documents the end-to-end economic
// intent of P1-3: with many authorizations pointed at existing accounts, the
// pre-fork model charges `CallNewAccountGas` per auth and depends on a StateDB
// refund that is capped by `gasUsed/5`; the post-fork model charges
// `TxAuthTupleGas` per auth directly and therefore cannot be truncated.
//
// This assertion does not exercise the refund cap (that lives in
// `refundGas`), but it does prove the intrinsic-gas component of the
// arithmetic used in the Eezo Shunt report.
func TestIntrinsicGasAuthListScalingPreVsPost(t *testing.T) {
	const n = 10
	authList := make([]types.SetCodeAuthorization, n)

	pre, err := IntrinsicGas(nil, nil, authList, false, false, true, true, false)
	if err != nil {
		t.Fatalf("pre-fork intrinsic: %v", err)
	}
	post, err := IntrinsicGas(nil, nil, authList, false, false, true, true, true)
	if err != nil {
		t.Fatalf("post-fork intrinsic: %v", err)
	}
	wantPre := uint64(params.TxGas + n*params.CallNewAccountGas)
	wantPost := uint64(params.TxGas + n*params.TxAuthTupleGas)
	if pre != wantPre {
		t.Fatalf("pre-fork gas: got %d, want %d", pre, wantPre)
	}
	if post != wantPost {
		t.Fatalf("post-fork gas: got %d, want %d", post, wantPost)
	}
	// Post-fork intrinsic is strictly smaller by exactly
	// n * (CallNewAccountGas - TxAuthTupleGas).
	diff := pre - post
	wantDiff := uint64(n) * (params.CallNewAccountGas - params.TxAuthTupleGas)
	if diff != wantDiff {
		t.Fatalf("delta: got %d, want %d", diff, wantDiff)
	}
}

// --- applyAuthorization tests --------------------------------------------------

// authTestEnv bundles a minimal StateDB + EVM + StateTransition, just enough to
// drive applyAuthorization directly without spinning up a full blockchain.
type authTestEnv struct {
	chainConfig *params.ChainConfig
	state       *state.StateDB
	evm         *vm.EVM
	st          *StateTransition
	authority   common.Address
	authKey     *ecdsa.PrivateKey
	auth        types.SetCodeAuthorization
}

// newAuthTestEnv builds an environment whose Rules report IsAmsterdam = isAmsterdam.
// If seedAuthority is true, the authority's account is created in state (nonce 0,
// balance 1 wei) so that st.state.Exist(authority) returns true.
func newAuthTestEnv(t *testing.T, isAmsterdam, seedAuthority bool) *authTestEnv {
	t.Helper()
	authKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen auth key: %v", err)
	}
	authority := crypto.PubkeyToAddress(authKey.PublicKey)

	cfg := &params.ChainConfig{
		ChainID:             big.NewInt(1),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		ArchimedesBlock:     big.NewInt(0),
		ShanghaiBlock:       big.NewInt(0),
		BernoulliBlock:      big.NewInt(0),
		CurieBlock:          big.NewInt(0),
		Morph203Time:        params.NewUint64(0),
		ViridianTime:        params.NewUint64(0),
		EmeraldTime:         params.NewUint64(0),
	}
	if isAmsterdam {
		cfg.AmsterdamTime = params.NewUint64(0)
	}
	db := rawdb.NewMemoryDatabase()
	sdb, err := state.New(common.Hash{}, state.NewDatabase(db), nil)
	if err != nil {
		t.Fatalf("new statedb: %v", err)
	}
	if seedAuthority {
		sdb.GetOrNewStateObject(authority)
		sdb.AddBalance(authority, big.NewInt(1), tracing.BalanceChangeUnspecified)
	}
	blockCtx := vm.BlockContext{
		CanTransfer: CanTransfer,
		Transfer:    Transfer,
		GetHash:     func(uint64) common.Hash { return common.Hash{} },
		Coinbase:    common.Address{},
		GasLimit:    10_000_000,
		BlockNumber: big.NewInt(1),
		Time:        big.NewInt(1),
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
	}
	evm := vm.NewEVM(blockCtx, vm.TxContext{}, sdb, cfg, vm.Config{})

	// Build a correctly signed authorization so validateAuthorization accepts it.
	auth := types.SetCodeAuthorization{
		ChainID: *uint256.MustFromBig(cfg.ChainID),
		Address: common.Address{0x42},
		Nonce:   0,
	}
	auth, err = types.SignSetCode(authKey, auth)
	if err != nil {
		t.Fatalf("sign auth: %v", err)
	}
	// Build a StateTransition with generous initial gas; individual tests set
	// st.gas explicitly where needed.
	st := &StateTransition{
		evm:        evm,
		state:      sdb,
		gas:        100_000,
		initialGas: 100_000,
	}
	return &authTestEnv{
		chainConfig: cfg,
		state:       sdb,
		evm:         evm,
		st:          st,
		authority:   authority,
		authKey:     authKey,
		auth:        auth,
	}
}

// TestApplyAuthorizationPreForkRefundOnExistingAccount: pre-Amsterdam, existing
// authority -> refund 12500 via StateDB refund counter; no st.gas debit.
func TestApplyAuthorizationPreForkRefundOnExistingAccount(t *testing.T) {
	env := newAuthTestEnv(t, false, true)
	gasBefore := env.st.gas
	if err := env.st.applyAuthorization(&env.auth, false); err != nil {
		t.Fatalf("applyAuthorization: %v", err)
	}
	if gasBefore != env.st.gas {
		t.Fatalf("pre-fork must not debit st.gas: before=%d after=%d", gasBefore, env.st.gas)
	}
	wantRefund := params.CallNewAccountGas - params.TxAuthTupleGas
	if got := env.state.GetRefund(); got != wantRefund {
		t.Fatalf("refund mismatch: got %d want %d", got, wantRefund)
	}
	if env.state.GetNonce(env.authority) != env.auth.Nonce+1 {
		t.Fatalf("authority nonce not bumped")
	}
	expectedCode := types.AddressToDelegation(env.auth.Address)
	if got := env.state.GetCode(env.authority); string(got) != string(expectedCode) {
		t.Fatalf("delegation code not installed")
	}
}

// TestApplyAuthorizationPreForkNoRefundOnNewAccount: pre-Amsterdam, non-existing
// authority -> no refund, no st.gas debit, but delegation is still installed.
// The authority account materializes as a side-effect of SetNonce/SetCode.
func TestApplyAuthorizationPreForkNoRefundOnNewAccount(t *testing.T) {
	env := newAuthTestEnv(t, false, false)
	gasBefore := env.st.gas
	if err := env.st.applyAuthorization(&env.auth, false); err != nil {
		t.Fatalf("applyAuthorization: %v", err)
	}
	if gasBefore != env.st.gas {
		t.Fatalf("pre-fork must not debit st.gas: before=%d after=%d", gasBefore, env.st.gas)
	}
	if got := env.state.GetRefund(); got != 0 {
		t.Fatalf("refund must be zero on new-account pre-fork path, got %d", got)
	}
	if env.state.GetNonce(env.authority) != env.auth.Nonce+1 {
		t.Fatalf("authority nonce not bumped")
	}
}

// TestApplyAuthorizationPostForkChargeOnNewAccount: post-Amsterdam, non-existing
// authority -> debit 12500 from st.gas, no refund.
func TestApplyAuthorizationPostForkChargeOnNewAccount(t *testing.T) {
	env := newAuthTestEnv(t, true, false)
	gasBefore := env.st.gas
	if err := env.st.applyAuthorization(&env.auth, true); err != nil {
		t.Fatalf("applyAuthorization: %v", err)
	}
	wantDebit := params.CallNewAccountGas - params.TxAuthTupleGas
	if gasBefore-env.st.gas != wantDebit {
		t.Fatalf("post-fork debit mismatch: got %d want %d (before=%d after=%d)",
			gasBefore-env.st.gas, wantDebit, gasBefore, env.st.gas)
	}
	if got := env.state.GetRefund(); got != 0 {
		t.Fatalf("refund must stay zero on post-fork path, got %d", got)
	}
	if env.state.GetNonce(env.authority) != env.auth.Nonce+1 {
		t.Fatalf("authority nonce not bumped")
	}
}

// TestApplyAuthorizationPostForkNoChargeOnExistingAccount: post-Amsterdam,
// existing authority -> no debit, no refund, delegation installed.
func TestApplyAuthorizationPostForkNoChargeOnExistingAccount(t *testing.T) {
	env := newAuthTestEnv(t, true, true)
	gasBefore := env.st.gas
	if err := env.st.applyAuthorization(&env.auth, true); err != nil {
		t.Fatalf("applyAuthorization: %v", err)
	}
	if gasBefore != env.st.gas {
		t.Fatalf("post-fork must not debit on existing-account path: before=%d after=%d", gasBefore, env.st.gas)
	}
	if got := env.state.GetRefund(); got != 0 {
		t.Fatalf("refund must stay zero on post-fork path, got %d", got)
	}
	if env.state.GetNonce(env.authority) != env.auth.Nonce+1 {
		t.Fatalf("authority nonce not bumped")
	}
}

// TestApplyAuthorizationPostForkSkipOnInsufficientGas: post-Amsterdam, non-existing
// authority, and st.gas is below the required delta -> the authorization is
// skipped, no nonce bump, no delegation install, st.gas is preserved, and the
// returned error is ErrInsufficientAuthorizationGas (so callers can skip like
// they already do for validation errors).
func TestApplyAuthorizationPostForkSkipOnInsufficientGas(t *testing.T) {
	env := newAuthTestEnv(t, true, false)
	env.st.gas = params.CallNewAccountGas - params.TxAuthTupleGas - 1 // one wei short
	gasBefore := env.st.gas
	err := env.st.applyAuthorization(&env.auth, true)
	if !errors.Is(err, ErrInsufficientAuthorizationGas) {
		t.Fatalf("expected ErrInsufficientAuthorizationGas, got %v", err)
	}
	if env.st.gas != gasBefore {
		t.Fatalf("st.gas must be preserved when skipping: before=%d after=%d", gasBefore, env.st.gas)
	}
	if env.state.GetNonce(env.authority) != 0 {
		t.Fatalf("nonce must not be bumped when skipping; got %d", env.state.GetNonce(env.authority))
	}
	if len(env.state.GetCode(env.authority)) != 0 {
		t.Fatalf("code must not be installed when skipping")
	}
}

// TestApplyAuthorizationPostForkExactGasBoundary: post-Amsterdam, the debit
// equals st.gas exactly -> the charge succeeds and st.gas lands on zero.
func TestApplyAuthorizationPostForkExactGasBoundary(t *testing.T) {
	env := newAuthTestEnv(t, true, false)
	env.st.gas = params.CallNewAccountGas - params.TxAuthTupleGas
	if err := env.st.applyAuthorization(&env.auth, true); err != nil {
		t.Fatalf("applyAuthorization at exact boundary: %v", err)
	}
	if env.st.gas != 0 {
		t.Fatalf("st.gas must hit zero at exact boundary: got %d", env.st.gas)
	}
	if env.state.GetNonce(env.authority) != env.auth.Nonce+1 {
		t.Fatalf("authority nonce not bumped")
	}
}

// TestApplyAuthorizationInvalidSignatureSkipped verifies that validation errors
// still take precedence: an authorization with a nonce mismatch is rejected in
// validateAuthorization before any gas accounting runs, and neither path mutates
// st.gas or the refund counter.
func TestApplyAuthorizationInvalidSignatureSkipped(t *testing.T) {
	for _, amsterdam := range []bool{false, true} {
		name := "pre-fork"
		if amsterdam {
			name = "post-fork"
		}
		t.Run(name, func(t *testing.T) {
			env := newAuthTestEnv(t, amsterdam, true)
			// Bump the state nonce so the signed auth (nonce=0) no longer matches.
			env.state.SetNonce(env.authority, 7, tracing.NonceChangeUnspecified)

			gasBefore := env.st.gas
			refundBefore := env.state.GetRefund()

			err := env.st.applyAuthorization(&env.auth, amsterdam)
			if !errors.Is(err, ErrAuthorizationNonceMismatch) {
				t.Fatalf("expected ErrAuthorizationNonceMismatch, got %v", err)
			}
			if env.st.gas != gasBefore {
				t.Fatalf("st.gas must be preserved on validation failure: before=%d after=%d", gasBefore, env.st.gas)
			}
			if env.state.GetRefund() != refundBefore {
				t.Fatalf("refund must be preserved on validation failure: before=%d after=%d", refundBefore, env.state.GetRefund())
			}
			if env.state.GetNonce(env.authority) != 7 {
				t.Fatalf("nonce must not be bumped on validation failure; got %d", env.state.GetNonce(env.authority))
			}
		})
	}
}

// --- P2-3: EIP-7825 MaxTxGas preCheck tests ----------------------------------

// newMaxTxGasEnv builds a StateTransition configured with enough balance to
// pass buyGas, so preCheck can exercise the EIP-7825 gas cap in isolation. The
// transaction gas limit is parameterised per test.
func newMaxTxGasEnv(t *testing.T, isAmsterdam bool, msg types.Message) *StateTransition {
	t.Helper()
	cfg := &params.ChainConfig{
		ChainID:             big.NewInt(1),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		ArchimedesBlock:     big.NewInt(0),
		ShanghaiBlock:       big.NewInt(0),
		BernoulliBlock:      big.NewInt(0),
		CurieBlock:          big.NewInt(0),
		Morph203Time:        params.NewUint64(0),
		ViridianTime:        params.NewUint64(0),
		EmeraldTime:         params.NewUint64(0),
	}
	if isAmsterdam {
		cfg.AmsterdamTime = params.NewUint64(0)
	}
	db := rawdb.NewMemoryDatabase()
	sdb, err := state.New(common.Hash{}, state.NewDatabase(db), nil)
	if err != nil {
		t.Fatalf("new statedb: %v", err)
	}
	// Fund the sender so buyGas (if reached) would not early-fail.
	sdb.GetOrNewStateObject(msg.From())
	sdb.AddBalance(msg.From(), new(big.Int).SetUint64(1e18), tracing.BalanceChangeUnspecified)

	blockCtx := vm.BlockContext{
		CanTransfer: CanTransfer,
		Transfer:    Transfer,
		GetHash:     func(uint64) common.Hash { return common.Hash{} },
		Coinbase:    common.Address{},
		GasLimit:    1 << 30, // large enough to not be the limiting factor
		BlockNumber: big.NewInt(1),
		Time:        big.NewInt(1),
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
	}
	evm := vm.NewEVM(blockCtx, vm.TxContext{}, sdb, cfg, vm.Config{})

	gp := new(GasPool).AddGas(blockCtx.GasLimit)
	return NewStateTransition(evm, msg, gp, big.NewInt(0))
}

func makeMaxTxGasMsg(t *testing.T, gas uint64, isFake bool) types.Message {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	to := common.HexToAddress("0x1")
	return types.NewMessage(
		crypto.PubkeyToAddress(key.PublicKey),
		&to,
		0,
		big.NewInt(0),           // value
		gas,                     // gas limit under test
		big.NewInt(1),           // gasPrice
		big.NewInt(1),           // gasFeeCap
		big.NewInt(1),           // gasTipCap
		0,                       // feeTokenID
		big.NewInt(0),           // feeLimit
		0,                       // version
		nil,                     // reference
		nil,                     // memo
		nil,                     // data
		nil,                     // accessList
		nil,                     // authList
		isFake,                  // isFake
	)
}

func TestPreCheckMaxTxGasPreAmsterdamIgnoresCap(t *testing.T) {
	// Pre-Amsterdam: a gas limit far above params.MaxTxGas is accepted by
	// preCheck (it must fail only on unrelated reasons, not ErrGasLimitTooHigh).
	msg := makeMaxTxGasMsg(t, params.MaxTxGas+1, false)
	st := newMaxTxGasEnv(t, false, msg)
	err := st.preCheck()
	if errors.Is(err, ErrGasLimitTooHigh) {
		t.Fatalf("pre-Amsterdam preCheck must not return ErrGasLimitTooHigh, got %v", err)
	}
}

func TestPreCheckMaxTxGasPostAmsterdamRejectsAboveCap(t *testing.T) {
	msg := makeMaxTxGasMsg(t, params.MaxTxGas+1, false)
	st := newMaxTxGasEnv(t, true, msg)
	err := st.preCheck()
	if !errors.Is(err, ErrGasLimitTooHigh) {
		t.Fatalf("post-Amsterdam preCheck must reject gas > MaxTxGas, got %v", err)
	}
}

func TestPreCheckMaxTxGasPostAmsterdamAcceptsAtCap(t *testing.T) {
	// Exactly at the cap must be accepted (the predicate is strict `>`).
	msg := makeMaxTxGasMsg(t, params.MaxTxGas, false)
	st := newMaxTxGasEnv(t, true, msg)
	err := st.preCheck()
	if errors.Is(err, ErrGasLimitTooHigh) {
		t.Fatalf("gas == MaxTxGas must be accepted, got %v", err)
	}
}

func TestPreCheckMaxTxGasPostAmsterdamAcceptsBelowCap(t *testing.T) {
	msg := makeMaxTxGasMsg(t, params.MaxTxGas-1, false)
	st := newMaxTxGasEnv(t, true, msg)
	err := st.preCheck()
	if errors.Is(err, ErrGasLimitTooHigh) {
		t.Fatalf("gas < MaxTxGas must be accepted, got %v", err)
	}
}

func TestPreCheckMaxTxGasFakeExempt(t *testing.T) {
	// eth_call / simulation ("fake") transactions skip the check block entirely
	// and therefore remain exempt even when Amsterdam is active.
	msg := makeMaxTxGasMsg(t, params.MaxTxGas+1, true)
	st := newMaxTxGasEnv(t, true, msg)
	err := st.preCheck()
	if errors.Is(err, ErrGasLimitTooHigh) {
		t.Fatalf("fake transactions must be exempt, got %v", err)
	}
}

// TestPreCheckMaxTxGasL1Exempt builds a real L1MessageTx transaction, derives
// its Message through AsMessage so the IsL1MessageTx flag is set correctly,
// and verifies that preCheck returns early (nil) without rejecting the
// oversized gas limit.
func TestPreCheckMaxTxGasL1Exempt(t *testing.T) {
	chainID := big.NewInt(1)
	signer := types.NewLondonSigner(chainID)
	to := common.HexToAddress("0x1")
	tx := types.NewTx(&types.L1MessageTx{
		QueueIndex: 0,
		Gas:        params.MaxTxGas + 1,
		To:         &to,
		Value:      big.NewInt(0),
		Data:       nil,
		Sender:     common.HexToAddress("0xdeadbeef"),
	})
	msg, err := tx.AsMessage(signer, big.NewInt(0))
	if err != nil {
		t.Fatalf("AsMessage: %v", err)
	}
	if !msg.IsL1MessageTx() {
		t.Fatalf("expected IsL1MessageTx to be true")
	}
	st := newMaxTxGasEnv(t, true, msg)
	if err := st.preCheck(); err != nil {
		t.Fatalf("L1MessageTx must be exempt, got %v", err)
	}
}
