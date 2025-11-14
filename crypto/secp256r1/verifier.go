/*
 * @Author: Segue huoda.china@163.com
 * @Date: 2025-10-21 15:41:39
 * @LastEditors: Segue huoda.china@163.com
 * @LastEditTime: 2025-10-21 15:41:47
 * @FilePath: /morph-go-ethereum/crypto/secp256r1/verifier.go
 * @Description: 这是默认设置,请设置`customMade`, 打开koroFileHeader查看配置 进行设置: https://github.com/OBKoro1/koro1FileHeader/wiki/%E9%85%8D%E7%BD%AE
 */
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

// Package secp256r1 implements signature verification for the P256VERIFY precompile.
package secp256r1

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"math/big"
)

// Verify checks the given signature (r, s) for the given hash and public key (x, y).
func Verify(hash []byte, r, s, x, y *big.Int) bool {
	if x == nil || y == nil || !elliptic.P256().IsOnCurve(x, y) {
		return false
	}
	pk := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	return ecdsa.Verify(pk, hash, r, s)
}
