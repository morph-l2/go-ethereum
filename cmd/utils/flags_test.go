// Copyright 2019 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

// Package utils contains internal helper functions for go-ethereum commands.
package utils

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func Test_SplitTagsFlag(t *testing.T) {
	tests := []struct {
		name string
		args string
		want map[string]string
	}{
		{
			"2 tags case",
			"host=localhost,bzzkey=123",
			map[string]string{
				"host":   "localhost",
				"bzzkey": "123",
			},
		},
		{
			"1 tag case",
			"host=localhost123",
			map[string]string{
				"host": "localhost123",
			},
		},
		{
			"empty case",
			"",
			map[string]string{},
		},
		{
			"garbage",
			"smth=smthelse=123",
			map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SplitTagsFlag(tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitTagsFlag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateTxSyncTimeouts(t *testing.T) {
	tests := []struct {
		name           string
		defaultTimeout time.Duration
		maxTimeout     time.Duration
		wantErr        string
	}{
		{
			name:           "valid",
			defaultTimeout: 20 * time.Second,
			maxTimeout:     time.Minute,
		},
		{
			name:           "zero default",
			defaultTimeout: 0,
			maxTimeout:     time.Minute,
			wantErr:        "--rpc.txsync.defaulttimeout must be positive",
		},
		{
			name:           "negative max",
			defaultTimeout: 20 * time.Second,
			maxTimeout:     -time.Second,
			wantErr:        "--rpc.txsync.maxtimeout must be positive",
		},
		{
			name:           "default exceeds max",
			defaultTimeout: 2 * time.Minute,
			maxTimeout:     time.Minute,
			wantErr:        "--rpc.txsync.defaulttimeout must be less than or equal to --rpc.txsync.maxtimeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTxSyncTimeouts(tt.defaultTimeout, tt.maxTimeout)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}
