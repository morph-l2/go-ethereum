package types

import "math/big"

// SuperAccount dual currency account
type SuperAccount struct {
	ethAmount   *big.Int
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