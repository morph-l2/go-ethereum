// Copyright 2016 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var customGenesisTests = []struct {
	genesis string
	query   string
	result  string
}{
	// Genesis file with an empty chain configuration (ensure missing fields work)
	{
		genesis: `{
			"alloc"      : {},
			"coinbase"   : "0x0000000000000000000000000000000000000000",
			"difficulty" : "0x20000",
			"extraData"  : "",
			"gasLimit"   : "0x2fefd8",
			"nonce"      : "0x0000000000001338",
			"mixhash"    : "0x0000000000000000000000000000000000000000000000000000000000000000",
			"parentHash" : "0x0000000000000000000000000000000000000000000000000000000000000000",
			"timestamp"  : "0x00",
			"config"     : {}
		}`,
		query:  "eth.getBlock(0).nonce",
		result: "0x0000000000001338",
	},
	// Genesis file with specific chain configurations
	{
		genesis: `{
			"alloc"      : {},
			"coinbase"   : "0x0000000000000000000000000000000000000000",
			"difficulty" : "0x20000",
			"extraData"  : "",
			"gasLimit"   : "0x2fefd8",
			"nonce"      : "0x0000000000001339",
			"mixhash"    : "0x0000000000000000000000000000000000000000000000000000000000000000",
			"parentHash" : "0x0000000000000000000000000000000000000000000000000000000000000000",
			"timestamp"  : "0x00",
			"config"     : {
				"homesteadBlock" : 42,
				"daoForkBlock"   : 141,
				"daoForkSupport" : true
			}
		}`,
		query:  "eth.getBlock(0).nonce",
		result: "0x0000000000001339",
	},
}

// Tests that initializing Geth with a custom genesis block and chain definitions
// work properly.
func TestCustomGenesis(t *testing.T) {
	for i, tt := range customGenesisTests {
		// Create a temporary data directory to use and inspect later
		datadir := tmpdir(t)
		defer os.RemoveAll(datadir)

		// Initialize the data directory with the custom genesis block
		json := filepath.Join(datadir, "genesis.json")
		if err := ioutil.WriteFile(json, []byte(tt.genesis), 0600); err != nil {
			t.Fatalf("test %d: failed to write genesis file: %v", i, err)
		}
		runGeth(t, "--datadir", datadir, "init", json).WaitExit()

		// Query the custom genesis block
		geth := runGeth(t, "--networkid", "1337", "--syncmode=full", "--snapshot=false", "--cache", "16",
			"--datadir", datadir, "--maxpeers", "0", "--port", "0",
			"--nodiscover", "--nat", "none", "--ipcdisable",
			"--exec", tt.query, "console")
		geth.ExpectRegexp(tt.result)
		geth.ExpectExit()
	}
}

func TestOverrideGenesisStartup(t *testing.T) {
	datadir := tmpdir(t)
	defer os.RemoveAll(datadir)

	json := writeOverrideGenesis(t, datadir, "genesis.json", "0x0000000000001338")

	geth := runGeth(t, "--override.genesis", json, "--syncmode=full", "--snapshot=false", "--cache", "16",
		"--datadir", datadir, "--maxpeers", "0", "--port", "0",
		"--nodiscover", "--nat", "none", "--ipcdisable",
		"--exec", "eth.getBlock(0).nonce", "console")
	geth.ExpectRegexp("0x0000000000001338")
	geth.ExpectExit()
}

func TestOverrideGenesisRejectsMissingFile(t *testing.T) {
	datadir := tmpdir(t)
	defer os.RemoveAll(datadir)

	geth := runGeth(t, "--override.genesis", filepath.Join(datadir, "missing.json"), "--datadir", datadir, "--ipcdisable")
	expectGethFailure(t, geth, "Failed to read genesis file")
}

func TestOverrideGenesisRejectsInvalidJSON(t *testing.T) {
	datadir := tmpdir(t)
	defer os.RemoveAll(datadir)

	json := filepath.Join(datadir, "invalid-genesis.json")
	if err := ioutil.WriteFile(json, []byte("{invalid-json"), 0600); err != nil {
		t.Fatalf("failed to write invalid genesis file: %v", err)
	}
	geth := runGeth(t, "--override.genesis", json, "--datadir", datadir, "--ipcdisable")
	expectGethFailure(t, geth, "Invalid genesis file")
}

