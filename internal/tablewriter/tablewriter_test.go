// Copyright 2024 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package tablewriter

import (
	"bytes"
	"strings"
	"testing"
)

func TestTableWriterHappyPath(t *testing.T) {
	var buf bytes.Buffer
	table := NewWriter(&buf)
	table.SetHeader([]string{"A", "B", "C"})
	table.AppendBulk([][]string{
		{"x", "y", "z"},
		{"1", "2", "3"},
	})
	table.SetFooter([]string{"Total", "3", ""})
	if err := table.Render(); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "A") || !strings.Contains(out, "Total") {
		t.Fatalf("output missing header/footer rows: %q", out)
	}
}

func TestTableWriterMissingHeader(t *testing.T) {
	var buf bytes.Buffer
	table := NewWriter(&buf)
	table.AppendBulk([][]string{{"x", "y", "z"}})
	if err := table.Render(); err == nil {
		t.Fatal("expected error for missing header")
	}
}

func TestTableWriterMismatchedRow(t *testing.T) {
	var buf bytes.Buffer
	table := NewWriter(&buf)
	table.SetHeader([]string{"A", "B", "C"})
	table.AppendBulk([][]string{{"x", "y"}})
	if err := table.Render(); err == nil {
		t.Fatal("expected error for row column mismatch")
	}
}

func TestTableWriterMismatchedFooter(t *testing.T) {
	var buf bytes.Buffer
	table := NewWriter(&buf)
	table.SetHeader([]string{"A", "B"})
	table.SetFooter([]string{"only one"})
	if err := table.Render(); err == nil {
		t.Fatal("expected error for footer column mismatch")
	}
}

func TestTableWriterAppendBulkCumulative(t *testing.T) {
	var buf bytes.Buffer
	table := NewWriter(&buf)
	table.SetHeader([]string{"A"})
	table.AppendBulk([][]string{{"one"}})
	table.AppendBulk([][]string{{"two"}})
	if err := table.Render(); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "one") || !strings.Contains(out, "two") {
		t.Fatalf("AppendBulk must be cumulative, got %q", out)
	}
}

func TestTableWriterNilWriterPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil writer")
		}
	}()
	NewWriter(nil)
}

func TestTableWriterRenderIdempotent(t *testing.T) {
	var buf bytes.Buffer
	table := NewWriter(&buf)
	table.SetHeader([]string{"A"})
	table.AppendBulk([][]string{{"x"}})
	if err := table.Render(); err != nil {
		t.Fatalf("first render: %v", err)
	}
	first := buf.String()
	buf.Reset()
	if err := table.Render(); err != nil {
		t.Fatalf("second render: %v", err)
	}
	if buf.String() != first {
		t.Fatalf("render not idempotent: first=%q second=%q", first, buf.String())
	}
}
