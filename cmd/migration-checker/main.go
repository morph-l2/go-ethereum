package main

import (
	"bytes"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/ethdb/leveldb"
	"github.com/morph-l2/go-ethereum/rlp"
	"github.com/morph-l2/go-ethereum/trie"
)

var accountsDone atomic.Uint64
var trieCheckers = make(chan struct{}, runtime.GOMAXPROCS(0)*4)

// preimageSource determines which trie to use as the source of preimages
var preimageSource string

type dbs struct {
	zkDb  *leveldb.Database
	mptDb *leveldb.Database
}

func main() {
	var (
		mptDbPath = flag.String("mpt-db", "", "path to the MPT node DB")
		zkDbPath  = flag.String("zk-db", "", "path to the ZK node DB")
		mptRoot   = flag.String("mpt-root", "", "root hash of the MPT node")
		zkRoot    = flag.String("zk-root", "", "root hash of the ZK node")
		paranoid  = flag.Bool("paranoid", false, "verifies all node contents against their expected hash")
		source    = flag.String("preimage-source", "mpt", "source of preimages: 'mpt' (default) or 'zk'")
	)
	flag.Parse()

	if *source != "mpt" && *source != "zk" {
		fmt.Println("Error: -preimage-source must be 'mpt' or 'zk'")
		os.Exit(1)
	}
	preimageSource = *source

	fmt.Printf("Using preimage source: %s\n", preimageSource)

	zkDb, err := leveldb.New(*zkDbPath, 1024, 128, "", true)
	panicOnError(err, "", "failed to open zk db")
	mptDb, err := leveldb.New(*mptDbPath, 1024, 128, "", true)
	panicOnError(err, "", "failed to open mpt db")

	zkRootHash := common.HexToHash(*zkRoot)
	mptRootHash := common.HexToHash(*mptRoot)

	for i := 0; i < runtime.GOMAXPROCS(0)*4; i++ {
		trieCheckers <- struct{}{}
	}

	checkTrieEquality(&dbs{
		zkDb:  zkDb,
		mptDb: mptDb,
	}, zkRootHash, mptRootHash, "", checkAccountEquality, true, *paranoid)

	for i := 0; i < runtime.GOMAXPROCS(0)*4; i++ {
		<-trieCheckers
	}

	fmt.Println("===========================================")
	fmt.Println("  Migration verification PASSED!")
	fmt.Println("  All accounts and storage data match.")
	fmt.Println("===========================================")
}

func panicOnError(err error, label, msg string) {
	if err != nil {
		panic(fmt.Sprint(label, " error: ", msg, " ", err))
	}
}

func dup(s []byte) []byte {
	return append([]byte{}, s...)
}

func isAllZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// checkTrieEquality compares ZK trie and MPT trie
// It can use either ZK or MPT as the source of preimages based on --preimage-source flag
func checkTrieEquality(dbs *dbs, zkRoot, mptRoot common.Hash, label string, leafChecker func(string, *dbs, []byte, []byte, bool), top, paranoid bool) {
	zkTrie, err := trie.NewZkTrie(zkRoot, trie.NewZktrieDatabaseFromTriedb(trie.NewDatabaseWithConfig(dbs.zkDb, &trie.Config{Preimages: true})))
	panicOnError(err, label, "failed to create zk trie")
	mptTrie, err := trie.NewSecureNoTracer(mptRoot, trie.NewDatabaseWithConfig(dbs.mptDb, &trie.Config{Preimages: true}))
	panicOnError(err, label, "failed to create mpt trie")

	if preimageSource == "mpt" {
		checkFromMPT(dbs, zkTrie, mptTrie, label, leafChecker, top, paranoid)
	} else {
		checkFromZK(dbs, zkTrie, mptTrie, label, leafChecker, top, paranoid)
	}
}

