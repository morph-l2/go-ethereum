package types

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAltToEthFloorRounding asserts AltToEth rounds DOWN. A token balance whose
// ETH-equivalent has a non-zero remainder must not be rounded up: rounding up
// would credit more ETH budget than the user can actually pay for at execution
// (where EthToAlt rounds up).
func TestAltToEthFloorRounding(t *testing.T) {
	cases := []struct {
		name               string
		erc20, rate, scale int64
		want               int64
	}{
		{"exact division, no remainder", 4, 2, 2, 4}, // 4*2/2 = 4
		{"remainder rounds down", 5, 3, 2, 7},        // 15/2 = 7.5 -> floor 7 (was 8 under ceiling)
		{"remainder rounds down 2", 7, 3, 2, 10},     // 21/2 = 10.5 -> floor 10
		{"scale larger than product", 1, 1, 4, 0},    // 1/4 = 0.25 -> floor 0
		{"zero balance", 0, 3, 2, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := AltToEth(big.NewInt(c.erc20), big.NewInt(c.rate), big.NewInt(c.scale))
			require.NoError(t, err)
			require.Zerof(t, got.Cmp(big.NewInt(c.want)), "got %s, want %d", got, c.want)
		})
	}
}

// TestEthToAltCeilRounding is a regression guard that the charging direction
// (EthToAlt) still rounds UP — the fee gate must never under-charge.
func TestEthToAltCeilRounding(t *testing.T) {
	cases := []struct {
		name                     string
		eth, rate, scale         int64
		want, wantRoundingCredit int64
	}{
		{"exact division, no remainder", 6, 3, 2, 4, 0}, // 6*2/3 = 4
		{"remainder rounds up", 5, 3, 2, 4, 2},          // 10/3 rem 1 -> ceil 4, credit 3-1
		{"remainder rounds up 2", 7, 3, 2, 5, 1},        // 14/3 rem 2 -> ceil 5, credit 3-2
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, credit, err := EthToAlt(big.NewInt(c.eth), big.NewInt(c.rate), big.NewInt(c.scale))
			require.NoError(t, err)
			require.Zerof(t, got.Cmp(big.NewInt(c.want)), "got %s, want %d", got, c.want)
			require.Zerof(t, credit.Cmp(big.NewInt(c.wantRoundingCredit)), "credit %s, want %d", credit, c.wantRoundingCredit)
		})
	}
}

// TestAltToEthIsExactInverseOfEthToAlt verifies the load-bearing invariant: for
// any token balance a, AltToEth(a) is EXACTLY the maximum ETH fee the balance
// can cover under EthToAlt-ceiling charging. That is, EthToAlt(AltToEth(a)) <= a
// (affordable) while EthToAlt(AltToEth(a)+1) > a (one wei more is not). This is
// what makes the estimation allowance agree with execution with no gap and no
// over-credit.
func TestAltToEthIsExactInverseOfEthToAlt(t *testing.T) {
	rates := []int64{1, 2, 3, 5, 7}
	scales := []int64{1, 2, 3, 4, 6}
	for _, rate := range rates {
		for _, scale := range scales {
			for a := int64(0); a <= 50; a++ {
				bal := big.NewInt(a)
				r := big.NewInt(rate)
				s := big.NewInt(scale)

				ethBudget, err := AltToEth(bal, r, s)
				require.NoError(t, err)

				costAtBudget, _, err := EthToAlt(ethBudget, r, s)
				require.NoError(t, err)
				require.True(t, costAtBudget.Cmp(bal) <= 0,
					"AltToEth(%d) budget %s must be affordable: EthToAlt=%s > balance %d (rate=%d scale=%d)",
					a, ethBudget, costAtBudget, a, rate, scale)

				costAboveBudget, _, err := EthToAlt(new(big.Int).Add(ethBudget, big.NewInt(1)), r, s)
				require.NoError(t, err)
				require.True(t, costAboveBudget.Cmp(bal) > 0,
					"AltToEth(%d) must be the MAX affordable budget: budget+1 (%s) still affordable at balance %d (rate=%d scale=%d)",
					a, new(big.Int).Add(ethBudget, big.NewInt(1)), a, rate, scale)
			}
		}
	}
}

