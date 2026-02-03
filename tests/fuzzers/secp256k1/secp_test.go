package secp256k1

import "testing"

// FuzzSecp256k1 is the native Go 1.18+ fuzz test.
// Run with: go test -fuzz=FuzzSecp256k1 -fuzztime=30s
func FuzzSecp256k1(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}

// TestFuzzer can be used to replicate crashers from the fuzzing tests.
func TestFuzzer(t *testing.T) {
	test := "00000000N0000000/R00000000000000000U0000S0000000mkhP000000000000000U"
	Fuzz([]byte(test))
}
