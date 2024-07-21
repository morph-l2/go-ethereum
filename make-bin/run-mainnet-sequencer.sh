#!/bin/sh
#script for sequencer entrypoint
set -ex

GETH_DATA_DIR="${GETH_DATA_DIR:-/data/morph-geth-db}"
JWT_SECRET_PATH="${JWT_SECRET_PATH:-/data/morph-geth/make-conf/jwt-secret.txt}"
DEFAULE_MINER_ETHERBASE="0x0e87cd091e091562F25CB1cf4641065dA2C049F5"
CHAIN_ID="${CHAIN_ID:-2810}"

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
--http.api=web3,debug,eth,txpool,net,morph,engine,admin \
--ws \
--ws.addr=0.0.0.0 \
--ws.port=8546 \
--ws.origins="*" \
--ws.api=web3,debug,eth,txpool,net,morph,engine,admin \
--networkid=$CHAIN_ID \
--authrpc.addr="0.0.0.0" \
--authrpc.port="8551" \
--authrpc.vhosts="*" \
--authrpc.jwtsecret=$JWT_SECRET_PATH \
--gcmode=archive \
--cache=4096 \
--nodiscover \
--metrics \
--metrics.addr=0.0.0.0 \
--metrics.port=6060 \
--log.filename=/data/logs/morph-geth/geth.log \
--log.maxsize=200 \
--log.maxage=30 \
--log.compress=true \
--mine \
--miner.gasprice="100000000" \
--miner.gaslimit="10000000" \
--miner.etherbase=$MINER_ETHERBASE $optional_bootnodes"

$COMMAND
