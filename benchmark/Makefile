# Benchmark Makefile - Standalone block production performance testing
#
# Complete workflow:
#   make all           - Build → genesis → init → start → bench (via RPC)
#   make setup         - Build + generate genesis + init chain (preparation only)
#   make run           - Start node + run benchmark via RPC (assumes setup done)
#
# Preload workflow (bypasses RPC, tests pure EVM execution):
#   make preload-run   - Build → genesis → init → preload 50k txs → start → observe
#   make preload       - Generate signed txs into transactions.rlp (after init)
#
# Individual steps:
#   make build         - Build geth binary
#   make genesis       - Generate genesis.json + accounts.json
#   make init          - Initialize geth datadir from genesis
#   make preload       - Generate N signed txs → datadir/transactions.rlp
#   make start         - Start geth node (background, with standalone producer)
#   make bench         - Run benchmark load test via RPC
#   make stop          - Stop the geth node
#   make clean         - Clean all generated files
#
# Configuration (override via environment or command line):
#   NUM_ACCOUNTS       - Number of benchmark accounts (default: 100)
#   BLOCK_INTERVAL_MS  - Block production interval in ms (default: 1000)
#   BENCH_SENDERS      - Number of concurrent senders (default: 10)
#   BENCH_TPS          - Target transactions per second (default: 100)
#   BENCH_DURATION     - Benchmark duration (default: 60s)
#   RPC_PORT           - HTTP RPC port (default: 8545)
#   PRELOAD_TX_COUNT   - Number of txs to preload into transactions.rlp (default: 50000)
#   PRELOAD_SENDERS    - Number of sender accounts for preload (default: 50)

.PHONY: all setup run build genesis init start stop bench clean compile-contract preload preload-run

# =====================
# Configuration
# =====================
ROOT_DIR       := $(shell cd .. && pwd)
BENCHMARK_DIR  := $(shell pwd)
GETH_BIN       := $(ROOT_DIR)/build/bin/geth
DATADIR        := $(BENCHMARK_DIR)/data
GENESIS_FILE   := $(BENCHMARK_DIR)/genesis.json
ACCOUNTS_FILE  := $(BENCHMARK_DIR)/accounts.json
PID_FILE       := $(BENCHMARK_DIR)/geth.pid
LOG_FILE       := $(BENCHMARK_DIR)/geth.log

# Tunable parameters
NUM_ACCOUNTS       ?= 1000
BLOCK_INTERVAL_MS  ?= 300
BENCH_SENDERS      ?= 10
BENCH_WORKERS      ?= 50
BENCH_TPS          ?= 100
BENCH_DURATION     ?= 60s
RPC_PORT           ?= 8545
RPC_URL            ?= http://localhost:$(RPC_PORT)
PRELOAD_TX_COUNT   ?= 50000
PRELOAD_SENDERS    ?= 50
TOKEN_CONTRACT     ?= 0xBEEF000000000000000000000000000000000001

# Optional: specify an existing accounts.json to reuse across runs.
# If set, gen_genesis will use these accounts instead of generating new ones.
#   make setup ACCOUNTS_INPUT=./accounts.json
ACCOUNTS_INPUT     ?=

# =====================
# High-level targets
# =====================
all: setup run

setup: build genesis init

run: start wait-ready bench

# Preload workflow: bypass RPC entirely, test pure EVM execution throughput
preload-run: setup preload start-preload

# =====================
# Build
# =====================
build:
	@echo "==> Building geth..."
	cd $(ROOT_DIR) && go build -o $(GETH_BIN) ./cmd/geth/
	@echo "==> Geth built: $(GETH_BIN)"

# =====================
# Contract Compilation (optional, for regenerating bytecode)
# =====================
compile-contract:
	@echo "==> Compiling BenchmarkToken.sol..."
	@mkdir -p contracts/build
	npx --yes solc@0.8.20 --optimize --optimize-runs 200 --bin --abi \
		contracts/BenchmarkToken.sol -o contracts/build/
	@echo "==> Contract compiled to contracts/build/"

# =====================
# Genesis Generation
# =====================
genesis: $(GENESIS_FILE)

# If ACCOUNTS_INPUT is set, pass -accounts-file to reuse existing accounts;
# otherwise generate new accounts with -accounts NUM_ACCOUNTS.
GEN_GENESIS_FLAGS = -output $(BENCHMARK_DIR) \
	-bytecode $(BENCHMARK_DIR)/contracts/build/BenchmarkToken_runtime.bin

ifneq ($(ACCOUNTS_INPUT),)
  GEN_GENESIS_FLAGS += -accounts-file $(ACCOUNTS_INPUT)
