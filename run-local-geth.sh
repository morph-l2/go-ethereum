#!/bin/bash
# Local private node startup script
set -e

# Configurable parameters
GETH_DATA_DIR="${GETH_DATA_DIR:-./private-node-data}"
JWT_SECRET_PATH="${JWT_SECRET_PATH:-$GETH_DATA_DIR/jwt-secret.txt}"
CHAIN_ID="${CHAIN_ID:-1337}"
GETH_LOG_FILE="${GETH_LOG_FILE:-$GETH_DATA_DIR/geth.log}"
GENESIS_FILE="${GENESIS_FILE:-$GETH_DATA_DIR/genesis.json}"
PASSWORD_FILE="${PASSWORD_FILE:-$GETH_DATA_DIR/password.txt}"

# Create necessary directories
mkdir -p "$GETH_DATA_DIR"
mkdir -p "$(dirname "$GETH_LOG_FILE")"

# Create password file with empty password
echo "" > "$PASSWORD_FILE"

# Create JWT secret file if it doesn't exist
if [ ! -f "$JWT_SECRET_PATH" ]; then
    echo "Creating JWT secret file..."
    openssl rand -hex 32 > "$JWT_SECRET_PATH"
    chmod 600 "$JWT_SECRET_PATH"
fi

# Create genesis file if it doesn't exist
if [ ! -f "$GENESIS_FILE" ]; then
    echo "Creating genesis file..."
    cat > "$GENESIS_FILE" << EOF
{
  "config": {
    "chainId": $CHAIN_ID,
    "homesteadBlock": 0,
    "eip150Block": 0,
    "eip155Block": 0,
    "eip158Block": 0,
    "byzantiumBlock": 0,
    "constantinopleBlock": 0,
    "petersburgBlock": 0,
    "istanbulBlock": 0,
    "berlinBlock": 0,
    "londonBlock": 0,
    "terminalTotalDifficulty": 0,
    "archimedesBlock": 0,
    "shanghaiBlock": 0,
    "bernoulliBlock": 0,
    "curieBlock": 0,
    "morph": {
      "useZktrie": true,
      "feeVaultAddress": "0x0e87cd091e091562F25CB1cf4641065dA2C049F5"
    }
  },
  "nonce": "0x0",
  "timestamp": "0x61bc34a0",
  "extraData": "0x00000000000000000000000000000000000000000000000000000000000000004cb1ab63af5d8931ce09673ebd8ae2ce16fd65710000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
  "gasLimit": "0x6691b7",
  "difficulty": "0x0",
  "mixHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
  "coinbase": "0x32d0e596499232Db07B1EccB29161d1CcF29E01D",
  "alloc": {
    "0x1111111111111111111111111111111111111111": {
      "balance": "10000000000000000000000"
    },
    "0x2222222222222222222222222222222222222222": {
      "balance": "10000000000000000000000"
    }
  },
  "number": "0x0",
  "gasUsed": "0x0",
  "parentHash": "0x0000000000000000000000000000000000000000000000000000000000000000"
}
EOF
fi

# Initialize blockchain if not already initialized
if [ ! -d "$GETH_DATA_DIR/geth" ]; then
    echo "Initializing blockchain..."
    ./build/bin/geth --datadir "$GETH_DATA_DIR" init "$GENESIS_FILE"
fi

# Create accounts if needed
if [ ! -d "$GETH_DATA_DIR/keystore" ] || [ -z "$(ls -A "$GETH_DATA_DIR/keystore" 2>/dev/null)" ]; then
    echo "Creating test accounts..."
    ./build/bin/geth --datadir "$GETH_DATA_DIR" account new --password "$PASSWORD_FILE"
    ./build/bin/geth --datadir "$GETH_DATA_DIR" account new --password "$PASSWORD_FILE"

    echo "Accounts created, listing account addresses:"
    ./build/bin/geth --datadir "$GETH_DATA_DIR" account list
fi

# Build startup command
COMMAND="./build/bin/geth \
--datadir=$GETH_DATA_DIR \
--verbosity=4 \
--http \
--http.corsdomain='*' \
--http.vhosts='*' \
--http.addr=0.0.0.0 \
--http.port=8545 \
--http.api=web3,eth,debug,txpool,net,admin,personal \
--ws \
--ws.addr=0.0.0.0 \
--ws.port=8546 \
--ws.origins='*' \
--ws.api=web3,eth,debug,txpool,net,admin,personal \
--networkid=$CHAIN_ID \
--authrpc.addr=0.0.0.0 \
--authrpc.port=8551 \
--authrpc.vhosts='*' \
--authrpc.jwtsecret=$JWT_SECRET_PATH \
--mine \
--miner.gasprice=0 \
--allow-insecure-unlock \
--unlock=0,1 \
--password=$PASSWORD_FILE \
--nodiscover \
--maxpeers=0"

# Print command
echo "Executing command: $COMMAND"

# Execute command
echo "Starting private node, logs output to $GETH_LOG_FILE"
mkdir -p "$(dirname "$GETH_LOG_FILE")"
$COMMAND 2>&1 | tee "$GETH_LOG_FILE"