// Copyright 2017 The go-ethereum Authors
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

package params

import (
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/morph-l2/go-ethereum/params/forks"
	"github.com/stretchr/testify/require"
)

func TestCheckCompatible(t *testing.T) {
	type test struct {
		stored, new   *ChainConfig
		headBlock     uint64
		headTimestamp uint64
		wantErr       *ConfigCompatError
	}
	tests := []test{
		{stored: AllEthashProtocolChanges, new: AllEthashProtocolChanges, headBlock: 0, headTimestamp: 0, wantErr: nil},
		{stored: AllEthashProtocolChanges, new: AllEthashProtocolChanges, headBlock: 0, headTimestamp: uint64(time.Now().Unix()), wantErr: nil},
		{stored: AllEthashProtocolChanges, new: AllEthashProtocolChanges, headBlock: 100, wantErr: nil},
		{
			stored:    &ChainConfig{EIP150Block: big.NewInt(10)},
			new:       &ChainConfig{EIP150Block: big.NewInt(20)},
			headBlock: 9,
			wantErr:   nil,
		},
		{
			stored:    AllEthashProtocolChanges,
			new:       &ChainConfig{HomesteadBlock: nil},
			headBlock: 3,
			wantErr: &ConfigCompatError{
				What:          "Homestead fork block",
				StoredBlock:   big.NewInt(0),
				NewBlock:      nil,
				RewindToBlock: 0,
			},
		},
		{
			stored:    AllEthashProtocolChanges,
			new:       &ChainConfig{HomesteadBlock: big.NewInt(1)},
			headBlock: 3,
			wantErr: &ConfigCompatError{
				What:          "Homestead fork block",
				StoredBlock:   big.NewInt(0),
				NewBlock:      big.NewInt(1),
				RewindToBlock: 0,
			},
		},
		{
			stored:    &ChainConfig{HomesteadBlock: big.NewInt(30), EIP150Block: big.NewInt(10)},
			new:       &ChainConfig{HomesteadBlock: big.NewInt(25), EIP150Block: big.NewInt(20)},
			headBlock: 25,
			wantErr: &ConfigCompatError{
				What:          "EIP150 fork block",
				StoredBlock:   big.NewInt(10),
				NewBlock:      big.NewInt(20),
				RewindToBlock: 9,
			},
		},
		{
			stored:    &ChainConfig{ConstantinopleBlock: big.NewInt(30)},
			new:       &ChainConfig{ConstantinopleBlock: big.NewInt(30), PetersburgBlock: big.NewInt(30)},
			headBlock: 40,
			wantErr:   nil,
		},
		{
			stored:    &ChainConfig{ConstantinopleBlock: big.NewInt(30)},
			new:       &ChainConfig{ConstantinopleBlock: big.NewInt(30), PetersburgBlock: big.NewInt(31)},
			headBlock: 40,
			wantErr: &ConfigCompatError{
				What:          "Petersburg fork block",
				StoredBlock:   nil,
				NewBlock:      big.NewInt(31),
				RewindToBlock: 30,
			},
		},
		{
			stored:        &ChainConfig{Morph203Time: NewUint64(10)},
			new:           &ChainConfig{Morph203Time: NewUint64(20)},
			headTimestamp: 9,
			wantErr:       nil,
		},
		{
			stored:        &ChainConfig{Morph203Time: NewUint64(10)},
			new:           &ChainConfig{Morph203Time: NewUint64(20)},
			headTimestamp: 25,
			wantErr: &ConfigCompatError{
				What:         "Morph203Time fork timestamp",
				StoredTime:   NewUint64(10),
				NewTime:      NewUint64(20),
				RewindToTime: 9,
			},
		},
	}

	for _, test := range tests {
		err := test.stored.CheckCompatible(test.new, test.headBlock, test.headTimestamp)
		if !reflect.DeepEqual(err, test.wantErr) {
			t.Errorf("error mismatch:\nstored: %v\nnew: %v\nheadBlock: %v\nheadTimestamp: %v\nerr: %v\nwant: %v", test.stored, test.new, test.headBlock, test.headTimestamp, err, test.wantErr)
		}
	}
}

