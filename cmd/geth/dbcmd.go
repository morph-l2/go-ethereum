// Copyright 2020 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/urfave/cli.v1"

	"github.com/morph-l2/go-ethereum/cmd/utils"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/console/prompt"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/trie"
)

var (
	inspectTrieTopFlag = cli.IntFlag{
		Name:  "top",
		Usage: "Number of storage tries to include in the top-N ranking (0 disables ranking)",
		Value: 10,
	}
	inspectTrieExcludeStorageFlag = cli.BoolFlag{
		Name:  "exclude-storage",
		Usage: "Skip walking per-account storage tries",
	}
	inspectTrieOutputFileFlag = cli.StringFlag{
		Name:  "output",
		Usage: "Write the report as JSON to the given file (default: stdout summary)",
	}
	inspectTrieDumpPathFlag = cli.StringFlag{
		Name:  "dump-path",
		Usage: "Path for the pass-1 trie dump file (default: <datadir>/trie-dump.bin)",
	}
	inspectTrieSummarizeFlag = cli.StringFlag{
		Name:  "summarize",
		Usage: "Summarize an existing trie dump file (skips trie traversal)",
	}
	inspectTrieContractFlag = cli.StringFlag{
		Name:  "contract",
		Usage: "Inspect a single contract's storage footprint by 0x address",
	}
)