func TestOverrideGenesisNetworkIDDefaultsToChainID(t *testing.T) {
	datadir := tmpdir(t)
	defer os.RemoveAll(datadir)

	json := writeOverrideGenesisWithChainID(t, datadir, "genesis.json", "0x0000000000001338", "1338")
	geth := runGeth(t, "--override.genesis", json, "--syncmode=full", "--snapshot=false", "--cache", "16",
		"--datadir", datadir, "--maxpeers", "0", "--port", "0",
		"--nodiscover", "--nat", "none", "--ipcdisable",
		"--exec", "net.version", "console")
	geth.ExpectRegexp("1338")
	geth.ExpectExit()
}

func TestOverrideGenesisAllowsExplicitNetworkID(t *testing.T) {
	datadir := tmpdir(t)
	defer os.RemoveAll(datadir)

	json := writeOverrideGenesisWithChainID(t, datadir, "genesis.json", "0x0000000000001338", "1338")
	geth := runGeth(t, "--override.genesis", json, "--networkid", "1337", "--syncmode=full", "--snapshot=false", "--cache", "16",
		"--datadir", datadir, "--maxpeers", "0", "--port", "0",
		"--nodiscover", "--nat", "none", "--ipcdisable",
		"--exec", "net.version", "console")
	geth.ExpectRegexp("1337")
	geth.ExpectExit()
}

func TestOverrideGenesisMismatchFails(t *testing.T) {
	datadir := tmpdir(t)
	defer os.RemoveAll(datadir)

	initial := writeOverrideGenesis(t, datadir, "initial-genesis.json", "0x0000000000001338")
	initGeth := runGeth(t, "--datadir", datadir, "init", initial)
	initGeth.WaitExit()
	if initGeth.ExitStatus() != 0 {
		t.Fatalf("geth init failed: %s", initGeth.StderrText())
	}

	other := writeOverrideGenesis(t, datadir, "other-genesis.json", "0x0000000000001339")
	geth := runGeth(t, "--override.genesis", other, "--syncmode=full", "--snapshot=false", "--cache", "16",
		"--datadir", datadir, "--maxpeers", "0", "--port", "0",
		"--nodiscover", "--nat", "none", "--ipcdisable")
	expectGethFailure(t, geth, "genesis")
}

func TestOverrideGenesisDoesNotChangeInitCommand(t *testing.T) {
	datadir := tmpdir(t)
	defer os.RemoveAll(datadir)

	json := writeOverrideGenesis(t, datadir, "genesis.json", "0x0000000000001338")
	initGeth := runGeth(t, "--datadir", datadir, "init", json)
	initGeth.WaitExit()
	if initGeth.ExitStatus() != 0 {
		t.Fatalf("geth init failed: %s", initGeth.StderrText())
	}
}

func writeOverrideGenesis(t *testing.T, dir, name, nonce string) string {
	t.Helper()
	return writeOverrideGenesisWithChainID(t, dir, name, nonce, "")
}

func writeOverrideGenesisWithChainID(t *testing.T, dir, name, nonce, chainID string) string {
	t.Helper()
	chainConfig := `"terminalTotalDifficulty": 0`
	if chainID != "" {
		chainConfig = `"chainId": ` + chainID + `,
			"terminalTotalDifficulty": 0`
	}
	genesis := `{
		"alloc"      : {},
		"coinbase"   : "0x0000000000000000000000000000000000000000",
		"difficulty" : "0x0",
		"extraData"  : "",
		"gasLimit"   : "0x2fefd8",
		"nonce"      : "` + nonce + `",
		"mixhash"    : "0x0000000000000000000000000000000000000000000000000000000000000000",
		"parentHash" : "0x0000000000000000000000000000000000000000000000000000000000000000",
		"timestamp"  : "0x00",
		"config"     : {
			` + chainConfig + `
		}
	}`
	path := filepath.Join(dir, name)
	if err := ioutil.WriteFile(path, []byte(genesis), 0600); err != nil {
		t.Fatalf("failed to write genesis file: %v", err)
	}
	return path
}

func expectGethFailure(t *testing.T, geth *testgeth, wantStderr string) {
	t.Helper()
	geth.WaitExit()
	if geth.ExitStatus() == 0 {
		t.Fatalf("expected geth to fail")
	}
	if stderr := geth.StderrText(); !strings.Contains(stderr, wantStderr) {
		t.Fatalf("stderr mismatch: got %q want substring %q", stderr, wantStderr)
	}
}
