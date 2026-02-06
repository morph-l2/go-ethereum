package vflux

import "testing"

func FuzzVfluxClientPool(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		FuzzClientPool(data)
	})
}
