package fees

import (
	"fmt"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/rollup/rcfg"
)

// TokenRegistryAddress is the address of the Token Registry contract
// This contract should implement:
// - getTokenAddress(uint16 tokenID) returns (address)
var TokenRegistryAddress = rcfg.L1GasPriceOracleAddress

// Common storage slots for token registry contract
var (
	//// MOCK DATA ////
	// TokenAddressMappingSlot is the storage slot for mapping(uint16 => address)
	TokenAddressMappingSlot = common.BigToHash(big.NewInt(0))
	// TokenPriceMappingSlot is the storage slot for mapping(uint16 => uint256)
	TokenPriceMappingSlot = common.BigToHash(big.NewInt(1))
	// TokenBalanceSlotMappingSlot is the storage slot for mapping(uint16 => bytes32)
	TokenBalanceSlotMappingSlot = common.BigToHash(big.NewInt(2))
)

func EthRate(tokenID uint16, db StateDB) *big.Int {
	addr, price, _, err := GetTokenInfoFromStorage(db, TokenRegistryAddress, tokenID)
	if err != nil {

	}
	// TODO erc20Price ethPrice
	return price
}

// CalculateMappingSlot calculates the storage slot for a mapping key
// For mapping(key => value), the slot is: keccak256(abi.encode(key, mappingSlot))
func CalculateMappingSlot(key uint16, mappingSlot common.Hash) common.Hash {
	// Convert key to 32 bytes (right-padded)
	keyBytes := make([]byte, 32)
	keyBytes[30] = byte(key >> 8) // high byte
	keyBytes[31] = byte(key)      // low byte

	// Convert mapping slot to 32 bytes (left-padded)
	slotBytes := mappingSlot.Bytes()
	paddedSlot := make([]byte, 32)
	copy(paddedSlot[32-len(slotBytes):], slotBytes)

	// Concatenate key and slot
	data := append(keyBytes, paddedSlot...)

	// Calculate keccak256 hash
	hash := crypto.Keccak256(data)

	return common.BytesToHash(hash)
}

// GetUint256MappingValue retrieves a value from a mapping storage slot
func GetUint256MappingValue(state StateDB, contractAddr common.Address, key uint16, mappingSlot common.Hash) (*big.Int, error) {
	// Calculate the storage slot
	storageKey := CalculateMappingSlot(key, mappingSlot)

	// Get the value from storage
	value := state.GetState(contractAddr, storageKey)

	// Convert hash to big.Int
	result := new(big.Int).SetBytes(value[:])

	return result, nil
}

// GetTokenPriceByIDWithState retrieves token price from storage slot using mapping(uint16 => uint256)
func GetTokenPriceByIDWithState(state StateDB, contractAddr common.Address, tokenID uint16, mappingSlot common.Hash) (*big.Int, error) {
	return GetUint256MappingValue(state, contractAddr, tokenID, mappingSlot)
}

// GetTokenAddressByIDWithState retrieves token address from storage slot using mapping(uint16 => address)
func GetTokenAddressByIDWithState(state StateDB, contractAddr common.Address, tokenID uint16, mappingSlot common.Hash) (common.Address, error) {
	// Calculate the storage slot
	storageKey := CalculateMappingSlot(tokenID, mappingSlot)

	// Get the value from storage
	value := state.GetState(contractAddr, storageKey)

	// Convert hash to address (last 20 bytes)
	address := common.BytesToAddress(value[12:32])

	return address, nil
}

// GetTokenBalanceSlotByIDWithState retrieves token balance slot from storage slot using mapping(uint16 => bytes32)
func GetTokenBalanceSlotByIDWithState(state StateDB, contractAddr common.Address, tokenID uint16, mappingSlot common.Hash) common.Hash {
	// Calculate the storage slot
	storageKey := CalculateMappingSlot(tokenID, mappingSlot)

	// Get the value from storage
	value := state.GetState(contractAddr, storageKey)

	return value
}

// GetTokenInfoFromStorage retrieves both token address and price from storage
func GetTokenInfoFromStorage(state StateDB, contractAddr common.Address, tokenID uint16) (common.Address, *big.Int, common.Hash, error) {
	// Get token address from mapping slot 0
	address, err := GetTokenAddressByIDWithState(state, contractAddr, tokenID, TokenAddressMappingSlot)
	if err != nil {
		return common.Address{}, nil, common.Hash{}, fmt.Errorf("failed to get token address: %v", err)
	}

	// Get token price from mapping slot 1
	price, err := GetTokenPriceByIDWithState(state, contractAddr, tokenID, TokenPriceMappingSlot)
	if err != nil {
		return common.Address{}, nil, common.Hash{}, fmt.Errorf("failed to get token price: %v", err)
	}

	balanceSlot := GetTokenBalanceSlotByIDWithState(state, contractAddr, tokenID, TokenBalanceSlotMappingSlot)

	return address, price, balanceSlot, nil
}
