# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

GORUN = env GO111MODULE=on go run

# in go-ethereum repo
build-bk-prod-morph-prod-mainnet-to-morph-geth:
	if [ ! -d dist ]; then mkdir -p dist; fi
	$(GORUN) build/ci.go install ./cmd/geth
	cp build/bin/geth dist/
	tar -czvf morph-geth.tar.gz dist
	aws s3 cp morph-geth.tar.gz s3://morph-0582-morph-technical-department-mainnet-data/morph-setup/morph-geth.tar.gz

build-bk-prod-morph-prod-mainnet-to-morph-nccc-geth:
	if [ ! -d dist ]; then mkdir -p dist; fi
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	cp build/bin/geth dist/
	tar -czvf morph-nccc-geth.tar.gz dist
	aws s3 cp morph-nccc-geth.tar.gz s3://morph-0582-morph-technical-department-mainnet-data/morph-setup/morph-nccc-geth.tar.gz


# build for holesky
build-bk-prod-morph-prod-testnet-to-morph-geth-holesky:
	if [ ! -d dist ]; then mkdir -p dist; fi
	$(GORUN) build/ci.go install ./cmd/geth
	cp build/bin/geth dist/
	tar -czvf morph-geth.tar.gz dist
	aws s3 cp morph-geth.tar.gz s3://morph-0582-morph-technical-department-testnet-data/testnet/holesky/morph-setup/morph-geth.tar.gz

build-bk-prod-morph-prod-testnet-to-morph-nccc-geth-holesky:
	if [ ! -d dist ]; then mkdir -p dist; fi
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	cp build/bin/geth dist/
	tar -czvf morph-nccc-geth.tar.gz dist
	aws s3 cp morph-nccc-geth.tar.gz s3://morph-0582-morph-technical-department-testnet-data/testnet/holesky/morph-setup/morph-nccc-geth.tar.gz


# build for hoodi
build-bk-prod-morph-prod-testnet-to-morph-geth-hoodi:
	if [ ! -d dist ]; then mkdir -p dist; fi
	$(GORUN) build/ci.go install ./cmd/geth
	cp build/bin/geth dist/
	tar -czvf morph-geth.tar.gz dist
	aws s3 cp morph-geth.tar.gz s3://morph-0582-morph-technical-department-testnet-data/testnet/hoodi/morph-setup/morph-geth.tar.gz

build-bk-prod-morph-prod-testnet-to-morph-nccc-geth-hoodi:
	if [ ! -d dist ]; then mkdir -p dist; fi
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	cp build/bin/geth dist/
	tar -czvf morph-nccc-geth.tar.gz dist
	aws s3 cp morph-nccc-geth.tar.gz s3://morph-0582-morph-technical-department-testnet-data/testnet/hoodi/morph-setup/morph-nccc-geth.tar.gz

# build for qanet
build-bk-test-morph-test-qanet-to-morph-geth-qanet:
	if [ ! -d dist ]; then mkdir -p dist; fi
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	cp build/bin/geth dist/
	tar -czvf morph-geth.tar.gz dist
	aws s3 cp morph-geth.tar.gz s3://morph-7637-morph-technical-department-qanet-data/morph-setup/morph-geth.tar.gz

build-bk-test-morph-test-qanet-to-morph-nccc-geth-qanet:
	if [ ! -d dist ]; then mkdir -p dist; fi
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	cp build/bin/geth dist/
	tar -czvf morph-nccc-geth.tar.gz dist
	aws s3 cp morph-nccc-geth.tar.gz s3://morph-7637-morph-technical-department-qanet-data/morph-setup/morph-nccc-geth.tar.gz