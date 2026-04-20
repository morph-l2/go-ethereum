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

package metrics

import (
	"testing"
	"time"
)

// TestDefaultInfluxDBInterval verifies that DefaultConfig.InfluxDBInterval
// defaults to 10 seconds, matching the previously hard-coded behavior in
// SetupMetrics. This guards against accidental default changes that would
// silently alter operators' metrics cadence. See upstream PR #33767.
func TestDefaultInfluxDBInterval(t *testing.T) {
	if got, want := DefaultConfig.InfluxDBInterval, 10*time.Second; got != want {
		t.Fatalf("DefaultConfig.InfluxDBInterval = %v, want %v", got, want)
	}
}

// TestConfigInfluxDBIntervalCustom verifies that a user-provided interval is
// preserved verbatim on a Config value and does not leak global state.
func TestConfigInfluxDBIntervalCustom(t *testing.T) {
	cfg := DefaultConfig
	cfg.InfluxDBInterval = 5 * time.Second
	if cfg.InfluxDBInterval != 5*time.Second {
		t.Fatalf("custom interval not preserved: got %v", cfg.InfluxDBInterval)
	}
	// The global default must remain unchanged.
	if DefaultConfig.InfluxDBInterval != 10*time.Second {
		t.Fatalf("DefaultConfig was mutated through local copy: got %v", DefaultConfig.InfluxDBInterval)
	}
}

func TestValidateInfluxDBInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		wantErr  bool
	}{
		{name: "positive", interval: 5 * time.Second},
		{name: "zero_rejected", interval: 0, wantErr: true},
		{name: "negative_rejected", interval: -1 * time.Second, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInfluxDBInterval(tt.interval)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for interval=%v", tt.interval)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for interval=%v: %v", tt.interval, err)
			}
		})
	}
}
