package bn256

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
)

func TestFuzzPair(t *testing.T) {
	for i := 0; i < 1000; i++ {
		data := make([]byte, 256)
		rand.Read(data)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Logf("Panic on iteration %d with input:\n%s", i, hex.EncodeToString(data))
					t.Fatalf("Panic: %v", r)
				}
			}()
			fuzzPair(data)
		}()
	}
	t.Log("1000 iterations passed")
}

func TestFuzzAdd(t *testing.T) {
	for i := 0; i < 1000; i++ {
		data := make([]byte, 256)
		rand.Read(data)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Logf("Panic on iteration %d with input:\n%s", i, hex.EncodeToString(data))
					t.Fatalf("Panic: %v", r)
				}
			}()
			fuzzAdd(data)
		}()
	}
	t.Log("1000 iterations passed")
}

func TestFuzzMul(t *testing.T) {
	for i := 0; i < 1000; i++ {
		data := make([]byte, 256)
		rand.Read(data)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Logf("Panic on iteration %d with input:\n%s", i, hex.EncodeToString(data))
					t.Fatalf("Panic: %v", r)
				}
			}()
			fuzzMul(data)
		}()
	}
	t.Log("1000 iterations passed")
}

