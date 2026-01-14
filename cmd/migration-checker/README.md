# Migration Checker

A tool to verify the correctness of ZK to MPT trie state migration.

## Overview

This tool compares the state data between a ZK trie node and an MPT trie node to ensure that the migration was successful. It verifies that all accounts and their storage data are identical between the two trie implementations.

## Recommended Node Configuration

Both ZK and MPT nodes should run with **archive mode** to ensure preimages are stored:

```bash
geth --gcmode=archive --cache.preimages ...
```

- `--gcmode=archive`: Disables state pruning, keeps all historical states
- `--cache.preimages`: Enables recording of trie key preimages (automatically enabled in archive mode)

## Prerequisites

### 1. Import Genesis Preimages

**Important**: Genesis preimages are NOT automatically stored during `geth init`. You must import them manually before running migration-checker.

```bash
# For ZK node (Poseidon hash format)
gen-preimages -genesis=genesis.json -db=/path/to/zk-geth/geth/chaindata --zk

# For MPT node (Keccak256 hash format)
gen-preimages -genesis=genesis.json -db=/path/to/mpt-geth/geth/chaindata
```

### 2. Stop Nodes

LevelDB requires exclusive access. Stop all geth processes before running migration-checker:

```bash
# Stop your geth nodes
./your-script.sh stop
```

### 3. Get State Roots

Use the `morph_diskRoot` RPC API to get state roots at the same block height:

```bash
# Get roots at block 100 (0x64)
curl -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"morph_diskRoot","params":["0x64"],"id":1}' \
  http://localhost:8545
```

Response:
```json
{
  "result": {
    "diskRoot": "0x...",    // Use this for verification
    "headerRoot": "0x..."
  }
}
```

## Usage

### Recommended: ZK as Preimage Source

Since migration direction is ZK → MPT, using ZK as preimage source ensures all ZK data is verified:

```bash
migration-checker \
  --zk-db=/path/to/zk-geth/geth/chaindata \
  --mpt-db=/path/to/mpt-geth/geth/chaindata \
  --zk-root=0x... \
  --mpt-root=0x... \
  --preimage-source=zk
```

### Alternative: MPT as Preimage Source

```bash
migration-checker \
  --zk-db=/path/to/zk-geth/geth/chaindata \
  --mpt-db=/path/to/mpt-geth/geth/chaindata \
  --zk-root=0x... \
  --mpt-root=0x... \
  --preimage-source=mpt
```

### Options

| Option | Description |
|--------|-------------|
| `--zk-db` | Path to ZK node's chaindata directory (required) |
| `--mpt-db` | Path to MPT node's chaindata directory (required) |
| `--zk-root` | ZK trie state root hash (required) |
| `--mpt-root` | MPT trie state root hash (required) |
| `--preimage-source` | Source of preimages: `zk` (recommended) or `mpt` (default: `mpt`) |
| `--paranoid` | Verify all node hashes during iteration |

## Expected Output

```
Using preimage source: zk
 [ZK source] Loaded 365 leaves from ZK (20-byte keys: 364, 32-byte keys: 1)
Accounts done: 1
Accounts done: 2
...
 Verified 365 leaves match
===========================================
  Migration verification PASSED!
  All accounts and storage data match.
===========================================
```

## gen-preimages Tool

### Import Genesis Preimages

```bash
# For ZK node (Poseidon hash)
gen-preimages -genesis=genesis.json -db=/path/to/chaindata --zk

# For MPT node (Keccak256 hash)
gen-preimages -genesis=genesis.json -db=/path/to/chaindata

# Dry run (preview without importing)
gen-preimages -genesis=genesis.json --dry-run
```

### Check Existing Preimages

```bash
# Check preimage statistics
gen-preimages --check -db=/path/to/chaindata

# Check and verify genesis preimages
gen-preimages --check -db=/path/to/chaindata -genesis=genesis.json
```

## Troubleshooting

### "preimage not found in ZK trie for key hash"

Genesis preimages missing from ZK node:
```bash
gen-preimages -genesis=genesis.json -db=/path/to/zk-geth/geth/chaindata --zk
```

### "preimage not found in MPT for hashed key"

Genesis preimages missing from MPT node:
```bash
gen-preimages -genesis=genesis.json -db=/path/to/mpt-geth/geth/chaindata
```

### "resource temporarily unavailable"

Database locked by running geth. Stop the node first.

### "key not found in zk/mpt trie"

State mismatch between tries. This indicates a migration issue that needs investigation.

## Technical Notes

### Preimage Storage Format

Both ZK and MPT store preimages in the same LevelDB format:
- Key: `secure-key-` + hash (32 bytes)
- Value: original key (20 bytes for address, 32 bytes for storage slot)

The difference is the hash function:
- **MPT**: Keccak256(original_key)
- **ZK**: Poseidon(padded_to_32_bytes(original_key))

### Zero Address Edge Case

The zero address (`0x0000...0000`, 20 bytes) and zero storage slot (32 bytes of zeros) have the same Poseidon hash because:
1. ZK trie pads keys to 32 bytes before hashing
2. 20-byte zero address padded = 32-byte zeros
3. Both produce identical Poseidon hash

The migration-checker handles this edge case automatically.

### Why Genesis Preimages Need Manual Import

During `geth init`, the genesis state is created using direct trie operations that bypass the preimage recording mechanism. This is a known limitation in go-ethereum. The `gen-preimages` tool fills this gap by computing and importing preimages from the genesis.json file.
