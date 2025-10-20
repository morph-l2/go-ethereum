package core

import (
	"fmt"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/log"
)

// GetERC20Balance returns the balance of an ERC20 token for a specific address.
func (st *StateTransition) GetERC20Balance(tokenAddress, userAddress common.Address) (*big.Int, error) {
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
	ret, _, err := st.evm.StaticCall(sender, tokenAddress, data, st.gas)
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

// TransferERC20 transfers ERC20 tokens from one address to another.
func (st *StateTransition) TransferERC20(tokenAddress, from, to common.Address, amount *big.Int) error {
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

// ERC20BalanceSlot is the storage slot for the balanceOf mapping in ERC20 contracts
// For mapping(address => uint256) public balanceOf, the slot is 0
var ERC20BalanceSlot = common.BigToHash(big.NewInt(0))

// CalculateERC20BalanceSlot calculates the storage slot for an ERC20 balance
// For mapping(address => uint256) balanceOf, the slot is: keccak256(abi.encode(address, 0))
func CalculateERC20BalanceSlot(userAddress common.Address, balanceSlot common.Hash) common.Hash {
	// Convert address to 32 bytes (left-padded)
	addressBytes := common.LeftPadBytes(userAddress.Bytes(), 32)

	// Convert slot 0 to 32 bytes (left-padded)
	slotBytes := balanceSlot.Bytes()

	// Concatenate address and slot
	data := append(addressBytes, slotBytes...)

	// Calculate keccak256 hash
	hash := crypto.Keccak256(data)

	return common.BytesToHash(hash)
}

// GetERC20BalanceFromSlot returns the balance of an ERC20 token for a specific address using storage slot
func (st *StateTransition) GetERC20BalanceFromSlot(tokenAddress, userAddress common.Address, balanceSlot common.Hash) (*big.Int, error) {
	// Calculate the storage slot for the user's balance
	storageSlot := CalculateERC20BalanceSlot(userAddress, balanceSlot)

	// Get the value from storage
	value := st.state.GetState(tokenAddress, storageSlot)

	// Convert hash to big.Int
	balance := new(big.Int).SetBytes(value[:])

	return balance, nil
}

// SetERC20BalanceFromSlot sets the balance of an ERC20 token for a specific address using storage slot
func (st *StateTransition) SetERC20BalanceFromSlot(tokenAddress, userAddress common.Address, amount *big.Int, balanceSlot common.Hash) error {
	if amount == nil || amount.Sign() < 0 {
		return fmt.Errorf("invalid balance amount")
	}

	// Calculate the storage slot for the user's balance
	storageSlot := CalculateERC20BalanceSlot(userAddress, balanceSlot)

	// Convert amount to 32 bytes (left-padded)
	amountBytes := common.LeftPadBytes(amount.Bytes(), 32)
	amountHash := common.BytesToHash(amountBytes)

	// Set the value in storage
	st.state.SetState(tokenAddress, storageSlot, amountHash)

	// Log the balance change
	log.Debug("ERC20 balance set via storage slot",
		"token", tokenAddress.Hex(),
		"user", userAddress.Hex(),
		"amount", amount)

	return nil
}

// AddERC20BalanceFromSlot adds tokens to an ERC20 balance for a specific address using storage slot
func (st *StateTransition) AddERC20BalanceFromSlot(tokenAddress, userAddress common.Address, amount *big.Int, balanceSlot common.Hash) error {
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("invalid amount to add")
	}

	// Get current balance
	currentBalance, err := st.GetERC20BalanceFromSlot(tokenAddress, userAddress, balanceSlot)
	if err != nil {
		return fmt.Errorf("failed to get current balance: %v", err)
	}

	// Calculate new balance
	newBalance := new(big.Int).Add(currentBalance, amount)

	// Set new balance
	return st.SetERC20BalanceFromSlot(tokenAddress, userAddress, newBalance, balanceSlot)
}

// SubERC20BalanceFromSlot subtracts tokens from an ERC20 balance for a specific address using storage slot
func (st *StateTransition) SubERC20BalanceFromSlot(tokenAddress, userAddress common.Address, amount *big.Int, balanceSlot common.Hash) error {
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("invalid amount to subtract")
	}

	// Get current balance
	currentBalance, err := st.GetERC20BalanceFromSlot(tokenAddress, userAddress, balanceSlot)
	if err != nil {
		return fmt.Errorf("failed to get current balance: %v", err)
	}

	// Check if balance is sufficient
	if currentBalance.Cmp(amount) < 0 {
		return fmt.Errorf("insufficient ERC20 balance: have %v, need %v", currentBalance, amount)
	}

	// Calculate new balance
	newBalance := new(big.Int).Sub(currentBalance, amount)

	// Set new balance
	return st.SetERC20BalanceFromSlot(tokenAddress, userAddress, newBalance, balanceSlot)
}

// TransferERC20FromSlot transfers ERC20 tokens from one address to another using storage slots
func (st *StateTransition) TransferERC20FromSlot(tokenAddress, from, to common.Address, amount *big.Int, balanceSlot common.Hash) error {
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("invalid transfer amount")
	}

	// Subtract from sender
	if err := st.SubERC20BalanceFromSlot(tokenAddress, from, amount, balanceSlot); err != nil {
		return fmt.Errorf("failed to subtract ERC20 balance from sender: %v", err)
	}

	// Add to recipient
	if err := st.AddERC20BalanceFromSlot(tokenAddress, to, amount, balanceSlot); err != nil {
		// If adding to recipient fails, we need to restore the sender's balance
		// This is a critical error that should not happen in normal circumstances
		restoreErr := st.AddERC20BalanceFromSlot(tokenAddress, from, amount, balanceSlot)
		if restoreErr != nil {
			log.Error("Critical error: failed to restore sender balance after transfer failure",
				"token", tokenAddress.Hex(),
				"sender", from.Hex(),
				"recipient", to.Hex(),
				"amount", amount,
				"restoreError", restoreErr)
		}
		return fmt.Errorf("failed to add ERC20 balance to recipient: %v", err)
	}

	// Log the transfer
	log.Debug("ERC20 transfer executed via storage slots",
		"token", tokenAddress.Hex(),
		"from", from.Hex(),
		"to", to.Hex(),
		"amount", amount)

	return nil
}

// GetERC20BalanceHybrid returns the balance of an ERC20 token using either storage slot or call method
// If balanceSlot is zero hash, uses call method; otherwise uses storage slot method
func (st *StateTransition) GetERC20BalanceHybrid(tokenAddress, userAddress common.Address, balanceSlot common.Hash) (*big.Int, error) {
	if balanceSlot == (common.Hash{}) {
		// Use call method
		return st.GetERC20Balance(tokenAddress, userAddress)
	}
	// Use storage slot method
	return st.GetERC20BalanceFromSlot(tokenAddress, userAddress, balanceSlot)
}

// TransferERC20Hybrid transfers ERC20 tokens using either storage slot or call method
// If balanceSlot is zero hash, uses call method; otherwise uses storage slot method
func (st *StateTransition) TransferERC20Hybrid(tokenAddress, from, to common.Address, amount *big.Int, balanceSlot common.Hash) error {
	if balanceSlot == (common.Hash{}) {
		// Use call method
		return st.TransferERC20(tokenAddress, from, to, amount)
	}
	// Use storage slot method
	return st.TransferERC20FromSlot(tokenAddress, from, to, amount, balanceSlot)
}
