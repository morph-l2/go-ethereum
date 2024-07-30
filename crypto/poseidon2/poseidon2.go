// from github.com/iden3/go-iden3-crypto/ff/poseidon

package poseidon2

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/iden3/go-iden3-crypto/ff"
	"github.com/iden3/go-iden3-crypto/utils"
	"github.com/scroll-tech/go-ethereum/log"
)

const MAX_WIDTH = 3

func permute(input []*ff.Element, t int) []*ff.Element {

	currentState := make([]*ff.Element, len(input))
	copy(currentState, input)

	// Linear layer at beginning
	MatmulExternal(currentState)

	for r := 0; r < 4; r++ {
		currentState = AddRc(currentState, RoundConstants[r])
		currentState = Sbox(currentState)
		MatmulExternal(currentState)
	}

	pEnd := 4 + 56
	for r := 4; r < pEnd; r++ {
		currentState[0].Add(currentState[0], &RoundConstants[r][0])
		currentState[0] = SboxP(currentState[0])
		MatmulInternal(currentState)
	}

	for r := pEnd; r < 64; r++ {
		currentState = AddRc(currentState, RoundConstants[r])
		currentState = Sbox(currentState)
		MatmulExternal(currentState)
	}
	return currentState
}

func Sbox(input []*ff.Element) []*ff.Element {
	output := make([]*ff.Element, len(input))
	for i, el := range input {
		output[i] = new(ff.Element)
		output[i] = SboxP(el)
	}
	return output
}

func SboxP(input *ff.Element) *ff.Element {
	input2 := new(ff.Element)
	input2.Square(input)
	out := new(ff.Element)
	out.Square(input2)
	out.Mul(out, input)
	return out
}

// MatmulExternal is a matrix multiplication with the external matrix
// [2, 1, 1]
// [1, 2, 1]
// [1, 1, 2]
func MatmulExternal(input []*ff.Element) {
	sum := new(ff.Element)
	sum.Add(input[0], input[1])
	sum.Add(sum, input[2])
	input[0].Add(input[0], sum)
	input[1].Add(input[1], sum)
	input[2].Add(input[2], sum)
}

// MatmulInternal is a matrix multiplication with the internal matrix
// [2, 1, 1]
// [1, 2, 1]
// [1, 1, 3]
func MatmulInternal(input []*ff.Element) {
	sum := new(ff.Element)
	sum.Add(input[0], input[1])
	sum.Add(sum, input[2])
	input[0].Add(input[0], sum)
	input[1].Add(input[1], sum)
	input[2].Double(input[2])
	input[2].Add(input[2], sum)
}

func AddRc(input []*ff.Element, rc []ff.Element) []*ff.Element {
	output := make([]*ff.Element, len(input))
	for i := range input {
		output[i] = new(ff.Element)
		output[i].Add(input[i], &rc[i])
	}
	return output
}

// Implementing MerkleTreeHash interface
type MerkleTreeHash interface {
	Compress(input []*ff.Element) *ff.Element
}

func Compress(input []*ff.Element) *ff.Element {
	return permute([]*ff.Element{input[0], input[1], ff.NewElement()}, 3)[0]
}

// for short, use size of inpBI as cap
func Hash(inpBI []*big.Int, width int) (*big.Int, error) {
	return HashWithCap(inpBI, width, int64(len(inpBI)))
}

// Hash using possible sponge specs specified by width (rate from 1 to 15), the size of input is applied as capacity
// (notice we do not include width in the capacity )
func HashWithCap(inpBI []*big.Int, width int, nBytes int64) (*big.Int, error) {
	if width < 2 {
		return nil, fmt.Errorf("width must be ranged from 2 to 16")
	}
	if width > MAX_WIDTH {
		return nil, fmt.Errorf("invalid inputs width %d, max %d", width, MAX_WIDTH) //nolint:gomnd,lll
	}

	// capflag = nBytes * 2^64
	pow64 := big.NewInt(1)
	pow64.Lsh(pow64, 64)
	capflag := ff.NewElement().SetBigInt(big.NewInt(nBytes))
	capflag.Mul(capflag, ff.NewElement().SetBigInt(pow64))

	// initialize the state
	state := make([]*ff.Element, MAX_WIDTH)
	state[0] = ff.NewElement().Set(capflag)

	rate := width - 1
	i := 0
	// always perform one round of permutation even when input is empty
	for {
		// each round absorb at most `rate` elements from `inpBI`
		for j := 0; j < rate && i < len(inpBI); i, j = i+1, j+1 {
			state[j+1].Add(state[j+1], ff.NewElement().SetBigInt(inpBI[i]))
		}
		state = permute(state, width)
		if i == len(inpBI) {
			break
		}
	}

	// squeeze
	rE := state[0]
	r := big.NewInt(0)
	rE.ToBigIntRegular(r)
	return r, nil

}

// Hash computes the Poseidon hash for the given fixed-size inputs, with specified domain field
func HashFixedWithDomain(inpBI []*big.Int, domain *big.Int) (*big.Int, error) {
	t := len(inpBI) + 1
	// if len(inpBI) == 0 || len(inpBI) > len(NROUNDSP) {
	// 	return nil, fmt.Errorf("invalid inputs length %d, max %d", len(inpBI), len(NROUNDSP)) //nolint:gomnd,lll
	// }
	if !utils.CheckBigIntArrayInField(inpBI[:]) {
		return nil, errors.New("inputs values not inside Finite Field")
	}
	inp := make([]*ff.Element, MAX_WIDTH)
	for idx, bi := range inpBI {
		inp[idx] = ff.NewElement().SetBigInt(bi)
	}

	state := make([]*ff.Element, MAX_WIDTH)
	state[0] = ff.NewElement().SetBigInt(domain)
	copy(state[1:], inp[:])

	state = permute(state, t)

	rE := state[0]
	r := big.NewInt(0)
	rE.ToBigIntRegular(r)
	return r, nil
}

// Deprecated HashFixed entry, with domain field is 0
func HashFixed(inpBI []*big.Int) (*big.Int, error) {
	log.Warn("called a deprecated method for poseidon fixed hash", "inputs", inpBI)
	return HashFixedWithDomain(inpBI, big.NewInt(0))
}
