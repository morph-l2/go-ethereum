package types

import "math/big"

// dual currency account
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

// price = erc20Price / ethPrice
// ethGasPrice / erc20GasPrice = erc20Price / ethPrice
// erc20GasPrice = ethGasPrice / price

func EthToERC20(amount, rate, tokenScale *big.Int) *big.Int {
	// TODO handle decimals
	if rate.Cmp(big.NewInt(0)) <= 0 {
		panic("invalid rate")
	}
	targetAmount := new(big.Int).Mul(amount, rate)
	return targetAmount
}

func ERC20ToEth(amount, rate, tokenScale *big.Int) *big.Int {
	// TODO handle decimals
	if rate.Cmp(big.NewInt(0)) <= 0 {
		panic("invalid rate")
	}
	return nil
}

type TokenRate struct {
	Rate  *big.Int
	Scale *big.Int
}
