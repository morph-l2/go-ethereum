// gen-preimages generates and imports genesis preimages into a ZK node's database.
//
// Genesis preimages are NOT automatically stored during geth init.
// This tool extracts account addresses and storage slots from genesis.json,
// computes their Poseidon hashes, and imports them as preimages.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/ethdb/leveldb"
	zkt "github.com/scroll-tech/zktrie/types"
)

func main() {
	var (
		genesisPath = flag.String("genesis", "", "path to genesis.json file")
		dbPath      = flag.String("db", "", "path to the ZK node chaindata directory")
		dryRun      = flag.Bool("dry-run", false, "only show what would be imported without actually importing")
		check       = flag.Bool("check", false, "check if preimages exist in the database")
	)
	flag.Parse()

	// Check mode
	if *check {
		if *dbPath == "" {
			fmt.Println("Error: -db flag is required for check mode")
			os.Exit(1)
		}
		checkPreimages(*dbPath)
		return
	}

	// Import mode
	if *genesisPath == "" {
		fmt.Println("Error: -genesis flag is required")
		flag.Usage()
		os.Exit(1)
	}

	if *dbPath == "" && !*dryRun {
		fmt.Println("Error: -db flag is required (or use -dry-run)")
		flag.Usage()
		os.Exit(1)
	}

	// Read genesis file
	genesisData, err := os.ReadFile(*genesisPath)
	if err != nil {
		fmt.Printf("Error reading genesis file: %v\n", err)
		os.Exit(1)
	}

	var genesis core.Genesis
	if err := json.Unmarshal(genesisData, &genesis); err != nil {
		fmt.Printf("Error parsing genesis file: %v\n", err)
		os.Exit(1)
	}

	// Collect preimages using Poseidon hash
	preimages := make(map[common.Hash][]byte)

	for addr, account := range genesis.Alloc {
		// Account address preimage (20 bytes)
		addrBytes := addr.Bytes()
		secureKey, err := zkt.ToSecureKey(addrBytes)
		if err != nil {
			fmt.Printf("Error computing secure key for address %s: %v\n", addr.Hex(), err)
			os.Exit(1)
		}
		addrHash := common.BytesToHash(secureKey.Bytes())
		preimages[addrHash] = addrBytes

		// Storage slot preimages (32 bytes)
		for slot := range account.Storage {
			slotBytes := slot.Bytes()
			slotSecureKey, err := zkt.ToSecureKey(slotBytes)
			if err != nil {
				fmt.Printf("Error computing secure key for slot %s: %v\n", slot.Hex(), err)
				os.Exit(1)
			}
			slotHash := common.BytesToHash(slotSecureKey.Bytes())
			preimages[slotHash] = slotBytes
		}
	}

	// Count statistics
	addrCount := len(genesis.Alloc)
	storageCount := 0
	for _, account := range genesis.Alloc {
		storageCount += len(account.Storage)
	}

	fmt.Printf("Found %d preimages from genesis:\n", len(preimages))
	fmt.Printf("  - %d account addresses\n", addrCount)
	fmt.Printf("  - %d storage slots\n", storageCount)

	if *dryRun {
		fmt.Println("\nDry run mode - showing first 10 preimages:")
		count := 0
		for hash, preimage := range preimages {
			if count >= 10 {
				fmt.Println("  ...")
				break
			}
			fmt.Printf("  %s -> %x\n", hash.Hex(), preimage)
			count++
		}
		return
	}

	// Open database and import
	db, err := leveldb.New(*dbPath, 1024, 128, "", false)
	if err != nil {
		fmt.Printf("Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	rawdb.WritePreimages(db, preimages)

	fmt.Printf("\nSuccessfully imported %d preimages to %s\n", len(preimages), *dbPath)
}

// checkPreimages checks if preimages exist in the database
func checkPreimages(dbPath string) {
	db, err := leveldb.New(dbPath, 1024, 128, "", true)
	if err != nil {
		fmt.Printf("Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Println("Scanning database for preimages...")
	preimagePrefix := []byte("secure-key-")

	iter := db.NewIterator(preimagePrefix, nil)
	defer iter.Release()

	totalCount := 0
	addrCount := 0
	slotCount := 0

	for iter.Next() {
		totalCount++
		value := iter.Value()
		switch len(value) {
		case 20:
			addrCount++
		case 32:
			slotCount++
		}
	}

	fmt.Println("========== Preimage Statistics ==========")
	fmt.Printf("Total preimages: %d\n", totalCount)
	fmt.Printf("  - Address preimages (20 bytes): %d\n", addrCount)
	fmt.Printf("  - Storage slot preimages (32 bytes): %d\n", slotCount)
	fmt.Println("==========================================")

	if totalCount == 0 {
		fmt.Println("\nNo preimages found!")
		fmt.Println("Run: gen-preimages -genesis=<file> -db=<path>")
		os.Exit(1)
	}
}
