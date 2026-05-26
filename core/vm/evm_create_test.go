package vm

import (
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	statedb "github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/params"
)

type setCodeCountingStateDB struct {
	StateDB
	setCodeCalls int
}

func (db *setCodeCountingStateDB) SetCode(addr common.Address, code []byte) []byte {
	db.setCodeCalls++
	return db.StateDB.SetCode(addr, code)
}

func newCreateTestEVM(t *testing.T) (*EVM, *setCodeCountingStateDB, common.Address) {
	t.Helper()

	base, err := statedb.New(common.Hash{}, statedb.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	caller := common.HexToAddress("0x1111111111111111111111111111111111111111")
	base.CreateAccount(caller)

	wrapped := &setCodeCountingStateDB{StateDB: base}
	blockCtx := BlockContext{
		CanTransfer: func(StateDB, common.Address, *big.Int) bool { return true },
		Transfer:    func(StateDB, common.Address, common.Address, *big.Int) {},
		GetHash:     func(uint64) common.Hash { return common.Hash{} },
		BlockNumber: big.NewInt(0),
		Time:        big.NewInt(0),
		GasLimit:    10_000_000,
		BaseFee:     big.NewInt(params.InitialBaseFee),
	}
	txCtx := TxContext{
		Origin:   caller,
		GasPrice: big.NewInt(params.InitialBaseFee),
	}
	return NewEVM(blockCtx, txCtx, wrapped, params.TestChainConfig, Config{}), wrapped, caller
}

func TestCreateEmptyReturnSkipsSetCode(t *testing.T) {
	evm, statedb, caller := newCreateTestEVM(t)

	ret, _, _, err := evm.Create(AccountRef(caller), nil, 1_000_000, new(big.Int))
	if err != nil {
		t.Fatal(err)
	}
	if len(ret) != 0 {
		t.Fatalf("expected empty create return, got %x", ret)
	}
	if statedb.setCodeCalls != 0 {
		t.Fatalf("empty create called SetCode %d times", statedb.setCodeCalls)
	}
}

func TestCreateNonEmptyReturnSetsCode(t *testing.T) {
	evm, statedb, caller := newCreateTestEVM(t)

	code := []byte{
		byte(PUSH1), 0x2a,
		byte(PUSH1), 0x00,
		byte(MSTORE),
		byte(PUSH1), 0x20,
		byte(PUSH1), 0x00,
		byte(RETURN),
	}
	ret, _, _, err := evm.Create(AccountRef(caller), code, 1_000_000, new(big.Int))
	if err != nil {
		t.Fatal(err)
	}
	if len(ret) != 32 {
		t.Fatalf("expected non-empty create return, got %x", ret)
	}
	if statedb.setCodeCalls != 1 {
		t.Fatalf("non-empty create called SetCode %d times", statedb.setCodeCalls)
	}
}
