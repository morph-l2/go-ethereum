// Copyright 2026 The go-ethereum Authors
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

package usbwallet

import (
	"math/big"
	"testing"
)

func TestLedgerEIP155Version(t *testing.T) {
	chainID := big.NewInt(1)
	tests := []struct {
		name        string
		version     [3]byte
		chainID     *big.Int
		unsupported bool
	}{
		{name: "0.0.0", version: [3]byte{0, 0, 0}, chainID: chainID, unsupported: true},
		{name: "0.1.0", version: [3]byte{0, 1, 0}, chainID: chainID, unsupported: true},
		{name: "1.0.2", version: [3]byte{1, 0, 2}, chainID: chainID, unsupported: true},
		{name: "1.0.3", version: [3]byte{1, 0, 3}, chainID: chainID},
		{name: "1.1.0", version: [3]byte{1, 1, 0}, chainID: chainID},
		{name: "2.0.0", version: [3]byte{2, 0, 0}, chainID: chainID},
		{name: "nil chain ID", version: [3]byte{1, 0, 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if unsupported := ledgerEIP155Unsupported(test.version, test.chainID); unsupported != test.unsupported {
				t.Fatalf("unsupported mismatch: have %v, want %v", unsupported, test.unsupported)
			}
		})
	}
}
