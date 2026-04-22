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

// Package trie — inspect.go is a port of upstream go-ethereum PR #28892
// adapted to morph's v1.10.26-era trie package and ZKTrie-aware state.
//
// The inspector walks a Merkle Patricia Trie in two passes:
//
//  1. Pass 1 (Inspect) walks the account trie, classifies every node by
//     type and depth into a LevelStats, streams one fixed-size record per
//     storage trie to a dump file on disk, and records the account trie
//     itself as a sentinel record (owner = zero hash) at the end.
//
//  2. Pass 2 (Summarize) reads the dump file back and produces the final
//     textual / JSON report, including three top-N rankings (by max
//     depth, total nodes, and value-node count).
//
// InspectContract runs the inspector against a single contract's storage
// trie and additionally compares against the snapshot view so operators
// can reason about both representations side-by-side.
//
// ZKTrie-encoded state is explicitly out of scope: the caller (typically
// the geth CLI) is responsible for checking the chain config and
// refusing to invoke Inspect / InspectContract against pre-JadeFork
// morph history. The ErrUnsupportedTrieFormat constant is provided for
// that purpose.
package trie

import (
	"bufio"
	"bytes"
	"cmp"
	"container/heap"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/internal/tablewriter"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/rlp"
)

// Dump / runtime constants. The record size is derived from the number of
// tracked trie levels so changing trieStatLevels stays in sync.
const (
	// inspectDumpRecordSize is the number of bytes occupied by one dump
	// record: 32 bytes of owner hash followed by (3 uint32 counters + 1
	// uint64 byte-size) per level.
	inspectDumpRecordSize = 32 + trieStatLevels*(3*4+8)
	// inspectDefaultTopN matches upstream for consistent reporting.
	inspectDefaultTopN = 10
	// inspectParallelism bounds the number of concurrent subtree walkers
	// spawned by the inspector. The value is chosen to saturate an NVMe
	// disk under random reads without overwhelming the operator's node.
	inspectParallelism = int64(16)
	// inspectDefaultDumpName is the dump file the inspector creates when
	// the caller does not supply an explicit path.
	inspectDefaultDumpName = "trie-dump.bin"
)

// ErrUnsupportedTrieFormat is returned when a caller attempts to inspect a
// state that is not stored in MPT format (e.g. morph pre-JadeFork ZKTrie
// state). The inspector itself does not sniff the database; callers must
// detect this condition and surface the error to the operator.
var ErrUnsupportedTrieFormat = errors.New("inspect-trie: state format not supported (zktrie mode not supported; only MPT is implemented)")

// InspectConfig is the set of options shared between the pass-1 inspector
// and the pass-2 summarizer. Field names and defaults match upstream so
// the CLI surface stays consistent.
type InspectConfig struct {
	// NoStorage, when true, skips the per-account storage trie walk.
	// The sentinel record for the account trie is still emitted.
	NoStorage bool

	// TopN sets the size of each top-N list produced by Summarize. Zero
	// or negative values fall back to inspectDefaultTopN.
	TopN int

	// Path, when non-empty, causes Summarize to serialize the final
	// report to that path as indented JSON. When empty the report is
	// printed to stdout instead.
	Path string

	// DumpPath is the pass-1 dump file location. When empty the default
	// inspectDefaultDumpName is used.
	DumpPath string
}

// normalizeInspectConfig fills in defaults on a (possibly nil) config so
// every downstream consumer works from a fully populated value.
func normalizeInspectConfig(config *InspectConfig) *InspectConfig {
	if config == nil {
		config = &InspectConfig{}
	}
	if config.TopN <= 0 {
		config.TopN = inspectDefaultTopN
	}
	if config.DumpPath == "" {
		config.DumpPath = inspectDefaultDumpName
	}
	return config
}

// inspector coordinates the pass-1 walk across multiple goroutines. The
// zero value is not usable; see Inspect for the correct construction
// sequence.
type inspector struct {
	triedb *Database
	root   common.Hash

	config      *InspectConfig
	accountStat *LevelStats

	sem *semaphore.Weighted

	// Pass-1 dump file state.
	dumpMu                sync.Mutex
	dumpBuf               *bufio.Writer
	dumpFile              *os.File
	storageRecordsWritten atomic.Uint64

	errMu sync.Mutex
	err   error
}

