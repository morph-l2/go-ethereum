package fees

import (
	"errors"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/log"
)

// TokenRate returns the ETH exchange rate for the specified token,erc20Price / ethPrice
func TokenRate(state StateDB, tokenID uint16) (*big.Int, *big.Int, error) {
	if tokenID == 0 {
		return nil, nil, errors.New("token id 0 not support")
	}
	info, rate, err := GetTokenInfoFromStorage(state, TokenRegistryAddress, tokenID)
	if err != nil {
		log.Error("Failed to get token info from storage", "tokenID", tokenID, "error", err)
		return nil, nil, err
	}

	if info.Scale == nil || info.Scale.Sign() == 0 {
		log.Error("Invalid token scale", "tokenID", tokenID, "tokenAddr", info.TokenAddress.Hex())
		return nil, nil, errors.New("invalid token scale")
	}

	// If token address is zero, this is not a valid token
	if info.TokenAddress == (common.Address{}) {
		log.Error("Invalid token address", "tokenID", tokenID)
		return nil, nil, errors.New("invalid token address")
	}

	// If price is nil or zero, this token doesn't have a valid price
	if rate == nil || rate.Sign() == 0 {
		log.Error("Invalid token price", "tokenID", tokenID, "tokenAddr", info.TokenAddress.Hex())
		return nil, nil, errors.New("invalid token price")
	}

	return rate, info.Scale, nil
}

func EthToAlt(state StateDB, tokenID uint16, amount *big.Int) (*big.Int, error) {
	rate, tokenSacle, err := TokenRate(state, tokenID)
	if err != nil {
		return nil, err
	}
	return types.EthToAlt(amount, rate, tokenSacle), nil
}

func AltToETH(state StateDB, tokenID uint16, amount *big.Int) (*big.Int, error) {
	rate, tokenSacle, err := TokenRate(state, tokenID)
	if err != nil {
		return nil, err
	}
	return types.AltToEth(amount, rate, tokenSacle), nil
}
