package core

import (
	"math/big"
	"testing"

	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/vm"
)

func TestStartSystemCallTraceFallsBackToLegacyHook(t *testing.T) {
	t.Parallel()

	var starts, ends int
	evm := &vm.EVM{
		Config: vm.Config{
			Tracer: &tracing.Hooks{
				OnSystemCallStart: func() { starts++ },
				OnSystemCallEnd:   func() { ends++ },
			},
		},
	}

	end := startSystemCallTrace(evm)
	if starts != 1 {
		t.Fatalf("unexpected legacy start count: %d", starts)
	}
	if end == nil {
		t.Fatal("expected end hook")
	}
	end()
	if ends != 1 {
		t.Fatalf("unexpected end count: %d", ends)
	}
}

func TestStartSystemCallTracePrefersV2Hook(t *testing.T) {
	t.Parallel()

	var legacyStarts, v2Starts int
	evm := &vm.EVM{
		Context: vm.BlockContext{
			Time:        big.NewInt(0),
			BlockNumber: big.NewInt(0),
		},
		Config: vm.Config{
			Tracer: &tracing.Hooks{
				OnSystemCallStart:   func() { legacyStarts++ },
				OnSystemCallStartV2: func(*tracing.VMContext) { v2Starts++ },
				OnSystemCallEnd:     func() {},
			},
		},
	}

	end := startSystemCallTrace(evm)
	if end == nil {
		t.Fatal("expected end hook")
	}
	if legacyStarts != 0 || v2Starts != 1 {
		t.Fatalf("unexpected hook counts: legacy=%d v2=%d", legacyStarts, v2Starts)
	}
}

func TestStartSystemCallTraceRequiresEndHook(t *testing.T) {
	t.Parallel()

	var legacyStarts, v2Starts int
	evm := &vm.EVM{
		Context: vm.BlockContext{
			Time:        big.NewInt(0),
			BlockNumber: big.NewInt(0),
		},
		Config: vm.Config{
			Tracer: &tracing.Hooks{
				OnSystemCallStart:   func() { legacyStarts++ },
				OnSystemCallStartV2: func(*tracing.VMContext) { v2Starts++ },
			},
		},
	}

	end := startSystemCallTrace(evm)
	if end != nil {
		t.Fatal("expected nil end hook")
	}
	if legacyStarts != 0 || v2Starts != 0 {
		t.Fatalf("unexpected hook counts without end hook: legacy=%d v2=%d", legacyStarts, v2Starts)
	}
}
