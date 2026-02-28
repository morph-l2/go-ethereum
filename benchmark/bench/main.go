// bench is a load testing tool that sends ERC20 transfer transactions to a geth node.
//
// It reads pre-funded accounts from accounts.json and sends ERC20 transfer()
// transactions at a configurable rate to stress-test block production performance.
//
// Architecture: tx creation/signing is decoupled from network I/O.
//   - Each sender account has a goroutine that creates & signs txs at the target rate
//   - A pool of HTTP worker goroutines sends them concurrently to avoid RPC blocking
//
// Usage: go run ./bench [flags]
//
//	-rpc string           RPC endpoint (default "http://localhost:8545")
//	-accounts string      Path to accounts.json (default "accounts.json")
//	-senders int          Number of concurrent sender accounts (default 10)
//	-workers int          Number of concurrent RPC sender workers (default 50)
//	-tps int              Target transactions per second (default 100)
//	-duration duration    Test duration (default 60s)
//	-token string         ERC20 token contract address
//	-amount uint          Transfer amount per tx in token units (default 1)
package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/ethclient"
	"github.com/morph-l2/go-ethereum/log"
)

const maxSendRetries = 20

// ERC20 transfer function selector: transfer(address,uint256)
// keccak256("transfer(address,uint256)") = 0xa9059cbb...
var transferSelector = common.Hex2Bytes("a9059cbb")

// Default token address (must match genesis)
var defaultTokenAddr = "0xBEEF000000000000000000000000000000000001"

type AccountInfo struct {
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
}

type BenchStats struct {
	TotalSent    atomic.Int64
	TotalSuccess atomic.Int64
	TotalFailed  atomic.Int64
	StartTime    time.Time
}

// OnChainStats tracks real on-chain transaction throughput by polling blocks.
type OnChainStats struct {
	mu              sync.Mutex
	startBlockNum   uint64 // block number when benchmark started
	startBlockTime  uint64 // timestamp of the start block
	lastBlockNum    uint64 // last block we've scanned
	lastBlockTime   uint64 // timestamp of the last scanned block
	totalOnChainTxs int64  // total transactions observed on chain
}

