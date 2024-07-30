package poseidon2

import (
	"math/big"
	"testing"

	"github.com/iden3/go-iden3-crypto/ff"
	"github.com/iden3/go-iden3-crypto/utils"
	"github.com/stretchr/testify/assert"
)

func TestPoseidonHashFixed(t *testing.T) {
	// b0 := big.NewInt(0)
	b1 := big.NewInt(1)
	b2 := big.NewInt(2)

	h, _ := HashFixed([]*big.Int{b1, b2})
	assert.Equal(t,
		"5297208644449048816064511434384511824916970985131888684874823260532015509555",
		h.String())
}

func TestSbox(t *testing.T) {
	b := ff.NewElement().SetBigInt(big.NewInt(2))
	assert.Equal(t,
		"2",
		b.String())
	b = SboxP(b)
	assert.Equal(t,
		"32",
		b.String())
}

func BenchmarkPoseidon2Hash(b *testing.B) {
	b0 := big.NewInt(0)
	b1 := utils.NewIntFromString("12242166908188651009877250812424843524687801523336557272219921456462821518061") //nolint:lll
	b2 := utils.NewIntFromString("12242166908188651009877250812424843524687801523336557272219921456462821518061") //nolint:lll

	bigArray3 := []*big.Int{b1, b2}

	for i := 0; i < b.N; i++ {
		HashFixedWithDomain(bigArray3, b0) //nolint:errcheck,gosec
	}
}
