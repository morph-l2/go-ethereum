// Command altfee_bench measures the REAL, under-lock cost of the alt-fee
// balance lookup that core/tx_pool.go performs inside validateTx() while
// holding pool.mu.
//
// Why this exists
// ---------------
// A bug report claims a MorphTx alt-fee flood freezes the chain because
// getBalanceFunc runs a full EVM StaticCall under pool.mu at ~1,370µs/tx,
// giving a saturation ceiling of ~730 tx/s. That figure came from a PoC that
// MOCKED getBalanceFunc with a fixed 500µs sleep. A balance lookup is not a
// sleep: its cost is dominated by state access (contract-code load + storage
// trie descent), which only a run against a REAL on-disk state tree can
// measure. This tool measures it.
//
// What it measures
// ----------------
// It replicates pool.getBalanceFunc (core/tx_pool.go:328-351) verbatim — same
// BlockContext, empty TxContext, empty vm.Config (NO tracer, exactly like the
// pool path), vm.NewEVM, IsTokenActive gate, then GetAltTokenBalance — and
// times it against freshly generated random addresses (absent storage keys,
// matching an attacker using ephemeral unfunded accounts).
//
// Caching model (this is the crux of "is it real")
// -------------------------------------------------
// The state.Database (trie clean-cache + contract-code cache) is created ONCE
// and shared across calls, mirroring BlockChain.stateCache which persists
// across blocks. A FRESH state.StateDB is created per call (state.New), which
// mirrors pool.currentState being replaced on every new head — so the per-call
// stateObjects map starts empty, exactly as it does for the first tx of a new
// block. Because the attack hammers the SAME token contract with DIFFERENT
// random addresses, the dominant per-call cost is the absent-key storage-trie
// descent; the contract code and the hot upper trie nodes are served from the
// shared clean cache once warmed — so the steady-state number below is the one
// that actually governs the saturation ceiling during a sustained flood.
//
// Two cache regimes are reported:
//   - WARM  : shared trie clean-cache sized by --triecache (models a node under
//             sustained attack whose token working-set is hot). This is the
//             decision-relevant number.
//   - COLD  : trie clean-cache = 0 (every trie node re-fetched from leveldb;
//             leveldb block-cache + OS page-cache still apply). A pessimistic
//             bound, not the steady state.
//
// Usage
// -----
//   go run ./benchmark/altfee_bench \
//       --chaindata /path/to/datadir/geth/chaindata \
//       --tokenid <ACTIVE_TOKEN_ID_WITHOUT_BALANCE_SLOT> \
//       [--slot-tokenid <ACTIVE_TOKEN_ID_WITH_BALANCE_SLOT>] \
//       [--block N] [--triecache 256] [--warmup 500] [--n 5000]
//
// Run read-only against a COPY of chaindata, or a stopped node's datadir
// (leveldb takes an exclusive lock; a running node will block the open).
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/rollup/fees"
	"github.com/morph-l2/go-ethereum/trie"
)

