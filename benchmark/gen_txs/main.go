// gen_txs generates a batch of signed ERC20 transfer transactions and writes
// them to a transactions.rlp file in the geth datadir. When geth starts, its
// txpool will automatically load these transactions, bypassing the RPC layer
// entirely. This enables accurate measurement of pure EVM execution throughput.
//
// Usage: go run ./gen_txs [flags]
//
//	-accounts string   Path to accounts.json (default "accounts.json")
//	-datadir  string   Geth datadir (transactions.rlp is written here)
//	-senders  int      Number of sender accounts to use (default 10)
//	-count    int      Total number of transactions to generate (default 50000)
//	-token    string   ERC20 token contract address
//	-chainid  int      Chain ID (default 2818)
package main

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/rlp"
)

// ERC20 transfer(address,uint256) function selector
var transferSelector = common.Hex2Bytes("a9059cbb")

type AccountInfo struct {
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
}

func main() {
	accountsFile := flag.String("accounts", "accounts.json", "Path to accounts.json")
	datadir := flag.String("datadir", "data", "Geth datadir (transactions.rlp is written here)")
	numSenders := flag.Int("senders", 10, "Number of sender accounts")
	totalTxs := flag.Int("count", 50000, "Total number of transactions to generate")
	tokenAddr := flag.String("token", "0xBEEF000000000000000000000000000000000001", "ERC20 token contract address")
	chainID := flag.Int64("chainid", 2818, "Chain ID")
	flag.Parse()

	// Load accounts
	data, err := os.ReadFile(*accountsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read accounts file: %v\n", err)
		os.Exit(1)
	}
	var allAccounts []AccountInfo
	if err := json.Unmarshal(data, &allAccounts); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse accounts: %v\n", err)
		os.Exit(1)
	}

	if *numSenders+1 > len(allAccounts) {
		fmt.Fprintf(os.Stderr, "Need at least %d accounts (senders + recipients), have %d\n", *numSenders+1, len(allAccounts))
		os.Exit(1)
	}

	// Parse sender keys
	senderKeys := make([]*ecdsa.PrivateKey, *numSenders)
	senderAddrs := make([]common.Address, *numSenders)
	for i := 0; i < *numSenders; i++ {
		keyBytes, err := hex.DecodeString(allAccounts[i].PrivateKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to decode key %d: %v\n", i, err)
			os.Exit(1)
		}
		key, err := crypto.ToECDSA(keyBytes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse key %d: %v\n", i, err)
			os.Exit(1)
		}
		senderKeys[i] = key
		senderAddrs[i] = crypto.PubkeyToAddress(key.PublicKey)
	}

	// Recipient addresses (accounts beyond senders)
	recipientAddrs := make([]common.Address, 0)
	for i := *numSenders; i < len(allAccounts); i++ {
		recipientAddrs = append(recipientAddrs, common.HexToAddress(allAccounts[i].Address))
	}

	token := common.HexToAddress(*tokenAddr)
	signer := types.NewEIP155Signer(big.NewInt(*chainID))
	amount := new(big.Int).Mul(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)) // 1 token

	// Gas price: use a small value that covers baseFee.
	// The genesis baseFee starts at 1,000,000 wei for EIP-1559 chains.
	gasPrice := big.NewInt(1_000_000)

	// Distribute txs evenly across senders
	txsPerSender := *totalTxs / *numSenders
	remainder := *totalTxs % *numSenders

	fmt.Printf("=== Generating Transactions ===\n")
	fmt.Printf("Total txs:       %d\n", *totalTxs)
	fmt.Printf("Senders:         %d\n", *numSenders)
	fmt.Printf("Txs per sender:  %d\n", txsPerSender)
	fmt.Printf("Token:           %s\n", token.Hex())
	fmt.Printf("Chain ID:        %d\n", *chainID)
	fmt.Printf("Gas Price:       %s wei\n", gasPrice.String())
	fmt.Println()

	// The txpool journal file lives at {datadir}/geth/transactions.rlp
	// (ResolvePath resolves to {datadir}/{instanceName}/transactions.rlp, instanceName="geth")
	journalPath := filepath.Join(*datadir, "geth", "transactions.rlp")
	outFile, err := os.OpenFile(journalPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create %s: %v\n", journalPath, err)
		os.Exit(1)
	}
	defer outFile.Close()

	start := time.Now()
	totalWritten := 0

	for i := 0; i < *numSenders; i++ {
		count := txsPerSender
		if i < remainder {
			count++
		}

		key := senderKeys[i]
		recipIdx := 0

		for n := 0; n < count; n++ {
			nonce := uint64(n)
			to := recipientAddrs[recipIdx%len(recipientAddrs)]
			recipIdx++

			calldata := buildTransferCalldata(to, amount)

			tx := types.NewTransaction(
				nonce,
				token,
				big.NewInt(0), // no ETH value
				100000,        // gas limit
				gasPrice,
				calldata,
			)

			signedTx, err := types.SignTx(tx, signer, key)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to sign tx (sender=%d, nonce=%d): %v\n", i, nonce, err)
				os.Exit(1)
			}

			if err := rlp.Encode(outFile, signedTx); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to write tx: %v\n", err)
				os.Exit(1)
			}
			totalWritten++
		}

		fmt.Printf("  Sender %d (%s): %d txs (nonce 0..%d)\n", i, senderAddrs[i].Hex()[:10]+"...", count, count-1)
	}

	elapsed := time.Since(start)

	// Get file size
	fi, _ := outFile.Stat()
	fileSizeKB := float64(fi.Size()) / 1024.0

	fmt.Printf("\n=== Done ===\n")
	fmt.Printf("Written:    %d transactions\n", totalWritten)
	fmt.Printf("File:       %s (%.1f KB)\n", journalPath, fileSizeKB)
	fmt.Printf("Time:       %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("Speed:      %.0f tx/s\n", float64(totalWritten)/elapsed.Seconds())
}

// buildTransferCalldata constructs the calldata for ERC20 transfer(address,uint256)
func buildTransferCalldata(to common.Address, amount *big.Int) []byte {
	data := make([]byte, 4+32+32)
	copy(data[0:4], transferSelector)
	copy(data[4+12:4+32], to.Bytes())
	amountBytes := amount.Bytes()
	copy(data[4+32+(32-len(amountBytes)):4+32+32], amountBytes)
	return data
}