var (
	removedbCommand = cli.Command{
		Action:    utils.MigrateFlags(removeDB),
		Name:      "removedb",
		Usage:     "Remove blockchain and state databases",
		ArgsUsage: "",
		Flags: []cli.Flag{
			utils.DataDirFlag,
		},
		Category: "DATABASE COMMANDS",
		Description: `
Remove blockchain and state databases`,
	}
	dbCommand = cli.Command{
		Name:      "db",
		Usage:     "Low level database operations",
		ArgsUsage: "",
		Category:  "DATABASE COMMANDS",
		Subcommands: []cli.Command{
			dbInspectCmd,
			dbInspectTrieCmd,
			dbStatCmd,
			dbCompactCmd,
			dbGetCmd,
			dbDeleteCmd,
			dbPutCmd,
			dbGetSlotsCmd,
			dbDumpFreezerIndex,
			dbImportCmd,
			dbExportCmd,
		},
	}
	dbInspectTrieCmd = cli.Command{
		Action:    utils.MigrateFlags(inspectTrie),
		Name:      "inspect-trie",
		ArgsUsage: "[blocknum|latest|snapshot]",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.AncientFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.SepoliaFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
			inspectTrieTopFlag,
			inspectTrieExcludeStorageFlag,
			inspectTrieOutputFileFlag,
			inspectTrieDumpPathFlag,
			inspectTrieSummarizeFlag,
			inspectTrieContractFlag,
		},
		Usage: "Walk an MPT state trie and report structural metrics",
		Description: `This command walks the MPT-encoded account trie for the given target and,
unless --exclude-storage is set, every non-empty storage trie. The walk
is a two-pass operation: pass 1 streams per-storage-trie records into
--dump-path (default <datadir>/trie-dump.bin), pass 2 summarizes the
dump and prints tables to stdout (or writes JSON via --output).

The argument selects the state to inspect:

    latest               the current head (default if omitted)
    <blocknum>           the canonical block at the given decimal height
    snapshot             the state root recorded by the snapshot layer

Alternative modes:

    --summarize <path>   skip the walk and re-summarize an existing dump
    --contract 0xADDR    inspect a single contract's storage trie + snap
                         view, bypassing the top-N account-trie scan

This command only supports MPT mode. It refuses to run against ZKTrie-
encoded history (pre-JadeFork on morph mainnet/Holesky); target a block
after the JadeFork activation time or run on an MPT-native chain.`,
	}
	dbInspectCmd = cli.Command{
		Action:    utils.MigrateFlags(inspect),
		Name:      "inspect",
		ArgsUsage: "<prefix> <start>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.AncientFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.SepoliaFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
		},
		Usage:       "Inspect the storage size for each type of data in the database",
		Description: `This commands iterates the entire database. If the optional 'prefix' and 'start' arguments are provided, then the iteration is limited to the given subset of data.`,
	}
	dbStatCmd = cli.Command{
		Action: utils.MigrateFlags(dbStats),
		Name:   "stats",
		Usage:  "Print leveldb statistics",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.SepoliaFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
		},
	}
	dbCompactCmd = cli.Command{
		Action: utils.MigrateFlags(dbCompact),
		Name:   "compact",
		Usage:  "Compact leveldb database. WARNING: May take a very long time",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.SepoliaFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
			utils.CacheFlag,
			utils.CacheDatabaseFlag,
		},
		Description: `This command performs a database compaction.
WARNING: This operation may take a very long time to finish, and may cause database
corruption if it is aborted during execution'!`,
	}
	dbGetCmd = cli.Command{
		Action:    utils.MigrateFlags(dbGet),
		Name:      "get",
		Usage:     "Show the value of a database key",
		ArgsUsage: "<hex-encoded key>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.SepoliaFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
		},
		Description: "This command looks up the specified database key from the database.",
	}
	dbDeleteCmd = cli.Command{
		Action:    utils.MigrateFlags(dbDelete),
		Name:      "delete",
		Usage:     "Delete a database key (WARNING: may corrupt your database)",
		ArgsUsage: "<hex-encoded key>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.SepoliaFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
		},
		Description: `This command deletes the specified database key from the database.
WARNING: This is a low-level operation which may cause database corruption!`,
	}
	dbPutCmd = cli.Command{
		Action:    utils.MigrateFlags(dbPut),
		Name:      "put",
		Usage:     "Set the value of a database key (WARNING: may corrupt your database)",
		ArgsUsage: "<hex-encoded key> <hex-encoded value>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.SepoliaFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
		},
		Description: `This command sets a given database key to the given value.
WARNING: This is a low-level operation which may cause database corruption!`,
	}
	dbGetSlotsCmd = cli.Command{
		Action:    utils.MigrateFlags(dbDumpTrie),
		Name:      "dumptrie",
		Usage:     "Show the storage key/values of a given storage trie",
		ArgsUsage: "<hex-encoded storage trie root> <hex-encoded start (optional)> <int max elements (optional)>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.SepoliaFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
		},
		Description: "This command looks up the specified database key from the database.",
	}
	dbDumpFreezerIndex = cli.Command{
		Action:    utils.MigrateFlags(freezerInspect),
		Name:      "freezer-index",
		Usage:     "Dump out the index of a given freezer type",
		ArgsUsage: "<type> <start (int)> <end (int)>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.SepoliaFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
		},
		Description: "This command displays information about the freezer index.",
	}
	dbImportCmd = cli.Command{
		Action:    utils.MigrateFlags(importLDBdata),
		Name:      "import",
		Usage:     "Imports leveldb-data from an exported RLP dump.",
		ArgsUsage: "<dumpfile> <start (optional)",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
		},
		Description: "The import command imports the specific chain data from an RLP encoded stream.",
	}
	dbExportCmd = cli.Command{
		Action:    utils.MigrateFlags(exportChaindata),
		Name:      "export",
		Usage:     "Exports the chain data into an RLP dump. If the <dumpfile> has .gz suffix, gzip compression will be used.",
		ArgsUsage: "<type> <dumpfile>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.SyncModeFlag,
			utils.MainnetFlag,
			utils.RopstenFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.MorphFlag,
			utils.MorphHoleskyFlag,
			utils.MorphHoodiFlag,
		},
		Description: "Exports the specified chain data to an RLP encoded stream, optionally gzip-compressed.",
	}
)

