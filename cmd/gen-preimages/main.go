// gen-preimages generates and imports preimages from a genesis.json file
// into a geth chaindata database.
//
// This is useful for migration-checker tool which needs preimages to
// correlate keys between ZK trie and MPT trie.
//
// Use --zk flag to generate Poseidon hash format (for ZK nodes)
// Default is Keccak256 hash format (for MPT nodes)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/ethdb/leveldb"
	zkt "github.com/scroll-tech/zktrie/types"
)

func main() {
	var (
		genesisPath = flag.String("genesis", "", "path to genesis.json file")
		dbPath      = flag.String("db", "", "path to the geth chaindata directory")
		zkMode      = flag.Bool("zk", false, "use Poseidon hash format for ZK nodes (default: Keccak256 for MPT)")
		dryRun      = flag.Bool("dry-run", false, "only show what would be imported without actually importing")
		check       = flag.Bool("check", false, "check if preimages exist in the database (requires -db)")
	)
	flag.Parse()

	// Check mode: verify if preimages exist in database
	if *check {
		if *dbPath == "" {
			fmt.Println("Error: -db flag is required for check mode")
			os.Exit(1)
		}
		checkPreimages(*dbPath, *genesisPath)
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

	// Collect preimages
	preimages := make(map[common.Hash][]byte)

	hashFuncName := "Keccak256"
	if *zkMode {
		hashFuncName = "Poseidon"
	}

	for addr, account := range genesis.Alloc {
		// Account address preimage
		addrBytes := addr.Bytes() // 20 bytes
		var addrHash common.Hash
		if *zkMode {
			// ZK mode: use Poseidon hash (ToSecureKey)
			// Note: ZK trie uses 20-byte address directly, NOT 32-byte padded
			secureKey, err := zkt.ToSecureKey(addrBytes)
			if err != nil {
				fmt.Printf("Error computing secure key for address %s: %v\n", addr.Hex(), err)
				os.Exit(1)
			}
			addrHash = common.BytesToHash(secureKey.Bytes())
			preimages[addrHash] = addrBytes // Store original 20-byte address
		} else {
			// MPT mode: use Keccak256 hash
			addrHash = crypto.Keccak256Hash(addrBytes)
			preimages[addrHash] = addrBytes
		}

		// Storage key preimages
		for slot := range account.Storage {
			slotBytes := slot.Bytes()
			var slotHash common.Hash
			if *zkMode {
				// ZK mode: use Poseidon hash
				secureKey, err := zkt.ToSecureKey(slotBytes)
				if err != nil {
					fmt.Printf("Error computing secure key for slot %s: %v\n", slot.Hex(), err)
					os.Exit(1)
				}
				slotHash = common.BytesToHash(secureKey.Bytes())
			} else {
				// MPT mode: use Keccak256 hash
				slotHash = crypto.Keccak256Hash(slotBytes)
			}
			preimages[slotHash] = slotBytes
		}
	}

	fmt.Printf("Mode: %s hash format\n", hashFuncName)
	fmt.Printf("Found %d preimages from genesis:\n", len(preimages))
	fmt.Printf("  - %d account addresses\n", len(genesis.Alloc))

	storageCount := 0
	for _, account := range genesis.Alloc {
		storageCount += len(account.Storage)
	}
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

	// Open database
	db, err := leveldb.New(*dbPath, 1024, 128, "", false)
	if err != nil {
		fmt.Printf("Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Write preimages
	rawdb.WritePreimages(db, preimages)

	fmt.Printf("\nSuccessfully imported %d preimages (%s format) to %s\n", len(preimages), hashFuncName, *dbPath)
}

// checkPreimages checks if preimages exist in the database
func checkPreimages(dbPath, genesisPath string) {
	// Open database (read-only)
	db, err := leveldb.New(dbPath, 1024, 128, "", true)
	if err != nil {
		fmt.Printf("Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Count total preimages in database by iterating with prefix
	fmt.Println("Scanning database for preimages...")
	preimagePrefix := []byte("secure-key-")

	iter := db.NewIterator(preimagePrefix, nil)
	defer iter.Release()

	totalCount := 0
	addrCount := 0    // 20-byte preimages (addresses)
	slotCount := 0    // 32-byte preimages (storage slots)
	otherCount := 0   // other sizes

	// Show first few preimages with their keys for debugging
	sampleCount := 0
	for iter.Next() {
		totalCount++
		key := iter.Key()
		value := iter.Value()

		// Key format: "secure-key-" (11 bytes) + hash (32 bytes)
		var hashKey []byte
		if len(key) > 11 {
			hashKey = key[11:] // Extract the hash part
		}

		switch len(value) {
		case 20:
			addrCount++
			if sampleCount < 5 {
				// Calculate expected Keccak256 hash
				expectedKeccak := crypto.Keccak256(value)
				isKeccak := bytes.Equal(hashKey, expectedKeccak)
				fmt.Printf("  Preimage: %x\n", value)
				fmt.Printf("    Key hash:      %x\n", hashKey)
				fmt.Printf("    Keccak256:     %x\n", expectedKeccak)
				fmt.Printf("    Is Keccak256:  %v\n", isKeccak)
				fmt.Println()
				sampleCount++
			}
		case 32:
			slotCount++
		default:
			otherCount++
		}

		if totalCount%100000 == 0 {
			fmt.Printf("  Scanned %d preimages...\n", totalCount)
		}
	}

	fmt.Println("========== Preimage Statistics ==========")
	fmt.Printf("Total preimages in database: %d\n", totalCount)
	fmt.Printf("  - Address preimages (20 bytes): %d\n", addrCount)
	fmt.Printf("  - Storage slot preimages (32 bytes): %d\n", slotCount)
	if otherCount > 0 {
		fmt.Printf("  - Other sizes: %d\n", otherCount)
	}

	if totalCount == 0 {
		fmt.Println("\n⚠️  WARNING: No preimages found in database!")
		fmt.Println("   This node likely started without --cache.preimages or --gcmode=archive")
		os.Exit(1)
	}

	// If genesis file provided, check if genesis preimages exist
	if genesisPath != "" {
		fmt.Println("\n---------- Genesis Preimage Check ----------")
		checkGenesisPreimages(db, genesisPath)
	}

	fmt.Println("==========================================")
}

// checkGenesisPreimages checks if genesis preimages exist in the database
func checkGenesisPreimages(db *leveldb.Database, genesisPath string) {
	genesisData, err := os.ReadFile(genesisPath)
	if err != nil {
		fmt.Printf("Error reading genesis file: %v\n", err)
		return
	}

	var genesis core.Genesis
	if err := json.Unmarshal(genesisData, &genesis); err != nil {
		fmt.Printf("Error parsing genesis file: %v\n", err)
		return
	}

	// Check a sample of genesis addresses
	found := 0
	missing := 0
	sampleSize := 10
	missingExamples := []string{}

	for addr := range genesis.Alloc {
		addrBytes := addr.Bytes()
		addrHash := crypto.Keccak256Hash(addrBytes)
		preimage := rawdb.ReadPreimage(db, addrHash)

		if len(preimage) > 0 {
			found++
		} else {
			missing++
			if len(missingExamples) < 3 {
				missingExamples = append(missingExamples, addr.Hex())
			}
		}

		if found+missing >= sampleSize && missing > 0 {
			break // Found some missing, no need to check all
		}
	}

	totalGenesis := len(genesis.Alloc)
	fmt.Printf("Genesis accounts: %d\n", totalGenesis)
	fmt.Printf("Sample checked: %d (found: %d, missing: %d)\n", found+missing, found, missing)

	if missing > 0 {
		fmt.Println("\n⚠️  WARNING: Genesis preimages are missing!")
		fmt.Println("   Missing examples:", missingExamples)
		fmt.Println("   Run: gen-preimages -genesis=<file> -db=<path> to import them")
	} else {
		fmt.Println("\n✅ Genesis preimages are present")
	}
}
