package native

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/crypto/codehash"
	etracers "github.com/morph-l2/go-ethereum/eth/tracers"
	"github.com/morph-l2/go-ethereum/params"
)

type tracerTestStateDB struct {
	balances map[common.Address]*big.Int
	nonces   map[common.Address]uint64
	codes    map[common.Address][]byte
	storage  map[common.Address]map[common.Hash]common.Hash
}

func (db *tracerTestStateDB) GetBalance(addr common.Address) *big.Int {
	if bal, ok := db.balances[addr]; ok {
		return new(big.Int).Set(bal)
	}
	return new(big.Int)
}

func (db *tracerTestStateDB) GetNonce(addr common.Address) uint64 {
	return db.nonces[addr]
}

func (db *tracerTestStateDB) GetCode(addr common.Address) []byte {
	return common.CopyBytes(db.codes[addr])
}

func (db *tracerTestStateDB) GetKeccakCodeHash(addr common.Address) common.Hash {
	code := db.codes[addr]
	if len(code) == 0 {
		return codehash.EmptyKeccakCodeHash
	}
	return crypto.Keccak256Hash(code)
}

func (db *tracerTestStateDB) GetPoseidonCodeHash(common.Address) common.Hash {
	return common.Hash{}
}

func (db *tracerTestStateDB) GetState(addr common.Address, key common.Hash) common.Hash {
	if slots, ok := db.storage[addr]; ok {
		return slots[key]
	}
	return common.Hash{}
}

func (db *tracerTestStateDB) GetTransientState(common.Address, common.Hash) common.Hash {
	return common.Hash{}
}

func (db *tracerTestStateDB) Exist(addr common.Address) bool {
	if bal := db.GetBalance(addr); bal.Sign() != 0 {
		return true
	}
	if db.GetNonce(addr) != 0 || len(db.codes[addr]) != 0 {
		return true
	}
	return len(db.storage[addr]) != 0
}

func (db *tracerTestStateDB) GetRefund() uint64 {
	return 0
}

func (db *tracerTestStateDB) GetCodeSize(addr common.Address) uint64 {
	return uint64(len(db.codes[addr]))
}

func newTracerTestStateDB() *tracerTestStateDB {
	return &tracerTestStateDB{
		balances: make(map[common.Address]*big.Int),
		nonces:   make(map[common.Address]uint64),
		codes:    make(map[common.Address][]byte),
		storage:  make(map[common.Address]map[common.Hash]common.Hash),
	}
}

func newPrestateTestTracer(cfg prestateTracerConfig, db *tracerTestStateDB) *prestateTracer {
	return &prestateTracer{
		env: &tracing.VMContext{
			BlockNumber: big.NewInt(0),
			Time:        0,
			StateDB:     db,
		},
		pre:         stateMap{},
		post:        stateMap{},
		config:      cfg,
		chainConfig: params.TestChainConfig,
		created:     make(map[common.Address]bool),
		deleted:     make(map[common.Address]bool),
	}
}

func TestPrestateTracerLookupStorageCreatesAccount(t *testing.T) {
	t.Parallel()

	db := newTracerTestStateDB()
	addr := common.HexToAddress("0x100")
	slot := common.HexToHash("0x1")
	value := common.HexToHash("0x2")
	db.codes[addr] = []byte{0x60, 0x00}
	db.storage[addr] = map[common.Hash]common.Hash{slot: value}

	tracer := newPrestateTestTracer(prestateTracerConfig{}, db)
	tracer.lookupStorage(addr, slot)

	if tracer.pre[addr] == nil {
		t.Fatalf("expected account to be added to prestate")
	}
	if got := tracer.pre[addr].Storage[slot]; got != value {
		t.Fatalf("unexpected storage value, have %s want %s", got, value)
	}
}

