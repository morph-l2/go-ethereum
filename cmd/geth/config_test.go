// Copyright 2026 The go-ethereum Authors
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
	"testing"
)

func TestConfigRejectsNegativeLogQueryLimit(t *testing.T) {
	datadir := tmpdir(t)
	defer os.RemoveAll(datadir)

	config := filepath.Join(datadir, "config.toml")
	if err := ioutil.WriteFile(config, []byte("[Eth]\nLogQueryLimit = -1\n"), 0600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	geth := runGeth(t, "--config", config, "--datadir", datadir, "--ipcdisable")
	expectGethFailure(t, geth, "LogQueryLimit must be non-negative")
}