func removeDB(ctx *cli.Context) error {
	stack, config := makeConfigNode(ctx)

	// Remove the full node state database
	path := stack.ResolvePath("chaindata")
	if common.FileExist(path) {
		confirmAndRemoveDB(path, "full node state database")
	} else {
		log.Info("Full node state database missing", "path", path)
	}
	// Remove the full node ancient database
	path = config.Eth.DatabaseFreezer
	switch {
	case path == "":
		path = filepath.Join(stack.ResolvePath("chaindata"), "ancient")
	case !filepath.IsAbs(path):
		path = config.Node.ResolvePath(path)
	}
	if common.FileExist(path) {
		confirmAndRemoveDB(path, "full node ancient database")
	} else {
		log.Info("Full node ancient database missing", "path", path)
	}
	// Remove the light node database
	path = stack.ResolvePath("lightchaindata")
	if common.FileExist(path) {
		confirmAndRemoveDB(path, "light node database")
	} else {
		log.Info("Light node database missing", "path", path)
	}
	return nil
}

// confirmAndRemoveDB prompts the user for a last confirmation and removes the
// folder if accepted.
func confirmAndRemoveDB(database string, kind string) {
	confirm, err := prompt.Stdin.PromptConfirm(fmt.Sprintf("Remove %s (%s)?", kind, database))
	switch {
	case err != nil:
		utils.Fatalf("%v", err)
	case !confirm:
		log.Info("Database deletion skipped", "path", database)
	default:
		start := time.Now()
		filepath.Walk(database, func(path string, info os.FileInfo, err error) error {
			// If we're at the top level folder, recurse into
			if path == database {
				return nil
			}
			// Delete all the files, but not subfolders
			if !info.IsDir() {
				os.Remove(path)
				return nil
			}
			return filepath.SkipDir
		})
		log.Info("Database successfully deleted", "path", database, "elapsed", common.PrettyDuration(time.Since(start)))
	}
}

func inspect(ctx *cli.Context) error {
	var (
		prefix []byte
		start  []byte
	)
	if ctx.NArg() > 2 {
		return fmt.Errorf("Max 2 arguments: %v", ctx.Command.ArgsUsage)
	}
	if ctx.NArg() >= 1 {
		if d, err := hexutil.Decode(ctx.Args().Get(0)); err != nil {
			return fmt.Errorf("failed to hex-decode 'prefix': %v", err)
		} else {
			prefix = d
		}
	}
	if ctx.NArg() >= 2 {
		if d, err := hexutil.Decode(ctx.Args().Get(1)); err != nil {
			return fmt.Errorf("failed to hex-decode 'start': %v", err)
		} else {
			start = d
		}
	}
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack, true)
	defer db.Close()

	return rawdb.InspectDatabase(db, prefix, start)
}

