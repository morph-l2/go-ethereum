package types

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/params"
)

func TestERC20Price(t *testing.T) {
	gasPriceByEth := big.NewInt(params.GWei) // 1 GWei = 10^9 wei
	ethPrice := big.NewInt(4000)             // 1 ETH = 4000 USD
	usdtPrice := big.NewInt(1)               // 1 USDT = 1 USD

	// gasPriceByUsdt = gasPriceByEth * ethPrice * 10^usdtDecimals / (usdtPrice * 10^ethDecimals)

	gasPriceByUsdt := new(big.Int).Mul(gasPriceByEth, ethPrice)
	gasPriceByUsdt.Mul(gasPriceByUsdt, big.NewInt(1e6)) // 10^usdtDecimals
	gasPriceByUsdt.Div(gasPriceByUsdt, usdtPrice)
	gasPriceByUsdt.Div(gasPriceByUsdt, big.NewInt(1e18)) // 10^ethDecimals

	fmt.Printf("Gas Price by ETH: %s wei\n", gasPriceByEth.String())
	fmt.Printf("Gas Price by USDT: %s (USDT smallest unit)\n", gasPriceByUsdt.String())

	expected := big.NewInt(4)
	if gasPriceByUsdt.Cmp(expected) != 0 {
		t.Errorf("Expected %s, got %s", expected.String(), gasPriceByUsdt.String())
	}
}
