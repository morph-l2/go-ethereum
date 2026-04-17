// Copyright 2024 The go-ethereum Authors
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

package utils

import (
	"flag"
	"testing"

	"gopkg.in/urfave/cli.v1"

	"github.com/morph-l2/go-ethereum/eth/ethconfig"
)

// newFlagContext builds a minimal *cli.Context that exposes both
// MaxBlockRangeFlag and RPCRangeLimitFlag, parsing the provided args in the
// same way urfave/cli.v1 does during command-line dispatch.
func newFlagContext(t *testing.T, args []string) *cli.Context {
	t.Helper()
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	MaxBlockRangeFlag.Apply(set)
	RPCRangeLimitFlag.Apply(set)
	if err := set.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return cli.NewContext(nil, set, nil)
}

// TestSetMaxBlockRangeResolution verifies the alias semantics between the
// legacy --rpc.getlogs.maxrange flag and the upstream-aligned
// --rpc.rangelimit flag introduced for P3-4(B). The rules are:
//
//  1. neither flag set  -> MaxBlockRange = -1 (preserve historical default)
//  2. only legacy flag  -> MaxBlockRange mirrors the user-supplied value
//     verbatim (including -1 which is the documented "unlimited" sentinel)
//  3. only new flag     -> non-positive values are coerced to -1, positive
//     values are stored as-is. This matches upstream's "0 = unlimited"
//     semantic while keeping morph's int64 field shape intact.
//  4. both flags set    -> --rpc.rangelimit wins so operators can opt into
//     the upstream name without scrubbing their existing launch scripts.
func TestSetMaxBlockRangeResolution(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int64
	}{
		{"default_unlimited", nil, -1},
		{"legacy_explicit_unlimited", []string{"-rpc.getlogs.maxrange", "-1"}, -1},
		{"legacy_bounded", []string{"-rpc.getlogs.maxrange", "1000"}, 1000},
		{"new_flag_zero_is_unlimited", []string{"-rpc.rangelimit", "0"}, -1},
		{"new_flag_negative_is_unlimited", []string{"-rpc.rangelimit", "-5"}, -1},
		{"new_flag_bounded", []string{"-rpc.rangelimit", "500"}, 500},
		{"both_set_new_wins_over_legacy", []string{
			"-rpc.getlogs.maxrange", "999",
			"-rpc.rangelimit", "500",
		}, 500},
		{"both_set_new_unlimited_wins", []string{
			"-rpc.getlogs.maxrange", "999",
			"-rpc.rangelimit", "0",
		}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newFlagContext(t, tt.args)
			cfg := &ethconfig.Config{}
			setMaxBlockRange(ctx, cfg)
			if cfg.MaxBlockRange != tt.want {
				t.Fatalf("MaxBlockRange = %d, want %d (args=%v)", cfg.MaxBlockRange, tt.want, tt.args)
			}
		})
	}
}