func main() {
	// Configure logging
	log.Root().SetHandler(log.LvlFilterHandler(log.LvlInfo, log.StreamHandler(os.Stderr, log.TerminalFormat(true))))

	rpcURL := flag.String("rpc", "http://localhost:8545", "Geth RPC endpoint")
	accountsFile := flag.String("accounts", "accounts.json", "Path to accounts.json")
	numSenders := flag.Int("senders", 10, "Number of sender accounts (each has its own nonce sequence)")
	numWorkers := flag.Int("workers", 50, "Number of concurrent RPC sender workers (parallel HTTP connections)")
	targetTPS := flag.Int("tps", 100, "Target transactions per second (total across all senders)")
	duration := flag.Duration("duration", 60*time.Second, "Test duration")
	tokenAddr := flag.String("token", defaultTokenAddr, "ERC20 token contract address")
	transferAmt := flag.Int64("amount", 1, "Transfer amount per tx (in token base units)")
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

	if *numSenders > len(allAccounts) {
		fmt.Fprintf(os.Stderr, "Not enough accounts: have %d, need %d senders\n", len(allAccounts), *numSenders)
		os.Exit(1)
	}

	// Need at least numSenders + 1 accounts (senders + recipients)
	if len(allAccounts) < *numSenders+1 {
		fmt.Fprintf(os.Stderr, "Need at least %d accounts (senders + 1 recipient), have %d\n", *numSenders+1, len(allAccounts))
		os.Exit(1)
	}

	// Connect to geth
	client, err := ethclient.Dial(*rpcURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to geth: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	// Get chain ID
	chainID, err := client.ChainID(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get chain ID: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Connected to chain ID: %s\n", chainID.String())

	// Query the latest block to get the current baseFee.
	// Transactions must have gasPrice >= baseFee to be valid under EIP-1559.
	latestBlock, err := client.HeaderByNumber(context.Background(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get latest block header: %v\n", err)
		os.Exit(1)
	}
	gasPrice := new(big.Int)
	if latestBlock.BaseFee != nil && latestBlock.BaseFee.Sign() > 0 {
		gasPrice.Set(latestBlock.BaseFee)
		fmt.Printf("BaseFee detected: %s wei, using as gasPrice\n", gasPrice.String())
	} else {
		fmt.Println("No baseFee (pre-EIP-1559 chain), using gasPrice=0")
	}

	token := common.HexToAddress(*tokenAddr)
	amount := new(big.Int).Mul(big.NewInt(*transferAmt), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))

	// Parse sender private keys
	senderKeys := make([]*ecdsa.PrivateKey, *numSenders)
	senderAddrs := make([]common.Address, *numSenders)
	for i := 0; i < *numSenders; i++ {
		keyBytes, err := hex.DecodeString(allAccounts[i].PrivateKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to decode private key for account %d: %v\n", i, err)
			os.Exit(1)
		}
		key, err := crypto.ToECDSA(keyBytes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse private key for account %d: %v\n", i, err)
			os.Exit(1)
		}
		senderKeys[i] = key
		senderAddrs[i] = crypto.PubkeyToAddress(key.PublicKey)
	}

	// Use accounts beyond senders as recipients (round-robin)
	recipientAddrs := make([]common.Address, 0)
	for i := *numSenders; i < len(allAccounts); i++ {
		recipientAddrs = append(recipientAddrs, common.HexToAddress(allAccounts[i].Address))
	}
	if len(recipientAddrs) == 0 {
		// If only senders+1, the last account is the recipient
		recipientAddrs = append(recipientAddrs, common.HexToAddress(allAccounts[len(allAccounts)-1].Address))
	}

	// Get initial nonces for all senders
	nonces := make([]uint64, *numSenders)
	for i := 0; i < *numSenders; i++ {
		nonce, err := client.PendingNonceAt(context.Background(), senderAddrs[i])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get nonce for %s: %v\n", senderAddrs[i].Hex(), err)
			os.Exit(1)
		}
		nonces[i] = nonce
	}

	signer := types.NewEIP155Signer(chainID)

	// Print configuration
	fmt.Println("\n=== Benchmark Configuration ===")
	fmt.Printf("RPC:          %s\n", *rpcURL)
	fmt.Printf("Token:        %s\n", token.Hex())
	fmt.Printf("Senders:      %d accounts\n", *numSenders)
	fmt.Printf("Workers:      %d (concurrent RPC connections)\n", *numWorkers)
	fmt.Printf("Recipients:   %d\n", len(recipientAddrs))
	fmt.Printf("Target TPS:   %d\n", *targetTPS)
	fmt.Printf("Duration:     %s\n", *duration)
	fmt.Printf("Transfer Amt: %s (base units)\n", amount.String())
	fmt.Printf("Gas Price:    %s wei\n", gasPrice.String())
	fmt.Println()

	stats := &BenchStats{
		StartTime: time.Now(),
	}

	// Record starting block number for on-chain TPS tracking
	startHeader, err := client.HeaderByNumber(context.Background(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get start block: %v\n", err)
		os.Exit(1)
	}
	onChain := &OnChainStats{
		startBlockNum:  startHeader.Number.Uint64(),
		startBlockTime: startHeader.Time,
		lastBlockNum:   startHeader.Number.Uint64(),
		lastBlockTime:  startHeader.Time,
	}
	fmt.Printf("Start block:  #%d (timestamp=%d)\n", onChain.startBlockNum, onChain.startBlockTime)

	// Start the benchmark
	fmt.Println("\n=== Starting Benchmark ===")

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	var wg sync.WaitGroup

	// === Signed tx channel: producers (sender accounts) → consumers (RPC workers) ===
	// Buffer size = 2x target TPS to absorb short bursts.
	// Producers BLOCK when channel is full (backpressure) — never drop txs to avoid nonce gaps.
	txCh := make(chan *types.Transaction, *targetTPS*2)

	// === RPC sender workers ===
	// Each worker gets its own ethclient (= its own HTTP connection) so they don't
	// serialize on Go's default MaxIdleConnsPerHost=2 limit.
	var workerWg sync.WaitGroup
	workerClients := make([]*ethclient.Client, *numWorkers)
	for w := 0; w < *numWorkers; w++ {
		wc, err := ethclient.Dial(*rpcURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create worker client %d: %v\n", w, err)
			os.Exit(1)
		}
		workerClients[w] = wc
	}
	defer func() {
		for _, wc := range workerClients {
			wc.Close()
		}
	}()

	for w := 0; w < *numWorkers; w++ {
		workerWg.Add(1)
		go func(wc *ethclient.Client) {
			defer workerWg.Done()
			for signedTx := range txCh {
				sendWithRetry(wc, signedTx, stats)
			}
		}(workerClients[w])
	}

	// === On-chain TPS monitor ===
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				scanOnChainBlocks(client, onChain)
			case <-ctx.Done():
				scanOnChainBlocks(client, onChain)
				return
			}
		}
	}()

	// === Progress reporter ===
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				elapsed := time.Since(stats.StartTime).Seconds()
				sent := stats.TotalSent.Load()
				success := stats.TotalSuccess.Load()
				failed := stats.TotalFailed.Load()
				sendTPS := float64(sent) / elapsed

				onChain.mu.Lock()
				chainTxs := onChain.totalOnChainTxs
				chainBlocks := onChain.lastBlockNum - onChain.startBlockNum
				var chainTPS float64
				if onChain.lastBlockTime > onChain.startBlockTime {
					chainTPS = float64(chainTxs) / float64(onChain.lastBlockTime-onChain.startBlockTime)
				}
				onChain.mu.Unlock()

				fmt.Printf("[%6.1fs] sent=%d success=%d failed=%d send_tps=%.1f | on-chain: blocks=%d txs=%d chain_tps=%.1f\n",
					elapsed, sent, success, failed, sendTPS, chainBlocks, chainTxs, chainTPS)
			case <-ctx.Done():
				return
			}
		}
	}()

	// === Tx producer goroutines (one per sender account) ===
	// Each producer creates & signs txs at its share of the target rate,
	// then pushes the signed tx into the channel for workers to send.
	// Tx creation + signing is CPU-only (fast), so it doesn't block on I/O.
	for i := 0; i < *numSenders; i++ {
		wg.Add(1)
		go func(senderIdx int) {
			defer wg.Done()
			key := senderKeys[senderIdx]
			nonce := nonces[senderIdx]
			recipIdx := 0

			// Each sender produces at (targetTPS / numSenders) rate
			perSenderInterval := time.Duration(*numSenders) * time.Second / time.Duration(*targetTPS)
			ticker := time.NewTicker(perSenderInterval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					to := recipientAddrs[recipIdx%len(recipientAddrs)]
					recipIdx++

					calldata := buildTransferCalldata(to, amount)

					tx := types.NewTransaction(
						nonce,
						token,
						big.NewInt(0),
						100000,
						gasPrice,
						calldata,
					)

					signedTx, err := types.SignTx(tx, signer, key)
					if err != nil {
						log.Error("Failed to sign tx", "sender", senderIdx, "err", err)
						stats.TotalFailed.Add(1)
						continue
					}
					nonce++

					// Blocking push: if channel is full, wait (backpressure).
					// This naturally rate-limits producers to what workers can handle,
					// and never drops txs — avoiding nonce gaps.
					select {
					case txCh <- signedTx:
					case <-ctx.Done():
						return
					}
				}
			}
		}(i)
	}

	// Wait for context to expire (all producers stop)
	wg.Wait()

	// Close channel to signal workers to drain and exit
	close(txCh)
	workerWg.Wait()

	// Final on-chain scan (wait a few seconds for the last blocks to be produced)
	fmt.Println("\nWaiting 3s for remaining blocks to be produced...")
	time.Sleep(3 * time.Second)
	scanOnChainBlocks(client, onChain)

	// Print final results
	elapsed := time.Since(stats.StartTime)
	sent := stats.TotalSent.Load()
	success := stats.TotalSuccess.Load()
	failed := stats.TotalFailed.Load()
	sendTPS := float64(sent) / elapsed.Seconds()

	onChain.mu.Lock()
	chainTxs := onChain.totalOnChainTxs
	chainBlocks := onChain.lastBlockNum - onChain.startBlockNum
	var chainTPS float64
	if onChain.lastBlockTime > onChain.startBlockTime {
		chainTPS = float64(chainTxs) / float64(onChain.lastBlockTime-onChain.startBlockTime)
	}
	endBlockNum := onChain.lastBlockNum
	onChain.mu.Unlock()

	fmt.Println("\n=== Benchmark Results ===")
	fmt.Printf("Duration:       %s\n", elapsed.Round(time.Millisecond))
	fmt.Println()
	fmt.Println("--- Send Stats ---")
	fmt.Printf("Total Sent:     %d\n", sent)
	fmt.Printf("Success:        %d\n", success)
	fmt.Printf("Failed:         %d\n", failed)
	fmt.Printf("Send TPS:       %.2f\n", sendTPS)
	fmt.Printf("Success Rate:   %.2f%%\n", float64(success)/float64(sent)*100)
	fmt.Println()
	fmt.Println("--- On-Chain Stats ---")
	fmt.Printf("Block Range:    #%d → #%d (%d blocks)\n", onChain.startBlockNum, endBlockNum, chainBlocks)
	fmt.Printf("On-Chain Txs:   %d\n", chainTxs)
	fmt.Printf("On-Chain TPS:   %.2f\n", chainTPS)
	if chainBlocks > 0 {
		fmt.Printf("Avg Txs/Block:  %.1f\n", float64(chainTxs)/float64(chainBlocks))
	}
}