// Inspect walks the trie at root, records per-level node statistics, and
// streams one record per storage trie to disk. After the walk completes
// the file is finalized and Summarize is invoked to produce the report.
func Inspect(triedb *Database, root common.Hash, config *InspectConfig) error {
	trie, err := New(root, triedb)
	if err != nil {
		return fmt.Errorf("fail to open trie %s: %w", root, err)
	}
	config = normalizeInspectConfig(config)

	dumpFile, err := os.OpenFile(config.DumpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create trie dump %s: %w", config.DumpPath, err)
	}
	in := inspector{
		triedb:      triedb,
		root:        root,
		config:      config,
		accountStat: NewLevelStats(),
		sem:         semaphore.NewWeighted(inspectParallelism),
		dumpBuf:     bufio.NewWriterSize(dumpFile, 1<<20),
		dumpFile:    dumpFile,
	}

	// Emit periodic progress lines so large-state scans do not look
	// hung. The reporter shuts down via done when the walk completes.
	start := time.Now()
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				accountNodes := in.accountStat.TotalNodes()
				storageRecords := in.storageRecordsWritten.Load()
				log.Info("Inspecting trie",
					"accountNodes", accountNodes,
					"storageRecords", storageRecords,
					"elapsed", common.PrettyDuration(time.Since(start)))
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	in.recordRootSize(root, in.accountStat)
	in.inspect(trie, trie.root, 0, []byte{}, in.accountStat)

	// inspect is synchronous: it waits for every spawned goroutine in
	// its subtree before returning, so no extra wait is needed here.

	// Sentinel record: zero owner hash marks the account trie stats.
	in.writeDumpRecord(common.Hash{}, in.accountStat)
	if err := in.closeDump(); err != nil {
		in.setError(err)
	}

	if err := in.getError(); err != nil {
		return err
	}
	return Summarize(config.DumpPath, config)
}

// InspectContract inspects a single contract's storage footprint. It
// reports the snapshot slot count / byte size and the storage-trie
// per-depth node distribution, running both paths in parallel so the
// wall-clock cost is bounded by the slower of the two.
func InspectContract(triedb *Database, db ethdb.Database, stateRoot common.Hash, address common.Address) error {
	accountHash := crypto.Keccak256Hash(address.Bytes())

	accountTrie, err := New(stateRoot, triedb)
	if err != nil {
		return fmt.Errorf("failed to open account trie: %w", err)
	}
	accountRLP, err := accountTrie.TryGet(crypto.Keccak256(address.Bytes()))
	if err != nil {
		return fmt.Errorf("failed to read account: %w", err)
	}
	if accountRLP == nil {
		return fmt.Errorf("account not found: %s", address)
	}
	var account types.StateAccount
	if err := rlp.DecodeBytes(accountRLP, &account); err != nil {
		return fmt.Errorf("failed to decode account: %w", err)
	}
	if account.Root == (common.Hash{}) || account.Root == emptyRoot {
		return fmt.Errorf("account %s has no storage", address)
	}

	// Look up account snapshot (may be absent on morph; handled below).
	accountData := rawdb.ReadAccountSnapshot(db, accountHash)

	var (
		snapSlots atomic.Uint64
		snapSize  atomic.Uint64
		g         errgroup.Group
		start     = time.Now()
	)

	// Goroutine 1: iterate the snapshot storage prefix for this account
	// to compute slot count and raw byte size.
	g.Go(func() error {
		prefix := append(rawdb.SnapshotStoragePrefix, accountHash.Bytes()...)
		it := db.NewIterator(prefix, nil)
		defer it.Release()

		for it.Next() {
			if !bytes.HasPrefix(it.Key(), prefix) {
				break
			}
			snapSlots.Add(1)
			snapSize.Add(uint64(len(it.Key()) + len(it.Value())))
		}
		return it.Error()
	})

	// Goroutine 2: walk the storage trie with an inspector instance
	// configured to drop dump writes (the output is never persisted
	// from InspectContract).
	storageStat := NewLevelStats()
	g.Go(func() error {
		storage, err := New(account.Root, triedb)
		if err != nil {
			return fmt.Errorf("failed to open storage trie: %w", err)
		}
		in := &inspector{
			triedb:      triedb,
			root:        stateRoot,
			config:      &InspectConfig{NoStorage: true},
			accountStat: NewLevelStats(),
			sem:         semaphore.NewWeighted(inspectParallelism),
			dumpBuf:     bufio.NewWriter(io.Discard),
		}
		in.recordRootSize(account.Root, storageStat)
		in.inspect(storage, storage.root, 0, []byte{}, storageStat)
		return in.getError()
	})

	// Lightweight progress reporter for long scans.
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				log.Info("Inspecting contract",
					"snapSlots", snapSlots.Load(),
					"trieNodes", storageStat.TotalNodes(),
					"elapsed", common.PrettyDuration(time.Since(start)))
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	if err := g.Wait(); err != nil {
		return err
	}

	// Display results.
	fmt.Printf("\n=== Contract Inspection: %s ===\n", address)
	fmt.Printf("Account hash: %s\n\n", accountHash)

	if len(accountData) == 0 {
		fmt.Println("Account snapshot: not found")
	} else {
		fmt.Printf("Account snapshot: %s\n", common.StorageSize(len(accountData)))
	}

	fmt.Printf("Snapshot storage: %d slots (%s)\n",
		snapSlots.Load(), common.StorageSize(snapSize.Load()))

	var trieTotal, trieSize uint64
	b := new(strings.Builder)
	table := tablewriter.NewWriter(b)
	table.SetHeader([]string{"Depth", "Short", "Full", "Value", "Nodes", "Size"})
	for i := 0; i < trieStatLevels; i++ {
		short, full, value, size := storageStat.level[i].load()
		trieTotal += short + full + value
		trieSize += size
		total := short + full + value
		if total == 0 && size == 0 {
			continue
		}
		table.AppendBulk([][]string{{
			fmt.Sprint(i),
			fmt.Sprint(short),
			fmt.Sprint(full),
			fmt.Sprint(value),
			fmt.Sprint(total),
			common.StorageSize(size).String(),
		}})
	}
	fmt.Printf("Storage trie: %d nodes (%s)\n", trieTotal, common.StorageSize(trieSize))
	fmt.Println("\nStorage Trie Depth Distribution:")
	if err := table.Render(); err != nil {
		return err
	}
	fmt.Print(b.String())
	return nil
}