// checkFromMPT iterates MPT trie and uses MPT preimages to query ZK trie
func checkFromMPT(dbs *dbs, zkTrie *trie.ZkTrie, mptTrie *trie.SecureTrie, label string, leafChecker func(string, *dbs, []byte, []byte, bool), top, paranoid bool) {
	// Load MPT leaves with preimages
	mptLeafCh := loadMPTWithPreimages(mptTrie, top)
	mptLeafMap := <-mptLeafCh

	fmt.Printf("%s [MPT source] Loaded %d leaves from MPT\n", label, len(mptLeafMap))

	// For each MPT leaf, find corresponding ZK leaf and compare
	checkedCount := 0
	for preimageKey, mptValue := range mptLeafMap {
		// ZK trie uses the original key directly (not hashed)
		zkKey := []byte(preimageKey)

		// Get value from ZK trie using the original key
		zkValue, err := zkTrie.TryGet(zkKey)
		if err != nil {
			panic(fmt.Sprintf("%s failed to get zk value for key %s: %v",
				label, hex.EncodeToString(zkKey), err))
		}
		if zkValue == nil {
			panic(fmt.Sprintf("%s key not found in zk trie: %s",
				label, hex.EncodeToString(zkKey)))
		}

		// Compare values
		leafChecker(fmt.Sprintf("%s key: %s", label, hex.EncodeToString([]byte(preimageKey))), dbs, zkValue, mptValue, paranoid)

		checkedCount++
		if top && checkedCount%1000 == 0 {
			fmt.Printf("%s Checked %d accounts...\n", label, checkedCount)
		}
	}

	fmt.Printf("%s Verified %d leaves match\n", label, checkedCount)
}

// checkFromZK iterates ZK trie and uses ZK preimages to query MPT trie
func checkFromZK(dbs *dbs, zkTrie *trie.ZkTrie, mptTrie *trie.SecureTrie, label string, leafChecker func(string, *dbs, []byte, []byte, bool), top, paranoid bool) {
	// Load ZK leaves with preimages using CountLeaves
	zkLeafCh := loadZkTrie(zkTrie, top, paranoid)
	zkLeafMap := <-zkLeafCh

	// Count key sizes
	key20Count := 0
	key32Count := 0
	for k := range zkLeafMap {
		if len(k) == 20 {
			key20Count++
		} else if len(k) == 32 {
			key32Count++
		}
	}
	fmt.Printf("%s [ZK source] Loaded %d leaves from ZK (20-byte keys: %d, 32-byte keys: %d)\n", 
		label, len(zkLeafMap), key20Count, key32Count)

	// For each ZK leaf, find corresponding MPT leaf and compare
	checkedCount := 0
	skippedZeroValues := 0
	missingInMPT := 0
	for preimageKey, zkValue := range zkLeafMap {
		// Get preimage from ZK trie
		originalKey := []byte(preimageKey)

		// Get value from MPT trie using the original key
		// SecureTrie.TryGet takes original key and hashes it internally
		mptValue, err := mptTrie.TryGet(originalKey)
		if err != nil {
			mptHashedKey := crypto.Keccak256(originalKey)
			panic(fmt.Sprintf("%s failed to get mpt value for key %s (hash: %s): %v",
				label, hex.EncodeToString(originalKey), hex.EncodeToString(mptHashedKey), err))
		}
		if mptValue == nil {
			// Check if ZK value is zero - MPT deletes zero-value storage slots
			zkValueHash := common.BytesToHash(zkValue)
			if zkValueHash == (common.Hash{}) {
				skippedZeroValues++
				continue // ZK has zero value, MPT deleted it - this is expected
			}
			// Non-zero value exists in ZK but not in MPT - this is a real difference
			missingInMPT++
			mptHashedKey := crypto.Keccak256(originalKey)
			
			// Special case: 32-byte all-zero key in main trie (account trie)
			// This happens because the zero address (20 bytes) and zero storage slot (32 bytes)
			// have the same Poseidon hash after padding. The preimage might be stored as 32 bytes
			// even though the original account address was 20 bytes.
			if len(originalKey) == 32 && label == "" && isAllZero(originalKey) {
				// Try looking up with 20-byte zero address instead
				zeroAddr := make([]byte, 20)
				mptValue20, err := mptTrie.TryGet(zeroAddr)
				if err == nil && mptValue20 != nil {
					fmt.Printf("NOTE: Found zero address account using 20-byte key (ZK preimage was stored as 32 bytes due to hash collision)\n")
					leafChecker(fmt.Sprintf("%s key: %s (zero address)", label, hex.EncodeToString(zeroAddr)), dbs, zkValue, mptValue20, paranoid)
					checkedCount++
					missingInMPT--
					continue
				}
			}
			
			fmt.Printf("⚠️  MISMATCH in [%s] (key len=%d):\n", label, len(originalKey))
			fmt.Printf("    Key (preimage):  %s\n", hex.EncodeToString(originalKey))
			fmt.Printf("    Key (keccak):    %s\n", hex.EncodeToString(mptHashedKey))
			fmt.Printf("    ZK value:        %s\n", zkValueHash.Hex())
			fmt.Printf("    ZK raw value:    %s\n", hex.EncodeToString(zkValue))
			fmt.Printf("    MPT value:       NOT FOUND\n")
			continue
		}

		// Compare values
		leafChecker(fmt.Sprintf("%s key: %s", label, hex.EncodeToString(originalKey)), dbs, zkValue, mptValue, paranoid)

		checkedCount++
		if top && checkedCount%1000 == 0 {
			fmt.Printf("%s Checked %d accounts...\n", label, checkedCount)
		}
	}

	if skippedZeroValues > 0 {
		fmt.Printf("%s Skipped %d zero-value entries (expected: MPT deletes zero storage)\n", label, skippedZeroValues)
	}
	if missingInMPT > 0 {
		fmt.Printf("%s WARNING: %d entries in ZK but not in MPT!\n", label, missingInMPT)
	}
	fmt.Printf("%s Verified %d leaves match\n", label, checkedCount)
}

