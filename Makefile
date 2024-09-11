# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make. 

.PHONY: geth android ios evm all test clean libzkp

GOBIN = ./build/bin
GO ?= latest
GORUN = env GO111MODULE=on go run

geth:
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	@echo "Run \"$(GOBIN)/geth\" to launch geth."

all:
	$(GORUN) build/ci.go install

android:
	$(GORUN) build/ci.go aar --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/geth.aar\" to use the library."
	@echo "Import \"$(GOBIN)/geth-sources.jar\" to add javadocs"
	@echo "For more info see https://stackoverflow.com/questions/20994336/android-studio-how-to-attach-javadoc"

ios:
	$(GORUN) build/ci.go xcode --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/Geth.framework\" to use the library."

test: all
	# genesis test
	cd ${PWD}/cmd/geth; go test -test.run TestCustomGenesis
	# module test
	$(GORUN) build/ci.go test ./consensus ./core ./eth ./miner ./node ./trie

lint: ## Run linters.
	$(GORUN) build/ci.go lint

clean:
	env GO111MODULE=on go clean -cache
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

# The devtools target installs tools required for 'go generate'.
# You need to put $GOBIN (or $GOPATH/bin) in your PATH to use 'go generate'.

devtools:
	env GOBIN= go install golang.org/x/tools/cmd/stringer@latest
	env GOBIN= go install github.com/kevinburke/go-bindata/go-bindata@latest
	env GOBIN= go install github.com/fjl/gencodec@latest
	env GOBIN= go install github.com/golang/protobuf/protoc-gen-go@latest
	env GOBIN= go install ./cmd/abigen
	@type "solc" 2> /dev/null || echo 'Please install solc'
	@type "protoc" 2> /dev/null || echo 'Please install protoc'

testnet-up:
	docker-compose -f testnet/docker-compose.yml up  -d
.PHONY: testnet-up

testnet-down:
	docker-compose -f testnet/docker-compose.yml down
.PHONY: testnet-down

testnet-clean:
	docker-compose -f testnet/docker-compose.yml down
	docker images -q morph_geth:latest | xargs -r docker rmi
	docker volume ls --filter "name=morph_data*" -q | xargs -r docker volume rm
.PHONY: testnet-clean

image:
	docker build -f Dockerfile -t morph-geth:latest .

docker:
	docker build -t morph/l2geth:latest ./ -f Dockerfile

