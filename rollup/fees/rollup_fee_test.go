package fees

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/holiman/uint256"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/stretchr/testify/assert"
)

func TestL1DataFeeBeforeCurie(t *testing.T) {
	l1BaseFee := new(big.Int).SetUint64(15000000)
	overhead := new(big.Int).SetUint64(100)
	scalar := new(big.Int).SetUint64(10)

	data := []byte{0, 10, 1, 0}

	expected := new(big.Int).SetUint64(30) // 30.6
	actual := calculateEncodedL1DataFee(data, overhead, l1BaseFee, scalar)
	assert.Equal(t, expected, actual)
}

func TestL1DataFeeAfterCurie(t *testing.T) {
	l1BaseFee := new(big.Int).SetUint64(1500000000)
	l1BlobBaseFee := new(big.Int).SetUint64(150000000)
	commitScalar := new(big.Int).SetUint64(10)
	blobScalar := new(big.Int).SetUint64(10)

	data := []byte{0, 10, 1, 0}

	expected := new(big.Int).SetUint64(21)
	actual := calculateEncodedL1DataFeeCurie(data, l1BaseFee, l1BlobBaseFee, commitScalar, blobScalar)
	assert.Equal(t, expected, actual)
}

// mockStateDB returns the same non-zero value for every GPO slot so that the
// post-Curie L1 data fee is linear in the encoded tx size:
// fee = slotValue * (1 + len(raw))
type mockStateDB struct{}

func (m *mockStateDB) GetState(addr common.Address, key common.Hash) common.Hash {
	return common.BigToHash(big.NewInt(1_000_000_000))
}
func (m *mockStateDB) SetState(addr common.Address, key, value common.Hash) common.Hash {
	return common.Hash{}
}
func (m *mockStateDB) Snapshot() int        { return 0 }
func (m *mockStateDB) RevertToSnapshot(int) {}

var (
	testTo       = common.HexToAddress("0x2222222222222222222222222222222222222222")
	testFrom     = common.HexToAddress("0x1111111111111111111111111111111111111111")
	testCallData = []byte{1, 2, 3, 4}
)

