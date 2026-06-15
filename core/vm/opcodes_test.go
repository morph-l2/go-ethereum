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
