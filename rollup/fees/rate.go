package fees

import (
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/log"
)

// TokenRate returns the ETH exchange rate for the specified token,erc20Price / ethPrice
func TokenRate(state StateDB, tokenID *uint16) (*big.Int, *big.Int, error) {
	if tokenID == nil || *tokenID == 0 {
		return big.NewInt(1), big.NewInt(1), nil
	}
	info, price, err := GetTokenInfoFromStorage(state, TokenRegistryAddress, *tokenID)
	if err != nil {
		log.Error("Failed to get token info from storage", "tokenID", tokenID, "error", err)
		return nil, nil, err
	}

	// If token address is zero, this is not a valid token
	if info.TokenAddress == (common.Address{}) {
		log.Error("Invalid token address", "tokenID", tokenID)
		return nil, nil, err
	}

	// If price is nil or zero, this token doesn't have a valid price
	if price == nil || price.Sign() == 0 {
		log.Error("Invalid token price", "tokenID", tokenID, "tokenAddr", info.TokenAddress.Hex())
		return nil, nil, err
	}

	// Get scale from token info
	scale, err := GetTokenScaleByIDWithState(state, TokenRegistryAddress, *tokenID)
	if err != nil {
		log.Error("Failed to get token scale", "tokenID", tokenID, "error", err)
		return nil, nil, err
	}

	return price, scale, err
}

func EthToERC20(state StateDB, tokenID *uint16, amount *big.Int) (*big.Int, error) {
	rate, tokenSacle, err := TokenRate(state, tokenID)
	if err != nil {
		return nil, err
	}
	return EthToERC20ByRateAndScale(amount, rate, tokenSacle), nil
}

func ERC20ToETH(state StateDB, tokenID *uint16, amount *big.Int) (*big.Int, error) {
	rate, tokenSacle, err := TokenRate(state, tokenID)
	if err != nil {
		return nil, err
	}
	return ERC20ToEthByRateAndScale(amount, rate, tokenSacle), nil
}

// EthToERC20ByRateAndScale erc20Amount = ethAmount / (tokenRate / tokenScale) = ethAmount * tokenScale / tokenRate
func EthToERC20ByRateAndScale(ethAmount, rate, tokenScale *big.Int) *big.Int {
	erc20Amount := new(big.Int)
	remainder := new(big.Int)
	erc20Amount.QuoRem(new(big.Int).Mul(ethAmount, tokenScale), rate, remainder)
	if remainder.Sign() != 0 {
		erc20Amount.Add(erc20Amount, big.NewInt(1))
	}
	return erc20Amount
}

// ERC20ToEthByRateAndScale ethAmount = erc20Amount * (tokenRate / tokenScale)
func ERC20ToEthByRateAndScale(erc20Amount, rate, tokenScale *big.Int) *big.Int {
	ethAmount := new(big.Int)
	remainder := new(big.Int)
	ethAmount.QuoRem(new(big.Int).Mul(erc20Amount, tokenScale), rate, remainder)
	if remainder.Sign() != 0 {
		ethAmount.Add(ethAmount, big.NewInt(1))
	}
	return ethAmount
}
