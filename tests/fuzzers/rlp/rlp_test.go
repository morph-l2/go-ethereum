package rlp

import "testing"

func FuzzRlp(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}