func TestConfigRules(t *testing.T) {
	c := &ChainConfig{
		LondonBlock:  new(big.Int),
		Morph203Time: NewUint64(500),
	}
	var stamp uint64
	if r := c.Rules(big.NewInt(0), stamp); r.IsMorph203 {
		t.Errorf("expected %v to not be morph203", stamp)
	}
	stamp = 500
	if r := c.Rules(big.NewInt(0), stamp); !r.IsMorph203 {
		t.Errorf("expected %v to be morph203", stamp)
	}
	stamp = math.MaxInt64
	if r := c.Rules(big.NewInt(0), stamp); !r.IsMorph203 {
		t.Errorf("expected %v to be morph203", stamp)
	}
}

// TestIsAmsterdam covers the three semantic states of an optional timestamp
// fork: unset (nil), set but not yet reached, and set and reached.
func TestIsAmsterdam(t *testing.T) {
	tests := []struct {
		name          string
		amsterdamTime *uint64
		headTime      uint64
		want          bool
	}{
		{name: "nil -> disabled", amsterdamTime: nil, headTime: 123, want: false},
		{name: "future -> not yet active", amsterdamTime: NewUint64(1000), headTime: 999, want: false},
		{name: "at activation -> active", amsterdamTime: NewUint64(1000), headTime: 1000, want: true},
		{name: "past activation -> active", amsterdamTime: NewUint64(1000), headTime: 5000, want: true},
		{name: "genesis activated", amsterdamTime: NewUint64(0), headTime: 0, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &ChainConfig{AmsterdamTime: tc.amsterdamTime}
			require.Equal(t, tc.want, c.IsAmsterdam(tc.headTime))
			// Rules should mirror the predicate.
			require.Equal(t, tc.want, c.Rules(big.NewInt(0), tc.headTime).IsAmsterdam)
		})
	}
}

// TestLatestForkIncludesJadeForkAndAmsterdam verifies that both JadeFork (which
// previously was missing from LatestFork) and Amsterdam report correctly, and
// that Amsterdam is ranked strictly after JadeFork.
func TestLatestForkIncludesJadeForkAndAmsterdam(t *testing.T) {
	tests := []struct {
		name  string
		cfg   *ChainConfig
		time  uint64
		want  forks.Fork
	}{
		{
			name: "no time forks yet -> Curie",
			cfg:  &ChainConfig{},
			time: 0,
			want: forks.Curie,
		},
		{
			name: "only Morph203 active",
			cfg:  &ChainConfig{Morph203Time: NewUint64(10)},
			time: 20,
			want: forks.Morph203,
		},
		{
			name: "Emerald active",
			cfg:  &ChainConfig{Morph203Time: NewUint64(1), ViridianTime: NewUint64(2), EmeraldTime: NewUint64(3)},
			time: 10,
			want: forks.Emerald,
		},
		{
			name: "JadeFork active (backfilled)",
			cfg:  &ChainConfig{Morph203Time: NewUint64(1), ViridianTime: NewUint64(2), EmeraldTime: NewUint64(3), JadeForkTime: NewUint64(4)},
			time: 10,
			want: forks.JadeFork,
		},
		{
			name: "Amsterdam overrides JadeFork",
			cfg: &ChainConfig{
				Morph203Time: NewUint64(1), ViridianTime: NewUint64(2), EmeraldTime: NewUint64(3),
				JadeForkTime: NewUint64(4), AmsterdamTime: NewUint64(5),
			},
			time: 10,
			want: forks.Amsterdam,
		},
		{
			name: "Amsterdam in the future -> returns JadeFork",
			cfg: &ChainConfig{
				Morph203Time: NewUint64(1), ViridianTime: NewUint64(2), EmeraldTime: NewUint64(3),
				JadeForkTime: NewUint64(4), AmsterdamTime: NewUint64(100),
			},
			time: 10,
			want: forks.JadeFork,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.cfg.LatestFork(tc.time))
		})
	}
}

