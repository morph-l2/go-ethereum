package tests

import (
	"encoding/json"
	"testing"

	"github.com/morph-l2/go-ethereum/core/vm"
)

const morphMinimalStateTest = `{
  "env": {
    "currentCoinbase": "0000000000000000000000000000000000000000",
    "currentDifficulty": "0x0",
    "currentGasLimit": "0x989680",
    "currentNumber": "0x1",
    "currentTimestamp": "0x1",
    "currentBaseFee": "0x1"
  },
  "pre": {
    "a94f5374fce5edbc8e2a8697c15331677e6ebf0b": {
      "balance": "0xde0b6b3a7640000",
      "nonce": "0x0",
      "code": "0x",
      "storage": {}
    }
  },
  "transaction": {
    "nonce": "0x0",
    "gasPrice": "0x1",
    "gasLimit": ["0x5208"],
    "to": "0x0000000000000000000000000000000000000001",
    "value": ["0x1"],
    "data": ["0x"],
    "accessLists": [null],
    "secretKey": "0x45a915e4d060149eb4365960e6a7a45f334393093061116b197e3240065ff2d8"
  },
  "post": {
    "Jade": [{
      "indexes": { "data": 0, "gas": 0, "value": 0 },
      "hash": "0000000000000000000000000000000000000000000000000000000000000000",
      "logs": "0000000000000000000000000000000000000000000000000000000000000000",
      "expectException": null
    }]
  }
}`

func TestMorphForkNamesAreAvailableForStateTests(t *testing.T) {
	for _, fork := range []string{"Bernoulli", "Curie", "Morph203", "Viridian", "Emerald", "Jade"} {
		if _, _, err := GetChainConfig(fork); err != nil {
			t.Fatalf("expected fork %s to be available: %v", fork, err)
		}
	}
}

func TestStateTestWithoutTxBytesDoesNotPanic(t *testing.T) {
	var test StateTest
	if err := json.Unmarshal([]byte(morphMinimalStateTest), &test); err != nil {
		t.Fatal(err)
	}

	_, statedb, _, err := test.RunNoVerify(StateSubtest{Fork: "Jade", Index: 0}, vmConfigForTest(), false)
	if err != nil {
		t.Fatal(err)
	}
	if statedb == nil {
		t.Fatal("expected statedb")
	}
}

func vmConfigForTest() vm.Config {
	return vm.Config{}
}
