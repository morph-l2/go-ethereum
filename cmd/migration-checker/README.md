# Migration Checker

A tool to verify the correctness of ZK to MPT trie state migration.

## Overview

This tool compares the state data between a ZK trie node and an MPT trie node to ensure migration was successful. It verifies that all accounts and their storage data are identical.

## Recommended Node Configuration

Both ZK and MPT nodes should run with **archive mode**:

```bash
geth --gcmode=archive --cache.preimages ...
```

## Prerequisites

### 1. Import Genesis Preimages

Genesis preimages are NOT automatically stored during `geth init`. Import them manually:

```bash
gen-preimages -genesis=genesis.json -db=/path/to/zk-geth/geth/chaindata
```

### 2. Stop Nodes

LevelDB requires exclusive access:

```bash
./your-script.sh stop
```

### 3. Get State Roots

```bash
curl -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"morph_diskRoot","params":["0x64"],"id":1}' \
  http://localhost:8545
```

## Usage

```bash
migration-checker \
  --zk-db=/path/to/zk-geth/geth/chaindata \
  --mpt-db=/path/to/mpt-geth/geth/chaindata \
  --zk-root=0x... \
  --mpt-root=0x...
```

### Options

| Option | Description |
|--------|-------------|
| `--zk-db` | Path to ZK node chaindata (required) |
| `--mpt-db` | Path to MPT node chaindata (required) |
| `--zk-root` | ZK trie state root hash (required) |
| `--mpt-root` | MPT trie state root hash (required) |
| `--paranoid` | Verify all node hashes during iteration |

## Expected Output

```
Accounts done: 1
Accounts done: 2
...
Accounts done: 365
```

No errors = verification passed.

## gen-preimages Tool

### Import Genesis Preimages

```bash
# Import to ZK node
gen-preimages -genesis=genesis.json -db=/path/to/chaindata

# Dry run (preview)
gen-preimages -genesis=genesis.json --dry-run
```

### Check Existing Preimages

```bash
gen-preimages --check -db=/path/to/chaindata
```

## Troubleshooting

### "preimage not found zk trie"

Genesis preimages missing:
```bash
gen-preimages -genesis=genesis.json -db=/path/to/zk-geth/geth/chaindata
```

### "resource temporarily unavailable"

Database locked. Stop the geth node first.

### "MPT and ZK trie leaf count mismatch"

State mismatch between tries - migration issue.

## Technical Notes

### Why Genesis Preimages Need Manual Import

During `geth init`, genesis state is created using direct trie operations that bypass preimage recording. The `gen-preimages` tool fills this gap.

### Preimage Format

- Key: `secure-key-` + Poseidon_hash (32 bytes)
- Value: original key (20 bytes for address, 32 bytes for storage slot)
