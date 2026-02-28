// gen_genesis generates a genesis.json file with:
//   - A Morph-compatible L2 chain config (MPT mode, standalone-friendly)
//   - A pre-deployed ERC20 token contract at a fixed address
//   - N pre-funded benchmark accounts with ETH and ERC20 token balances
//   - Output: genesis.json + accounts.json (private keys for the benchmark tool)
//
// Usage: go run ./gen_genesis [flags]
//
//	-accounts int     Number of benchmark accounts (default 100)
//	-output   string  Output directory (default ".")
//	-bytecode string  Path to ERC20 runtime bytecode hex file (default "benchmark/contracts/build/BenchmarkToken_runtime.bin")
package main

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/crypto"
	"github.com/morph-l2/go-ethereum/params"
)

// ERC20 contract pre-deployed address
var TokenAddress = common.HexToAddress("0xBEEF000000000000000000000000000000000001")

// FeeVault address for the benchmark chain
var BenchFeeVaultAddress = common.HexToAddress("0xDEAD000000000000000000000000000000000FEE")

// Account info for JSON output
type AccountInfo struct {
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
}

func main() {
	numAccounts := flag.Int("accounts", 100, "Number of benchmark accounts to generate")
	outputDir := flag.String("output", ".", "Output directory for genesis.json and accounts.json")
	bytecodePath := flag.String("bytecode", "benchmark/contracts/build/BenchmarkToken_runtime.bin",
		"Path to ERC20 runtime bytecode hex file (compiled from contracts/BenchmarkToken.sol)")
	flag.Parse()

	fmt.Printf("Generating genesis with %d benchmark accounts...\n", *numAccounts)

	// Read runtime bytecode from file (eliminates hardcoding; just recompile the contract to update)
	tokenCode, err := readRuntimeBytecode(*bytecodePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read runtime bytecode from %s: %v\n", *bytecodePath, err)
		os.Exit(1)
	}
	fmt.Printf("Loaded runtime bytecode: %d bytes from %s\n", len(tokenCode), *bytecodePath)

	// Generate benchmark accounts
	accounts := make([]AccountInfo, *numAccounts)
	keys := make([]*ecdsa.PrivateKey, *numAccounts)
	for i := 0; i < *numAccounts; i++ {
		key, err := crypto.GenerateKey()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to generate key: %v\n", err)
			os.Exit(1)
		}
		keys[i] = key
		addr := crypto.PubkeyToAddress(key.PublicKey)
		accounts[i] = AccountInfo{
			Address:    addr.Hex(),
			PrivateKey: hex.EncodeToString(crypto.FromECDSA(key)),
		}
	}

	// Build genesis alloc using core.GenesisAlloc and core.GenesisAccount
	alloc := make(core.GenesisAlloc)

	// 1. Add precompiles (required for EVM)
	for i := 1; i <= 9; i++ {
		addr := common.BytesToAddress([]byte{byte(i)})
		alloc[addr] = core.GenesisAccount{
			Balance: big.NewInt(1),
		}
	}

	// 2. Pre-deploy ERC20 token contract with storage
	tokenStorage := make(map[common.Hash]common.Hash)

	// Each account gets 1 billion tokens (10^27 with 18 decimals)
	tokenBalance := new(big.Int).Mul(big.NewInt(1e9), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))

	// Total supply = numAccounts * tokenBalance
	totalSupply := new(big.Int).Mul(big.NewInt(int64(*numAccounts)), tokenBalance)

	// Set totalSupply at slot 2
	totalSupplySlot := common.BigToHash(big.NewInt(2))
	tokenStorage[totalSupplySlot] = common.BigToHash(totalSupply)

	// Set balanceOf for each account (mapping at slot 0)
	for i := 0; i < *numAccounts; i++ {
		addr := crypto.PubkeyToAddress(keys[i].PublicKey)
		storageKey := balanceOfStorageKey(addr)
		tokenStorage[storageKey] = common.BigToHash(tokenBalance)
	}

	alloc[TokenAddress] = core.GenesisAccount{
		Balance: big.NewInt(0),
		Code:    tokenCode,
		Storage: tokenStorage,
		Nonce:   1,
	}

	// 3. Pre-fund each benchmark account with 10000 ETH
	ethBalance := new(big.Int).Mul(big.NewInt(10000), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	for i := 0; i < *numAccounts; i++ {
		addr := crypto.PubkeyToAddress(keys[i].PublicKey)
		alloc[addr] = core.GenesisAccount{
			Balance: ethBalance,
		}
	}

	// 4. Fee vault address (required by Morph config)
	alloc[BenchFeeVaultAddress] = core.GenesisAccount{
		Balance: big.NewInt(0),
	}

	// Build chain config using params.ChainConfig and params.MorphConfig directly
	maxTxPerBlock := 10000
	maxTxPayloadBytesPerBlock := 2097152 // 2MB
	benchGasLimit := uint64(250_000_000) // 400M gas

	chainConfig := &params.ChainConfig{
		ChainID:                 big.NewInt(2818),
		HomesteadBlock:          big.NewInt(0),
		DAOForkBlock:            nil,
		DAOForkSupport:          false,
		EIP150Block:             big.NewInt(0),
		EIP155Block:             big.NewInt(0),
		EIP158Block:             big.NewInt(0),
		ByzantiumBlock:          big.NewInt(0),
		ConstantinopleBlock:     big.NewInt(0),
		PetersburgBlock:         big.NewInt(0),
		IstanbulBlock:           big.NewInt(0),
		BerlinBlock:             big.NewInt(0),
		LondonBlock:             big.NewInt(0),
		ArchimedesBlock:         big.NewInt(0),
		ShanghaiBlock:           big.NewInt(0),
		BernoulliBlock:          big.NewInt(0),
		CurieBlock:              big.NewInt(0),
		Morph203Time:            params.NewUint64(0),
		ViridianTime:            params.NewUint64(0),
		EmeraldTime:             params.NewUint64(0),
		JadeForkTime:            params.NewUint64(0),
		TerminalTotalDifficulty: big.NewInt(0),
		Morph: params.MorphConfig{
			UseZktrie:                 false,
			MaxTxPerBlock:             &maxTxPerBlock,
			MaxTxPayloadBytesPerBlock: &maxTxPayloadBytesPerBlock,
			FeeVaultAddress:           &BenchFeeVaultAddress,
		},
	}

	// Build genesis using core.Genesis directly
	genesis := &core.Genesis{
		Config:     chainConfig,
		Timestamp:  0,
		ExtraData:  []byte{},
		GasLimit:   benchGasLimit,
		Difficulty: big.NewInt(0),
		Alloc:      alloc,
	}

	// Write genesis.json (core.Genesis has gencodec MarshalJSON that handles hex encoding)
	genesisPath := *outputDir + "/genesis.json"
	genesisData, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal genesis: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(genesisPath, genesisData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write genesis.json: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Written %s (%d bytes)\n", genesisPath, len(genesisData))

	// Write accounts.json
	accountsPath := *outputDir + "/accounts.json"
	accountsData, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal accounts: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(accountsPath, accountsData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write accounts.json: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Written %s (%d accounts)\n", accountsPath, len(accounts))

	// Print summary
	fmt.Println("\n=== Genesis Summary ===")
	fmt.Printf("Chain ID:        %s\n", chainConfig.ChainID.String())
	fmt.Printf("Gas Limit:       %d\n", benchGasLimit)
	fmt.Printf("Mode:            MPT (non-zktrie)\n")
	fmt.Printf("Token Contract:  %s\n", TokenAddress.Hex())
	fmt.Printf("Accounts:        %d\n", *numAccounts)
	fmt.Printf("ETH per account: 10,000 ETH\n")
	fmt.Printf("ERC20 per acct:  1,000,000,000 BENCH\n")
}

// readRuntimeBytecode reads the runtime bytecode hex from a file and returns the decoded bytes.
func readRuntimeBytecode(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	hexStr := strings.TrimSpace(string(data))
	hexStr = strings.TrimPrefix(hexStr, "0x")
	code, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("decode hex: %w", err)
	}
	if len(code) == 0 {
		return nil, fmt.Errorf("bytecode file is empty")
	}
	return code, nil
}

// balanceOfStorageKey computes the storage key for balanceOf[addr] in the ERC20 contract.
// balanceOf mapping is at slot 0.
// Storage key = keccak256(abi.encode(address, uint256(0)))
func balanceOfStorageKey(addr common.Address) common.Hash {
	// abi.encode(address, uint256(0)) = left-padded address (32 bytes) ++ slot (32 bytes)
	slot := common.BigToHash(big.NewInt(0)) // slot 0
	key := common.LeftPadBytes(addr.Bytes(), 32)
	data := append(key, slot.Bytes()...)
	return crypto.Keccak256Hash(data)
}
