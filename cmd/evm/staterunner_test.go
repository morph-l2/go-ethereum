package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morph-l2/go-ethereum/internal/cmdtest"
)

func TestStateTestTraceEmitsJSONOpcodes(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "trace-smoke.json")
	if err := os.WriteFile(fixture, []byte(`{
  "trace_smoke": {
    "env": {
      "currentCoinbase": "0x0000000000000000000000000000000000000000",
      "currentDifficulty": "0x0",
      "currentGasLimit": "0x989680",
      "currentNumber": "0x1",
      "currentTimestamp": "0x1",
      "currentBaseFee": "0x0"
    },
    "pre": {
      "0xa94f5374fce5edbc8e2a8697c15331677e6ebf0b": {"balance":"0xde0b6b3a7640000","nonce":"0x0","code":"0x","storage":{}},
      "0x0000000000000000000000000000000000001234": {"balance":"0x0","nonce":"0x0","code":"0x600160020100","storage":{}}
    },
    "transaction": {
      "nonce":"0x0","gasPrice":"0x1","gasLimit":["0x186a0"],
      "to":"0x0000000000000000000000000000000000001234",
      "value":["0x0"],"data":["0x"],
      "secretKey":"0x45a915e4d060149eb4365960e6a7a45f334393093061116b197e3240065ff2d8",
      "sender":"0xa94f5374fce5edbc8e2a8697c15331677e6ebf0b",
      "accessLists":[null]
    },
    "post": {
      "Jade": [{
        "indexes": {"data":0,"gas":0,"value":0},
        "hash":"0x0000000000000000000000000000000000000000000000000000000000000000",
        "logs":"0x0000000000000000000000000000000000000000000000000000000000000000",
        "expectException": null
      }]
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	tt := new(testT8n)
	tt.TestCmd = cmdtest.NewTestCmd(t, tt)
	tt.Run("evm-test", "--trace", "--trace.format", "json", "--json", "statetest", fixture)
	_ = tt.Output()
	tt.WaitExit()

	stderr := tt.StderrText()
	if !strings.Contains(stderr, `"pc":0`) || !strings.Contains(stderr, `"op":`) {
		t.Fatalf("expected JSON opcode trace on stderr, got:\n%s", stderr)
	}
	var result map[string]any
	for _, line := range strings.Split(stderr, "\n") {
		var candidate map[string]any
		if json.Unmarshal([]byte(line), &candidate) == nil && candidate["stateRoot"] != nil {
			result = candidate
			break
		}
	}
	if result == nil {
		t.Fatalf("expected MachineFlag statetest result JSON on stderr, got:\n%s", stderr)
	}
	for _, field := range []string{"logsRoot", "postLogsHash", "gasUsed", "output"} {
		if _, ok := result[field]; !ok {
			t.Fatalf("expected MachineFlag statetest result to include %s, got result=%v\nstderr:\n%s", field, result, stderr)
		}
	}
}
