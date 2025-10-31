package fees

import (
	"github.com/morph-l2/go-ethereum/core/types"
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/log"
)

// TokenRate returns the ETH exchange rate for the specified token,erc20Price / ethPrice
func TokenRate(state StateDB, tokenID *uint16) (*big.Int, *big.Int, error) {
	if tokenID == nil || *tokenID == 0 {
		return big.NewInt(1), big.NewInt(1), nil
	}
	addr, price, _, err := GetTokenInfoFromStorage(state, TokenRegistryAddress, *tokenID)
	if err != nil {
		log.Error("Failed to get token info from storage", "tokenID", tokenID, "error", err)
		return nil, nil, err
	}

	// If token address is zero, this is not a valid token
	if addr == (common.Address{}) {
		log.Error("Invalid token address", "tokenID", tokenID)
		return nil, nil, err
	}

	// If price is nil or zero, this token doesn't have a valid price
	if price == nil || price.Sign() == 0 {
		log.Error("Invalid token price", "tokenID", tokenID, "tokenAddr", addr.Hex())
		return nil, nil, err
	}
	scale := big.NewInt(10000) // TODO

	return price, scale, err
}

func EthToERC20(state StateDB, tokenID *uint16, amount *big.Int) (*big.Int, error) {
	rate, tokenSacle, err := TokenRate(state, tokenID)
	if err != nil {
		return nil, err
	}
	return types.EthToERC20(amount, rate, tokenSacle), nil
}

func ERC20ToETH(state StateDB, tokenID *uint16, amount *big.Int) (*big.Int, error) {
	rate, tokenSacle, err := TokenRate(state, tokenID)
	if err != nil {
		return nil, err
	}
	return types.ERC20ToEth(amount, rate, tokenSacle), nil
}
