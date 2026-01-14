#!/bin/bash
# Test script for zkTrie to MPT migration using migration-checker tool
#
# This script:
# 1. Starts zkTrie node (if not already running)
# 2. Starts MPT node (if not already running)
# 3. Connects MPT node to ZK node for block synchronization
# 4. Sends transactions and creates blocks on ZK node
# 5. Waits for MPT node to sync the same blocks
# 6. Gets state roots from both nodes at the same block height
# 7. Runs migration-checker to verify state equality

set -e

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
GETH_BIN="${GETH_BIN:-$SCRIPT_DIR/../build/bin/geth}"
MIGRATION_CHECKER_BIN="${MIGRATION_CHECKER_BIN:-$SCRIPT_DIR/../build/bin/migration-checker}"

# Data directories
ZK_DATA_DIR="${ZK_DATA_DIR:-$SCRIPT_DIR/geth-data}"
MPT_DATA_DIR="${MPT_DATA_DIR:-$SCRIPT_DIR/mpt-data}"

# RPC endpoints
# ZK node: HTTP 8545, WS 8546, AuthRPC 8551
# MPT node: HTTP 8547, WS 8548, AuthRPC 8552
ZK_RPC="${ZK_RPC:-http://localhost:8545}"
ZK_AUTH_RPC="${ZK_AUTH_RPC:-http://localhost:8551}"
MPT_RPC="${MPT_RPC:-http://localhost:8547}"
MPT_AUTH_RPC="${MPT_AUTH_RPC:-http://localhost:8552}"

# Test configuration
NUM_TXS="${NUM_TXS:-5}"           # Number of transactions to send
NUM_BLOCKS="${NUM_BLOCKS:-3}"     # Number of blocks to create
JWT_SECRET_PATH="${JWT_SECRET_PATH:-$SCRIPT_DIR/jwt-secret.txt}"

# PIDs for cleanup
ZK_PID=""
MPT_PID=""

# Cleanup function
cleanup() {
    echo "Cleaning up..."
    if [ -n "$ZK_PID" ]; then
        kill $ZK_PID 2>/dev/null || true
        wait $ZK_PID 2>/dev/null || true
    fi
    if [ -n "$MPT_PID" ]; then
        kill $MPT_PID 2>/dev/null || true
        wait $MPT_PID 2>/dev/null || true
    fi
}
trap cleanup EXIT

# Function to stop nodes gracefully
stop_nodes() {
    echo "Stopping nodes to release database locks..."
    if [ -n "$ZK_PID" ]; then
        echo "Stopping ZK node (PID $ZK_PID)..."
        kill $ZK_PID 2>/dev/null || true
        wait $ZK_PID 2>/dev/null || true
        ZK_PID=""
    fi
    if [ -n "$MPT_PID" ]; then
        echo "Stopping MPT node (PID $MPT_PID)..."
        kill $MPT_PID 2>/dev/null || true
        wait $MPT_PID 2>/dev/null || true
        MPT_PID=""
    fi
    # Give databases time to release locks
    sleep 2
    echo "Nodes stopped"
}

# Check dependencies
require() {
    command -v "$1" >/dev/null 2>&1 || { echo "ERROR: $1 not found" >&2; exit 1; }
}
require curl
require jq
require openssl

# Check if binary exists
if [ ! -x "$GETH_BIN" ]; then
    echo "ERROR: geth binary not found at $GETH_BIN"
    echo "Build with: make geth"
    exit 1
fi

if [ ! -x "$MIGRATION_CHECKER_BIN" ]; then
    echo "WARNING: migration-checker binary not found at $MIGRATION_CHECKER_BIN"
    echo "Attempting to build it..."
    cd "$SCRIPT_DIR/.."
    go build -o "$MIGRATION_CHECKER_BIN" ./cmd/migration-checker
    if [ ! -x "$MIGRATION_CHECKER_BIN" ]; then
        echo "ERROR: Failed to build migration-checker"
        exit 1
    fi
    echo "Successfully built migration-checker"
    cd "$SCRIPT_DIR"
fi

# Function to check if RPC is available
check_rpc() {
    local rpc_url=$1
    curl -s -X POST -H "Content-Type: application/json" \
        --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
        "$rpc_url" > /dev/null 2>&1
}

# Function to get block number
get_block_number() {
    local rpc_url=$1
    curl -s -X POST -H "Content-Type: application/json" \
        --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
        "$rpc_url" | jq -r '.result' | xargs printf "%d"
}

