package stacktrie

import "testing"

func FuzzStacktrie(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}