func TestPrestateTracerOnStorageChangeUsesPreviousValue(t *testing.T) {
	t.Parallel()

	db := newTracerTestStateDB()
	addr := common.HexToAddress("0x200")
	slot := common.HexToHash("0x3")
	prev := common.HexToHash("0x4")
	next := common.HexToHash("0x5")
	db.codes[addr] = []byte{0x60, 0x00}
	db.storage[addr] = map[common.Hash]common.Hash{slot: next}

	tracer := newPrestateTestTracer(prestateTracerConfig{}, db)
	tracer.OnStorageChange(addr, slot, prev, next)

	if got := tracer.pre[addr].Storage[slot]; got != prev {
		t.Fatalf("unexpected prestate slot value, have %s want %s", got, prev)
	}
}

func TestPrestateTracerOnBalanceChangeUsesPreviousBalance(t *testing.T) {
	t.Parallel()

	db := newTracerTestStateDB()
	addr := common.HexToAddress("0x300")
	db.balances[addr] = big.NewInt(9)

	tracer := newPrestateTestTracer(prestateTracerConfig{}, db)
	tracer.OnBalanceChange(addr, big.NewInt(4), big.NewInt(9), tracing.BalanceChangeTransfer)

	if got := tracer.pre[addr].Balance; got.Cmp(big.NewInt(4)) != 0 {
		t.Fatalf("unexpected prestate balance, have %v want %v", got, 4)
	}
}

func TestFourByteTracerSkipsSystemCalls(t *testing.T) {
	t.Parallel()

	tracer, err := newFourByteTracer(nil, nil, params.TestChainConfig)
	if err != nil {
		t.Fatalf("failed to create tracer: %v", err)
	}
	tx := types.NewTx(&types.LegacyTx{
		To:       &common.Address{},
		Gas:      21000,
		GasPrice: big.NewInt(0),
		Value:    big.NewInt(0),
	})
	tracer.OnTxStart(&tracing.VMContext{BlockNumber: big.NewInt(0)}, tx, common.Address{})

	systemInput := append(common.FromHex("0x70a08231"), make([]byte, 32)...)
	userInput := append(common.FromHex("0xa9059cbb"), make([]byte, 64)...)

	tracer.OnSystemCallStartV2(&tracing.VMContext{})
	tracer.OnEnter(1, byte(vm.CALL), common.Address{}, common.HexToAddress("0x1111"), systemInput, 0, big.NewInt(0))
	tracer.OnSystemCallEnd()
	tracer.OnEnter(1, byte(vm.CALL), common.Address{}, common.HexToAddress("0x2222"), userInput, 0, big.NewInt(0))

	res, err := tracer.GetResult()
	if err != nil {
		t.Fatalf("failed to get trace result: %v", err)
	}
	var ids map[string]int
	if err := json.Unmarshal(res, &ids); err != nil {
		t.Fatalf("failed to decode trace result: %v", err)
	}
	if len(ids) != 1 || ids["0xa9059cbb-64"] != 1 {
		t.Fatalf("unexpected 4byte result: %v", ids)
	}
}

func TestMuxTracerForwardsSystemCallsAndNonceChangeV2(t *testing.T) {
	t.Parallel()

	var (
		systemStarts int
		systemEnds   int
		nonceV2Calls int
	)
	sub := &etracers.Tracer{
		Hooks: &tracing.Hooks{
			OnSystemCallStartV2: func(*tracing.VMContext) { systemStarts++ },
			OnSystemCallEnd:     func() { systemEnds++ },
			OnNonceChangeV2: func(common.Address, uint64, uint64, tracing.NonceChangeReason) {
				nonceV2Calls++
			},
		},
		GetResult: func() (json.RawMessage, error) { return json.RawMessage(`{}`), nil },
		Stop:      func(error) {},
	}
	mux := &muxTracer{tracers: []*etracers.Tracer{sub}}

	mux.OnSystemCallStartV2(&tracing.VMContext{})
	mux.OnSystemCallEnd()
	mux.OnNonceChangeV2(common.Address{}, 1, 2, tracing.NonceChangeUnspecified)

	if systemStarts != 1 || systemEnds != 1 || nonceV2Calls != 1 {
		t.Fatalf("unexpected forwarded counts: starts=%d ends=%d nonceV2=%d", systemStarts, systemEnds, nonceV2Calls)
	}
}