// recordRootSize accounts the on-disk size of the root node into stat at
// depth 0. Root nodes, being topmost, are frequently resolved via the
// database directly; we ask the database for the raw blob and record its
// length. Missing nodes are logged but not fatal — the inspector keeps
// going so partial reports still appear in the summary.
func (in *inspector) recordRootSize(root common.Hash, stat *LevelStats) {
	if root == (common.Hash{}) || root == emptyRoot {
		return
	}
	blob, err := in.triedb.Node(root)
	if err != nil || len(blob) == 0 {
		log.Error("Failed to read trie root for size accounting", "root", root, "err", err)
		return
	}
	stat.addSize(0, uint64(len(blob)))
}

// closeDump flushes and closes the pass-1 dump file, returning the
// aggregated error if either step fails.
func (in *inspector) closeDump() error {
	var ret error
	if in.dumpBuf != nil {
		if err := in.dumpBuf.Flush(); err != nil {
			ret = errors.Join(ret, fmt.Errorf("failed to flush trie dump %s: %w", in.config.DumpPath, err))
		}
	}
	if in.dumpFile != nil {
		if err := in.dumpFile.Close(); err != nil {
			ret = errors.Join(ret, fmt.Errorf("failed to close trie dump %s: %w", in.config.DumpPath, err))
		}
	}
	return ret
}

func (in *inspector) setError(err error) {
	if err == nil {
		return
	}
	in.errMu.Lock()
	defer in.errMu.Unlock()
	in.err = errors.Join(in.err, err)
}

func (in *inspector) getError() error {
	in.errMu.Lock()
	defer in.errMu.Unlock()
	return in.err
}

func (in *inspector) hasError() bool {
	return in.getError() != nil
}

// trySpawn attempts to run fn in a new goroutine bounded by the
// inspector's semaphore. When a slot is available the goroutine starts
// and is tracked via wg so the caller can wait before observing any
// state fn writes. Returns false (without starting anything) when no
// slot is currently available — the caller then runs fn inline.
func (in *inspector) trySpawn(wg *sync.WaitGroup, fn func()) bool {
	if !in.sem.TryAcquire(1) {
		return false
	}
	wg.Add(1)
	go func() {
		defer in.sem.Release(1)
		defer wg.Done()
		fn()
	}()
	return true
}

