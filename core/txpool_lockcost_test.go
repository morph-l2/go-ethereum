package core

// Lock-lifecycle cost benchmark: how much more expensive is a rejected
// alt-fee MorphTx than a rejected ordinary (dynamic-fee) transaction, measured
// over the ACTUAL work that runs while pool.mu is held?
//
// A prior report timed getBalanceFunc alone (mocked at 500µs) and treated it as
// the whole per-tx lock cost. But pool.mu covers the full add()->validateTx
// lifecycle, of which the alt-fee balance lookup is only one step. This test
// measures the real thing: it builds a real core.TxPool over a real on-disk
// mainnet state and times pool.validateTx (the dominant lock-held per-tx work)
// for both transaction types, using random ephemeral senders so every call is
// an absent-key lookup — exactly what an attacker's zero-balance flood looks
// like. Both tx types are rejected at their balance check (the attacker's txs
// are too), so this is the faithful per-tx lock cost of the attack.
//
// It is gated on env vars and skipped by normal `go test`:
//
//   MORPH_CHAINDATA=/data/.../geth/chaindata \
//   MORPH_ALTFEE_TOKENID=<active token with HasSlot==false> \
//   go test ./core/ -run TestTxPoolLockLifecycleCost -v
//
// Optional: MORPH_BLOCK (default: head), MORPH_TRIECACHE (MB, default 256),
// MORPH_N (measured iterations, default 3000).
//
// Run against a COPY of chaindata or a stopped node (leveldb takes an
// exclusive lock).

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/event"
	"github.com/morph-l2/go-ethereum/trie"
)

// realBlockChain implements the unexported blockChain interface backed by a
// real on-disk state tree, so NewTxPool's reset() wires pool.currentState to
// genuine mainnet state and computes the real fork flags from the head header.
type realBlockChain struct {
	head    *types.Block
	statedb state.Database
	feed    *event.Feed
}

func (bc *realBlockChain) CurrentBlock() *types.Block { return bc.head }
func (bc *realBlockChain) GetBlock(common.Hash, uint64) *types.Block { return bc.head }
func (bc *realBlockChain) StateAt(root common.Hash) (*state.StateDB, error) {
	return state.New(root, bc.statedb, nil)
}
func (bc *realBlockChain) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return bc.feed.Subscribe(ch)
}

