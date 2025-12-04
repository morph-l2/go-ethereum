package logger

import (
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/vm"
)

type traceFunc func(l *StructLogger, scope tracing.OpContext) error

var (
	// OpcodeExecs the map to load opcodes' trace funcs.
	OpcodeExecs = map[vm.OpCode][]traceFunc{
		vm.CALL:         {traceToAddressCode, traceLastNAddressCode(1), traceContractAccount, traceLastNAddressAccount(1)}, // contract account is the caller, stack.nth_last(1) is the callee's address
		vm.CALLCODE:     {traceToAddressCode, traceLastNAddressCode(1), traceContractAccount, traceLastNAddressAccount(1)}, // contract account is the caller, stack.nth_last(1) is the callee's address
		vm.DELEGATECALL: {traceToAddressCode, traceLastNAddressCode(1)},
		vm.STATICCALL:   {traceToAddressCode, traceLastNAddressCode(1), traceLastNAddressAccount(1)},
		vm.SELFDESTRUCT: {traceContractAccount, traceLastNAddressAccount(0)},
		vm.SELFBALANCE:  {traceContractAccount},
		vm.BALANCE:      {traceLastNAddressAccount(0)},
		vm.EXTCODEHASH:  {traceLastNAddressAccount(0)},
		vm.EXTCODESIZE:  {traceLastNAddressAccount(0)},
		vm.EXTCODECOPY:  {traceLastNAddressCode(0)},
	}
)

// traceToAddressCode gets tx.to address’s code
func traceToAddressCode(l *StructLogger, scope tracing.OpContext) error {
	if l.env.To == nil {
		return nil
	}
	traceCodeWithAddress(l, *l.env.To)
	return nil
}

// traceLastNAddressCode
func traceLastNAddressCode(n int) traceFunc {
	return func(l *StructLogger, scope tracing.OpContext) error {
		stack := scope.StackData()
		if len(stack) <= n {
			return nil
		}
		address := common.Address(stack[len(stack)-1-n].Bytes20())
		traceCodeWithAddress(l, address)
		l.statesAffected[address] = struct{}{}
		return nil
	}
}

func traceCodeWithAddress(l *StructLogger, address common.Address) {
	code := l.env.StateDB.GetCode(address)

	keccakCodeHash := l.env.StateDB.GetKeccakCodeHash(address)
	codeHash := keccakCodeHash

	// Try to get poseidon code hash - if it's non-zero, we're in zkTrie mode
	poseidonCodeHash := l.env.StateDB.GetPoseidonCodeHash(address)
	if poseidonCodeHash != (common.Hash{}) {
		// In zkTrie mode, use poseidon hash as key
		codeHash = poseidonCodeHash
	}
	// Otherwise use keccak hash (MPT mode or post-Euclid)

	codeSize := l.env.StateDB.GetCodeSize(address)
	l.bytecodes[codeHash] = CodeInfo{
		codeSize,
		keccakCodeHash,
		poseidonCodeHash,
		code,
	}
}

// traceContractAccount gets the contract's account
func traceContractAccount(l *StructLogger, scope tracing.OpContext) error {

	l.statesAffected[scope.Address()] = struct{}{}

	return nil
}

// traceLastNAddressAccount returns func about the last N's address account.
func traceLastNAddressAccount(n int) traceFunc {
	return func(l *StructLogger, scope tracing.OpContext) error {
		stack := scope.StackData()
		if len(stack) <= n {
			return nil
		}

		address := common.Address(stack[len(stack)-1-n].Bytes20())
		l.statesAffected[address] = struct{}{}

		return nil
	}
}
