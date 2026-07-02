package js

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/holiman/uint256"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/eth/tracers"
	"github.com/morph-l2/go-ethereum/params"
)

type jsTestStateDB struct{}

func (jsTestStateDB) GetBalance(common.Address) *big.Int               { return new(big.Int) }
func (jsTestStateDB) GetNonce(common.Address) uint64                   { return 0 }
func (jsTestStateDB) GetCode(common.Address) []byte                    { return nil }
func (jsTestStateDB) GetKeccakCodeHash(common.Address) common.Hash     { return common.Hash{} }
func (jsTestStateDB) GetPoseidonCodeHash(common.Address) common.Hash   { return common.Hash{} }
func (jsTestStateDB) GetState(common.Address, common.Hash) common.Hash { return common.Hash{} }
func (jsTestStateDB) GetTransientState(common.Address, common.Hash) common.Hash {
	return common.Hash{}
}
func (jsTestStateDB) Exist(common.Address) bool         { return false }
func (jsTestStateDB) GetRefund() uint64                 { return 0 }
func (jsTestStateDB) GetCodeSize(common.Address) uint64 { return 0 }

type jsTestScope struct{}

func (jsTestScope) MemoryData() []byte       { return nil }
func (jsTestScope) StackData() []uint256.Int { return nil }
func (jsTestScope) Caller() common.Address   { return common.Address{} }
func (jsTestScope) Address() common.Address  { return common.Address{} }
func (jsTestScope) CallValue() *big.Int      { return new(big.Int) }
func (jsTestScope) CallInput() []byte        { return nil }
func (jsTestScope) ContractCode() []byte     { return nil }

func TestJSTracerSkipsSystemCalls(t *testing.T) {
	t.Parallel()

	const code = `{
		steps: 0,
		enters: 0,
		exits: 0,
		step: function(log, db) { this.steps++; },
		enter: function(frame) { this.enters++; },
		exit: function(frame) { this.exits++; },
		fault: function(log, db) {},
		result: function(ctx, db) { return {steps: this.steps, enters: this.enters, exits: this.exits}; }
	}`

	tracer, err := newJsTracer(code, &tracers.Context{}, nil, params.TestChainConfig)
	if err != nil {
		t.Fatalf("failed to create js tracer: %v", err)
	}
	tx := types.NewTx(&types.LegacyTx{
		To:       &common.Address{},
		Gas:      21000,
		GasPrice: big.NewInt(0),
		Value:    big.NewInt(0),
	})
	env := &tracing.VMContext{
		BlockNumber: big.NewInt(0),
		Time:        0,
		BaseFee:     big.NewInt(0),
		Coinbase:    common.Address{},
		StateDB:     jsTestStateDB{},
	}
	scope := jsTestScope{}

	tracer.OnTxStart(env, tx, common.Address{})
	tracer.OnSystemCallStartV2(env)
	tracer.OnEnter(1, byte(vm.CALL), common.Address{}, common.Address{}, []byte{1, 2, 3, 4}, 21000, big.NewInt(0))
	tracer.OnOpcode(0, 0x00, 1, 1, scope, nil, 1, nil)
	tracer.OnExit(1, nil, 0, nil, false)
	tracer.OnSystemCallEnd()

	tracer.OnEnter(1, byte(vm.CALL), common.Address{}, common.Address{}, []byte{1, 2, 3, 4}, 21000, big.NewInt(0))
	tracer.OnOpcode(1, 0x00, 1, 1, scope, nil, 1, nil)
	tracer.OnExit(1, nil, 0, nil, false)

	res, err := tracer.GetResult()
	if err != nil {
		t.Fatalf("failed to get trace result: %v", err)
	}
	var got struct {
		Steps  int `json:"steps"`
		Enters int `json:"enters"`
		Exits  int `json:"exits"`
	}
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatalf("failed to decode trace result: %v", err)
	}
	if got.Steps != 1 || got.Enters != 1 || got.Exits != 1 {
		t.Fatalf("unexpected result: %+v", got)
	}
}
