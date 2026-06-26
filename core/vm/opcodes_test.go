// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.

package vm

import "testing"

func TestOpCodeStringForImplementedRecentOpcodes(t *testing.T) {
	tests := []struct {
		op   OpCode
		name string
	}{
		{BASEFEE, "BASEFEE"},
		{TLOAD, "TLOAD"},
		{TSTORE, "TSTORE"},
		{MCOPY, "MCOPY"},
	}

	for _, test := range tests {
		if got := test.op.String(); got != test.name {
			t.Fatalf("%#x.String() = %q, want %q", byte(test.op), got, test.name)
		}
		if got := StringToOp(test.name); got != test.op {
			t.Fatalf("StringToOp(%q) = %#x, want %#x", test.name, byte(got), byte(test.op))
		}
	}
}

// TestOpCodeStringMapsBidirectional walks the whole opCodeToString map and its
// reverse stringToOp map to guarantee they stay in lock-step: every opcode
// round-trips through String/StringToOp, names are unique, and neither map has
// an entry the other lacks. This catches a future opcode being added to only
// one of the two maps.
func TestOpCodeStringMapsBidirectional(t *testing.T) {
	seenNames := make(map[string]OpCode, len(opCodeToString))
	for op, name := range opCodeToString {
		if name == "" {
			t.Errorf("opcode %#x maps to empty name", byte(op))
			continue
		}
		if prev, dup := seenNames[name]; dup {
			t.Errorf("name %q maps from both %#x and %#x", name, byte(prev), byte(op))
		}
		seenNames[name] = op

		if got := op.String(); got != name {
			t.Errorf("%#x.String() = %q, want %q", byte(op), got, name)
		}
		if got := StringToOp(name); got != op {
			t.Errorf("StringToOp(%q) = %#x, want %#x (missing/wrong in stringToOp)", name, byte(got), byte(op))
		}
	}

	for name, op := range stringToOp {
		if got := opCodeToString[op]; got != name {
			t.Errorf("stringToOp[%q] = %#x but opCodeToString[%#x] = %q", name, byte(op), byte(op), got)
		}
	}

	if len(opCodeToString) != len(stringToOp) {
		t.Errorf("map size mismatch: opCodeToString=%d stringToOp=%d", len(opCodeToString), len(stringToOp))
	}
}