// writeDumpRecord serializes stats for owner into the pass-1 dump file.
// The zero owner hash is reserved for the account trie sentinel record;
// any other value is treated as a storage trie owner.
func (in *inspector) writeDumpRecord(owner common.Hash, s *LevelStats) {
	if in.hasError() {
		return
	}
	var buf [inspectDumpRecordSize]byte
	copy(buf[:32], owner[:])

	off := 32
	for i := 0; i < trieStatLevels; i++ {
		short := s.level[i].short.Load()
		if short > math.MaxUint32 {
			in.setError(fmt.Errorf("dump record overflow: level %d short counter %d exceeds uint32 max", i, short))
			return
		}
		full := s.level[i].full.Load()
		if full > math.MaxUint32 {
			in.setError(fmt.Errorf("dump record overflow: level %d full counter %d exceeds uint32 max", i, full))
			return
		}
		value := s.level[i].value.Load()
		if value > math.MaxUint32 {
			in.setError(fmt.Errorf("dump record overflow: level %d value counter %d exceeds uint32 max", i, value))
			return
		}
		binary.LittleEndian.PutUint32(buf[off:], uint32(short))
		off += 4
		binary.LittleEndian.PutUint32(buf[off:], uint32(full))
		off += 4
		binary.LittleEndian.PutUint32(buf[off:], uint32(value))
		off += 4
		binary.LittleEndian.PutUint64(buf[off:], s.level[i].size.Load())
		off += 8
	}
	in.dumpMu.Lock()
	_, err := in.dumpBuf.Write(buf[:])
	in.dumpMu.Unlock()
	if err != nil {
		in.setError(fmt.Errorf("failed writing trie dump record: %w", err))
		return
	}
	if owner != (common.Hash{}) {
		in.storageRecordsWritten.Add(1)
	}
}

// inspect walks the subtree rooted at n and records its statistics into
// stat. It may spawn goroutines for children when the semaphore allows,
// but always waits for them before returning so stat is fully populated
// by the time the caller observes it.
func (in *inspector) inspect(trie *Trie, n node, height uint32, path []byte, stat *LevelStats) {
	if n == nil {
		return
	}

	// Goroutines spawned at this level are tracked locally so we block
	// on them before recording n itself. This preserves the invariant
	// "stat is complete on return", which downstream writeDumpRecord
	// calls rely on.
	var wg sync.WaitGroup

	switch n := (n).(type) {
	case *shortNode:
		nextPath := slices.Concat(path, n.Key)
		in.inspect(trie, n.Val, height+1, nextPath, stat)
	case *fullNode:
		for idx, child := range n.Children {
			if child == nil {
				continue
			}
			childPath := slices.Concat(path, []byte{byte(idx)})
			childNode := child
			if in.trySpawn(&wg, func() {
				in.inspect(trie, childNode, height+1, childPath, stat)
			}) {
				continue
			}
			in.inspect(trie, childNode, height+1, childPath, stat)
		}
	case hashNode:
		// Resolve the raw node via the shared database; count its
		// on-disk byte size against the current level before decoding
		// and recursing. This mirrors upstream's reader.Node() path.
		hash := common.BytesToHash(n)
		blob, err := in.triedb.Node(hash)
		if err != nil {
			log.Error("Failed to resolve HashNode", "err", err, "trie", trie.Hash(), "height", height+1, "path", path)
			return
		}
		stat.addSize(height, uint64(len(blob)))
		resolved := mustDecodeNode(n, blob)
		in.inspect(trie, resolved, height, path, stat)
		// Return early: hash nodes are a transparent indirection and
		// must not be counted twice at this depth.
		return
	case valueNode:
		if !hasTerm(path) {
			break
		}
		var account types.StateAccount
		if err := rlp.Decode(bytes.NewReader(n), &account); err != nil {
			break
		}
		if account.Root == (common.Hash{}) || account.Root == emptyRoot {
			break
		}
		if !in.config.NoStorage {
			owner := common.BytesToHash(hexToCompact(path))
			storage, err := New(account.Root, in.triedb)
			if err != nil {
				log.Error("Failed to open account storage trie", "node", n, "error", err, "height", height, "path", common.Bytes2Hex(path))
				break
			}
			storageStat := NewLevelStats()
			run := func() {
				in.recordRootSize(account.Root, storageStat)
				in.inspect(storage, storage.root, 0, []byte{}, storageStat)
				in.writeDumpRecord(owner, storageStat)
			}
			if in.trySpawn(&wg, run) {
				break
			}
			run()
		}
	default:
		panic(fmt.Sprintf("%T: invalid node: %v", n, n))
	}

	wg.Wait()

	// Record stats for the current node once the subtree (and any
	// spawned siblings) have been fully accounted for.
	stat.add(n, height)
}

