// Copyright 2024 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

//go:build gofuzz
// +build gofuzz

package blake2b

import "testing"

// FuzzBlake2bF is the native Go 1.18+ fuzz test for blake2b compression function.
// Run with: go test -tags=gofuzz -fuzz=FuzzBlake2bF -fuzztime=30s
func FuzzBlake2bF(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		Fuzz(data)
	})
}
