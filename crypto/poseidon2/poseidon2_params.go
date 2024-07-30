package poseidon2

import (
	"errors"

	"github.com/iden3/go-iden3-crypto/ff"
)

type Poseidon2Params struct {
	t                 int
	d                 int
	roundsFBeginning  int
	roundsP           int
	roundsFEnd        int
	rounds            int
	MatInternalDiagM1 []ff.Element
	MatInternal       [][]ff.Element
	RoundConstants    [][]ff.Element
}

const INIT_SHAKE = "Poseidon2"

func NewPoseidon2Params(
	t int,
	d int,
	roundsF int,
	roundsP int,
	matInternalDiagM1 []ff.Element,
	matInternal [][]ff.Element,
	roundConstants [][]ff.Element,
) (*Poseidon2Params, error) {
	if d != 3 && d != 5 && d != 7 && d != 11 {
		return nil, errors.New("invalid sbox degree")
	}
	if roundsF%2 != 0 {
		return nil, errors.New("roundsF must be even")
	}
	r := roundsF / 2
	rounds := roundsF + roundsP

	return &Poseidon2Params{
		t:                 t,
		d:                 d,
		roundsFBeginning:  r,
		roundsP:           roundsP,
		roundsFEnd:        r,
		rounds:            rounds,
		MatInternalDiagM1: matInternalDiagM1,
		MatInternal:       matInternal,
		RoundConstants:    roundConstants,
	}, nil
}

func MatVecMul(mat [][]*ff.Element, input []*ff.Element) []*ff.Element {
	t := len(mat)
	if t != len(input) {
		panic("matrix and input size mismatch")
	}
	out := make([]*ff.Element, t)
	for i := range out {
		out[i] = ff.NewElement()
	}
	for row := 0; row < t; row++ {
		for col, inp := range input {
			tmp := mat[row][col]
			tmp.Mul(tmp, inp)
			out[row].Add(out[row], tmp)
		}
	}
	return out
}