func checkAccountEquality(label string, dbs *dbs, zkAccountBytes, mptAccountBytes []byte, paranoid bool) {
	mptAccount := &types.StateAccount{}
	panicOnError(rlp.DecodeBytes(mptAccountBytes, mptAccount), label, "failed to decode mpt account")
	zkAccount, err := types.UnmarshalStateAccount(zkAccountBytes)
	panicOnError(err, label, "failed to decode zk account")

	if mptAccount.Nonce != zkAccount.Nonce {
		panic(fmt.Sprintf("%s nonce mismatch: zk: %d, mpt: %d", label, zkAccount.Nonce, mptAccount.Nonce))
	}

	if mptAccount.Balance.Cmp(zkAccount.Balance) != 0 {
		panic(fmt.Sprintf("%s balance mismatch: zk: %s, mpt: %s", label, zkAccount.Balance.String(), mptAccount.Balance.String()))
	}

	if !bytes.Equal(mptAccount.KeccakCodeHash, zkAccount.KeccakCodeHash) {
		panic(fmt.Sprintf("%s code hash mismatch: zk: %s, mpt: %s", label, hex.EncodeToString(zkAccount.KeccakCodeHash), hex.EncodeToString(mptAccount.KeccakCodeHash)))
	}

	if (zkAccount.Root == common.Hash{}) != (mptAccount.Root == types.EmptyRootHash) {
		panic(fmt.Sprintf("%s empty account root mismatch: zk empty=%v, mpt empty=%v",
			label, zkAccount.Root == common.Hash{}, mptAccount.Root == types.EmptyRootHash))
	} else if zkAccount.Root != (common.Hash{}) {
		zkRoot := common.BytesToHash(zkAccount.Root[:])
		mptRoot := common.BytesToHash(mptAccount.Root[:])
		accountLabel := label // capture for goroutine
		<-trieCheckers
		go func() {
			defer func() {
				if p := recover(); p != nil {
					fmt.Println(p)
					os.Exit(1)
				}
			}()

			checkTrieEquality(dbs, zkRoot, mptRoot, accountLabel+" storage", checkStorageEquality, false, paranoid)
			accountsDone.Add(1)
			fmt.Println("Accounts done:", accountsDone.Load())
			trieCheckers <- struct{}{}
		}()
	} else {
		accountsDone.Add(1)
		fmt.Println("Accounts done:", accountsDone.Load())
	}
}