else
  GEN_GENESIS_FLAGS += -accounts $(NUM_ACCOUNTS)
endif

$(GENESIS_FILE): gen_genesis/main.go contracts/build/BenchmarkToken_runtime.bin
	@if [ -n "$(ACCOUNTS_INPUT)" ]; then \
		echo "==> Generating genesis using existing accounts: $(ACCOUNTS_INPUT)"; \
	else \
		echo "==> Generating genesis with $(NUM_ACCOUNTS) new accounts..."; \
	fi
	cd $(ROOT_DIR) && go run ./benchmark/gen_genesis/ $(GEN_GENESIS_FLAGS)
	@echo "==> Genesis generated: $(GENESIS_FILE)"

# =====================
# Chain Initialization
# =====================
init: $(GENESIS_FILE)
	@echo "==> Initializing geth datadir..."
	@rm -rf $(DATADIR)
	$(GETH_BIN) init --datadir $(DATADIR) $(GENESIS_FILE)
	@echo "==> Datadir initialized: $(DATADIR)"

# =====================
# Node Management
# =====================
GETH_COMMON_FLAGS = \
	--datadir $(DATADIR) \
	--networkid 2818 \
	--nodiscover \
	--maxpeers 0 \
	--http \
	--http.addr "0.0.0.0" \
	--http.port $(RPC_PORT) \
	--http.api "eth,net,web3,txpool,debug" \
	--http.corsdomain "*" \
	--http.vhosts "*" \
	--standalone.producer \
	--standalone.blockinterval $(BLOCK_INTERVAL_MS) \
	--gcmode archive \
	--miner.gasprice 0 

start: $(GETH_BIN)
	@if [ -f $(PID_FILE) ] && kill -0 $$(cat $(PID_FILE)) 2>/dev/null; then \
		echo "==> Geth is already running (PID: $$(cat $(PID_FILE)))"; \
	else \
		echo "==> Starting geth node (block interval: $(BLOCK_INTERVAL_MS)ms)..."; \
		$(GETH_BIN) $(GETH_COMMON_FLAGS) \
			> $(LOG_FILE) 2>&1 & \
		echo $$! > $(PID_FILE); \
		echo "==> Geth started (PID: $$(cat $(PID_FILE)), log: $(LOG_FILE))"; \
	fi

# Start geth with enlarged txpool to hold all preloaded transactions.
# The journal file (transactions.rlp) was generated by "make preload".
start-preload: $(GETH_BIN)
	@if [ -f $(PID_FILE) ] && kill -0 $$(cat $(PID_FILE)) 2>/dev/null; then \
		echo "==> Geth is already running (PID: $$(cat $(PID_FILE)))"; \
	else \
		echo "==> Starting geth with preloaded txpool ($(PRELOAD_TX_COUNT) txs, block interval: $(BLOCK_INTERVAL_MS)ms)..."; \
		$(GETH_BIN) $(GETH_COMMON_FLAGS) \
			--txpool.globalslots $(shell echo '$(PRELOAD_TX_COUNT) * 12 / 10' | bc) \
			--txpool.accountslots $(shell echo '$(PRELOAD_TX_COUNT) / $(PRELOAD_SENDERS) * 12 / 10' | bc) \
			--txpool.globalqueue 1000 \
			--txpool.accountqueue 1000 \
			> $(LOG_FILE) 2>&1 & \
		echo $$! > $(PID_FILE); \
		echo "==> Geth started (PID: $$(cat $(PID_FILE)), log: $(LOG_FILE))"; \
		echo "==> TxPool will load transactions.rlp on startup. Watch log:"; \
		echo "    tail -f $(LOG_FILE)"; \
	fi

wait-ready:
	@echo "==> Waiting for geth RPC to be ready..."
	@for i in $$(seq 1 30); do \
		if curl -s -X POST -H "Content-Type: application/json" \
			--data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
			$(RPC_URL) > /dev/null 2>&1; then \
			echo "==> Geth RPC is ready!"; \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "==> ERROR: Geth RPC not ready after 30s. Check $(LOG_FILE)"; \
	exit 1

stop:
	@if [ -f $(PID_FILE) ]; then \
		PID=$$(cat $(PID_FILE)); \
		if kill -0 $$PID 2>/dev/null; then \
			echo "==> Stopping geth (PID: $$PID)..."; \
			kill $$PID; \
			sleep 2; \
			kill -0 $$PID 2>/dev/null && kill -9 $$PID; \
			echo "==> Geth stopped."; \
		else \
			echo "==> Geth not running."; \
		fi; \
		rm -f $(PID_FILE); \
	else \
		echo "==> No PID file found."; \
	fi

