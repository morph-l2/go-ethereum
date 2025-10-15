package core

import (
	"fmt"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/log"
)

// GetERC20Balance returns the balance of an ERC20 token for a specific address.
// tokenAddress: The address of the ERC20 token contract
// userAddress: The address of the user whose balance we want to check
// Returns: *big.Int - The token balance, or nil if the call fails
func (st *StateTransition) GetERC20Balance(tokenAddress common.Address, userAddress common.Address) (*big.Int, error) {
	// Define the ERC20 balanceOf method signature: balanceOf(address)
	// Function signature: 0x70a08231
	methodID := []byte{0x70, 0xa0, 0x82, 0x31}

	// Pad the address to 32 bytes
	paddedAddress := common.LeftPadBytes(userAddress.Bytes(), 32)

	// Construct the call data: methodID + paddedAddress
	data := append(methodID, paddedAddress...)

	// Create a message call context
	sender := vm.AccountRef(st.msg.From())

	// Execute the call (using StaticCall since we're only reading state)
	ret, remainingGas, err := st.evm.StaticCall(sender, tokenAddress, data, st.gas)
	if err != nil {
		return nil, err
	}

	// Restore gas to its previous value since we don't want this call to affect the actual transaction
	st.gas = remainingGas

	// If return data is too short, it's an error
	if len(ret) < 32 {
		return nil, fmt.Errorf("invalid return data from ERC20 balanceOf call")
	}

	// Parse the result as a big.Int
	balance := new(big.Int).SetBytes(ret[:32])

	return balance, nil
}

// TransferERC20 transfers ERC20 tokens from one address to another.
// tokenAddress: The address of the ERC20 token contract
// from: The address to transfer tokens from
// to: The address to transfer tokens to
// amount: The amount of tokens to transfer
// Returns: error - nil if the transfer was successful, error otherwise
func (st *StateTransition) TransferERC20(tokenAddress common.Address, from common.Address, to common.Address, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("invalid transfer amount")
	}

	// Define the ERC20 transfer method signature: transfer(address,uint256)
	// Function signature: 0xa9059cbb
	methodID := []byte{0xa9, 0x05, 0x9c, 0xbb}

	// Pad the recipient address to 32 bytes
	paddedAddress := common.LeftPadBytes(to.Bytes(), 32)

	// Pad the amount to 32 bytes
	paddedAmount := common.LeftPadBytes(amount.Bytes(), 32)

	// Construct the call data: methodID + to + amount
	data := append(methodID, append(paddedAddress, paddedAmount...)...)

	// Create a message call context
	sender := vm.AccountRef(from)

	// Execute the call
	ret, remainingGas, err := st.evm.Call(sender, tokenAddress, data, st.gas, big.NewInt(0))
	if err != nil {
		return fmt.Errorf("ERC20 transfer call failed: %v", err)
	}

	// Restore gas to its previous value
	st.gas = remainingGas

	// Check if the call was successful (ERC20 transfer returns boolean)
	if len(ret) == 0 {
		return fmt.Errorf("ERC20 transfer returned no data")
	}

	// Log the transfer
	log.Debug("ERC20 transfer executed",
		"token", tokenAddress.Hex(),
		"from", from.Hex(),
		"to", to.Hex(),
		"amount", amount)

	return nil
}
