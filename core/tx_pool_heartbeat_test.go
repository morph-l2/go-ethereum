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

package core

import (
	"math/big"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/crypto"
)

// The tests in this file exercise the heartbeat semantics introduced in
// P3-6 (upstream PR #33704). The invariants are:
//
//   1. bumpBeats is a no-op when the account has no existing heartbeat.
//   2. bumpBeats refreshes the timestamp when one already exists.
//   3. enqueueTx refreshes the heartbeat for external enqueues (addAll=true)
//      so long-lived but still-active queues are not evicted by Lifetime;
//      internal reshuffles (addAll=false) must not reset the heartbeat.
//   4. A pending-only replace via add() does not resurrect a heartbeat for
//      an account that has nothing queued.

func TestTxPoolBumpBeatsNoop(t *testing.T) {
	pool, _ := setupTxPool()
	defer pool.Stop()

	addr := common.HexToAddress("0xdeadbeef")
	pool.mu.Lock()
	defer pool.mu.Unlock()

	pool.bumpBeats(addr)
	if _, ok := pool.beats[addr]; ok {
		t.Fatalf("bumpBeats inserted a heartbeat for an unknown account")
	}
}

func TestTxPoolBumpBeatsUpdate(t *testing.T) {
	pool, _ := setupTxPool()
	defer pool.Stop()

	addr := common.HexToAddress("0xcafebabe")
	pool.mu.Lock()
	defer pool.mu.Unlock()

	past := time.Now().Add(-1 * time.Hour)
	pool.beats[addr] = past

	pool.bumpBeats(addr)
	got, ok := pool.beats[addr]
	if !ok {
		t.Fatalf("heartbeat was removed instead of refreshed")
	}
	if !got.After(past) {
		t.Fatalf("heartbeat not refreshed: got %v, want after %v", got, past)
	}
}

// TestTxPoolEnqueueAlwaysBumpsBeats verifies that repeat non-executable
// enqueues from the same sender refresh the heartbeat, preventing premature
// Lifetime eviction of long-lived-but-still-active queues. This matches the
// post-#33704 semantics for the enqueueTx path.
func TestTxPoolEnqueueAlwaysBumpsBeats(t *testing.T) {
	pool, key := setupTxPool()
	defer pool.Stop()

	from := crypto.PubkeyToAddress(key.PublicKey)
	pool.currentState.AddBalance(from, big.NewInt(1_000_000_000_000_000_000), tracing.BalanceChangeUnspecified)

	// First enqueue: nonce 2 (non-executable, current state nonce is 0).
	if err := pool.addRemoteSync(transaction(2, 100000, key)); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}

	pool.mu.RLock()
	first, ok := pool.beats[from]
	pool.mu.RUnlock()
	if !ok {
		t.Fatalf("heartbeat missing after first enqueue")
	}

	// Clamp the heartbeat to the past so the second enqueue produces a
	// distinctly larger timestamp without needing a real sleep.
	pool.mu.Lock()
	pool.beats[from] = first.Add(-time.Hour)
	pool.mu.Unlock()

	// Second enqueue: nonce 3 (also non-executable).
	if err := pool.addRemoteSync(transaction(3, 100000, key)); err != nil {
		t.Fatalf("second enqueue failed: %v", err)
	}

	pool.mu.RLock()
	second, ok := pool.beats[from]
	pool.mu.RUnlock()
	if !ok {
		t.Fatalf("heartbeat missing after second enqueue")
	}
	if !second.After(first) {
		t.Fatalf("second enqueue did not refresh heartbeat: first=%v second=%v", first, second)
	}
}

// TestTxPoolPendingReplaceDoesNotSpawnBeats verifies that the add() pending
// replacement path no longer unconditionally writes to beats. A sender
// whose transactions only ever reach the pending list (never the queue)
// must not accumulate a heartbeat entry, otherwise Lifetime eviction would
// track the wrong cohort.
func TestTxPoolPendingReplaceDoesNotSpawnBeats(t *testing.T) {
	pool, key := setupTxPool()
	defer pool.Stop()

	from := crypto.PubkeyToAddress(key.PublicKey)
	pool.currentState.AddBalance(from, big.NewInt(1_000_000_000_000_000_000), tracing.BalanceChangeUnspecified)

	// Insert an executable tx (nonce 0) so it lands directly in pending.
	if err := pool.addRemoteSync(transaction(0, 100000, key)); err != nil {
		t.Fatalf("initial pending insert failed: %v", err)
	}

	// Sanity: the queue for this sender is empty, so beats should be
	// untouched. If this invariant ever changes in morph, this test
	// serves as a clear signal.
	pool.mu.RLock()
	_, hadBeats := pool.beats[from]
	pool.mu.RUnlock()
	if hadBeats {
		t.Fatalf("beats populated after pure pending insertion; was expecting queue-only bookkeeping")
	}

	// Replace the pending tx with a higher-priced one of the same nonce
	// to exercise the L957 path.
	replacement := pricedTransaction(0, 100000, big.NewInt(2), key)
	if err := pool.addRemoteSync(replacement); err != nil {
		t.Fatalf("replacement insert failed: %v", err)
	}

	pool.mu.RLock()
	_, stillNoBeats := pool.beats[from]
	pool.mu.RUnlock()
	if stillNoBeats {
		t.Fatalf("pending replace path resurrected a heartbeat for queue-less sender")
	}

	// Verify the expected state: pending has the replacement, queue is empty.
	pool.mu.RLock()
	pendingList := pool.pending[from]
	queueList := pool.queue[from]
	pool.mu.RUnlock()
	if pendingList == nil || pendingList.Len() != 1 {
		t.Fatalf("pending list shape unexpected after replace: %+v", pendingList)
	}
	if queueList != nil && queueList.Len() > 0 {
		t.Fatalf("queue should remain empty, got %d entries", queueList.Len())
	}
	flat := pendingList.txs.Flatten()
	if len(flat) != 1 {
		t.Fatalf("expected 1 pending tx, got %d", len(flat))
	}
	if got, want := flat[0].Hash(), replacement.Hash(); got != want {
		t.Fatalf("pending head mismatch: got %x want %x", got, want)
	}
}