# =====================
# Preload Transactions (bypass RPC)
# =====================
preload: $(ACCOUNTS_FILE)
	@echo "==> Generating $(PRELOAD_TX_COUNT) signed txs → $(DATADIR)/transactions.rlp..."
	cd $(ROOT_DIR) && go run ./benchmark/gen_txs/ \
		-accounts $(BENCHMARK_DIR)/accounts.json \
		-datadir $(DATADIR) \
		-senders $(PRELOAD_SENDERS) \
		-count $(PRELOAD_TX_COUNT) \
		-token "$(TOKEN_CONTRACT)" \
		-chainid 2818
	@echo "==> Preloaded transactions ready."

# =====================
# Benchmark (via RPC)
# =====================
bench: $(ACCOUNTS_FILE)
	@echo "==> Running benchmark (senders=$(BENCH_SENDERS), workers=$(BENCH_WORKERS), tps=$(BENCH_TPS), duration=$(BENCH_DURATION))..."
	cd $(ROOT_DIR) && go run ./benchmark/bench/ \
		-rpc $(RPC_URL) \
		-accounts $(BENCHMARK_DIR)/accounts.json \
		-senders $(BENCH_SENDERS) \
		-workers $(BENCH_WORKERS) \
		-tps $(BENCH_TPS) \
		-duration $(BENCH_DURATION) \
		-token "$(TOKEN_CONTRACT)"

# =====================
# Cleanup
# =====================
clean: stop
	@echo "==> Cleaning up..."
	rm -rf $(DATADIR) $(GENESIS_FILE) $(PID_FILE) $(LOG_FILE)
	@echo "==> Clean."

clean-all: clean
	@echo "==> Cleaning build artifacts..."
	rm -rf contracts/build
	@echo "==> All clean."

# =====================
# Help
# =====================
help:
	@echo "Morph Execution Layer Benchmark"
	@echo ""
	@echo "=== Workflows ==="
	@echo ""
	@echo "  RPC-based benchmark (tests full stack including RPC):"
	@echo "    make all                       # build → genesis → init → start → bench"
	@echo ""
	@echo "  Preload benchmark (tests pure EVM execution, bypasses RPC):"
	@echo "    make preload-run               # build → genesis → init → preload 50k txs → start"
	@echo "    tail -f benchmark/geth.log     # Then watch block production in log"
	@echo ""
	@echo "=== Individual Targets ==="
	@echo "  make build       Build geth binary"
	@echo "  make genesis     Generate genesis.json + accounts.json"
	@echo "  make init        Initialize geth datadir from genesis"
	@echo "  make preload     Generate signed txs → datadir/transactions.rlp"
	@echo "  make start       Start geth node (normal mode)"
	@echo "  make start-preload  Start geth with enlarged txpool for preloaded txs"
	@echo "  make bench       Run RPC-based benchmark"
	@echo "  make stop        Stop geth node"
	@echo "  make clean       Clean all generated files"
	@echo ""
	@echo "=== Configuration ==="
	@echo "  NUM_ACCOUNTS=$(NUM_ACCOUNTS)             Benchmark accounts in genesis"
	@echo "  ACCOUNTS_INPUT=                  Path to existing accounts.json (optional)"
	@echo "  BLOCK_INTERVAL_MS=$(BLOCK_INTERVAL_MS)         Block production interval (ms)"
	@echo "  BENCH_SENDERS=$(BENCH_SENDERS)              Sender accounts for RPC bench"
	@echo "  BENCH_WORKERS=$(BENCH_WORKERS)              Concurrent RPC sender workers"
	@echo "  BENCH_TPS=$(BENCH_TPS)                  Target TPS for RPC bench"
	@echo "  BENCH_DURATION=$(BENCH_DURATION)             RPC benchmark duration"
	@echo "  PRELOAD_TX_COUNT=$(PRELOAD_TX_COUNT)         Txs to preload into transactions.rlp"
	@echo "  PRELOAD_SENDERS=$(PRELOAD_SENDERS)            Sender accounts for preload"
	@echo "  RPC_PORT=$(RPC_PORT)                 HTTP RPC port"
	@echo ""
	@echo "=== Examples ==="
	@echo "  make preload-run PRELOAD_TX_COUNT=100000 PRELOAD_SENDERS=100  # Pure EVM test"
	@echo "  make all BENCH_TPS=500 BENCH_DURATION=120s                    # RPC test"
	@echo "  make all BLOCK_INTERVAL_MS=5000 BENCH_TPS=2000                # Larger blocks"
	@echo "  make setup ACCOUNTS_INPUT=./my_accounts.json                  # Reuse existing accounts"