func testAuthList(chainID *big.Int) []types.SetCodeAuthorization {
	return []types.SetCodeAuthorization{
		{
			ChainID: *uint256.MustFromBig(chainID),
			Address: common.HexToAddress("0x3333333333333333333333333333333333333333"),
			Nonce:   7,
			V:       1,
			R:       *uint256.MustFromHex("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			S:       *uint256.MustFromHex("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		},
		{
			ChainID: *uint256.MustFromBig(chainID),
			Address: common.HexToAddress("0x4444444444444444444444444444444444444444"),
			Nonce:   8,
			V:       0,
			R:       *uint256.MustFromHex("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
			S:       *uint256.MustFromHex("0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
		},
	}
}

func newTestMessage(to *common.Address, accessList types.AccessList, authList []types.SetCodeAuthorization, feeTokenID uint16) types.Message {
	var feeLimit *big.Int
	if feeTokenID != 0 {
		feeLimit = big.NewInt(1e18)
	}
	return types.NewMessage(
		testFrom, to, 1, big.NewInt(0), 100000,
		big.NewInt(1e9), big.NewInt(1e9), big.NewInt(1e9),
		feeTokenID, feeLimit, types.MorphTxVersion0, nil, nil,
		testCallData, accessList, authList, false,
	)
}

func TestAsUnsignedTxTypeReconstruction(t *testing.T) {
	chainID := big.NewInt(1)
	baseFee := big.NewInt(1)
	accessList := types.AccessList{{Address: testTo, StorageKeys: []common.Hash{{1}}}}
	authList := testAuthList(chainID)

	tests := []struct {
		name     string
		msg      types.Message
		baseFee  *big.Int
		wantType uint8
	}{
		{"legacy pre-london", newTestMessage(&testTo, nil, nil, 0), nil, types.LegacyTxType},
		{"access list pre-london", newTestMessage(&testTo, accessList, nil, 0), nil, types.AccessListTxType},
		{"dynamic fee post-london", newTestMessage(&testTo, nil, nil, 0), baseFee, types.DynamicFeeTxType},
		{"morph tx post-london", newTestMessage(&testTo, nil, nil, 1), baseFee, types.MorphTxType},
		{"set code post-london", newTestMessage(&testTo, nil, authList, 0), baseFee, types.SetCodeTxType},
		{"set code with access list", newTestMessage(&testTo, accessList, authList, 0), baseFee, types.SetCodeTxType},
		// A SetCodeTx cannot have a nil destination; fall back to dynamic fee tx.
		{"auth list without to falls back to dynamic", newTestMessage(nil, nil, authList, 0), baseFee, types.DynamicFeeTxType},
		// Empty (non-nil) auth list cannot execute (ErrEmptyAuthList); keep dynamic fee encoding.
		{"empty auth list stays dynamic", newTestMessage(&testTo, nil, []types.SetCodeAuthorization{}, 0), baseFee, types.DynamicFeeTxType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := asUnsignedTx(tt.msg, tt.baseFee, chainID)
			assert.Equal(t, tt.wantType, tx.Type())
		})
	}
}

func TestAsUnsignedTxPreservesAuthList(t *testing.T) {
	chainID := big.NewInt(1)
	authList := testAuthList(chainID)

	tx := asUnsignedTx(newTestMessage(&testTo, nil, authList, 0), big.NewInt(1), chainID)
	assert.Equal(t, authList, tx.SetCodeAuthorizations())

	withAuth, err := tx.MarshalBinary()
	assert.NoError(t, err)
	withoutAuth, err := asUnsignedTx(newTestMessage(&testTo, nil, nil, 0), big.NewInt(1), chainID).MarshalBinary()
	assert.NoError(t, err)
	assert.Greater(t, len(withAuth), len(withoutAuth),
		"encoded SetCodeTx must include authorization list bytes")
}

func TestU256RejectsNegativeInput(t *testing.T) {
	assert.PanicsWithValue(t, "cannot convert negative big.Int to uint256.Int", func() {
		u256(big.NewInt(-1))
	})
}

func TestEstimateL1DataFeeSetCodeBeforeViridianReturnsUnsupportedType(t *testing.T) {
	config := params.TestChainConfig
	state := &mockStateDB{}
	authList := testAuthList(config.ChainID)

	_, err := EstimateL1DataFeeForMessage(
		newTestMessage(&testTo, nil, authList, 0),
		big.NewInt(1),
		config,
		types.NewLondonSigner(config.ChainID),
		state,
		big.NewInt(1),
	)
	assert.ErrorIs(t, err, types.ErrTxTypeNotSupported)
}

func TestEstimateL1DataFeeIncludesSetCodeAuthorizations(t *testing.T) {
	config := params.TestChainConfig
	chainID := config.ChainID
	signer := types.LatestSignerForChainID(chainID)
	state := &mockStateDB{}
	blockNumber := big.NewInt(1)
	baseFee := big.NewInt(1)
	authList := testAuthList(chainID)

	withAuth, err := EstimateL1DataFeeForMessage(
		newTestMessage(&testTo, nil, authList, 0), baseFee, config, signer, state, blockNumber)
	assert.NoError(t, err)
	withoutAuth, err := EstimateL1DataFeeForMessage(
		newTestMessage(&testTo, nil, nil, 0), baseFee, config, signer, state, blockNumber)
	assert.NoError(t, err)
	assert.Equal(t, 1, withAuth.Cmp(withoutAuth),
		"L1 data fee with auth list must exceed plain EIP-1559 estimate")

	// The estimate must match the real transaction path for the same fields,
	// using the same fake signature as EstimateL1DataFeeForMessage.
	fakeSig := append(bytes.Repeat([]byte{0xff}, crypto.SignatureLength-1), 0x01)
	realTx, err := types.NewTx(&types.SetCodeTx{
		ChainID:   uint256.MustFromBig(chainID),
		Nonce:     1,
		GasTipCap: uint256.NewInt(1e9),
		GasFeeCap: uint256.NewInt(1e9),
		Gas:       100000,
		To:        testTo,
		Value:     uint256.NewInt(0),
		Data:      testCallData,
		AuthList:  authList,
	}).WithSignature(signer, fakeSig)
	assert.NoError(t, err)

	actual, err := CalculateL1DataFee(realTx, state, config, blockNumber)
	assert.NoError(t, err)
	assert.Equal(t, actual, withAuth,
		"fallback estimate must equal the real SetCodeTx fee for identical fields")
}
