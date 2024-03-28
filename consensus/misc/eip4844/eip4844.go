package eip4844

import (
	"math/big"

	"github.com/scroll-tech/go-ethereum/params"
)

var (
	minBlobGasPrice            = big.NewInt(params.BlobTxMinBlobGasprice)
	blobGaspriceUpdateFraction = big.NewInt(params.BlobTxBlobGaspriceUpdateFraction)
)

// CalcExcessBlobGas calculates the excess blob gas after applying the set of
// blobs on top of the excess blob gas.
func CalcExcessBlobGas(parentExcessBlobGas uint64, parentBlobGasUsed uint64) uint64 {
	excessBlobGas := parentExcessBlobGas + parentBlobGasUsed
	if excessBlobGas < params.BlobTxTargetBlobGasPerBlock {
		return 0
	}
	return excessBlobGas - params.BlobTxTargetBlobGasPerBlock
}

// CalcBlobFee calculates the blobfee from the header's excess blob gas field.
func CalcBlobFee(excessBlobGas uint64) *big.Int {
	return fakeExponential(minBlobGasPrice, new(big.Int).SetUint64(excessBlobGas), blobGaspriceUpdateFraction)
}

// fakeExponential approximates factor * e ** (numerator / denominator) using
// Taylor expansion.
func fakeExponential(factor, numerator, denominator *big.Int) *big.Int {
	var (
		output = new(big.Int)
		accum  = new(big.Int).Mul(factor, denominator)
	)
	for i := 1; accum.Sign() > 0; i++ {
		output.Add(output, accum)

		accum.Mul(accum, numerator)
		accum.Div(accum, denominator)
		accum.Div(accum, big.NewInt(int64(i)))
	}
	return output.Div(output, denominator)
}
