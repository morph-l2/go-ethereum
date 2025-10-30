package fees

import (
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/log"
)

// TokenRate returns the ETH exchange rate for the specified token,erc20Price / ethPrice
func TokenRate(db StateDB, tokenID *uint16) (*big.Int, *big.Int, error) {
	if tokenID == nil || *tokenID == 0 {
		return big.NewInt(1), big.NewInt(1), nil
	}
	addr, price, _, err := GetTokenInfoFromStorage(db, TokenRegistryAddress, *tokenID)
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
