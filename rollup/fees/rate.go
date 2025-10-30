package fees

import (
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/log"
)

// EthRate returns the ETH exchange rate for the specified token,erc20Price / ethPrice
func EthRate(db StateDB, tokenID *uint16) (*big.Int, error) {
	if tokenID == nil || *tokenID == 0 {
		return big.NewInt(1), nil
	}
	addr, price, _, err := GetTokenInfoFromStorage(db, TokenRegistryAddress, *tokenID)
	if err != nil {
		log.Error("Failed to get token info from storage", "tokenID", tokenID, "error", err)
		return nil, err
	}

	// If token address is zero, this is not a valid token
	if addr == (common.Address{}) {
		log.Error("Invalid token address", "tokenID", tokenID)
		return nil, err
	}

	// If price is nil or zero, this token doesn't have a valid price
	if price == nil || price.Sign() == 0 {
		log.Error("Invalid token price", "tokenID", tokenID, "tokenAddr", addr.Hex())
		return nil, err
	}

	return price, err
}
