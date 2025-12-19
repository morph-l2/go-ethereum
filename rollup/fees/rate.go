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
	info, rate, err := GetTokenInfoFromStorage(state, tokenID)
	if err != nil {
		log.Error("Failed to get token info from storage", "tokenID", tokenID, "error", err)
		return nil, nil, err
	}
	// If token address is zero, this is not a valid token
	if info.TokenAddress == (common.Address{}) {
		log.Error("Invalid token address", "tokenID", tokenID)
		return nil, nil, errors.New("invalid token address")
	}
	return rate, info.Scale, nil
}

func EthToAlt(state StateDB, tokenID uint16, amount *big.Int) (*big.Int, error) {
	rate, scale, err := TokenRate(state, tokenID)
	if err != nil {
		return nil, err
	}
	return types.EthToAlt(amount, rate, scale)
}

func AltToETH(state StateDB, tokenID uint16, amount *big.Int) (*big.Int, error) {
	rate, scale, err := TokenRate(state, tokenID)
	if err != nil {
		return nil, err
	}
	return types.AltToEth(amount, rate, scale)
}
