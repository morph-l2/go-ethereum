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

func (gf *TokenFee) Eth() *big.Int {
	return new(big.Int).Mul(gf.Fee, gf.Rate)
}

// dual currency account
type SuperAccount struct {
	ethAmount *big.Int
	// TODO erc20 accounts
	erc20Amount ERC20Account
}

type ERC20Account = map[uint16]*big.Int

func (dca *SuperAccount) Eth() *big.Int {
	return dca.ethAmount
}

func (dca *SuperAccount) ERC20(id uint16) *big.Int {
	return dca.erc20Amount[id]
}

func (dca *SuperAccount) SetEthAmount(amount *big.Int) {
	dca.ethAmount = amount
}

func (dca *SuperAccount) SetERC20Amount(id uint16, amount *big.Int) {
	dca.erc20Amount[id] = amount
}