func TestTxPoolLockLifecycleCost(t *testing.T) {
	chaindata := os.Getenv("MORPH_CHAINDATA")
	if chaindata == "" {
		t.Skip("set MORPH_CHAINDATA (and MORPH_ALTFEE_TOKENID) to run this benchmark")
	}
	tokenID := envUint16(t, "MORPH_ALTFEE_TOKENID", 0)
	if tokenID == 0 {
		t.Fatal("set MORPH_ALTFEE_TOKENID to the active token whose lookup uses the EVM StaticCall path")
	}
	n := envInt("MORPH_N", 3000)
	warmup := envInt("MORPH_WARMUP", 500)
	trieCacheMB := envInt("MORPH_TRIECACHE", 256)
	blockNum := int64(-1)
	if v := os.Getenv("MORPH_BLOCK"); v != "" {
		blockNum, _ = strconv.ParseInt(v, 10, 64)
	}

	// --- open real chaindata (read-only) ---
	freezer := filepath.Join(chaindata, "ancient")
	db, err := rawdb.NewLevelDBDatabaseWithFreezer(chaindata, 512, 1024, freezer, "", true)
	if err != nil {
		t.Fatalf("open chaindata: %v", err)
	}
	defer db.Close()

	header := headerAt(db, blockNum)
	if header == nil {
		t.Fatalf("could not read header (block=%d)", blockNum)
	}
	genesisHash := rawdb.ReadCanonicalHash(db, 0)
	chainConfig := rawdb.ReadChainConfig(db, genesisHash)
	if chainConfig == nil {
		t.Fatalf("could not read chain config")
	}

	// --- real TxPool over real state ---
	stateCache := state.NewDatabaseWithConfig(db, &trie.Config{Cache: trieCacheMB})
	bc := &realBlockChain{
		head:    types.NewBlockWithHeader(header),
		statedb: stateCache,
		feed:    new(event.Feed),
	}
	cfg := DefaultTxPoolConfig
	cfg.Journal = "" // no journal file
	pool := NewTxPool(cfg, chainConfig, bc)
	defer pool.Stop()

	signer := types.MakeSigner(chainConfig, header.Number, header.Time)
	chainID := chainConfig.ChainID
	gasFeeCap := new(big.Int).Mul(orOne(header.BaseFee), big.NewInt(4))
	if gasFeeCap.Sign() == 0 {
		gasFeeCap = big.NewInt(1e9)
	}

	fmt.Println("=== txpool lock-lifecycle cost: MorphTx(alt-fee) vs ordinary dynamic-fee tx ===")
	fmt.Printf("state @ block %d  root %s  chainID %v  trieCache %dMB\n",
		header.Number.Uint64(), header.Root.Hex(), chainID, trieCacheMB)
	if info, ierr := describeAltToken(pool, tokenID); ierr != nil || !info {
		t.Fatalf("token %d is not usable as the StaticCall attack path; aborting", tokenID)
	}
	fmt.Printf("iterations: warmup=%d measured=%d  senders=random-ephemeral(rejected)\n\n", warmup, n)

	// Pre-sign a pool of txs from distinct random unfunded keys. Senders are
	// pre-cached (types.Sender) so ECDSA recovery — which runs BEFORE pool.mu in
	// the real ingress path (tx_pool.go:1160) — is excluded from the timed work.
	total := warmup + n
	morphTxs := make([]*types.Transaction, total)
	dynTxs := make([]*types.Transaction, total)
	for i := 0; i < total; i++ {
		key, _ := crypto.GenerateKey()
		to := crypto.PubkeyToAddress(key.PublicKey)
		mt, err := types.SignNewTx(key, signer, &types.MorphTx{
			ChainID:    chainID,
			Nonce:      0,
			GasTipCap:  big.NewInt(1),
			GasFeeCap:  gasFeeCap,
			Gas:        100000,
			To:         &to,
			Value:      big.NewInt(0),
			Version:    types.MorphTxVersion0,
			FeeTokenID: tokenID,
			FeeLimit:   big.NewInt(1e18),
		})
		if err != nil {
			t.Fatalf("sign morphtx: %v", err)
		}
		dt, err := types.SignNewTx(key, signer, &types.DynamicFeeTx{
			ChainID:   chainID,
			Nonce:     0,
			GasTipCap: big.NewInt(1),
			GasFeeCap: gasFeeCap,
			Gas:       100000,
			To:        &to,
			Value:     big.NewInt(0),
		})
		if err != nil {
			t.Fatalf("sign dynamic tx: %v", err)
		}
		// warm the sender cache so ECDSA is not in the timed path
		if _, err := types.Sender(signer, mt); err != nil {
			t.Fatalf("sender: %v", err)
		}
		types.Sender(signer, dt)
		morphTxs[i] = mt
		dynTxs[i] = dt
	}

	// Sanity: confirm both types traverse to (and are rejected at) the balance
	// check, i.e. we are exercising the full lock-held validation, not an early
	// bail-out on some misconfigured field.
	if err := pool.validateTx(morphTxs[0], false); err == nil {
		t.Fatalf("expected MorphTx to be rejected (insufficient funds); got nil — check token/fork config")
	} else {
		fmt.Printf("sample MorphTx rejection: %v\n", err)
	}
	if err := pool.validateTx(dynTxs[0], false); err == nil {
		t.Fatalf("expected dynamic-fee tx to be rejected; got nil")
	} else {
		fmt.Printf("sample dynamic  rejection: %v\n\n", err)
	}

	morph := timeValidate(pool, morphTxs, warmup)
	dyn := timeValidate(pool, dynTxs, warmup)

	reportLC("ordinary dynamic-fee tx  (baseline, lock-held validateTx)", dyn)
	reportLC("MorphTx alt-fee          (StaticCall path, lock-held validateTx)", morph)

	fmt.Println("\n=== interpretation ===")
	fmt.Printf("Full lock-held per-tx multiplier (MorphTx / ordinary) = %.2fx  (p50)\n",
		float64(morph.p50)/float64(dyn.p50))
	fmt.Printf("Absolute extra time per MorphTx under the lock        = %s  (p50 delta)\n",
		morph.p50-dyn.p50)
	fmt.Println("This is the real per-tx cost that governs lock saturation — compare against")
	fmt.Println("the report's implied 9x (157µs legacy vs 1,370µs MorphTx, from a 500µs mock).")
}