// Summarize is pass 2: read the dump file, aggregate every storage-trie
// record, compute the top-N rankings, and emit either stdout tables or a
// JSON blob depending on InspectConfig.Path.
func Summarize(dumpPath string, config *InspectConfig) error {
	config = normalizeInspectConfig(config)
	if dumpPath == "" {
		dumpPath = config.DumpPath
	}
	if dumpPath == "" {
		return errors.New("missing dump path")
	}
	file, err := os.Open(dumpPath)
	if err != nil {
		return fmt.Errorf("failed to open trie dump %s: %w", dumpPath, err)
	}
	defer file.Close()

	if info, err := file.Stat(); err == nil {
		if info.Size()%inspectDumpRecordSize != 0 {
			return fmt.Errorf("invalid trie dump size %d (not a multiple of %d)", info.Size(), inspectDumpRecordSize)
		}
	}

	depthTop := newStorageStatsTopN(config.TopN, compareStorageStatsByDepth)
	totalTop := newStorageStatsTopN(config.TopN, compareStorageStatsByTotal)
	valueTop := newStorageStatsTopN(config.TopN, compareStorageStatsByValue)

	summary := &inspectSummary{}
	reader := bufio.NewReaderSize(file, 1<<20)
	var buf [inspectDumpRecordSize]byte

	for {
		_, err := io.ReadFull(reader, buf[:])
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("truncated trie dump %s", dumpPath)
		}
		if err != nil {
			return fmt.Errorf("failed reading trie dump %s: %w", dumpPath, err)
		}

		record := decodeDumpRecord(buf[:])
		snapshot := newStorageStats(record.Owner, record.Levels)
		if record.Owner == (common.Hash{}) {
			summary.Account = snapshot
			continue
		}
		summary.StorageCount++
		summary.DepthHistogram[snapshot.MaxDepth]++
		for i := 0; i < trieStatLevels; i++ {
			summary.StorageLevels[i].Short += record.Levels[i].Short
			summary.StorageLevels[i].Full += record.Levels[i].Full
			summary.StorageLevels[i].Value += record.Levels[i].Value
			summary.StorageLevels[i].Size += record.Levels[i].Size
		}
		depthTop.TryInsert(snapshot)
		totalTop.TryInsert(snapshot)
		valueTop.TryInsert(snapshot)
	}
	if summary.Account == nil {
		return fmt.Errorf("dump file %s does not contain the account trie sentinel record", dumpPath)
	}
	for i := 0; i < trieStatLevels; i++ {
		summary.StorageTotals.Short += summary.StorageLevels[i].Short
		summary.StorageTotals.Full += summary.StorageLevels[i].Full
		summary.StorageTotals.Value += summary.StorageLevels[i].Value
		summary.StorageTotals.Size += summary.StorageLevels[i].Size
	}
	summary.TopByDepth = depthTop.Sorted()
	summary.TopByTotalNodes = totalTop.Sorted()
	summary.TopByValueNodes = valueTop.Sorted()

	if config.Path != "" {
		return summary.writeJSON(config.Path)
	}
	summary.display()
	return nil
}

// -----------------------------------------------------------------------
// Internal dump-record types and accessors
// -----------------------------------------------------------------------

type dumpRecord struct {
	Owner  common.Hash
	Levels [trieStatLevels]jsonLevel
}

func decodeDumpRecord(raw []byte) dumpRecord {
	var (
		record dumpRecord
		off    = 32
	)
	copy(record.Owner[:], raw[:32])
	for i := 0; i < trieStatLevels; i++ {
		record.Levels[i] = jsonLevel{
			Short: uint64(binary.LittleEndian.Uint32(raw[off:])),
			Full:  uint64(binary.LittleEndian.Uint32(raw[off+4:])),
			Value: uint64(binary.LittleEndian.Uint32(raw[off+8:])),
			Size:  binary.LittleEndian.Uint64(raw[off+12:]),
		}
		off += 20
	}
	return record
}