// inspectTrie is the handler for `geth db inspect-trie`. It dispatches to
// one of three upstream-aligned modes depending on the flags supplied:
//
//   - --summarize <path>: skip the trie walk and re-analyze an existing
//     pass-1 dump. Useful to regenerate a report cheaply after a long
//     walk, or to share dumps across machines for offline inspection.
//   - --contract 0xADDR: run trie.InspectContract against the resolved
//     state root to compare the trie and snapshot views for one
//     contract.
//   - otherwise: run the two-pass inspector (pass 1 writes a dump, pass
//     2 produces the summary) via trie.Inspect.
//
// The positional argument selects the state root for the trie-walk and
// contract modes:
//
//	latest      (default)  head block's state root
//	<blocknum>             canonical block at decimal height
//	snapshot               state root recorded by the snapshot layer
//
// ZKTrie-encoded morph history is refused via trie.ErrUnsupportedTrieFormat.
// This matches upstream's "MPT only" contract since the inspector does
// not understand morph's zkTrie encoding.
func inspectTrie(ctx *cli.Context) error {
	topN := ctx.Int(inspectTrieTopFlag.Name)
	if topN < 0 {
		return fmt.Errorf("invalid --%s value %d (must be >= 0)", inspectTrieTopFlag.Name, topN)
	}

	// Mode 1: summarize an existing dump. The trie database is not
	// needed since we only read the binary dump.
	if summarizePath := ctx.String(inspectTrieSummarizeFlag.Name); summarizePath != "" {
		if ctx.NArg() > 0 {
			return fmt.Errorf("block argument is not supported with --%s", inspectTrieSummarizeFlag.Name)
		}
		cfg := &trie.InspectConfig{
			TopN:     topN,
			Path:     ctx.String(inspectTrieOutputFileFlag.Name),
			DumpPath: summarizePath,
		}
		log.Info("Summarizing trie dump", "path", summarizePath, "top", topN)
		return trie.Summarize(summarizePath, cfg)
	}

	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack, true)
	defer db.Close()

	root, blockNumber, blockTime, blockMetaKnown, err := resolveInspectTarget(ctx, db)
	if err != nil {
		return err
	}

	// Detect ZKTrie mode: when the chain began in ZKTrie format
	// (UseZktrie=true) and the target block predates JadeFork, the state
	// is still zkTrie-encoded. We refuse rather than produce garbage.
	chainConfig := rawdb.ReadChainConfig(db, rawdb.ReadCanonicalHash(db, 0))
	if chainConfig != nil && chainConfig.Morph.UseZktrie && blockMetaKnown && !chainConfig.IsJadeFork(blockTime) {
		return fmt.Errorf("%w (block %d time %d predates JadeFork)",
			trie.ErrUnsupportedTrieFormat, blockNumber, blockTime)
	}

	triedb := trie.NewDatabase(db)

	// Mode 2: single-contract inspection. The result is printed to
	// stdout; --output/--dump-path are ignored in this mode because
	// InspectContract emits a fixed report directly.
	if contractArg := ctx.String(inspectTrieContractFlag.Name); contractArg != "" {
		if !common.IsHexAddress(contractArg) {
			return fmt.Errorf("invalid --%s value %q: not an address", inspectTrieContractFlag.Name, contractArg)
		}
		address := common.HexToAddress(contractArg)
		log.Info("Inspecting contract", "address", address, "root", root, "block", blockNumber)
		return trie.InspectContract(triedb, db, root, address)
	}

	// Mode 3: full two-pass trie inspection.
	dumpPath := ctx.String(inspectTrieDumpPathFlag.Name)
	if dumpPath == "" {
		dumpPath = stack.ResolvePath("trie-dump.bin")
	}
	cfg := &trie.InspectConfig{
		NoStorage: ctx.Bool(inspectTrieExcludeStorageFlag.Name),
		TopN:      topN,
		Path:      ctx.String(inspectTrieOutputFileFlag.Name),
		DumpPath:  dumpPath,
	}
	log.Info("Inspecting trie",
		"root", root,
		"block", blockNumber,
		"time", blockTime,
		"excludeStorage", cfg.NoStorage,
		"top", topN,
		"dump", cfg.DumpPath,
	)
	start := time.Now()
	if err := trie.Inspect(triedb, root, cfg); err != nil {
		return err
	}
	log.Info("Trie inspection complete", "elapsed", common.PrettyDuration(time.Since(start)))
	return nil
}

// resolveInspectTarget picks a state root and best-effort block metadata
// from the positional argument. For the "snapshot" keyword it first tries to
// recover canonical block metadata from the snapshot root; if no matching
// header is available the block metadata is returned as unknown.
func resolveInspectTarget(ctx *cli.Context, db ethdb.Database) (common.Hash, uint64, uint64, bool, error) {
	if ctx.NArg() > 1 {
		return common.Hash{}, 0, 0, false, fmt.Errorf("max 1 argument: %v", ctx.Command.ArgsUsage)
	}

	arg := "latest"
	if ctx.NArg() == 1 {
		arg = ctx.Args().Get(0)
	}

	if arg == "snapshot" {
		return resolveSnapshotInspectTarget(db)
	}

	if arg == "latest" {
		header := rawdb.ReadHeadHeader(db)
		if header == nil {
			return common.Hash{}, 0, 0, false, errors.New("head header not found; database may be empty")
		}
		return header.Root, header.Number.Uint64(), header.Time, true, nil
	}

	n, err := strconv.ParseUint(arg, 10, 64)
	if err != nil {
		return common.Hash{}, 0, 0, false, fmt.Errorf("invalid block argument %q: %w", arg, err)
	}
	hash := rawdb.ReadCanonicalHash(db, n)
	if hash == (common.Hash{}) {
		return common.Hash{}, 0, 0, false, fmt.Errorf("canonical hash for block %d not found", n)
	}
	header := rawdb.ReadHeader(db, hash, n)
	if header == nil {
		return common.Hash{}, 0, 0, false, fmt.Errorf("header for block %d / %x not found", n, hash)
	}
	return header.Root, header.Number.Uint64(), header.Time, true, nil
}

