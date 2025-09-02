// Copyright 2024 The go-ethereum Authors
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

package state

import (
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/crypto/codehash"
	"github.com/morph-l2/go-ethereum/params"
)

// hookedStateDB represents a statedb which emits calls to tracing-hooks
// on state operations.
type hookedStateDB struct {
	inner *StateDB
	hooks *tracing.Hooks
}

// NewHookedState wraps the given stateDb with the given hooks
func NewHookedState(stateDb *StateDB, hooks *tracing.Hooks) *hookedStateDB {
	s := &hookedStateDB{stateDb, hooks}
	if s.hooks == nil {
		s.hooks = new(tracing.Hooks)
	}
	return s
}

func (s *hookedStateDB) CreateAccount(addr common.Address) {
	s.inner.CreateAccount(addr)
}

func (s *hookedStateDB) GetBalance(addr common.Address) *big.Int {
	return s.inner.GetBalance(addr)
}

func (s *hookedStateDB) GetNonce(addr common.Address) uint64 {
	return s.inner.GetNonce(addr)
}

func (s *hookedStateDB) GetKeccakCodeHash(addr common.Address) common.Hash {
	return s.inner.GetKeccakCodeHash(addr)
}

func (s *hookedStateDB) GetPoseidonCodeHash(addr common.Address) common.Hash {
	return s.inner.GetPoseidonCodeHash(addr)
}

func (s *hookedStateDB) GetCommittedState(addr common.Address, hash common.Hash) common.Hash {
	return s.inner.GetCommittedState(addr, hash)
}

func (s *hookedStateDB) GetCode(addr common.Address) []byte {
	return s.inner.GetCode(addr)
}

func (s *hookedStateDB) GetCodeSize(addr common.Address) uint64 {
	return s.inner.GetCodeSize(addr)
}

func (s *hookedStateDB) AddRefund(u uint64) {
	s.inner.AddRefund(u)
}

func (s *hookedStateDB) SubRefund(u uint64) {
	s.inner.SubRefund(u)
}

func (s *hookedStateDB) GetRefund() uint64 {
	return s.inner.GetRefund()
}

func (s *hookedStateDB) GetState(addr common.Address, hash common.Hash) common.Hash {
	return s.inner.GetState(addr, hash)
}

func (s *hookedStateDB) GetTransientState(addr common.Address, key common.Hash) common.Hash {
	return s.inner.GetTransientState(addr, key)
}

func (s *hookedStateDB) SetTransientState(addr common.Address, key, value common.Hash) {
	s.inner.SetTransientState(addr, key, value)
}

func (s *hookedStateDB) Exist(addr common.Address) bool {
	return s.inner.Exist(addr)
}

func (s *hookedStateDB) Empty(addr common.Address) bool {
	return s.inner.Empty(addr)
}

func (s *hookedStateDB) AddressInAccessList(addr common.Address) bool {
	return s.inner.AddressInAccessList(addr)
}

func (s *hookedStateDB) SlotInAccessList(addr common.Address, slot common.Hash) (addressOk bool, slotOk bool) {
	return s.inner.SlotInAccessList(addr, slot)
}

func (s *hookedStateDB) AddAddressToAccessList(addr common.Address) {
	s.inner.AddAddressToAccessList(addr)
}

func (s *hookedStateDB) AddSlotToAccessList(addr common.Address, slot common.Hash) {
	s.inner.AddSlotToAccessList(addr, slot)
}

func (s *hookedStateDB) Prepare(rules params.Rules, sender, coinbase common.Address, dest *common.Address, precompiles []common.Address, txAccesses types.AccessList) {
	s.inner.Prepare(rules, sender, coinbase, dest, precompiles, txAccesses)
}

func (s *hookedStateDB) RevertToSnapshot(i int) {
	s.inner.RevertToSnapshot(i)
}

func (s *hookedStateDB) Snapshot() int {
	return s.inner.Snapshot()
}

func (s *hookedStateDB) AddPreimage(hash common.Hash, bytes []byte) {
	s.inner.AddPreimage(hash, bytes)
}