func main() {
	var (
		chaindata   = flag.String("chaindata", "", "path to <datadir>/geth/chaindata (required)")
		freezer     = flag.String("freezer", "", "path to ancient/freezer dir (default: <chaindata>/ancient)")
		tokenID     = flag.Uint("tokenid", 0, "alt-fee token ID to exercise (the attack path: an active token WITHOUT a balance slot)")
		slotTokenID = flag.Uint("slot-tokenid", 0, "optional: an active token WITH a balance slot, to show the mitigation's cost")
		blockNum    = flag.Int64("block", -1, "block number to build state at (default: -1 = chain head)")
		triecacheMB = flag.Int("triecache", 256, "trie clean-cache size in MB for the WARM regime")
		leveldbMB   = flag.Int("leveldbcache", 512, "leveldb block-cache size in MB")
		handles     = flag.Int("handles", 1024, "leveldb max open file handles")
		warmup      = flag.Int("warmup", 500, "warmup iterations (excluded from steady-state stats)")
		n           = flag.Int("n", 5000, "measured iterations for steady-state stats")
	)
	flag.Parse()

	if *chaindata == "" || *tokenID == 0 {
		fmt.Fprintln(os.Stderr, "error: --chaindata and --tokenid are required")
		flag.Usage()
		os.Exit(2)
	}
	if *tokenID > 0xffff {
		fmt.Fprintln(os.Stderr, "error: --tokenid must fit in uint16")
		os.Exit(2)
	}
	frz := *freezer
	if frz == "" {
		frz = filepath.Join(*chaindata, "ancient")
	}

	// --- open the real chain database (read-only) ---
	db, err := rawdb.NewLevelDBDatabaseWithFreezer(*chaindata, *leveldbMB, *handles, frz, "", true)
	if err != nil {
		fatalf("open chaindata: %v", err)
	}
	defer db.Close()

	// --- resolve the target header (head or pinned block) ---
	header := resolveHeader(db, *blockNum)
	if header == nil {
		fatalf("could not read target header (block=%d)", *blockNum)
	}

	// --- chain config, read from the db's stored genesis ---
	genesisHash := rawdb.ReadCanonicalHash(db, 0)
	chainConfig := rawdb.ReadChainConfig(db, genesisHash)
	if chainConfig == nil {
		fatalf("could not read chain config (genesis %s)", genesisHash.Hex())
	}

	fmt.Println("=== altfee_bench: real-state cost of getBalanceFunc under pool.mu ===")
	fmt.Printf("chaindata      : %s\n", *chaindata)
	fmt.Printf("chainID        : %v\n", chainConfig.ChainID)
	fmt.Printf("state @ block  : %d  (root %s)\n", header.Number.Uint64(), header.Root.Hex())
	fmt.Printf("trie cache     : warm=%d MB / cold=0 MB   leveldb=%d MB\n", *triecacheMB, *leveldbMB)
	fmt.Printf("iterations     : warmup=%d measured=%d   addresses=random-ephemeral\n\n", *warmup, *n)

	// Shared trie/code cache regimes (see file header for the model).
	warmDB := state.NewDatabaseWithConfig(db, &trie.Config{Cache: *triecacheMB})
	coldDB := state.NewDatabaseWithConfig(db, &trie.Config{Cache: 0})

	// --- confirm which path we are actually exercising ---
	if !describeToken(warmDB, header.Root, uint16(*tokenID)) {
		os.Exit(1)
	}
	fmt.Println()

	// getBalanceFunc replicated verbatim from core/tx_pool.go:328-351.
	getBalanceFunc := func(sdb *state.StateDB, id uint16, addr common.Address) (*big.Int, error) {
		active, err := fees.IsTokenActive(sdb, id)
		if err != nil || !active {
			return big.NewInt(0), fmt.Errorf("invalid token")
		}
		blockContext := vm.BlockContext{
			CanTransfer: core.CanTransfer,
			Transfer:    core.Transfer,
			GetHash:     func(_ uint64) common.Hash { return common.Hash{} },
			Coinbase:    header.Coinbase,
			BlockNumber: header.Number,
			Time:        new(big.Int).SetUint64(header.Time),
			Difficulty:  header.Difficulty,
			BaseFee:     header.BaseFee,
			GasLimit:    header.GasLimit,
		}
		evm := vm.NewEVM(blockContext, vm.TxContext{}, sdb, chainConfig, vm.Config{})
		return core.GetAltTokenBalance(evm, id, addr)
	}

	// The measured operation: fresh StateDB per call (models per-tx / per-block
	// reset), one getBalanceFunc call against a fresh random address.
	measure := func(sdb *state.Database, id uint16) func() {
		return func() {
			s, err := state.New(header.Root, *sdb, nil)
			if err != nil {
				fatalf("state.New: %v", err)
			}
			if _, err := getBalanceFunc(s, id, randAddr()); err != nil {
				fatalf("getBalanceFunc(token %d): %v", id, err)
			}
		}
	}

	fmt.Printf("--- attack path: getBalanceFunc(tokenid=%d) ---\n", *tokenID)
	warm := run(measure(&warmDB, uint16(*tokenID)), *warmup, *n)
	cold := run(measure(&coldDB, uint16(*tokenID)), *warmup, *n)
	report("WARM (trie cache hot — sustained-flood steady state)", warm)
	report("COLD (trie clean-cache = 0 — pessimistic bound)", cold)

	// Baseline: native ETH balance read, the "cheap legacy check" reference.
	base := run(func() {
		s, err := state.New(header.Root, warmDB, nil)
		if err != nil {
			fatalf("state.New: %v", err)
		}
		s.GetBalance(randAddr())
	}, *warmup, *n)
	report("BASELINE native GetBalance (legacy-tx reference, warm)", base)

	// Optional: slot-path token (the mitigation) for comparison.
	if *slotTokenID != 0 && *slotTokenID <= 0xffff {
		fmt.Println()
		if describeToken(warmDB, header.Root, uint16(*slotTokenID)) {
			slot := run(measure(&warmDB, uint16(*slotTokenID)), *warmup, *n)
			report(fmt.Sprintf("MITIGATED slot-path getBalanceFunc(tokenid=%d, warm)", *slotTokenID), slot)
		}
	}

	fmt.Println("\n=== interpretation ===")
	fmt.Printf("Saturation ceiling = 1 / (per-tx in-lock time). Using WARM p50=%s → ~%.0f tx/s\n",
		warm.p50, 1.0/warm.p50.Seconds())
	fmt.Printf("Report's mocked figure was 500µs sleep → 1,370µs/tx → ~730 tx/s.\n")
	fmt.Println("Compare the measured WARM p50 above against the 500µs mock to see how far the")
	fmt.Println("mocked saturation threshold is from the real reachable cost.")
}