type storageStats struct {
	Owner      common.Hash
	Levels     [trieStatLevels]jsonLevel
	Summary    jsonLevel
	MaxDepth   int
	TotalNodes uint64
	TotalSize  uint64
}

func newStorageStats(owner common.Hash, levels [trieStatLevels]jsonLevel) *storageStats {
	snapshot := &storageStats{Owner: owner, Levels: levels}
	for i := 0; i < trieStatLevels; i++ {
		level := levels[i]
		if level.Short != 0 || level.Full != 0 || level.Value != 0 {
			snapshot.MaxDepth = i
		}
		snapshot.Summary.Short += level.Short
		snapshot.Summary.Full += level.Full
		snapshot.Summary.Value += level.Value
		snapshot.Summary.Size += level.Size
	}
	snapshot.TotalNodes = snapshot.Summary.Short + snapshot.Summary.Full + snapshot.Summary.Value
	snapshot.TotalSize = snapshot.Summary.Size
	return snapshot
}

func trimLevels(levels [trieStatLevels]jsonLevel) []jsonLevel {
	n := len(levels)
	for n > 0 && levels[n-1] == (jsonLevel{}) {
		n--
	}
	return levels[:n]
}

func (s *storageStats) MarshalJSON() ([]byte, error) {
	type jsonStorageSnapshot struct {
		Owner      common.Hash `json:"Owner"`
		MaxDepth   int         `json:"MaxDepth"`
		TotalNodes uint64      `json:"TotalNodes"`
		TotalSize  uint64      `json:"TotalSize"`
		ValueNodes uint64      `json:"ValueNodes"`
		Levels     []jsonLevel `json:"Levels"`
		Summary    jsonLevel   `json:"Summary"`
	}
	return json.Marshal(jsonStorageSnapshot{
		Owner:      s.Owner,
		MaxDepth:   s.MaxDepth,
		TotalNodes: s.TotalNodes,
		TotalSize:  s.TotalSize,
		ValueNodes: s.Summary.Value,
		Levels:     trimLevels(s.Levels),
		Summary:    s.Summary,
	})
}

func (s *storageStats) toLevelStats() *LevelStats {
	stats := NewLevelStats()
	for i := 0; i < trieStatLevels; i++ {
		stats.level[i].short.Store(s.Levels[i].Short)
		stats.level[i].full.Store(s.Levels[i].Full)
		stats.level[i].value.Store(s.Levels[i].Value)
		stats.level[i].size.Store(s.Levels[i].Size)
	}
	return stats
}

// -----------------------------------------------------------------------
// Top-N bookkeeping
// -----------------------------------------------------------------------

type storageStatsCompare func(a, b *storageStats) int

type storageStatsTopN struct {
	limit int
	cmp   storageStatsCompare
	heap  storageStatsHeap
}

type storageStatsHeap struct {
	items []*storageStats
	cmp   storageStatsCompare
}

func (h storageStatsHeap) Len() int { return len(h.items) }

func (h storageStatsHeap) Less(i, j int) bool {
	// Keep the weakest entry at the root (min-heap semantics) so we
	// can evict the smallest in O(log N) when a bigger one arrives.
	return h.cmp(h.items[i], h.items[j]) < 0
}

func (h storageStatsHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *storageStatsHeap) Push(x any) {
	h.items = append(h.items, x.(*storageStats))
}

func (h *storageStatsHeap) Pop() any {
	item := h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	return item
}

func newStorageStatsTopN(limit int, cmp storageStatsCompare) *storageStatsTopN {
	h := storageStatsHeap{cmp: cmp}
	heap.Init(&h)
	return &storageStatsTopN{limit: limit, cmp: cmp, heap: h}
}

func (t *storageStatsTopN) TryInsert(item *storageStats) {
	if t.limit <= 0 {
		return
	}
	if t.heap.Len() < t.limit {
		heap.Push(&t.heap, item)
		return
	}
	if t.cmp(item, t.heap.items[0]) <= 0 {
		return
	}
	heap.Pop(&t.heap)
	heap.Push(&t.heap, item)
}