func resolveSnapshotInspectTarget(db ethdb.Database) (common.Hash, uint64, uint64, bool, error) {
	root := rawdb.ReadSnapshotRoot(db)
	if root == (common.Hash{}) {
		return common.Hash{}, 0, 0, false, errors.New("snapshot root not found in database")
	}
	if header := rawdb.ReadHeadHeader(db); header != nil && header.Root == root {
		return root, header.Number.Uint64(), header.Time, true, nil
	}
	if number := rawdb.ReadSnapshotRecoveryNumber(db); number != nil {
		hash := rawdb.ReadCanonicalHash(db, *number)
		if hash != (common.Hash{}) {
			if header := rawdb.ReadHeader(db, hash, *number); header != nil && header.Root == root {
				return root, header.Number.Uint64(), header.Time, true, nil
			}
		}
	}
	return root, 0, 0, false, nil
}

func showLeveldbStats(db ethdb.Stater) {
	if stats, err := db.Stat("leveldb.stats"); err != nil {
		log.Warn("Failed to read database stats", "error", err)
	} else {
		fmt.Println(stats)
	}
	if ioStats, err := db.Stat("leveldb.iostats"); err != nil {
		log.Warn("Failed to read database iostats", "error", err)
	} else {
		fmt.Println(ioStats)
	}
}

func dbStats(ctx *cli.Context) error {
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack, true)
	defer db.Close()

	showLeveldbStats(db)
	return nil
}

func dbCompact(ctx *cli.Context) error {
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack, false)
	defer db.Close()

	log.Info("Stats before compaction")
	showLeveldbStats(db)

	log.Info("Triggering compaction")
	if err := db.Compact(nil, nil); err != nil {
		log.Info("Compact err", "error", err)
		return err
	}
	log.Info("Stats after compaction")
	showLeveldbStats(db)
	return nil
}

// dbGet shows the value of a given database key
func dbGet(ctx *cli.Context) error {
	if ctx.NArg() != 1 {
		return fmt.Errorf("required arguments: %v", ctx.Command.ArgsUsage)
	}
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack, true)
	defer db.Close()

	key, err := parseHexOrString(ctx.Args().Get(0))
	if err != nil {
		log.Info("Could not decode the key", "error", err)
		return err
	}

	data, err := db.Get(key)
	if err != nil {
		log.Info("Get operation failed", "key", fmt.Sprintf("0x%#x", key), "error", err)
		return err
	}
	fmt.Printf("key %#x: %#x\n", key, data)
	return nil
}

// dbDelete deletes a key from the database
func dbDelete(ctx *cli.Context) error {
	if ctx.NArg() != 1 {
		return fmt.Errorf("required arguments: %v", ctx.Command.ArgsUsage)
	}
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack, false)
	defer db.Close()

	key, err := parseHexOrString(ctx.Args().Get(0))
	if err != nil {
		log.Info("Could not decode the key", "error", err)
		return err
	}
	data, err := db.Get(key)
	if err == nil {
		fmt.Printf("Previous value: %#x\n", data)
	}
	if err = db.Delete(key); err != nil {
		log.Info("Delete operation returned an error", "key", fmt.Sprintf("0x%#x", key), "error", err)
		return err
	}
	return nil
}