// TestTokenFeeConversionInvalidArgs covers the guard clauses for both directions.
func TestTokenFeeConversionInvalidArgs(t *testing.T) {
	valid := big.NewInt(1)
	for _, tc := range []struct {
		name        string
		rate, scale *big.Int
	}{
		{"nil rate", nil, valid},
		{"zero rate", big.NewInt(0), valid},
		{"negative rate", big.NewInt(-1), valid},
		{"nil scale", valid, nil},
		{"zero scale", valid, big.NewInt(0)},
		{"negative scale", valid, big.NewInt(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AltToEth(big.NewInt(10), tc.rate, tc.scale)
			require.Error(t, err)
			_, _, err = EthToAlt(big.NewInt(10), tc.rate, tc.scale)
			require.Error(t, err)
			_, err = EthToAltFloor(big.NewInt(10), big.NewInt(0), tc.rate, tc.scale)
			require.Error(t, err)
		})
	}
}

func TestEthToAltFloorRounding(t *testing.T) {
	cases := []struct {
		name                     string
		eth, credit, rate, scale int64
		want                     int64
	}{
		{"exact division, no credit", 6, 0, 3, 2, 4},
		{"remainder rounds down", 5, 0, 3, 2, 3},
		{"credit carries into refund", 5, 2, 3, 2, 4},
		{"zero amount and credit", 0, 0, 3, 2, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := EthToAltFloor(
				big.NewInt(c.eth),
				big.NewInt(c.credit),
				big.NewInt(c.rate),
				big.NewInt(c.scale),
			)
			require.NoError(t, err)
			require.Zerof(t, got.Cmp(big.NewInt(c.want)), "got %s, want %d", got, c.want)
		})
	}
}

// Dual ceil(prepaid)-ceil(refund) can undercharge vs the theoretical net ETH
// fee. The WBTC oracle sample (net 4_902_816_120_808 wei, ~15.83 sat) reproduces
// that; carrying prepaid rounding credit into a floored refund produces 16 sat.
func TestCeilPrepaidCeilRefundCanUndercharge(t *testing.T) {
	rate := big.NewInt(309631617993)
	scale := big.NewInt(1)
	netEth := big.NewInt(4902816120808)
	refundEth := new(big.Int).Add(new(big.Int).Mul(rate, big.NewInt(84)), big.NewInt(1))
	prepaidEth := new(big.Int).Add(netEth, refundEth)

	prepaid, roundingCredit, err := EthToAlt(prepaidEth, rate, scale)
	require.NoError(t, err)
	refundCeil, _, err := EthToAlt(refundEth, rate, scale)
	require.NoError(t, err)
	refundWithCredit, err := EthToAltFloor(refundEth, roundingCredit, rate, scale)
	require.NoError(t, err)

	netCeilCeil := new(big.Int).Sub(prepaid, refundCeil)
	netWithCredit := new(big.Int).Sub(prepaid, refundWithCredit)
	t.Logf("theoretical≈15.83 ceil-ceil=%s credit-carry=%s", netCeilCeil, netWithCredit)

	require.Equal(t, int64(15), netCeilCeil.Int64(), "dual ceil undercharges this fixture")
	require.Equal(t, int64(16), netWithCredit.Int64(), "credit carry must not undercharge")
}

func TestRoundingCreditEqualsCeilOfNetFee(t *testing.T) {
	for rate := int64(1); rate <= 10; rate++ {
		for scale := int64(1); scale <= 10; scale++ {
			r := big.NewInt(rate)
			s := big.NewInt(scale)
			for prepaidEth := int64(1); prepaidEth <= 50; prepaidEth++ {
				for refundEth := int64(0); refundEth <= prepaidEth; refundEth++ {
					prepaid := big.NewInt(prepaidEth)
					refund := big.NewInt(refundEth)

					prepaidTokens, credit, err := EthToAlt(prepaid, r, s)
					require.NoError(t, err)
					refundTokens, err := EthToAltFloor(refund, credit, r, s)
					require.NoError(t, err)
					netTokens := new(big.Int).Sub(prepaidTokens, refundTokens)

					netEth := new(big.Int).Sub(prepaid, refund)
					want, _, err := EthToAlt(netEth, r, s)
					require.NoError(t, err)
					require.Equalf(t, 0, netTokens.Cmp(want),
						"rate=%d scale=%d prepaid=%d refund=%d got=%s want=%s",
						rate, scale, prepaidEth, refundEth, netTokens, want)
				}
			}
		}
	}
}
