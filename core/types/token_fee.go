package types

import (
	"errors"
	"math/big"
)

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
	if dca.ethAmount == nil {
		return new(big.Int)
	}
	return dca.ethAmount
}

func (dca *SuperAccount) Alt(id uint16) *big.Int {
	if dca.altAmount[id] == nil {
		return new(big.Int)
	}
	return dca.altAmount[id]
}

func (dca *SuperAccount) Alts() AltAccount {
	if dca.altAmount == nil {
		return make(AltAccount)
	}
	return dca.altAmount
}

func (dca *SuperAccount) SetEthAmount(amount *big.Int) {
	dca.ethAmount = amount
}

func (dca *SuperAccount) SetAltAmount(id uint16, amount *big.Int) {
	dca.altAmount[id] = amount
}

// EthToAlt altAmount = ethAmount / (tokenRate / tokenScale) = ethAmount * tokenScale / tokenRate
func EthToAlt(ethAmount, rate, tokenScale *big.Int) (*big.Int, error) {
	if rate == nil || rate.Sign() <= 0 {
		return nil, errors.New("invalid rate")
	}
	if tokenScale == nil || tokenScale.Sign() <= 0 {
		return nil, errors.New("invalid token scale")
	}
	altAmount := new(big.Int)
	remainder := new(big.Int)
	altAmount.QuoRem(new(big.Int).Mul(ethAmount, tokenScale), rate, remainder)
	if remainder.Sign() != 0 {
		altAmount.Add(altAmount, big.NewInt(1))
	}
	return altAmount, nil
}

// AltToEth ethAmount = altAmount * (tokenRate / tokenScale)
//
// Rounds DOWN (floor). AltToEth converts a user's token balance into the
// ETH-equivalent budget used to cap the gas allowance in estimation
// (eth_estimateGas / eth_call). Fees are actually charged at execution via
// EthToAlt, which rounds UP: paying for G wei of gas costs
// ceil(G*tokenScale/rate) tokens. The largest ETH fee a balance `a` can cover
// is therefore exactly floor(a*rate/tokenScale) — the floor here is the exact
// inverse of EthToAlt's ceiling. Rounding up would credit up to 1 wei more
// than the user can actually pay, over-stating the allowance and producing
// estimates that fail at execution. Floor also matches morph-reth's
// token_amount_to_eth, keeping the two clients' estimates consistent.
func AltToEth(erc20Amount, rate, tokenScale *big.Int) (*big.Int, error) {
	if rate == nil || rate.Sign() <= 0 {
		return nil, errors.New("invalid rate")
	}
	if tokenScale == nil || tokenScale.Sign() <= 0 {
		return nil, errors.New("invalid token scale")
	}
	return new(big.Int).Quo(new(big.Int).Mul(erc20Amount, rate), tokenScale), nil
}