# Function to get state root
get_state_root() {
    local rpc_url=$1
    local block_num=$2
    local block_param
    
    # Convert decimal block number to hex format if provided
    if [ -n "$block_num" ] && [ "$block_num" != "latest" ] && [ "$block_num" != "earliest" ] && [ "$block_num" != "pending" ]; then
        # Check if it's already hex format
        if [[ "$block_num" =~ ^0x ]]; then
            block_param="$block_num"
        else
            # Convert decimal to hex
            block_param=$(printf "0x%X" "$block_num")
        fi
    else
        block_param="${block_num:-latest}"
    fi
    
    curl -s -X POST -H "Content-Type: application/json" \
        --data "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"$block_param\",false],\"id\":1}" \
        "$rpc_url" | jq -r '.result.stateRoot'
}

# Function to get node enode
get_enode() {
    local rpc_url=$1
    curl -s -X POST -H "Content-Type: application/json" \
        --data '{"jsonrpc":"2.0","method":"admin_nodeInfo","params":[],"id":1}' \
        "$rpc_url" | jq -r '.result.enode'
}

# Function to add peer
add_peer() {
    local rpc_url=$1
    local enode=$2
    curl -s -X POST -H "Content-Type: application/json" \
        --data "{\"jsonrpc\":\"2.0\",\"method\":\"admin_addPeer\",\"params\":[\"$enode\"],\"id\":1}" \
        "$rpc_url" | jq -r '.result'
}

