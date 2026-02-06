package difficulty

import "testing"

func FuzzDifficulty(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}
