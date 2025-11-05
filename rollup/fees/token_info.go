package fees

import (
	"fmt"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/rollup/rcfg"
)

// TokenRegistryAddress is the address of the ERC20PriceOracle contract
// - getTokenInfo(uint16 tokenID) returns (TokenInfo)
// - getTokenPrice(uint16 tokenID) returns (uint256)
// - getTokenIdByAddress(address) returns (uint16)
var TokenRegistryAddress = rcfg.L2TokenRegistryAddress

// Storage slots for ERC20PriceOracle contract
// - slot 0: mapping(uint16 => TokenInfo) public tokenRegistry
// - slot 1: mapping(address => uint16) public tokenRegistration
// - slot 2: mapping(uint16 => uint256) public priceRatio
var (
	// TokenRegistrySlot is the storage slot for mapping(uint16 => TokenInfo)
	// TokenInfo struct layout:
	//   - tokenAddress: address (offset 0)
	//   - balanceSlot: bytes32 (offset 1)
	//   - isActive: bool (offset 2, byte 0)
	//   - decimals: uint8 (offset 2, byte 1)
	//   - scale: uint256 (offset 3)
	TokenRegistrySlot = common.BigToHash(big.NewInt(0))
	// TokenRegistrationSlot is the storage slot for mapping(address => uint16)
	TokenRegistrationSlot = common.BigToHash(big.NewInt(1))
	// PriceRatioSlot is the storage slot for mapping(uint16 => uint256)
	PriceRatioSlot = common.BigToHash(big.NewInt(2))
)

// TokenInfo represents the token information structure
type TokenInfo struct {
	TokenAddress common.Address
	BalanceSlot  common.Hash
	IsActive     bool
	Decimals     uint8
	Scale        *big.Int
}

// CalculateUint16MappingSlot calculates the storage slot for a mapping key
// For mapping(key => value), the slot is: keccak256(abi.encode(key, mappingSlot))
func CalculateUint16MappingSlot(key uint16, mappingSlot common.Hash) common.Hash {
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

// CalculateStructFieldSlot calculates the storage slot for a struct field within a mapping
// For a struct at baseSlot, fieldOffset is the offset within the struct
func CalculateStructFieldSlot(baseSlot common.Hash, fieldOffset uint64) common.Hash {
	// Add fieldOffset to baseSlot
	baseInt := new(big.Int).SetBytes(baseSlot[:])
	fieldInt := big.NewInt(int64(fieldOffset))
	result := new(big.Int).Add(baseInt, fieldInt)
	return common.BigToHash(result)
}

// GetUint256MappingValue retrieves a value from a mapping storage slot
func GetUint256MappingValue(state StateDB, contractAddr common.Address, key uint16, mappingSlot common.Hash) (*big.Int, error) {
	// Calculate the storage slot
	storageKey := CalculateUint16MappingSlot(key, mappingSlot)

	// Get the value from storage
	value := state.GetState(contractAddr, storageKey)

	// Convert hash to big.Int
	result := new(big.Int).SetBytes(value[:])

	return result, nil
}

// GetTokenInfoStructBaseSlot calculates the base storage slot for a TokenInfo struct in the mapping
func GetTokenInfoStructBaseSlot(tokenID uint16) common.Hash {
	return CalculateUint16MappingSlot(tokenID, TokenRegistrySlot)
}

// GetTokenInfo retrieves the complete TokenInfo structure from storage
func GetTokenInfo(state StateDB, contractAddr common.Address, tokenID uint16) (*TokenInfo, error) {
	// Calculate the base slot for the TokenInfo struct
	baseSlot := GetTokenInfoStructBaseSlot(tokenID)

	// Read tokenAddress (offset 0)
	tokenAddressSlot := baseSlot
	tokenAddressValue := state.GetState(contractAddr, tokenAddressSlot)
	tokenAddress := common.BytesToAddress(tokenAddressValue[12:32])

	// Check if token exists (address != 0)
	if tokenAddress == (common.Address{}) {
		return nil, fmt.Errorf("token with ID %d not found", tokenID)
	}

	// Read balanceSlot (offset 1)
	balanceSlot := CalculateStructFieldSlot(baseSlot, 1)
	balanceSlotValue := state.GetState(contractAddr, balanceSlot)

	// Read isActive and decimals (offset 2)
	// In Solidity packed storage, bool and uint8 are packed from right to left
	// isActive (bool) is at byte 31 (rightmost/least significant)
	// decimals (uint8) is at byte 30 (second from right)
	statusSlot := CalculateStructFieldSlot(baseSlot, 2)
	statusValue := state.GetState(contractAddr, statusSlot)
	isActive := statusValue[31] != 0 // bool at byte 31 (rightmost)
	decimals := statusValue[30]      // uint8 at byte 30 (second from right)

	// Read scale (offset 3)
	scaleSlot := CalculateStructFieldSlot(baseSlot, 3)
	scaleValue := state.GetState(contractAddr, scaleSlot)
	scale := new(big.Int).SetBytes(scaleValue[:])

	return &TokenInfo{
		TokenAddress: tokenAddress,
		BalanceSlot:  balanceSlotValue,
		IsActive:     isActive,
		Decimals:     decimals,
		Scale:        scale,
	}, nil
}

// GetTokenScaleByIDWithState retrieves token scale from TokenInfo struct
func GetTokenScaleByIDWithState(state StateDB, contractAddr common.Address, tokenID uint16) (*big.Int, error) {
	info, err := GetTokenInfo(state, contractAddr, tokenID)
	if err != nil {
		return nil, err
	}
	return info.Scale, nil
}

// IsTokenActive checks if a token is active
func IsTokenActive(state StateDB, contractAddr common.Address, tokenID uint16) (bool, error) {
	info, err := GetTokenInfo(state, contractAddr, tokenID)
	if err != nil {
		return false, err
	}
	return info.IsActive, nil
}

// GetTokenPriceByIDWithState retrieves token price ratio from priceRatio mapping
func GetTokenPriceByIDWithState(state StateDB, contractAddr common.Address, tokenID uint16) (*big.Int, error) {
	return GetUint256MappingValue(state, contractAddr, tokenID, PriceRatioSlot)
}

// GetTokenInfoFromStorage retrieves token address, price, and balance slot from storage
// This is a convenience function that combines multiple storage reads
func GetTokenInfoFromStorage(state StateDB, contractAddr common.Address, tokenID uint16) (*TokenInfo, *big.Int, error) {
	// Get token info from TokenInfo struct
	info, err := GetTokenInfo(state, contractAddr, tokenID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get token info: %v", err)
	}

	// Get token price from priceRatio mapping
	price, err := GetTokenPriceByIDWithState(state, contractAddr, tokenID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get token price: %v", err)
	}

	return info, price, nil
}