// resolveHeader returns the header at blockNum, or the chain head if blockNum < 0.
func resolveHeader(db ethdb.Database, blockNum int64) *types.Header {
	if blockNum >= 0 {
		h := rawdb.ReadCanonicalHash(db, uint64(blockNum))
		if h == (common.Hash{}) {
			return nil
		}
		return rawdb.ReadHeader(db, h, uint64(blockNum))
	}
	headHash := rawdb.ReadHeadHeaderHash(db)
	if headHash == (common.Hash{}) {
		return nil
	}
	num := rawdb.ReadHeaderNumber(db, headHash)
	if num == nil {
		return nil
	}
	return rawdb.ReadHeader(db, headHash, *num)
}

// describeToken prints the registry view of a token so the operator can confirm
// which code path is exercised. Returns false if the token cannot be used.
func describeToken(sdb state.Database, root common.Hash, id uint16) bool {
	s, err := state.New(root, sdb, nil)
	if err != nil {
		fatalf("state.New: %v", err)
	}
	info, err := fees.GetTokenInfo(s, id)
	if err != nil {
		fmt.Printf("token %d: NOT in registry (%v)\n", id, err)
		fmt.Printf("  -> a MorphTx with this ID short-circuits at IsTokenActive; getBalanceFunc is never called.\n")
		return false
	}
	path := "StaticCall (full EVM) — THE ATTACK PATH"
	if info.HasSlot {
		path = "storage-slot read (cheap) — mitigated path"
	}
	fmt.Printf("token %d: active=%v  hasSlot=%v  addr=%s\n", id, info.IsActive, info.HasSlot, info.TokenAddress.Hex())
	fmt.Printf("  -> balance path: %s\n", path)
	if !info.IsActive {
		fmt.Printf("  -> WARNING: token is inactive; getBalanceFunc returns early, no balance lookup runs.\n")
		return false
	}
	return true
}

type stats struct {
	p50, p90, p99, max, mean, first time.Duration
}

// run executes op warmup+n times, records the first call and the last n
// durations, and returns their distribution.
func run(op func(), warmup, n int) stats {
	ds := make([]time.Duration, 0, n)
	var first time.Duration
	for i := 0; i < warmup+n; i++ {
		t := time.Now()
		op()
		d := time.Since(t)
		if i == 0 {
			first = d
		}
		if i >= warmup {
			ds = append(ds, d)
		}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	var sum time.Duration
	for _, d := range ds {
		sum += d
	}
	pick := func(q float64) time.Duration {
		if len(ds) == 0 {
			return 0
		}
		idx := int(q * float64(len(ds)))
		if idx >= len(ds) {
			idx = len(ds) - 1
		}
		return ds[idx]
	}
	return stats{
		first: first,
		p50:   pick(0.50),
		p90:   pick(0.90),
		p99:   pick(0.99),
		max:   ds[len(ds)-1],
		mean:  sum / time.Duration(len(ds)),
	}
}

func report(label string, s stats) {
	fmt.Printf("%-56s p50=%8s p90=%8s p99=%8s max=%8s mean=%8s first=%8s\n",
		label, s.p50, s.p90, s.p99, s.max, s.mean, s.first)
}

func randAddr() common.Address {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		fatalf("rand: %v", err)
	}
	return common.BytesToAddress(b[:])
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "altfee_bench: "+format+"\n", args...)
	os.Exit(1)
}
