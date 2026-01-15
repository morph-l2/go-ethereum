package rangeproof

import "testing"

func FuzzRangeproof(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}
