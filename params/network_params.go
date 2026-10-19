// Copyright 2017 The go-ethereum Authors
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

package params

// These are network parameters that need to be constant between clients, but
// aren't necessarily consensus related.

const (
	// BloomBitsBlocks is the number of blocks a single bloom bit section vector
	// contains on the server side.
	BloomBitsBlocks uint64 = 4096

	// BloomBitsBlocksClient is the number of blocks a single bloom bit section vector
	// contains on the light client side
	BloomBitsBlocksClient uint64 = 32768

	// BloomConfirms is the number of confirmation blocks before a bloom section is
	// considered probably final and its rotated bits are calculated.
	BloomConfirms = 256

	// CHTFrequency is the block frequency for creating CHTs
	CHTFrequency = 32768

	// BloomTrieFrequency is the block frequency for creating BloomTrie on both
	// server/client sides.
	BloomTrieFrequency = 32768

	// HelperTrieConfirmations is the number of confirmations before a client is expected
	// to have the given HelperTrie available.
	HelperTrieConfirmations = 2048

	// HelperTrieProcessConfirmations is the number of confirmations before a HelperTrie
	// is generated
	HelperTrieProcessConfirmations = 256

	// CheckpointFrequency is the block frequency for creating checkpoint
	CheckpointFrequency = 32768

	// CheckpointProcessConfirmations is the number before a checkpoint is generated
	CheckpointProcessConfirmations = 256

	// FullImmutabilityThreshold is the number of blocks after which a chain segment is
	// considered immutable (i.e. soft finality). It is used by the downloader as a
	// hard limit against deep ancestors, by the blockchain against deep reorgs, by
	// the freezer as the cutoff threshold and by clique as the snapshot trust limit.
	//
	// Morph note: raised from upstream's 90000 because on this chain the value's
	// only reachable consumer is the freezer cutoff, and 90000 is too small there.
	//
	// Ancient truncation is the sole irreversible step in chain rewinding.
	// setHeadBeyondRoot takes the destructive branch (`wipe`) only when the rewind
	// target falls below the freezer cutoff; a rewind that stays above it leaves
	// every block on disk and lets the node re-execute forward, which is what
	// upstream's "try to skip touching the header chain altogether" repair path
	// intends. Morph's rewind depth is driven by how far the snapshot disk layer
	// lags the head -- a storage property unrelated to reorg depth -- and on a
	// low-throughput chain that lag has been observed at ~286k blocks, far beyond
	// 90000. A cutoff of 1000000 keeps such a rewind non-destructive and
	// self-recoverable.
	//
	// The other consumers are unreachable here and so unaffected: Morph blocks
	// carry zero difficulty, so chainSyncer.nextSyncOp always short-circuits on
	// `op.td.Cmp(ourTD) <= 0` and the eth/les downloaders never reach
	// findAncestor's fullMaxForkAncestry floor; clique is not the engine in use.
	// If block difficulty ever becomes non-zero, revisit the downloader impact.
	//
	// Cost: the freezer holds ~340 bytes per block, so this keeps roughly 340MB
	// more (compressed-equivalent) in the key-value store, traded against
	// irreversibly deleting block data.
	FullImmutabilityThreshold = 1000000

	// LightImmutabilityThreshold is the number of blocks after which a header chain
	// segment is considered immutable for light client(i.e. soft finality). It is used by
	// the downloader as a hard limit against deep ancestors, by the blockchain against deep
	// reorgs, by the light pruner as the pruning validity guarantee.
	LightImmutabilityThreshold = 30000
)
