package bn256

import "testing"

func FuzzBn256Add(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzAdd(data)
	})
}

func FuzzBn256Mul(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzMul(data)
	})
}

func FuzzBn256Pair(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzPair(data)
	})
}

func FuzzBn256UnmarshalG1(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzUnmarshalG1(data)
	})
}

func FuzzBn256UnmarshalG2(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzUnmarshalG2(data)
	})
}