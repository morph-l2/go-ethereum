GETH_DATA_DIR="${GETH_DATA_DIR:-/data/morphism/setup/geth-data}"
JWT_SECRET_PATH="${JWT_SECRET_PATH:-/data/morphism/setup/jwt-secret.txt}"
DEFAULE_MINER_ETHERBASE="0x0e87cd091e091562F25CB1cf4641065dA2C049F5"
CHAIN_ID="${CHAIN_ID:-2710}"
GETH_LOG_FILE="${GETH_LOG_FILE:-/data/logs/geth.log}"

if [[ -z "$MINER_ETHERBASE" ]]; then
  # the environment variable is missing, set a default value
  MINER_ETHERBASE=$DEFAULE_MINER_ETHERBASE
fi

optional_bootnodes=${BOOT_NODES:+"--bootnodes=$BOOT_NODES"}

# shellcheck disable=SC2125
COMMAND="geth \
--datadir="$GETH_DATA_DIR" \
--verbosity=3 \
--http \
--http.corsdomain="*" \
--http.vhosts="*" \
--http.addr=0.0.0.0 \
--http.port=8545 \
--http.api=web3,eth,txpool,net,scroll,engine,admin \
--ws \
--ws.addr=0.0.0.0 \
--ws.port=8546 \
--ws.origins="*" \
--ws.api=web3,eth,txpool,net,scroll,engine,admin \
--networkid=$CHAIN_ID \
--authrpc.addr="0.0.0.0" \
--authrpc.port="8551" \
--authrpc.vhosts="*" \
--authrpc.jwtsecret=$JWT_SECRET_PATH \
--gcmode=archive \
--mine \
--miner.etherbase=$MINER_ETHERBASE $optional_bootnodes"

nohup $COMMAND > $GETH_LOG_FILE 2>&1 &
