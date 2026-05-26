package state

import (
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/tracing"
)

func TestSetCodeNoChangeDoesNotHook(t *testing.T) {
	statedb, err := New(common.Hash{}, NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	var calls int
	hooked := NewHookedState(statedb, &tracing.Hooks{
		OnCodeChange: func(common.Address, common.Hash, []byte, common.Hash, []byte) {
			calls++
		},
	})

	code := []byte{1, 2, 3}
	hooked.SetCode(addr, code)
	if calls != 1 {
		t.Fatalf("first SetCode should hook once, got %d", calls)
	}
	hooked.SetCode(addr, append([]byte(nil), code...))
	if calls != 1 {
		t.Fatalf("unchanged SetCode should not hook, got %d", calls)
	}
	hooked.SetCode(addr, nil)
	if calls != 2 {
		t.Fatalf("clearing code should hook once, got %d", calls)
	}
	hooked.SetCode(addr, nil)
	if calls != 2 {
		t.Fatalf("repeated empty SetCode should not hook, got %d", calls)
	}
}
