#!/bin/bash -eu
# Copyright 2024 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Move to the project directory
cd $SRC/go-ethereum

# Install native Go fuzzer build tool
go install github.com/AdamKorcz/go-118-fuzz-build@latest
go get github.com/AdamKorcz/go-118-fuzz-build/testing

# Define module path
MODULE="github.com/morph-l2/go-ethereum"

# ==============================================================================
# Compile native Go 1.18+ fuzz targets
# Using compile_native_go_fuzzer for func FuzzXxx(f *testing.F) style
# ==============================================================================

# ABI fuzzer - tests ABI encoding/decoding
compile_native_go_fuzzer $MODULE/tests/fuzzers/abi FuzzABI fuzz_abi

# Bitutil fuzzer - tests bit compression algorithms
compile_native_go_fuzzer $MODULE/tests/fuzzers/bitutil FuzzBitutil fuzz_bitutil

# Difficulty fuzzer - tests difficulty calculation algorithms
compile_native_go_fuzzer $MODULE/tests/fuzzers/difficulty FuzzDifficulty fuzz_difficulty

# Keystore fuzzer - tests keystore creation and unlocking
compile_native_go_fuzzer $MODULE/tests/fuzzers/keystore FuzzKeystore fuzz_keystore

# RLP fuzzer - tests RLP encoding/decoding
compile_native_go_fuzzer $MODULE/tests/fuzzers/rlp FuzzRlp fuzz_rlp

# Runtime fuzzer - tests EVM bytecode execution
compile_native_go_fuzzer $MODULE/tests/fuzzers/runtime FuzzRuntime fuzz_runtime

# Trie fuzzer - tests Merkle Patricia Trie operations
compile_native_go_fuzzer $MODULE/tests/fuzzers/trie FuzzTrie fuzz_trie

# StackTrie fuzzer - tests StackTrie consistency
compile_native_go_fuzzer $MODULE/tests/fuzzers/stacktrie FuzzStacktrie fuzz_stacktrie

# Range proof fuzzer - tests Merkle range proof verification
compile_native_go_fuzzer $MODULE/tests/fuzzers/rangeproof FuzzRangeproof fuzz_rangeproof

# TxFetcher fuzzer - tests P2P transaction fetcher state machine
compile_native_go_fuzzer $MODULE/tests/fuzzers/txfetcher FuzzTxfetcher fuzz_txfetcher

# VFlux ClientPool fuzzer - tests LES client pool management
compile_native_go_fuzzer $MODULE/tests/fuzzers/vflux FuzzVfluxClientPool fuzz_vflux_clientpool

# LES fuzzer - tests Light Ethereum Subprotocol message handling
compile_native_go_fuzzer $MODULE/tests/fuzzers/les FuzzLes fuzz_les

# ==============================================================================
# BN256 fuzzers - tests BN256 elliptic curve operations
# ==============================================================================
compile_native_go_fuzzer $MODULE/tests/fuzzers/bn256 FuzzBn256Add fuzz_bn256_add
compile_native_go_fuzzer $MODULE/tests/fuzzers/bn256 FuzzBn256Mul fuzz_bn256_mul
compile_native_go_fuzzer $MODULE/tests/fuzzers/bn256 FuzzBn256Pair fuzz_bn256_pair

# ==============================================================================
# secp256k1 fuzzer - tests secp256k1 curve point addition
# ==============================================================================
compile_native_go_fuzzer $MODULE/tests/fuzzers/secp256k1 FuzzSecp256k1 fuzz_secp256k1

# ==============================================================================
# BLS12-381 fuzzers - tests BLS12-381 elliptic curve operations
# ==============================================================================
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzG1Add fuzz_bls12381_g1_add
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzG1MultiExp fuzz_bls12381_g1_multiexp
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzG2Add fuzz_bls12381_g2_add
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzG2MultiExp fuzz_bls12381_g2_multiexp
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzPairing fuzz_bls12381_pairing
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzMapG1 fuzz_bls12381_map_g1
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzMapG2 fuzz_bls12381_map_g2
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzG1SubgroupChecks fuzz_bls12381_g1_subgroup
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzG2SubgroupChecks fuzz_bls12381_g2_subgroup

# Cross-library consistency checks
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzCrossG1Add fuzz_bls12381_cross_g1_add
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzCrossG2Add fuzz_bls12381_cross_g2_add
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzCrossG1MultiExp fuzz_bls12381_cross_g1_multiexp
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzCrossG2MultiExp fuzz_bls12381_cross_g2_multiexp
compile_native_go_fuzzer $MODULE/tests/fuzzers/bls12381 FuzzCrossPairing fuzz_bls12381_cross_pairing

# ==============================================================================
# Blake2b fuzzer - tests blake2b compression across CPU instruction sets
# ==============================================================================
compile_native_go_fuzzer $MODULE/crypto/blake2b FuzzBlake2bF fuzz_blake2b

echo "Native Go fuzzer build completed successfully!"
