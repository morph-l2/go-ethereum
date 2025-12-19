package fees

import (
	"fmt"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/log"
)

func TransferAltTokenByState(state StateDB, tokenAddress common.Address, balanceSlot common.Hash, from, to common.Address, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return fmt.Errorf("invalid transfer amount")
	}

	snapshot := state.Snapshot()

	// Subtract from sender
	if err := changeAltTokenBalanceByState(state, tokenAddress, balanceSlot, from, amount, true); err != nil {
		return fmt.Errorf("failed to subtract token balance from sender: %v", err)
	}

	// Add to recipient
	if err := changeAltTokenBalanceByState(state, tokenAddress, balanceSlot, to, amount, false); err != nil {
		state.RevertToSnapshot(snapshot)
		return fmt.Errorf("failed to add token balance to recipient: %v", err)
	}

	// Log the transfer
	log.Debug("Alt token transfer executed via storage slots",
		"token", tokenAddress.Hex(),
		"from", from.Hex(),
		"to", to.Hex(),
		"amount", amount)
	return nil
}

func changeAltTokenBalanceByState(state StateDB, tokenAddress common.Address, balanceSlot common.Hash, user common.Address, amount *big.Int, isDeduct bool) error {
	// Get current balance
	currentBalance, storageSlot, err := GetAltTokenBalanceFromSlot(state, tokenAddress, user, balanceSlot)
	if err != nil {
		return fmt.Errorf("failed to get current balance: %v", err)
	}
	newBalance := new(big.Int).SetUint64(0)
	if isDeduct {
		// Check if balance is sufficient
		if currentBalance.Cmp(amount) < 0 {
			return fmt.Errorf("insufficient token balance: have %v, need %v", currentBalance, amount)
		}
		// Calculate new balance
		newBalance = new(big.Int).Sub(currentBalance, amount)
	} else {
		// Calculate new balance
		newBalance = new(big.Int).Add(currentBalance, amount)
	}

	// Convert amount to 32 bytes (left-padded)
	amountBytes := common.LeftPadBytes(newBalance.Bytes(), 32)
	amountHash := common.BytesToHash(amountBytes)

	// Change user balance
	state.SetState(tokenAddress, storageSlot, amountHash)

	return nil
}

// CalculateAltTokenBalanceSlot calculates the storage slot for an alt token balance
// For mapping(address => uint256) balanceOf, the slot is: keccak256(abi.encode(address, 0))
func CalculateAltTokenBalanceSlot(userAddress common.Address, balanceSlot common.Hash) common.Hash {
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

// GetAltTokenBalanceFromSlot returns the balance of an alt token for a specific address using storage slot
func GetAltTokenBalanceFromSlot(state StateDB, tokenAddress, userAddress common.Address, balanceSlot common.Hash) (*big.Int, common.Hash, error) {
	// Calculate the storage slot for the user's balance
	storageSlot := CalculateAltTokenBalanceSlot(userAddress, balanceSlot)

	// Get the value from storage
	value := state.GetState(tokenAddress, storageSlot)

	// Convert hash to big.Int
	balance := new(big.Int).SetBytes(value[:])

	return balance, storageSlot, nil
}
