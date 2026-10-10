package js

import (
	"math/big"
	"strings"
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/params"
)

func TestCyclicArrayJoin(t *testing.T) {
	const code = `{
		step: function() {},
		fault: function() {},
		result: function() { var a = []; a.push(a); return a.join(","); }
	}`
	_, err := runResult(t, code)
	if err == nil {
		t.Fatal("expected self-referential array join to fail")
	}
	if !strings.Contains(err.Error(), "result") {
		t.Fatalf("expected failure from result(), got %v", err)
	}
}

func TestArrayJoin(t *testing.T) {
	const code = `{
		step: function() {},
		fault: function() {},
		result: function() { return ["a", "b"].join("-"); }
	}`
	ret, err := runResult(t, code)
	if err != nil {
		t.Fatal(err)
	}
	if string(ret) != `"a-b"` {
		t.Fatalf("got %s", ret)
	}
}

func runResult(t *testing.T, code string) ([]byte, error) {
	t.Helper()
	tracer, err := newJsTracer(code, nil, nil, params.TestChainConfig)
	if err != nil {
		t.Fatal(err)
	}
	tx := types.NewTx(&types.LegacyTx{
		To:       &common.Address{},
		Gas:      21000,
		GasPrice: big.NewInt(0),
		Value:    big.NewInt(0),
	})
	env := &tracing.VMContext{
		BlockNumber: big.NewInt(0),
		Time:        0,
		BaseFee:     big.NewInt(0),
		Coinbase:    common.Address{},
		StateDB:     jsTestStateDB{},
	}
	tracer.OnTxStart(env, tx, common.Address{})
	return tracer.GetResult()
}