// scanOnChainBlocks queries all new blocks since the last scan and counts transactions.
func scanOnChainBlocks(client *ethclient.Client, stats *OnChainStats) {
	latest, err := client.HeaderByNumber(context.Background(), nil)
	if err != nil {
		return
	}
	latestNum := latest.Number.Uint64()

	stats.mu.Lock()
	fromBlock := stats.lastBlockNum + 1
	stats.mu.Unlock()

	if latestNum < fromBlock {
		return
	}

	var newTxs int64
	var lastTime uint64
	for num := fromBlock; num <= latestNum; num++ {
		block, err := client.BlockByNumber(context.Background(), new(big.Int).SetUint64(num))
		if err != nil {
			log.Debug("Failed to fetch block", "number", num, "err", err)
			break
		}
		txCount := len(block.Transactions())
		newTxs += int64(txCount)
		lastTime = block.Time()

		if txCount > 0 {
			log.Debug("Block with transactions", "number", num, "txs", txCount, "gasUsed", block.GasUsed())
		}
	}

	stats.mu.Lock()
	stats.totalOnChainTxs += newTxs
	if latestNum > stats.lastBlockNum {
		stats.lastBlockNum = latestNum
	}
	if lastTime > 0 {
		stats.lastBlockTime = lastTime
	}
	stats.mu.Unlock()
}