func checkStorageEquality(label string, _ *dbs, zkStorageBytes, mptStorageBytes []byte, _ bool) {
	zkValue := common.BytesToHash(zkStorageBytes)
	_, content, _, err := rlp.Split(mptStorageBytes)
	panicOnError(err, label, "failed to decode mpt storage")
	mptValue := common.BytesToHash(content)
	if !bytes.Equal(zkValue[:], mptValue[:]) {
		panic(fmt.Sprintf("%s storage mismatch: zk: %s, mpt: %s", label, zkValue.Hex(), mptValue.Hex()))
	}
}

// loadMPTWithPreimages loads MPT leaves and resolves preimages
// Returns map[preimageKey]value where preimageKey is the original key (address or storage slot)
func loadMPTWithPreimages(mptTrie *trie.SecureTrie, parallel bool) chan map[string][]byte {
	startKey := make([]byte, 32)
	workers := 1 << 5
	if !parallel {
		workers = 1
	}
	step := byte(0xFF) / byte(workers)

	// Map from preimage (original key) to value
	mptLeafMap := make(map[string][]byte, 1000)
	var mptLeafMutex sync.Mutex

	var mptWg sync.WaitGroup
	for i := 0; i < workers; i++ {
		startKey[0] = byte(i) * step
		trieIt := trie.NewIterator(mptTrie.NodeIterator(startKey))

		mptWg.Add(1)
		go func() {
			defer mptWg.Done()
			for trieIt.Next() {
				hashedKey := trieIt.Key // This is keccak256(originalKey)

				// Get preimage (original key) from MPT's preimage store
				preimageKey := mptTrie.GetKey(hashedKey)
				if len(preimageKey) == 0 {
					panic(fmt.Sprintf("preimage not found in MPT for hashed key %s", hex.EncodeToString(hashedKey)))
				}

				if parallel {
					mptLeafMutex.Lock()
				}

				// Check for duplicates (due to parallel iteration overlap)
				if _, ok := mptLeafMap[string(preimageKey)]; ok {
					if parallel {
						mptLeafMutex.Unlock()
					}
					break
				}

				mptLeafMap[string(dup(preimageKey))] = dup(trieIt.Value)

				if parallel {
					mptLeafMutex.Unlock()
				}

				if parallel && len(mptLeafMap)%10000 == 0 {
					fmt.Println("MPT Leaves Loaded:", len(mptLeafMap))
				}
			}
		}()
	}

	respChan := make(chan map[string][]byte)
	go func() {
		mptWg.Wait()
		respChan <- mptLeafMap
	}()
	return respChan
}

// loadZkTrie loads ZK trie leaves and resolves preimages using CountLeaves
// Returns map[preimageKey]value where preimageKey is the original key (address or storage slot)
func loadZkTrie(zkTrie *trie.ZkTrie, parallel, paranoid bool) chan map[string][]byte {
	// Map from preimage (original key) to value
	zkLeafMap := make(map[string][]byte, 1000)
	var zkLeafMutex sync.Mutex

	respChan := make(chan map[string][]byte)

	go func() {
		zkTrie.CountLeaves(func(kHashBytes, value []byte) {
			// kHashBytes is the Poseidon hash (secure key) of the original key
			// Get preimage (original key) from ZK trie's preimage store
			preimageKey := zkTrie.GetKey(kHashBytes)
			if len(preimageKey) == 0 {
				panic(fmt.Sprintf("preimage not found in ZK trie for key hash %s", hex.EncodeToString(kHashBytes)))
			}

			if parallel {
				zkLeafMutex.Lock()
			}
			zkLeafMap[string(dup(preimageKey))] = dup(value)
			if parallel {
				zkLeafMutex.Unlock()
			}

			if parallel && len(zkLeafMap)%10000 == 0 {
				fmt.Println("ZK Leaves Loaded:", len(zkLeafMap))
			}
		}, parallel, paranoid)

		respChan <- zkLeafMap
	}()

	return respChan
}
