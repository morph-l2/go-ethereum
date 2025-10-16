package types

import "math/big"

var DefaultRate = big.NewInt(1)
var ZeroTokenFee = &TokenFee{
	Fee:  big.NewInt(0),
	Rate: DefaultRate,
}

type TokenFee struct {
	Fee  *big.Int
	Rate *big.Int
}

func (gf *TokenFee) EthAmount() *big.Int {
	return big.NewInt(0)
}