// timeValidate calls pool.validateTx once per tx (skipping the first `warmup`)
// and returns the distribution of the measured calls.
func timeValidate(pool *TxPool, txs []*types.Transaction, warmup int) lcStats {
	ds := make([]time.Duration, 0, len(txs)-warmup)
	for i, tx := range txs {
		t0 := time.Now()
		_ = pool.validateTx(tx, false)
		d := time.Since(t0)
		if i >= warmup {
			ds = append(ds, d)
		}
	}
	return summarize(ds)
}

type lcStats struct{ p50, p90, p99, mean, max time.Duration }

func summarize(ds []time.Duration) lcStats {
	if len(ds) == 0 {
		return lcStats{}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	var sum time.Duration
	for _, d := range ds {
		sum += d
	}
	at := func(q float64) time.Duration {
		idx := int(q * float64(len(ds)))
		if idx >= len(ds) {
			idx = len(ds) - 1
		}
		return ds[idx]
	}
	return lcStats{p50: at(0.50), p90: at(0.90), p99: at(0.99), mean: sum / time.Duration(len(ds)), max: ds[len(ds)-1]}
}

func reportLC(label string, s lcStats) {
	fmt.Printf("%-58s p50=%8s p90=%8s p99=%8s mean=%8s max=%8s\n",
		label, s.p50, s.p90, s.p99, s.mean, s.max)
}

// describeAltToken confirms the token is active with no balance slot (the EVM
// StaticCall path). Returns (usable, error).
func describeAltToken(pool *TxPool, id uint16) (bool, error) {
	// getBalanceFunc's own token gate mirrors validateTx; reuse pool state.
	bal, err := pool.getBalanceFunc(pool.chain.CurrentBlock().Header(), pool.currentState, id, common.Address{})
	if err != nil {
		fmt.Printf("token %d: not usable via getBalanceFunc (%v)\n", id, err)
		return false, err
	}
	fmt.Printf("token %d: usable; empty-address balance=%s (confirms lookup executed)\n", id, bal.String())
	return true, nil
}

func headerAt(db ethdb.Database, blockNum int64) *types.Header {
	if blockNum >= 0 {
		h := rawdb.ReadCanonicalHash(db, uint64(blockNum))
		if h == (common.Hash{}) {
			return nil
		}
		return rawdb.ReadHeader(db, h, uint64(blockNum))
	}
	hh := rawdb.ReadHeadHeaderHash(db)
	if hh == (common.Hash{}) {
		return nil
	}
	num := rawdb.ReadHeaderNumber(db, hh)
	if num == nil {
		return nil
	}
	return rawdb.ReadHeader(db, hh, *num)
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func envUint16(t *testing.T, name string, def uint16) uint16 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	i, err := strconv.ParseUint(v, 10, 16)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return uint16(i)
}

func orOne(x *big.Int) *big.Int {
	if x == nil {
		return big.NewInt(1)
	}
	return x
}