# Function to build JWT token
build_jwt_token() {
    local secret_path=$1
    if [ ! -f "$secret_path" ]; then
        echo "ERROR: JWT secret file not found at $secret_path" >&2
        exit 1
    fi
    local secret=$(tr -d '\n\r\t ' < "$secret_path")
    secret=${secret#0x}
    
    local h_b64=$(printf '{"alg":"HS256","typ":"JWT"}' | openssl base64 -A | tr '+/' '-_' | tr -d '=')
    local iat=$(date +%s)
    local p_b64=$(printf '{"iat":%s}' "$iat" | openssl base64 -A | tr '+/' '-_' | tr -d '=')
    local sig=$(printf '%s.%s' "$h_b64" "$p_b64" | \
        openssl dgst -binary -sha256 -mac HMAC -macopt hexkey:$secret | \
        openssl base64 -A | tr '+/' '-_' | tr -d '=')
    printf '%s.%s.%s' "$h_b64" "$p_b64" "$sig"
}

# Function to assemble and insert block via Engine API
make_block() {
    local rpc_url=$1
    local auth_rpc=$2
    local jwt_token=$3
    
    # Get next block number
    local cur_hex=$(curl -s "$rpc_url" -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' | jq -r '.result')
    if [ -z "$cur_hex" ] || [ "$cur_hex" = "null" ]; then
        echo "ERROR: failed to fetch current block number" >&2
        return 1
    fi
    local cur_nox=${cur_hex#0x}
    local next_dec=$((16#$cur_nox + 1))
    local next_hex=$(printf '0x%X' "$next_dec")
    
    # Assemble L2 block
    local ed_resp=$(curl -s "$auth_rpc" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $jwt_token" \
        -d "{\"jsonrpc\":\"2.0\",\"method\":\"engine_assembleL2Block\",\"params\":[{\"number\":\"$next_hex\",\"transactions\":[]}],\"id\":2}")
    
    # Debug: print full response
    echo "DEBUG: engine_assembleL2Block response: $ed_resp" >&2
    
    # Check for error in response
    local has_error=$(printf '%s' "$ed_resp" | jq -r 'has("error")')
    if [ "$has_error" = "true" ]; then
        local error_obj=$(printf '%s' "$ed_resp" | jq -c '.error')
        if [ "$error_obj" != "null" ] && [ -n "$error_obj" ]; then
            local error_code=$(printf '%s' "$ed_resp" | jq -r '.error.code // "unknown"')
            local error_msg=$(printf '%s' "$ed_resp" | jq -r '.error.message // "unknown error"')
            echo "ERROR: engine_assembleL2Block failed. Code: $error_code, Message: $error_msg" >&2
            echo "Full response: $ed_resp" >&2
            return 1
        fi
    fi
    
    local ed=$(printf '%s' "$ed_resp" | jq -c '.result')
    if [ -z "$ed" ] || [ "$ed" = "null" ]; then
        echo "ERROR: assemble failed - no result. Response: $ed_resp" >&2
        return 1
    fi
    
    # Insert new block
    local new_resp=$(curl -s "$auth_rpc" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $jwt_token" \
        -d "{\"jsonrpc\":\"2.0\",\"method\":\"engine_newL2Block\",\"params\":[$ed,null],\"id\":3}")
    
    # Debug: print full response
    echo "DEBUG: engine_newL2Block response: $new_resp" >&2
    
    # Check for error in response (error field exists and is not null)
    local has_error=$(printf '%s' "$new_resp" | jq -r 'has("error")')
    if [ "$has_error" = "true" ]; then
        local error_obj=$(printf '%s' "$new_resp" | jq -c '.error')
        if [ "$error_obj" != "null" ] && [ -n "$error_obj" ]; then
            local error_code=$(printf '%s' "$new_resp" | jq -r '.error.code // "unknown"')
            local error_msg=$(printf '%s' "$new_resp" | jq -r '.error.message // "unknown error"')
            echo "ERROR: engine_newL2Block failed. Code: $error_code, Message: $error_msg" >&2
            echo "Full response: $new_resp" >&2
            return 1
        fi
    fi
    
    # Verify block was actually created by checking block number
    sleep 1  # Give node time to process
    local new_block_hex=$(curl -s "$rpc_url" -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' | jq -r '.result')
    local new_block_dec=$((16#${new_block_hex#0x}))
    
    if [ "$new_block_dec" -lt "$next_dec" ]; then
        echo "ERROR: Block was not created. Expected block $next_dec, but current block is $new_block_dec" >&2
        echo "Full response: $new_resp" >&2
        return 1
    fi
    
    echo "Block $next_hex created successfully (verified: current block is $new_block_hex)"
    return 0
}

# Function to send transaction
send_transaction() {
    local rpc_url=$1
    local to_addr="${2:-0x000000000000000000000000000000000000dEaD}"
    local value="${3:-1}"
    
    # Use send-tx.sh if available, otherwise use go run
    if [ -x "$SCRIPT_DIR/send-tx.sh" ]; then
        "$SCRIPT_DIR/send-tx.sh" "$rpc_url" "$to_addr" "$value" > /dev/null 2>&1
    else
        cd "$SCRIPT_DIR/.."
        go run ./local-test/send-tx.go "$rpc_url" "$to_addr" "$value" > /dev/null 2>&1
        cd "$SCRIPT_DIR"
    fi
}

# Function to start zkTrie node
start_zk_node() {
    if check_rpc "$ZK_RPC"; then
        echo "ZK node already running at $ZK_RPC"
        return
    fi
    
    echo "Starting ZK node..."
    if [ ! -d "$ZK_DATA_DIR/geth/chaindata" ]; then
        echo "Initializing ZK node with genesis..."
        "$GETH_BIN" --datadir "$ZK_DATA_DIR" init "$SCRIPT_DIR/genesis.json"
    fi
    
    # Start ZK node in background (zkTrie mode, no --morph-mpt flag)
    "$GETH_BIN" \
        --datadir "$ZK_DATA_DIR" \
        --networkid 53077 \
        --verbosity 3 \
        --http \
        --http.addr 0.0.0.0 \
        --http.port 8545 \
        --http.corsdomain "*" \
        --http.vhosts "*" \
        --http.api web3,eth,txpool,net,morph,engine,admin,debug \
        --ws \
        --ws.addr 0.0.0.0 \
        --ws.port 8546 \
        --ws.origins "*" \
        --ws.api web3,eth,txpool,net,morph,engine,admin,debug \
        --authrpc.addr 0.0.0.0 \
        --authrpc.port 8551 \
        --authrpc.vhosts "*" \
        --authrpc.jwtsecret "$JWT_SECRET_PATH" \
        --gcmode archive \
        --rpc.allow-unprotected-txs \
        --port 30303 \
        --nodiscover \
        --maxpeers 10 \
        > "$SCRIPT_DIR/zk-node.log" 2>&1 &
    ZK_PID=$!
    echo "ZK node started with PID $ZK_PID"
    
    # Wait for RPC to be ready
    echo "Waiting for ZK node to start..."
    for i in {1..30}; do
        if check_rpc "$ZK_RPC"; then
            echo "ZK node is ready"
            return
        fi
        sleep 1
    done
    echo "ERROR: ZK node failed to start"
    exit 1
}

# Function to start MPT node
start_mpt_node() {
    if check_rpc "$MPT_RPC"; then
        echo "MPT node already running at $MPT_RPC"
        return
    fi
    
    echo "Starting MPT node..."
    if [ ! -d "$MPT_DATA_DIR/geth/chaindata" ]; then
        echo "Initializing MPT node with genesis..."
        "$GETH_BIN" --datadir "$MPT_DATA_DIR" init "$SCRIPT_DIR/genesis-mpt.json"
    fi
    
    # Start MPT node in background with different ports
    # Ports: HTTP 8547, WS 8548, AuthRPC 8552 (avoid conflict with ZK node)
    "$GETH_BIN" \
        --datadir "$MPT_DATA_DIR" \
        --networkid 53077 \
        --verbosity 3 \
        --cache.snapshot=0 \
        --http \
        --http.addr 0.0.0.0 \
        --http.port 8547 \
        --http.corsdomain "*" \
        --http.vhosts "*" \
        --http.api web3,eth,txpool,net,morph,engine,admin,debug \
        --ws \
        --ws.addr 0.0.0.0 \
        --ws.port 8548 \
        --ws.origins "*" \
        --ws.api web3,eth,txpool,net,morph,engine,admin,debug \
        --authrpc.addr 0.0.0.0 \
        --authrpc.port 8552 \
        --authrpc.vhosts "*" \
        --authrpc.jwtsecret "$JWT_SECRET_PATH" \
        --gcmode archive \
        --rpc.allow-unprotected-txs \
        --port 30304 \
        --nodiscover \
        --morph-mpt \
        --maxpeers 10 \
        > "$SCRIPT_DIR/mpt-node.log" 2>&1 &
    MPT_PID=$!
    echo "MPT node started with PID $MPT_PID"
    
    # Wait for RPC to be ready
    echo "Waiting for MPT node to start..."
    for i in {1..30}; do
        if check_rpc "$MPT_RPC"; then
            echo "MPT node is ready"
            return
        fi
        sleep 1
    done
    echo "ERROR: MPT node failed to start"
    exit 1
}


# Main execution
echo "=== Migration Test Script ==="
echo "ZK Data Dir: $ZK_DATA_DIR"
echo "MPT Data Dir: $MPT_DATA_DIR"
echo "ZK RPC: $ZK_RPC"
echo "MPT RPC: $MPT_RPC"
echo "Transactions per block: $NUM_TXS"
echo "Blocks to create: $NUM_BLOCKS"
echo ""

# Start nodes
start_zk_node
start_mpt_node

# Get initial block numbers (should be same if using same genesis)
INITIAL_ZK_BLOCK=$(get_block_number "$ZK_RPC")
INITIAL_MPT_BLOCK=$(get_block_number "$MPT_RPC")
echo "Initial ZK block: $INITIAL_ZK_BLOCK"
echo "Initial MPT block: $INITIAL_MPT_BLOCK"

if [ "$INITIAL_ZK_BLOCK" -ne "$INITIAL_MPT_BLOCK" ]; then
    echo "WARNING: Initial block numbers differ. ZK=$INITIAL_ZK_BLOCK, MPT=$INITIAL_MPT_BLOCK"
    echo "This may cause issues. Make sure both nodes use compatible genesis."
fi

# Build JWT tokens for both nodes
ZK_JWT_TOKEN=$(build_jwt_token "$JWT_SECRET_PATH")
MPT_JWT_TOKEN=$(build_jwt_token "$JWT_SECRET_PATH")

# Send transactions and create blocks on both nodes
echo ""
echo "=== Sending transactions and creating blocks on both nodes ==="
for block_num in $(seq 1 $NUM_BLOCKS); do
    echo ""
    echo "Creating block $block_num..."
    
    # Send same transactions to both nodes
    echo "  Sending transactions to both nodes..."
    for tx_num in $(seq 1 $NUM_TXS); do
        # Generate deterministic transaction parameters
        to_addr="0x000000000000000000000000000000000000dEaD"
        value="0.1"
        
        echo "    Transaction $tx_num: sending to ZK node..."
        send_transaction "$ZK_RPC" "$to_addr" "$value"
        
        echo "    Transaction $tx_num: sending to MPT node..."
        send_transaction "$MPT_RPC" "$to_addr" "$value"
        
        sleep 0.3  # Small delay between transactions
    done
    
    # Wait a bit for transactions to be added to pools
    sleep 1
    
    # Create block on ZK node
    echo "  Creating block on ZK node..."
    if ! make_block "$ZK_RPC" "$ZK_AUTH_RPC" "$ZK_JWT_TOKEN"; then
        echo "ERROR: Failed to create block on ZK node"
        exit 1
    fi
    
    # Create block on MPT node
    echo "  Creating block on MPT node..."
    if ! make_block "$MPT_RPC" "$MPT_AUTH_RPC" "$MPT_JWT_TOKEN"; then
        echo "ERROR: Failed to create block on MPT node"
        exit 1
    fi
    
    # Verify both nodes are at the same block height
    zk_block=$(get_block_number "$ZK_RPC")
    mpt_block=$(get_block_number "$MPT_RPC")
    echo "  Block heights: ZK=$zk_block, MPT=$mpt_block"
    
    if [ "$zk_block" -ne "$mpt_block" ]; then
        echo "WARNING: Block heights differ after block $block_num"
    fi
    
    sleep 1  # Small delay between blocks
done

# Get final block numbers
ZK_BLOCK=$(get_block_number "$ZK_RPC")
MPT_BLOCK=$(get_block_number "$MPT_RPC")

echo ""
echo "=== Final block numbers ==="
echo "ZK block: $ZK_BLOCK"
echo "MPT block: $MPT_BLOCK"

if [ "$ZK_BLOCK" -ne "$MPT_BLOCK" ]; then
    echo "ERROR: Block heights differ. Cannot compare state roots."
    echo "ZK node is at block $ZK_BLOCK, MPT node is at block $MPT_BLOCK"
    exit 1
fi

# Both nodes should be at the same block height
BLOCK_TO_CHECK=$ZK_BLOCK
echo ""
echo "=== Getting state roots ==="
echo "Checking state at block $BLOCK_TO_CHECK"

ZK_ROOT=$(get_state_root "$ZK_RPC" "$BLOCK_TO_CHECK")
MPT_ROOT=$(get_state_root "$MPT_RPC" "$BLOCK_TO_CHECK")

echo "ZK State Root:  $ZK_ROOT"
echo "MPT State Root: $MPT_ROOT"

# Check if roots are valid
if [ "$ZK_ROOT" = "null" ] || [ -z "$ZK_ROOT" ]; then
    echo "ERROR: Failed to get ZK state root"
    exit 1
fi

if [ "$MPT_ROOT" = "null" ] || [ -z "$MPT_ROOT" ]; then
    echo "ERROR: Failed to get MPT state root"
    exit 1
fi

# Database paths
ZK_DB_PATH="$ZK_DATA_DIR/geth/chaindata"
MPT_DB_PATH="$MPT_DATA_DIR/geth/chaindata"

if [ ! -d "$ZK_DB_PATH" ]; then
    echo "ERROR: ZK database not found at $ZK_DB_PATH"
    exit 1
fi

if [ ! -d "$MPT_DB_PATH" ]; then
    echo "ERROR: MPT database not found at $MPT_DB_PATH"
    exit 1
fi

# Stop nodes before running migration-checker (database needs to be unlocked)
stop_nodes

# Run migration checker
echo ""
echo "=== Running Migration Checker ==="
echo "Command: $MIGRATION_CHECKER_BIN \\"
echo "  -zk-db $ZK_DB_PATH \\"
echo "  -mpt-db $MPT_DB_PATH \\"
echo "  -zk-root $ZK_ROOT \\"
echo "  -mpt-root $MPT_ROOT"
echo ""

"$MIGRATION_CHECKER_BIN" \
    -zk-db "$ZK_DB_PATH" \
    -mpt-db "$MPT_DB_PATH" \
    -zk-root "$ZK_ROOT" \
    -mpt-root "$MPT_ROOT"

MIGRATION_CHECK_RESULT=$?

if [ $MIGRATION_CHECK_RESULT -eq 0 ]; then
    echo ""
    echo "✅ Migration check PASSED!"
    echo "Both nodes have identical state at block $BLOCK_TO_CHECK"
    exit 0
else
    echo ""
    echo "❌ Migration check FAILED!"
    exit 1
fi
