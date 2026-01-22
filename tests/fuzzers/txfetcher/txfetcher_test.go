package txfetcher

import "testing"

func FuzzTxfetcher(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}
