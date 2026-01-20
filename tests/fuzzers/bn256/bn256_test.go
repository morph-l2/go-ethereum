package bn256

import "testing"

func FuzzBn256Add(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		FuzzAdd(data)
	})
}

func FuzzBn256Mul(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		FuzzMul(data)
	})
}

func FuzzBn256Pair(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		FuzzPair(data)
	})
}