// dbPut overwrite a value in the database
func dbPut(ctx *cli.Context) error {
	if ctx.NArg() != 2 {
		return fmt.Errorf("required arguments: %v", ctx.Command.ArgsUsage)
	}
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack, false)
	defer db.Close()

	var (
		key   []byte
		value []byte
		data  []byte
		err   error
	)
	key, err = parseHexOrString(ctx.Args().Get(0))
	if err != nil {
		log.Info("Could not decode the key", "error", err)
		return err
	}
	value, err = hexutil.Decode(ctx.Args().Get(1))
	if err != nil {
		log.Info("Could not decode the value", "error", err)
		return err
	}
	data, err = db.Get(key)
	if err == nil {
		fmt.Printf("Previous value: %#x\n", data)
	}
	return db.Put(key, value)
}

// dbDumpTrie shows the key-value slots of a given storage trie
func dbDumpTrie(ctx *cli.Context) error {
	if ctx.NArg() < 1 {
		return fmt.Errorf("required arguments: %v", ctx.Command.ArgsUsage)
	}
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack, true)
	defer db.Close()
	var (
		root  []byte
		start []byte
		max   = int64(-1)
		err   error
	)
	if root, err = hexutil.Decode(ctx.Args().Get(0)); err != nil {
		log.Info("Could not decode the root", "error", err)
		return err
	}
	stRoot := common.BytesToHash(root)
	if ctx.NArg() >= 2 {
		if start, err = hexutil.Decode(ctx.Args().Get(1)); err != nil {
			log.Info("Could not decode the seek position", "error", err)
			return err
		}
	}
	if ctx.NArg() >= 3 {
		if max, err = strconv.ParseInt(ctx.Args().Get(2), 10, 64); err != nil {
			log.Info("Could not decode the max count", "error", err)
			return err
		}
	}
	theTrie, err := trie.New(stRoot, trie.NewDatabase(db))
	if err != nil {
		return err
	}
	var count int64
	it := trie.NewIterator(theTrie.NodeIterator(start))
	for it.Next() {
		if max > 0 && count == max {
			fmt.Printf("Exiting after %d values\n", count)
			break
		}
		fmt.Printf("  %d. key %#x: %#x\n", count, it.Key, it.Value)
		count++
	}
	return it.Err
}

func freezerInspect(ctx *cli.Context) error {
	var (
		start, end    int64
		disableSnappy bool
		err           error
	)
	if ctx.NArg() < 3 {
		return fmt.Errorf("required arguments: %v", ctx.Command.ArgsUsage)
	}
	kind := ctx.Args().Get(0)
	if noSnap, ok := rawdb.FreezerNoSnappy[kind]; !ok {
		var options []string
		for opt := range rawdb.FreezerNoSnappy {
			options = append(options, opt)
		}
		sort.Strings(options)
		return fmt.Errorf("Could read freezer-type '%v'. Available options: %v", kind, options)
	} else {
		disableSnappy = noSnap
	}
	if start, err = strconv.ParseInt(ctx.Args().Get(1), 10, 64); err != nil {
		log.Info("Could read start-param", "error", err)
		return err
	}
	if end, err = strconv.ParseInt(ctx.Args().Get(2), 10, 64); err != nil {
		log.Info("Could read count param", "error", err)
		return err
	}
	stack, _ := makeConfigNode(ctx)
	defer stack.Close()
	path := filepath.Join(stack.ResolvePath("chaindata"), "ancient")
	log.Info("Opening freezer", "location", path, "name", kind)
	if f, err := rawdb.NewFreezerTable(path, kind, disableSnappy); err != nil {
		return err
	} else {
		f.DumpIndex(start, end)
	}
	return nil
}

// ParseHexOrString tries to hexdecode b, but if the prefix is missing, it instead just returns the raw bytes
func parseHexOrString(str string) ([]byte, error) {
	b, err := hexutil.Decode(str)
	if errors.Is(err, hexutil.ErrMissingPrefix) {
		return []byte(str), nil
	}
	return b, err
}