func (t *storageStatsTopN) Sorted() []*storageStats {
	out := append([]*storageStats(nil), t.heap.items...)
	sort.Slice(out, func(i, j int) bool { return t.cmp(out[i], out[j]) > 0 })
	return out
}

// The three ordering functions below share the same tie-breaking chain
// (secondary by MaxDepth / TotalNodes / value count and finally by owner
// hash for stability). Adjusting any one should be mirrored in the other
// two so ranking output stays consistent.

func compareStorageStatsByDepth(a, b *storageStats) int {
	return cmp.Or(
		cmp.Compare(a.MaxDepth, b.MaxDepth),
		cmp.Compare(a.TotalNodes, b.TotalNodes),
		cmp.Compare(a.Summary.Value, b.Summary.Value),
		bytes.Compare(a.Owner[:], b.Owner[:]),
	)
}

func compareStorageStatsByTotal(a, b *storageStats) int {
	return cmp.Or(
		cmp.Compare(a.TotalNodes, b.TotalNodes),
		cmp.Compare(a.MaxDepth, b.MaxDepth),
		cmp.Compare(a.Summary.Value, b.Summary.Value),
		bytes.Compare(a.Owner[:], b.Owner[:]),
	)
}

func compareStorageStatsByValue(a, b *storageStats) int {
	return cmp.Or(
		cmp.Compare(a.Summary.Value, b.Summary.Value),
		cmp.Compare(a.MaxDepth, b.MaxDepth),
		cmp.Compare(a.TotalNodes, b.TotalNodes),
		bytes.Compare(a.Owner[:], b.Owner[:]),
	)
}

// -----------------------------------------------------------------------
// Summary report assembly / rendering
// -----------------------------------------------------------------------

type inspectSummary struct {
	Account         *storageStats
	StorageCount    uint64
	StorageTotals   jsonLevel
	StorageLevels   [trieStatLevels]jsonLevel
	DepthHistogram  [trieStatLevels]uint64
	TopByDepth      []*storageStats
	TopByTotalNodes []*storageStats
	TopByValueNodes []*storageStats
}

func (s *inspectSummary) display() {
	s.displayCombinedDepthTable()
	s.Account.toLevelStats().display("Accounts trie")
	fmt.Println("Storage trie aggregate summary")
	fmt.Printf("Total storage tries: %d\n", s.StorageCount)
	totalNodes := s.StorageTotals.Short + s.StorageTotals.Full + s.StorageTotals.Value
	fmt.Printf("Total nodes: %d\n", totalNodes)
	fmt.Printf("Total size: %s\n", common.StorageSize(s.StorageTotals.Size))
	fmt.Printf(" Short nodes: %d\n", s.StorageTotals.Short)
	fmt.Printf(" Full nodes: %d\n", s.StorageTotals.Full)
	fmt.Printf(" Value nodes: %d\n", s.StorageTotals.Value)

	b := new(strings.Builder)
	table := tablewriter.NewWriter(b)
	table.SetHeader([]string{"Max Depth", "Storage Tries"})
	for i, count := range s.DepthHistogram {
		table.AppendBulk([][]string{{fmt.Sprint(i), fmt.Sprint(count)}})
	}
	_ = table.Render()
	fmt.Print(b.String())
	fmt.Println()

	s.displayTop("Top storage tries by max depth", s.TopByDepth)
	s.displayTop("Top storage tries by total node count", s.TopByTotalNodes)
	s.displayTop("Top storage tries by value (slot) count", s.TopByValueNodes)
}

func (s *inspectSummary) displayCombinedDepthTable() {
	accountTotal := s.Account.Summary.Short + s.Account.Summary.Full + s.Account.Summary.Value
	storageTotal := s.StorageTotals.Short + s.StorageTotals.Full + s.StorageTotals.Value
	accountTotalSize := s.Account.Summary.Size
	storageTotalSize := s.StorageTotals.Size

	fmt.Println("Trie Depth Distribution")
	fmt.Printf("Account Trie: %d nodes (%s)\n", accountTotal, common.StorageSize(accountTotalSize))
	fmt.Printf("Storage Tries: %d nodes (%s) across %d tries\n", storageTotal, common.StorageSize(storageTotalSize), s.StorageCount)

	b := new(strings.Builder)
	table := tablewriter.NewWriter(b)
	table.SetHeader([]string{"Depth", "Account Nodes", "Account Size", "Storage Nodes", "Storage Size"})
	for i := 0; i < trieStatLevels; i++ {
		accountNodes := s.Account.Levels[i].Short + s.Account.Levels[i].Full + s.Account.Levels[i].Value
		accountSize := s.Account.Levels[i].Size
		storageNodes := s.StorageLevels[i].Short + s.StorageLevels[i].Full + s.StorageLevels[i].Value
		storageSize := s.StorageLevels[i].Size
		if accountNodes == 0 && storageNodes == 0 {
			continue
		}
		table.AppendBulk([][]string{{
			fmt.Sprint(i),
			fmt.Sprint(accountNodes),
			common.StorageSize(accountSize).String(),
			fmt.Sprint(storageNodes),
			common.StorageSize(storageSize).String(),
		}})
	}
	_ = table.Render()
	fmt.Print(b.String())
	fmt.Println()
}

