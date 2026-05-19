package tests

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
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

func TestStateTestEstimatesL1DataFeeWithoutTxBytes(t *testing.T) {
	var test StateTest
	if err := json.Unmarshal([]byte(l1FeeWithoutTxBytesStateTest), &test); err != nil {
		t.Fatal(err)
	}

	_, statedb, _, result, err := test.RunNoVerifyWithResult(StateSubtest{Fork: "Jade", Index: 0}, vmConfigForTest(), false)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected execution result")
	}

	caller := common.HexToAddress("0xa94f5374fce5edbc8e2a8697c15331677e6ebf0b")
	initial := big.NewInt(1_000_000_000_000_000_000)
	spent := new(big.Int).Sub(initial, statedb.GetBalance(caller))
	if spent.Cmp(big.NewInt(21_000)) <= 0 {
		t.Fatalf("expected state test without txbytes to charge non-zero L1 data fee, spent %s", spent)
	}
}

func TestStateTestIgnoresMorphFeeFieldsOnStandardTypedTransactions(t *testing.T) {
	var test StateTest
	if err := json.Unmarshal([]byte(standardTypedTxWithMorphFeeFieldsStateTest), &test); err != nil {
		t.Fatal(err)
	}

	_, statedb, _, result, err := test.RunNoVerifyWithResult(StateSubtest{Fork: "Jade", Index: 0}, vmConfigForTest(), false)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.GasUsed == 0 {
		t.Fatalf("expected typed transaction to execute EVM bytecode, result=%#v", result)
	}
	got := statedb.GetState(common.HexToAddress("0x0000000000000000000000000000000000005700"), common.Hash{})
	if got != common.BigToHash(big.NewInt(0x2a)) {
		t.Fatalf("expected contract storage slot 0 to be written, got %s", got)
	}
}

func vmConfigForTest() vm.Config {
	return vm.Config{}
}