func importLDBdata(ctx *cli.Context) error {
	start := 0
	switch ctx.NArg() {
	case 1:
		break
	case 2:
		s, err := strconv.Atoi(ctx.Args().Get(1))
		if err != nil {
			return fmt.Errorf("second arg must be an integer: %v", err)
		}
		start = s
	default:
		return fmt.Errorf("required arguments: %v", ctx.Command.ArgsUsage)
	}
	var (
		fName     = ctx.Args().Get(0)
		stack, _  = makeConfigNode(ctx)
		interrupt = make(chan os.Signal, 1)
		stop      = make(chan struct{})
	)
	defer stack.Close()
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	defer close(interrupt)
	go func() {
		if _, ok := <-interrupt; ok {
			log.Info("Interrupted during ldb import, stopping at next batch")
		}
		close(stop)
	}()
	db := utils.MakeChainDatabase(ctx, stack, false)
	return utils.ImportLDBData(db, fName, int64(start), stop)
}

type preimageIterator struct {
	iter ethdb.Iterator
}

func (iter *preimageIterator) Next() (byte, []byte, []byte, bool) {
	for iter.iter.Next() {
		key := iter.iter.Key()
		if bytes.HasPrefix(key, rawdb.PreimagePrefix) && len(key) == (len(rawdb.PreimagePrefix)+common.HashLength) {
			return utils.OpBatchAdd, key, iter.iter.Value(), true
		}
	}
	return 0, nil, nil, false
}

func (iter *preimageIterator) Release() {
	iter.iter.Release()
}

type snapshotIterator struct {
	init    bool
	account ethdb.Iterator
	storage ethdb.Iterator
}

func (iter *snapshotIterator) Next() (byte, []byte, []byte, bool) {
	if !iter.init {
		iter.init = true
		return utils.OpBatchDel, rawdb.SnapshotRootKey, nil, true
	}
	for iter.account.Next() {
		key := iter.account.Key()
		if bytes.HasPrefix(key, rawdb.SnapshotAccountPrefix) && len(key) == (len(rawdb.SnapshotAccountPrefix)+common.HashLength) {
			return utils.OpBatchAdd, key, iter.account.Value(), true
		}
	}
	for iter.storage.Next() {
		key := iter.storage.Key()
		if bytes.HasPrefix(key, rawdb.SnapshotStoragePrefix) && len(key) == (len(rawdb.SnapshotStoragePrefix)+2*common.HashLength) {
			return utils.OpBatchAdd, key, iter.storage.Value(), true
		}
	}
	return 0, nil, nil, false
}

func (iter *snapshotIterator) Release() {
	iter.account.Release()
	iter.storage.Release()
}

// chainExporters defines the export scheme for all exportable chain data.
var chainExporters = map[string]func(db ethdb.Database) utils.ChainDataIterator{
	"preimage": func(db ethdb.Database) utils.ChainDataIterator {
		iter := db.NewIterator(rawdb.PreimagePrefix, nil)
		return &preimageIterator{iter: iter}
	},
	"snapshot": func(db ethdb.Database) utils.ChainDataIterator {
		account := db.NewIterator(rawdb.SnapshotAccountPrefix, nil)
		storage := db.NewIterator(rawdb.SnapshotStoragePrefix, nil)
		return &snapshotIterator{account: account, storage: storage}
	},
}

func exportChaindata(ctx *cli.Context) error {
	if ctx.NArg() < 2 {
		return fmt.Errorf("required arguments: %v", ctx.Command.ArgsUsage)
	}
	// Parse the required chain data type, make sure it's supported.
	kind := ctx.Args().Get(0)
	kind = strings.ToLower(strings.Trim(kind, " "))
	exporter, ok := chainExporters[kind]
	if !ok {
		var kinds []string
		for kind := range chainExporters {
			kinds = append(kinds, kind)
		}
		return fmt.Errorf("invalid data type %s, supported types: %s", kind, strings.Join(kinds, ", "))
	}
	var (
		stack, _  = makeConfigNode(ctx)
		interrupt = make(chan os.Signal, 1)
		stop      = make(chan struct{})
	)
	defer stack.Close()
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	defer close(interrupt)
	go func() {
		if _, ok := <-interrupt; ok {
			log.Info("Interrupted during db export, stopping at next batch")
		}
		close(stop)
	}()
	db := utils.MakeChainDatabase(ctx, stack, true)
	return utils.ExportChaindata(ctx.Args().Get(1), kind, exporter(db), stop)
}