func (s *hookedStateDB) SubBalance(addr common.Address, amount *big.Int, reason tracing.BalanceChangeReason) *big.Int {
	prev := s.inner.SubBalance(addr, amount, reason)
	if s.hooks.OnBalanceChange != nil && amount.Sign() != 0 {
		newBalance := new(big.Int).Sub(prev, amount)
		s.hooks.OnBalanceChange(addr, prev, newBalance, reason)
	}
	return prev
}

func (s *hookedStateDB) AddBalance(addr common.Address, amount *big.Int, reason tracing.BalanceChangeReason) *big.Int {
	prev := s.inner.AddBalance(addr, amount, reason)
	if s.hooks.OnBalanceChange != nil && amount.Sign() != 0 {
		newBalance := new(big.Int).Add(prev, amount)
		s.hooks.OnBalanceChange(addr, prev, newBalance, reason)
	}
	return prev
}

func (s *hookedStateDB) SetNonce(address common.Address, nonce uint64, reason tracing.NonceChangeReason) {
	prev := s.inner.GetNonce(address)
	s.inner.SetNonce(address, nonce, reason)
	if s.hooks.OnNonceChangeV2 != nil {
		s.hooks.OnNonceChangeV2(address, prev, nonce, reason)
	} else if s.hooks.OnNonceChange != nil {
		s.hooks.OnNonceChange(address, prev, nonce)
	}
}

func (s *hookedStateDB) SetCode(address common.Address, code []byte) []byte {
	prev := s.inner.SetCode(address, code)
	if s.hooks.OnCodeChange != nil {
		prevHash := codehash.EmptyKeccakCodeHash
		if len(prev) != 0 {
			prevHash = crypto.Keccak256Hash(prev)
		}
		s.hooks.OnCodeChange(address, prevHash, prev, crypto.Keccak256Hash(code), code)
	}
	return prev
}

func (s *hookedStateDB) SetState(address common.Address, key common.Hash, value common.Hash) common.Hash {
	prev := s.inner.SetState(address, key, value)
	if s.hooks.OnStorageChange != nil && prev != value {
		s.hooks.OnStorageChange(address, key, prev, value)
	}
	return prev
}

func (s *hookedStateDB) GetRootHash() common.Hash {
	return s.inner.GetRootHash()
}

func (s *hookedStateDB) GetLiveStateAccount(addr common.Address) *types.StateAccount {
	return s.inner.GetLiveStateAccount(addr)
}

func (s *hookedStateDB) GetProof(addr common.Address) ([][]byte, error) {
	return s.inner.GetProof(addr)
}

func (s *hookedStateDB) AddLog(log *types.Log) {
	// The inner will modify the log (add fields), so invoke that first
	s.inner.AddLog(log)
	if s.hooks.OnLog != nil {
		s.hooks.OnLog(log)
	}
}

func (s *hookedStateDB) Finalise(deleteEmptyObjects bool) {
	defer s.inner.Finalise(deleteEmptyObjects)
	if s.hooks.OnBalanceChange == nil {
		return
	}
	for addr := range s.inner.journal.dirties {
		obj := s.inner.stateObjects[addr]
		if obj != nil && obj.suicided {
			// If ether was sent to account post-selfdestruct it is burnt.
			if bal := obj.Balance(); bal.Sign() != 0 {
				s.hooks.OnBalanceChange(addr, bal, new(big.Int), tracing.BalanceDecreaseSelfdestructBurn)
			}
		}
	}
}

func (s *hookedStateDB) ForEachStorage(addr common.Address, f func(common.Hash, common.Hash) bool) error {
	return s.inner.ForEachStorage(addr, f)
}

func (s *hookedStateDB) GetProofByHash(addrHash common.Hash) ([][]byte, error) {
	return s.inner.GetProofByHash(addrHash)
}

func (s *hookedStateDB) GetStorageProof(a common.Address, key common.Hash) ([][]byte, error) {
	return s.inner.GetStorageProof(a, key)
}

func (s *hookedStateDB) HasSuicided(addr common.Address) bool {
	return s.inner.HasSuicided(addr)
}

func (s *hookedStateDB) Suicide(addr common.Address) bool {
	return s.inner.Suicide(addr)
}