func (s *inspectSummary) displayTop(title string, list []*storageStats) {
	fmt.Println(title)
	if len(list) == 0 {
		fmt.Println("No storage tries found")
		fmt.Println()
		return
	}
	for i, item := range list {
		fmt.Printf("%d: %s\n", i+1, item.Owner)
		item.toLevelStats().display("storage trie")
	}
}

func (s *inspectSummary) MarshalJSON() ([]byte, error) {
	type jsonAccountTrie struct {
		Name    string      `json:"Name"`
		Levels  []jsonLevel `json:"Levels"`
		Summary jsonLevel   `json:"Summary"`
	}
	type jsonStorageSummary struct {
		TotalStorageTries uint64                         `json:"TotalStorageTries"`
		Totals            jsonLevel                      `json:"Totals"`
		Levels            []jsonLevel                    `json:"Levels"`
		DepthHistogram    [trieStatLevels]uint64         `json:"DepthHistogram"`
	}
	type jsonInspectSummary struct {
		AccountTrie     jsonAccountTrie    `json:"AccountTrie"`
		StorageSummary  jsonStorageSummary `json:"StorageSummary"`
		TopByDepth      []*storageStats    `json:"TopByDepth"`
		TopByTotalNodes []*storageStats    `json:"TopByTotalNodes"`
		TopByValueNodes []*storageStats    `json:"TopByValueNodes"`
	}
	return json.Marshal(jsonInspectSummary{
		AccountTrie: jsonAccountTrie{
			Name:    "account trie",
			Levels:  trimLevels(s.Account.Levels),
			Summary: s.Account.Summary,
		},
		StorageSummary: jsonStorageSummary{
			TotalStorageTries: s.StorageCount,
			Totals:            s.StorageTotals,
			Levels:            trimLevels(s.StorageLevels),
			DepthHistogram:    s.DepthHistogram,
		},
		TopByDepth:      s.TopByDepth,
		TopByTotalNodes: s.TopByTotalNodes,
		TopByValueNodes: s.TopByValueNodes,
	})
}

func (s *inspectSummary) writeJSON(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// display prints a per-level breakdown for the statistics collected by
// the given LevelStats. Title is truncated when long so console tables
// stay readable even for storage trie owners (32-byte hex strings).
func (s *LevelStats) display(title string) {
	if len(title) > 32 {
		title = title[0:8] + "..." + title[len(title)-8:]
	}

	b := new(strings.Builder)
	table := tablewriter.NewWriter(b)
	table.SetHeader([]string{title, "Level", "Short Nodes", "Full Node", "Value Node"})

	total := &stat{}
	for i := range s.level {
		if s.level[i].empty() {
			continue
		}
		short, full, value, _ := s.level[i].load()
		table.AppendBulk([][]string{{"-", fmt.Sprint(i), fmt.Sprint(short), fmt.Sprint(full), fmt.Sprint(value)}})
		total.add(&s.level[i])
	}
	short, full, value, _ := total.load()
	table.SetFooter([]string{"Total", "", fmt.Sprint(short), fmt.Sprint(full), fmt.Sprint(value)})
	_ = table.Render()
	fmt.Print(b.String())
	fmt.Println("Max depth", s.MaxDepth())
	fmt.Println()
}

// jsonLevel is the per-level record laid out in dump files and serialized
// into JSON summaries. Fields are exported so the binary and textual
// representations share the same layout.
type jsonLevel struct {
	Short uint64
	Full  uint64
	Value uint64
	Size  uint64
}
