package bitutil

import "testing"

func FuzzBitutil(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}
