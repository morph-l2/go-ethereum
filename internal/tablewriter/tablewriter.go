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

// Package tablewriter is a minimal, self-contained stand-in for the
// third-party tablewriter library used by upstream go-ethereum, trimmed to
// the subset exercised by morph's CLI reporters. The public API mirrors
// upstream so call sites can be ported verbatim, but the rendering is
// backed by the standard library's text/tabwriter for alignment.
//
// This package is intentionally naive: it performs light validation at
// Render() time rather than buffering per-row diagnostics, and it panics
// only for programmer errors (nil writer). Operators receive readable
// errors for common misuse (missing headers, mismatched column counts).
package tablewriter

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode/utf8"
)

// Table accumulates headers, data rows, and an optional footer, and
// renders them as an aligned text table through text/tabwriter.
//
// A Table is not safe for concurrent use by multiple goroutines.
type Table struct {
	out    io.Writer
	header []string
	footer []string
	rows   [][]string
}

// NewWriter returns a new Table that writes to w. Subsequent calls to
// SetHeader / AppendBulk / SetFooter populate the table; Render emits the
// aligned output to w in one pass.
func NewWriter(w io.Writer) *Table {
	if w == nil {
		panic("tablewriter: nil writer")
	}
	return &Table{out: w}
}

// SetHeader records the column headers. The first header also fixes the
// expected column count for subsequent rows and the optional footer.
func (t *Table) SetHeader(header []string) {
	t.header = append(t.header[:0], header...)
}

// SetFooter records the footer row shown after all data rows. Pass a nil
// slice to clear a previously set footer.
func (t *Table) SetFooter(footer []string) {
	if footer == nil {
		t.footer = nil
		return
	}
	t.footer = append(t.footer[:0], footer...)
}

// AppendBulk appends one or more data rows to the table.
//
// Each row must match the header column count; the check is deferred to
// Render so partially-built tables still round-trip through inspection.
// The input slice is deep-copied so later mutations by the caller do not
// affect the stored rows (matching the defensive behaviour of SetHeader and
// SetFooter).
func (t *Table) AppendBulk(rows [][]string) {
	snapshot := make([][]string, len(rows))
	for i, row := range rows {
		r := make([]string, len(row))
		copy(r, row)
		snapshot[i] = r
	}
	t.rows = append(t.rows, snapshot...)
}

// Render validates the accumulated state and writes the aligned table to
// the configured writer. Render returns an error when:
//
//   - no header has been set,
//   - any row has a different number of columns than the header,
//   - the footer (when set) has a different number of columns.
//
// Rendering is idempotent: call it multiple times to emit the same table
// repeatedly.
func (t *Table) Render() error {
	if len(t.header) == 0 {
		return errors.New("tablewriter: no header configured")
	}
	cols := len(t.header)
	for i, row := range t.rows {
		if len(row) != cols {
			return fmt.Errorf("tablewriter: row %d has %d columns, want %d", i, len(row), cols)
		}
	}
	if len(t.footer) > 0 && len(t.footer) != cols {
		return fmt.Errorf("tablewriter: footer has %d columns, want %d", len(t.footer), cols)
	}

	// Compute per-column max widths (in runes) across header, rows and
	// footer so the separator aligns with the actual column content.
	w := make([]int, cols)
	measure := func(cells []string) {
		for i, c := range cells {
			if n := utf8.RuneCountInString(c); n > w[i] {
				w[i] = n
			}
		}
	}
	measure(t.header)
	for _, row := range t.rows {
		measure(row)
	}
	if len(t.footer) > 0 {
		measure(t.footer)
	}
	sepParts := make([]string, cols)
	for i, n := range w {
		sepParts[i] = strings.Repeat("─", n)
	}
	sep := strings.Join(sepParts, "\t")

	tw := tabwriter.NewWriter(t.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(t.header, "\t"))
	fmt.Fprintln(tw, sep)
	for _, row := range t.rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	if len(t.footer) > 0 {
		fmt.Fprintln(tw, sep)
		fmt.Fprintln(tw, strings.Join(t.footer, "\t"))
	}
	return tw.Flush()
}