// sendWithRetry sends a signed transaction, retrying on transient errors.
// This prevents nonce gaps: if a send fails, we retry the SAME tx (same nonce)
// until it's accepted. The txpool handles out-of-order nonce arrivals by
// queueing future-nonce txs and promoting them when the gap is filled.
func sendWithRetry(wc *ethclient.Client, tx *types.Transaction, stats *BenchStats) {
	for attempt := 0; attempt <= maxSendRetries; attempt++ {
		err := wc.SendTransaction(context.Background(), tx)
		if err == nil {
			stats.TotalSent.Add(1)
			stats.TotalSuccess.Add(1)
			return
		}

		errMsg := err.Error()

		// Already accepted — count as success, don't retry
		if strings.Contains(errMsg, "already known") ||
			strings.Contains(errMsg, "nonce too low") {
			stats.TotalSent.Add(1)
			stats.TotalSuccess.Add(1)
			return
		}

		// Permanent errors — don't retry
		if strings.Contains(errMsg, "exceeds block gas limit") ||
			strings.Contains(errMsg, "intrinsic gas too low") ||
			strings.Contains(errMsg, "insufficient funds") {
			stats.TotalSent.Add(1)
			stats.TotalFailed.Add(1)
			log.Error("Permanent tx send error", "hash", tx.Hash().Hex(), "err", err)
			return
		}

		// Transient error — retry with backoff
		if attempt < maxSendRetries {
			backoff := time.Duration(attempt+1) * 5 * time.Millisecond
			if backoff > 200*time.Millisecond {
				backoff = 200 * time.Millisecond
			}
			time.Sleep(backoff)
		}
	}

	// Exhausted all retries
	stats.TotalSent.Add(1)
	stats.TotalFailed.Add(1)
	log.Error("Tx send failed after max retries", "hash", tx.Hash().Hex(), "retries", maxSendRetries)
}

// buildTransferCalldata constructs the calldata for ERC20 transfer(address,uint256)
func buildTransferCalldata(to common.Address, amount *big.Int) []byte {
	// transfer(address,uint256) selector + abi-encoded params
	data := make([]byte, 4+32+32)
	copy(data[0:4], transferSelector)
	// address parameter (left-padded to 32 bytes)
	copy(data[4+12:4+32], to.Bytes())
	// uint256 parameter (left-padded to 32 bytes)
	amountBytes := amount.Bytes()
	copy(data[4+32+(32-len(amountBytes)):4+32+32], amountBytes)
	return data
}