func TestStateTestOutOfBoundsIndexReturnsErrorNotPanic(t *testing.T) {
	// post.indexes.data == len(tx.data) must surface as a schema error,
	// not panic at the indexing line. Regression for the >= vs > check
	// in state_test_util.go::toMessage.
	var test StateTest
	if err := json.Unmarshal([]byte(outOfBoundsIndexStateTest), &test); err != nil {
		t.Fatal(err)
	}

	_, _, _, _, err := test.RunNoVerifyWithResult(StateSubtest{Fork: "Jade", Index: 0}, vmConfigForTest(), false)
	if err == nil {
		t.Fatal("expected out-of-bounds index to return an error, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "out of bounds") {
		t.Fatalf("expected an out-of-bounds error message, got: %v", got)
	}
}

func TestStateTestShortAccessListDoesNotPanic(t *testing.T) {
	var test StateTest
	if err := json.Unmarshal([]byte(shortAccessListStateTest), &test); err != nil {
		t.Fatal(err)
	}

	_, statedb, _, result, err := test.RunNoVerifyWithResult(StateSubtest{Fork: "Jade", Index: 0}, vmConfigForTest(), false)
	if err != nil {
		t.Fatal(err)
	}
	if statedb == nil {
		t.Fatal("expected statedb")
	}
	if result == nil {
		t.Fatal("expected execution result")
	}
	contract := common.HexToAddress("0x0000000000000000000000000000000000005701")
	got := statedb.GetState(contract, common.Hash{})
	if got != common.BigToHash(big.NewInt(0x2a)) {
		t.Fatalf("expected contract storage slot 0 to be written, got %s", got)
	}
}

const outOfBoundsIndexStateTest = `{
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
      "indexes": { "data": 1, "gas": 0, "value": 0 },
      "hash": "0000000000000000000000000000000000000000000000000000000000000000",
      "logs": "0000000000000000000000000000000000000000000000000000000000000000",
      "expectException": null
    }]
  }
}`

const shortAccessListStateTest = `{
  "env": {
    "currentCoinbase": "0000000000000000000000000000000000000000",
    "currentDifficulty": "0x0",
    "currentGasLimit": "0x989680",
    "currentNumber": "0x1",
    "currentTimestamp": "0x1",
    "currentBaseFee": "0x1"
  },
  "pre": {
    "0000000000000000000000000000000000005701": {
      "balance": "0x0",
      "nonce": "0x0",
      "code": "0x602a60005500",
      "storage": {}
    },
    "a94f5374fce5edbc8e2a8697c15331677e6ebf0b": {
      "balance": "0xde0b6b3a7640000",
      "nonce": "0x0",
      "code": "0x",
      "storage": {}
    }
  },
  "transaction": {
    "type": "0x2",
    "nonce": "0x0",
    "gasLimit": ["0x7a1200"],
    "to": "0x0000000000000000000000000000000000005701",
    "value": ["0x0"],
    "data": ["0x", "0x"],
    "maxFeePerGas": "0x10",
    "maxPriorityFeePerGas": "0x1",
    "accessLists": [null],
    "secretKey": "0x45a915e4d060149eb4365960e6a7a45f334393093061116b197e3240065ff2d8"
  },
  "post": {
    "Jade": [{
      "indexes": { "data": 1, "gas": 0, "value": 0 },
      "hash": "0000000000000000000000000000000000000000000000000000000000000000",
      "logs": "0000000000000000000000000000000000000000000000000000000000000000",
      "expectException": null
    }]
  }
}`

const l1FeeWithoutTxBytesStateTest = `{
  "env": {
    "currentCoinbase": "0000000000000000000000000000000000000000",
    "currentDifficulty": "0x0",
    "currentGasLimit": "0x989680",
    "currentNumber": "0x1",
    "currentTimestamp": "0x1",
    "currentBaseFee": "0x0"
  },
  "pre": {
    "00000000000000000000000000000000000000f1": {
      "balance": "0x0",
      "nonce": "0x0",
      "code": "0x00",
      "storage": {}
    },
    "530000000000000000000000000000000000000f": {
      "balance": "0x0",
      "nonce": "0x0",
      "code": "0x60006000f3",
      "storage": {
        "0x0000000000000000000000000000000000000000000000000000000000000001": "0x3b9aca00",
        "0x0000000000000000000000000000000000000000000000000000000000000006": "0x01",
        "0x0000000000000000000000000000000000000000000000000000000000000007": "0x3b9aca00",
        "0x0000000000000000000000000000000000000000000000000000000000000008": "0x01",
        "0x0000000000000000000000000000000000000000000000000000000000000009": "0x01"
      }
    },
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
    "to": "0x00000000000000000000000000000000000000f1",
    "value": ["0x0"],
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

const standardTypedTxWithMorphFeeFieldsStateTest = `{
  "env": {
    "currentCoinbase": "0000000000000000000000000000000000000000",
    "currentDifficulty": "0x0",
    "currentGasLimit": "0x989680",
    "currentNumber": "0x1",
    "currentTimestamp": "0x1",
    "currentBaseFee": "0x0"
  },
  "pre": {
    "0000000000000000000000000000000000005700": {
      "balance": "0x0",
      "nonce": "0x0",
      "code": "0x602a60005500",
      "storage": {}
    },
    "a94f5374fce5edbc8e2a8697c15331677e6ebf0b": {
      "balance": "0xde0b6b3a7640000",
      "nonce": "0x0",
      "code": "0x",
      "storage": {}
    }
  },
  "transaction": {
    "type": "0x2",
    "nonce": "0x0",
    "gasLimit": ["0x7a1200"],
    "to": "0x0000000000000000000000000000000000005700",
    "value": ["0x0"],
    "data": ["0x"],
    "maxFeePerGas": "0x10",
    "maxPriorityFeePerGas": "0x1",
    "accessLists": [null],
    "feeTokenID": "0x1",
    "feeLimit": "0xf4240",
    "version": "0x0",
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
