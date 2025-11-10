package core

import (
	"bytes"
	"fmt"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/math"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/rollup/fees"

	"golang.org/x/crypto/sha3"
)

var (
	TokenTransferSig  = "transfer(address,uint256)"
	TokenBalanceOfSig = "balanceOf(address)"
)

// GetERC20BalanceHybrid returns the balance of an ERC20 token using either storage slot or call method
// If balanceSlot is zero hash, uses call method; otherwise uses storage slot method
func (st *StateTransition) GetERC20BalanceHybrid(tokenID uint16, user common.Address) (*fees.TokenInfo, *big.Int, error) {
	info, err := fees.GetTokenInfo(st.state, tokenID)
	if err != nil {
		return nil, nil, err
	}
	balance := new(big.Int)
	if info.BalanceSlot == (common.Hash{}) {
		balance, err = GetERC20BalanceByEVM(st.evm, info.TokenAddress, user)
		if err != nil {
			return nil, nil, err
		}
		return info, balance, nil
	}
	balance, _, err = fees.GetERC20BalanceFromSlot(st.state, info.TokenAddress, user, info.BalanceSlot)
	if err != nil {
		return nil, nil, err
	}
	return info, balance, nil
}

// TransferERC20Hybrid transfers ERC20 tokens using either storage slot or call method
// If balanceSlot is zero hash, uses call method; otherwise uses storage slot method
func (st *StateTransition) TransferERC20Hybrid(tokenAddress, from, to common.Address, amount *big.Int, balanceSlot common.Hash, userBalanceBefore *big.Int) error {
	if amount == nil || amount.Cmp(big.NewInt(0)) == 0 {
		return nil
	}
	if balanceSlot == (common.Hash{}) {
		// Use call method
		return transferERC20ByEVM(st.evm, tokenAddress, from, to, amount, userBalanceBefore)
	}
	// Use storage slot method
	return fees.TransferERC20ByState(st.state, tokenAddress, balanceSlot, from, to, amount)
}

// GetERC20Balance returns the balance of an ERC20 token for a specific address.
func GetERC20Balance(evm *vm.EVM, tokenID uint16, user common.Address) (*big.Int, error) {
	info, err := fees.GetTokenInfo(evm.StateDB, tokenID)
	if err != nil {
		return nil, fmt.Errorf("failed to get token address for token ID %d: %v", tokenID, err)
	}
	balance := new(big.Int)
	if !bytes.Equal(info.BalanceSlot.Bytes(), common.Hash{}.Bytes()) {
		// balance slot exist
		balance, _, err = fees.GetERC20BalanceFromSlot(evm.StateDB, info.TokenAddress, user, info.BalanceSlot)
		if err != nil {
			return nil, err
		}
		return balance, nil
	}
	// get balance by evm call
	balance, err = GetERC20BalanceByEVM(evm, info.TokenAddress, user)
	if err != nil {
		return nil, err
	}

	return balance, nil
}

// GetERC20BalanceByEVM returns the balance of an ERC20 token for a specific address.
func GetERC20BalanceByEVM(evm *vm.EVM, tokenAddress, userAddress common.Address) (*big.Int, error) {
	methodID := generateMethodSignature(TokenBalanceOfSig)
	// Pad the address to 32 bytes
	paddedAddress := common.LeftPadBytes(userAddress.Bytes(), 32)
	// Construct the call data: methodID + paddedAddress
	data := append(methodID, paddedAddress...)
	// Create a message call context
	sender := vm.AccountRef(userAddress)
	// Execute the call (using StaticCall since we're only reading state)
	ret, _, err := evm.StaticCall(sender, tokenAddress, data, math.MaxUint64)
	if err != nil {
		return nil, err
	}

	// If return data is too short, it's an error
	if len(ret) < 32 {
		return nil, fmt.Errorf("invalid return data from ERC20 balanceOf call")
	}

	// Parse the result as a big.Int
	balance := new(big.Int).SetBytes(ret[:32])

	return balance, nil
}

// TransferERC20ByEVM transfers ERC20 tokens from one address to another.
func transferERC20ByEVM(evm *vm.EVM, tokenAddress, from, to common.Address, amount *big.Int, userBalanceBefore *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("invalid transfer amount")
	}

	var fromBalanceBefore *big.Int
	var err error
	if userBalanceBefore != nil {
		fromBalanceBefore = userBalanceBefore
	} else {
		fromBalanceBefore, err = GetERC20BalanceByEVM(evm, tokenAddress, from)
		if err != nil {
			return fmt.Errorf("failed to get sender balance before transfer: %v", err)
		}
	}

	// Check if sender has sufficient balance
	if fromBalanceBefore.Cmp(amount) < 0 {
		return fmt.Errorf("insufficient balance: have %s, need %s", fromBalanceBefore.String(), amount.String())
	}

	methodID := generateMethodSignature(TokenTransferSig)
	// Pad the recipient address to 32 bytes
	paddedAddress := common.LeftPadBytes(to.Bytes(), 32)
	// Pad the amount to 32 bytes
	paddedAmount := common.LeftPadBytes(amount.Bytes(), 32)
	// Construct the call data: methodID + to + amount
	data := append(methodID, append(paddedAddress, paddedAmount...)...)
	// Create a message call context
	sender := vm.AccountRef(from)
	// Execute the call
	ret, _, err := evm.Call(sender, tokenAddress, data, math.MaxUint64, big.NewInt(0))
	if err != nil {
		return fmt.Errorf("ERC20 transfer call failed: %v", err)
	}

	// Consider both variants: no return (old tokens) and bool true (standard).
	if len(ret) > 0 {
		// ABI bool is 32 bytes; success if last byte == 1
		if len(ret) < 32 || ret[31] != 1 {
			return fmt.Errorf("ERC20 transfer returned failure")
		}
	}

	// Check balance after transfer
	fromBalanceAfter, err := GetERC20BalanceByEVM(evm, tokenAddress, from)
	if err != nil {
		return fmt.Errorf("failed to get sender balance after transfer: %v", err)
	}

	// Verify balance changes
	expectedFromBalance := new(big.Int).Sub(fromBalanceBefore, amount)
	if fromBalanceAfter.Cmp(expectedFromBalance) != 0 {
		return fmt.Errorf("sender balance mismatch: expected %s, got %s", expectedFromBalance.String(), fromBalanceAfter.String())
	}

	// Log the transfer with balance information
	log.Debug("ERC20 transfer executed",
		"token", tokenAddress.Hex(),
		"from", from.Hex(),
		"to", to.Hex(),
		"amount", amount,
		"from_balance_before", fromBalanceBefore,
		"from_balance_after", fromBalanceAfter,
	)

	return nil
}

func generateMethodSignature(functionSignature string) []byte {
	hash := sha3.NewLegacyKeccak256()
	hash.Write([]byte(functionSignature))
	hashBytes := hash.Sum(nil)

	return hashBytes[:4]
}
