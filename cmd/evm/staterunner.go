// Copyright 2017 The go-ethereum Authors
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
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"

	"golang.org/x/crypto/sha3"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/rlp"
	"github.com/morph-l2/go-ethereum/tests"

	"gopkg.in/urfave/cli.v1"
)

var stateTestCommand = cli.Command{
	Action:    stateTestCmd,
	Name:      "statetest",
	Usage:     "executes the given state tests",
	ArgsUsage: "<file>",
}

// StatetestResult contains the execution status after running a state test, any
// error that might have occurred and a dump of the final state if requested.
type StatetestResult struct {
	Name  string      `json:"name"`
	Pass  bool        `json:"pass"`
	Fork  string      `json:"fork"`
	Error string      `json:"error,omitempty"`
	State *state.Dump `json:"state,omitempty"`
}

func stateTestCmd(ctx *cli.Context) error {
	if len(ctx.Args().First()) == 0 {
		return errors.New("path-to-test argument required")
	}
	// Configure the go-ethereum logger
	glogger := log.NewGlogHandler(log.StreamHandler(os.Stderr, log.TerminalFormat(false)))
	glogger.Verbosity(log.Lvl(ctx.GlobalInt(VerbosityFlag.Name)))
	log.Root().SetHandler(glogger)

	src, err := ioutil.ReadFile(ctx.Args().First())
	if err != nil {
		return err
	}
	var tests map[string]tests.StateTest
	if err = json.Unmarshal(src, &tests); err != nil {
		return err
	}
	// Iterate over all the tests, run them and aggregate the results
	cfg := vm.Config{
		Tracer: tracerFromFlags(ctx),
	}
	results := make([]StatetestResult, 0, len(tests))
	for key, test := range tests {
		for _, st := range test.Subtests() {
			// Run the test and aggregate the result
			result := &StatetestResult{Name: key, Fork: st.Fork, Pass: true}
			_, s, evmResult, err := test.RunWithResult(st, cfg, false)
			// Emit stateRoot + logsRoot + postLogsHash + returndata/gas for
			// evmlab tracing and cross-client diff harnesses. logsRoot and
			// postLogsHash are identical here (both are
			// keccak256(rlp(stateDB.Logs()))) and emitted under both keys so
			// consumers tracking either convention pick them up. gasUsed is the
			// EVM-only gas from ApplyMessage; harnesses can normalize runner
			// convention skew against clients that report total tx gas.
			if ctx.GlobalBool(MachineFlag.Name) && s != nil {
				logsHash := rlpHash(s.Logs())
				var output []byte
				var gasUsed uint64
				if evmResult != nil {
					output = evmResult.ReturnData
					gasUsed = evmResult.GasUsed
				}
				fmt.Fprintf(os.Stderr,
					"{\"stateRoot\": \"%x\", \"logsRoot\": \"%x\", \"postLogsHash\": \"%x\", \"output\": \"0x%x\", \"gasUsed\": %d}\n",
					s.IntermediateRoot(false), logsHash, logsHash, output, gasUsed)
			}
			if err != nil {
				// Test failed, mark as so and dump any state to aid debugging
				result.Pass, result.Error = false, err.Error()
				if ctx.GlobalBool(DumpFlag.Name) && s != nil {
					dump := s.RawDump(nil)
					result.State = &dump
				}
			}

			results = append(results, *result)
		}
	}
	out, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println(string(out))
	return nil
}

// rlpHash returns the keccak256 hash of the RLP encoding of x. Used to
// derive the post-execution logs root in the same way tests/state_test_util.go
// does so the MachineFlag-mode output matches what the cross-client diff
// harness expects.
func rlpHash(x interface{}) (h common.Hash) {
	hw := sha3.NewLegacyKeccak256()
	rlp.Encode(hw, x)
	hw.Sum(h[:0])
	return h
}
