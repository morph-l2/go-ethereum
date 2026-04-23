package logger

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/holiman/uint256"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
)

type loggerTestStateDB struct{}

func (loggerTestStateDB) GetBalance(common.Address) *big.Int               { return new(big.Int) }
func (loggerTestStateDB) GetNonce(common.Address) uint64                   { return 0 }
func (loggerTestStateDB) GetCode(common.Address) []byte                    { return nil }
func (loggerTestStateDB) GetKeccakCodeHash(common.Address) common.Hash     { return common.Hash{} }
func (loggerTestStateDB) GetPoseidonCodeHash(common.Address) common.Hash   { return common.Hash{} }
func (loggerTestStateDB) GetState(common.Address, common.Hash) common.Hash { return common.Hash{} }
func (loggerTestStateDB) GetTransientState(common.Address, common.Hash) common.Hash {
	return common.Hash{}
}
func (loggerTestStateDB) Exist(common.Address) bool         { return false }
func (loggerTestStateDB) GetRefund() uint64                 { return 0 }
func (loggerTestStateDB) GetCodeSize(common.Address) uint64 { return 0 }

type loggerTestScope struct{}

func (loggerTestScope) MemoryData() []byte       { return nil }
func (loggerTestScope) StackData() []uint256.Int { return nil }
func (loggerTestScope) Caller() common.Address   { return common.Address{} }
func (loggerTestScope) Address() common.Address  { return common.Address{} }
func (loggerTestScope) CallValue() *big.Int      { return new(big.Int) }
func (loggerTestScope) CallInput() []byte        { return nil }
func (loggerTestScope) ContractCode() []byte     { return nil }

func TestJSONLoggerSkipsSystemCallV2(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	hooks := NewJSONLogger(nil, &out)
	tx := types.NewTx(&types.LegacyTx{
		To:       &common.Address{},
		Gas:      21000,
		GasPrice: big.NewInt(0),
		Value:    big.NewInt(0),
	})
	env := &tracing.VMContext{StateDB: loggerTestStateDB{}}
	scope := loggerTestScope{}

	hooks.OnTxStart(env, tx, common.Address{})
	hooks.OnSystemCallStartV2(env)
	hooks.OnOpcode(0, byte(vm.SLOAD), 1, 1, scope, nil, 0, nil)
	hooks.OnSystemCallEnd()
	hooks.OnOpcode(1, byte(vm.SLOAD), 1, 1, scope, nil, 0, nil)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("unexpected number of log lines: %q", out.String())
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("failed to decode log line: %v", err)
	}
	if entry["pc"] != float64(1) {
		t.Fatalf("unexpected logged pc: %v", entry["pc"])
	}
}
