# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

GORUN = env GO111MODULE=on go run

libzkp:
	cd $(PWD)/rollup/circuitcapacitychecker/libzkp && make libzkp

# in go-ethereum repo
build-bk-prod-morph-prod-mainnet-to-morph-geth: libzkp
	if [ ! -d dist ]; then mkdir -p dist; fi
	$(GORUN) build/ci.go install -buildtags circuit_capacity_checker ./cmd/geth
	cp build/bin/geth dist/
	cp $(PWD)/rollup/circuitcapacitychecker/libzkp/libzkp.so dist/

build-bk-prod-morph-prod-mainnet-to-morph-nccc-geth: ## geth without circuit capacity checker
	if [ ! -d dist ]; then mkdir -p dist; fi
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	cp build/bin/geth dist/

start-morph-geth:
	geth --datadir="/data/morph-geth-db"

start-morph-sentry-geth:
	geth --datadir="/data/morph-geth-db"

