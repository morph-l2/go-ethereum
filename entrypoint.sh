GETH_DATA_DIR=/db
GETH_CHAINDATA_DIR="$GETH_DATA_DIR/geth"
GENESIS_FILE_PATH="${GENESIS_FILE_PATH:-/genesis.json}"


if [[ ! -e "$GETH_CHAINDATA_DIR" ]]; then
  echo "$GETH_CHAINDATA_DIR missing, running init"
  echo "Initializing genesis."
  geth --verbosity=3 init --datadir="$GETH_DATA_DIR"  "$GENESIS_FILE_PATH"
else
	echo "$GETH_KEYSTORE_DIR exists."
fi


geth \
--datadir="$GETH_DATA_DIR" \
--verbosity=3 \
--http \
--http.corsdomain="*" \
--http.vhosts="*" \
--http.addr=0.0.0.0 \
--http.port=8545 \
--http.api=web3,debug,eth,txpool,net,scroll,engine \
--networkid=53077 \
--authrpc.addr="0.0.0.0" \
--authrpc.port="8551" \
--authrpc.vhosts="*" \
--authrpc.jwtsecret=/config/jwt-secret.txt \
--gcmode=archive