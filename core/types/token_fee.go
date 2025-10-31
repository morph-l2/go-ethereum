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

// EthToERC20 erc20Amount = ethAmount / (tokenRate / tokenScale) = ethAmount * tokenScale / tokenRate
func EthToERC20(ethAmount, rate, tokenScale *big.Int) *big.Int {
	erc20Amount := new(big.Int)
	remainder := new(big.Int)
	erc20Amount.QuoRem(new(big.Int).Mul(ethAmount, tokenScale), rate, remainder)
	if remainder.Sign() != 0 {
		erc20Amount.Add(erc20Amount, big.NewInt(1))
	}
	return erc20Amount
}

// ERC20ToEth ethAmount = erc20Amount * (tokenRate / tokenScale)
func ERC20ToEth(erc20Amount, rate, tokenScale *big.Int) *big.Int {
	ethAmount := new(big.Int)
	remainder := new(big.Int)
	ethAmount.QuoRem(new(big.Int).Mul(erc20Amount, tokenScale), rate, remainder)
	if remainder.Sign() != 0 {
		ethAmount.Add(ethAmount, big.NewInt(1))
	}
	return ethAmount
}