// TestTimestampForkLookup verifies the Timestamp() switch returns the correct
// pointer for every time-based fork, including the JadeFork and Amsterdam
// entries added in the current change.
func TestTimestampForkLookup(t *testing.T) {
	cfg := &ChainConfig{
		Morph203Time:  NewUint64(11),
		ViridianTime:  NewUint64(22),
		EmeraldTime:   NewUint64(33),
		JadeForkTime:  NewUint64(44),
		AmsterdamTime: NewUint64(55),
	}
	cases := map[forks.Fork]*uint64{
		forks.Morph203:  cfg.Morph203Time,
		forks.Viridian:  cfg.ViridianTime,
		forks.Emerald:   cfg.EmeraldTime,
		forks.JadeFork:  cfg.JadeForkTime,
		forks.Amsterdam: cfg.AmsterdamTime,
	}
	for fork, want := range cases {
		t.Run(fork.String(), func(t *testing.T) {
			require.Equal(t, want, cfg.Timestamp(fork))
		})
	}
	// Non-timestamp fork must return nil.
	require.Nil(t, cfg.Timestamp(forks.Berlin))
}

// TestCheckCompatibleAmsterdamTimestamp asserts that switching the Amsterdam
// activation after the head is past the stored time is rejected.
func TestCheckCompatibleAmsterdamTimestamp(t *testing.T) {
	stored := &ChainConfig{AmsterdamTime: NewUint64(100)}
	newer := &ChainConfig{AmsterdamTime: NewUint64(200)}

	// Head is past the stored amsterdam timestamp -> incompatible.
	err := stored.CheckCompatible(newer, 0, 150)
	require.NotNil(t, err)
	require.Equal(t, "AmsterdamTime fork timestamp", err.What)
	require.Equal(t, NewUint64(100), err.StoredTime)
	require.Equal(t, NewUint64(200), err.NewTime)

	// Head still before both timestamps -> compatible.
	require.Nil(t, stored.CheckCompatible(newer, 0, 50))
}

// TestAmsterdamForkOrdering asserts the fork ordering check accepts Amsterdam
// after JadeFork and rejects a reversed ordering.
func TestAmsterdamForkOrdering(t *testing.T) {
	ok := &ChainConfig{
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		ArchimedesBlock:     big.NewInt(0),
		ShanghaiBlock:       big.NewInt(0),
		BernoulliBlock:      big.NewInt(0),
		CurieBlock:          big.NewInt(0),
		Morph203Time:        NewUint64(1),
		ViridianTime:        NewUint64(2),
		EmeraldTime:         NewUint64(3),
		JadeForkTime:        NewUint64(4),
		AmsterdamTime:       NewUint64(5),
	}
	require.NoError(t, ok.CheckConfigForkOrder())

	bad := *ok
	bad.JadeForkTime = NewUint64(10) // > AmsterdamTime(5) - invalid.
	require.Error(t, bad.CheckConfigForkOrder())
}

func TestTimestampCompatError(t *testing.T) {
	require.Equal(t, new(ConfigCompatError).Error(), "")

	errWhat := "Morph203 fork timestamp"
	require.Equal(t, newTimestampCompatError(errWhat, nil, NewUint64(1681338455)).Error(),
		"mismatching Morph203 fork timestamp in database (have timestamp nil, want timestamp 1681338455, rewindto timestamp 1681338454)")

	require.Equal(t, newTimestampCompatError(errWhat, NewUint64(1681338455), nil).Error(),
		"mismatching Morph203 fork timestamp in database (have timestamp 1681338455, want timestamp nil, rewindto timestamp 1681338454)")

	require.Equal(t, newTimestampCompatError(errWhat, NewUint64(1681338455), NewUint64(600624000)).Error(),
		"mismatching Morph203 fork timestamp in database (have timestamp 1681338455, want timestamp 600624000, rewindto timestamp 600623999)")

	require.Equal(t, newTimestampCompatError(errWhat, NewUint64(0), NewUint64(1681338455)).Error(),
		"mismatching Morph203 fork timestamp in database (have timestamp 0, want timestamp 1681338455, rewindto timestamp 0)")
}
