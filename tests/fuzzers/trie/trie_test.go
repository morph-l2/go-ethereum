package trie

import "testing"

func FuzzTrie(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}
