package types

import "math/big"

// SuperAccount dual currency account
type SuperAccount struct {
	ethAmount *big.Int
	altAmount AltAccount
}

type AltAccount = map[uint16]*big.Int

func NewSuperAccount() *SuperAccount {
	return &SuperAccount{
		ethAmount: new(big.Int),
		altAmount: make(AltAccount),
	}
}
func (dca *SuperAccount) Eth() *big.Int {
	return dca.ethAmount
}

func (dca *SuperAccount) Alt(id uint16) *big.Int {
	if dca.altAmount[id] == nil {
		return new(big.Int)
	}
	return dca.altAmount[id]
}

func (dca *SuperAccount) SetEthAmount(amount *big.Int) {
	dca.ethAmount = amount
}

func (dca *SuperAccount) SetAltAmount(id uint16, amount *big.Int) {
	dca.altAmount[id] = amount
}

// EthToAlt altAmount = ethAmount / (tokenRate / tokenScale) = ethAmount * tokenScale / tokenRate
func EthToAlt(ethAmount, rate, tokenScale *big.Int) *big.Int {
	altAmount := new(big.Int)
	remainder := new(big.Int)
	altAmount.QuoRem(new(big.Int).Mul(ethAmount, tokenScale), rate, remainder)
	if remainder.Sign() != 0 {
		altAmount.Add(altAmount, big.NewInt(1))
	}
	return altAmount
}

// AltToEth ethAmount = altAmount * (tokenRate / tokenScale)
func AltToEth(erc20Amount, rate, tokenScale *big.Int) *big.Int {
	ethAmount := new(big.Int)
	remainder := new(big.Int)
	ethAmount.QuoRem(new(big.Int).Mul(erc20Amount, tokenScale), rate, remainder)
	if remainder.Sign() != 0 {
		ethAmount.Add(ethAmount, big.NewInt(1))
	}
	return ethAmount
}
