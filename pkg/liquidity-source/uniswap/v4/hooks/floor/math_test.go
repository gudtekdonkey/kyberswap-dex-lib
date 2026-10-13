package floor

import (
	"testing"

	"github.com/holiman/uint256"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	uniswapv4 "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/uniswap/v4"
)

var testRates = Rates{
	SkimBuyFar: 100, SkimSellFar: 100, SkimBuyAtFloor: 0, SkimSellAtFloor: 500,
	CreatorBuy: 50, CreatorSell: 75, HolderBuy: 25, HolderSell: 0, TeamFee: 25,
}

func TestMarketTickAndBand(t *testing.T) {
	assert.Equal(t, -5, marketTick(-5, false))
	assert.Equal(t, 4, marketTick(-5, true), "a currency1 quote maps -tick - 1, not -tick")
	assert.Equal(t, 120, bandTicks(60))
	assert.Equal(t, 200, bandTicks(100))
	assert.Equal(t, 120, bandTicks(1))
}

// skimBpsAt must be FloorHookMath.skimBps at its corners and in between.
func TestSkimBpsAt(t *testing.T) {
	// at or inside the dear edge: the at-floor rate; at or past the ramp: the far rate
	assert.Equal(t, 0, skimBpsAt(true, 0, &testRates))
	assert.Equal(t, 0, skimBpsAt(true, -50, &testRates))
	assert.Equal(t, 100, skimBpsAt(true, skimRampTicks, &testRates))
	assert.Equal(t, 500, skimBpsAt(false, 0, &testRates))
	assert.Equal(t, 100, skimBpsAt(false, skimRampTicks+1, &testRates))
	// half way: closeness 10,000 - floor(8,047 * 10,000 / 16,094) = 5,000
	assert.Equal(t, 50, skimBpsAt(true, 8047, &testRates))
	assert.Equal(t, 300, skimBpsAt(false, 8047, &testRates))
	// one tick into the ramp: closeness 10,000 - floor(10,000 / 16,094) = 10,000, still the at-floor rate
	assert.Equal(t, 0, skimBpsAt(true, 1, &testRates))
	assert.Equal(t, 500, skimBpsAt(false, 1, &testRates))
	// a sell whose far rate is at or above its at-floor rate does not ramp
	flat := testRates
	flat.SkimSellAtFloor = 100
	assert.Equal(t, 100, skimBpsAt(false, 0, &flat))
	// a buy whose at-floor rate is above its far rate ramps the other way
	inverted := testRates
	inverted.SkimBuyAtFloor = 300
	assert.Equal(t, 300, skimBpsAt(true, 0, &inverted))
	assert.Equal(t, 200, skimBpsAt(true, 8047, &inverted))
	assert.Equal(t, 100, skimBpsAt(true, skimRampTicks, &inverted))
}

// gapSkimBps must be SingletonFloorEngine._gapSkim: 1x to 2,232 ticks, a linear ramp to 1.5x at 5,109, capped at
// 1,500 bps.
func TestGapSkimBps(t *testing.T) {
	const top = 10_000
	at := func(bps, gap int) int { return gapSkimBps(bps, true, top, top-gap) }

	// at and under 2,232 ticks: untouched, not even capped
	for _, gap := range []int{-500, 0, 2_232} {
		assert.Equal(t, []int{75, 100, 1_000, 1_600}, []int{at(75, gap), at(100, gap), at(1_000, gap), at(1_600, gap)})
	}
	// 2,233: m = 10,000 + floor(5,000 * 1 / 2,877) = 10,001, and a scaled skim is capped
	assert.Equal(t, []int{1_000, 1_500}, []int{at(1_000, 2_233), at(1_600, 2_233)})
	// half way, 3,670: m = 10,000 + floor(5,000 * 1,438 / 2,877) = 12,499
	assert.Equal(t, []int{93, 124, 1_249}, []int{at(75, 3_670), at(100, 3_670), at(1_000, 3_670)})
	// 5,108: m = 14,998; 5,109 and past: 15,000
	assert.Equal(t, 1_499, at(1_000, 5_108))
	for _, gap := range []int{5_109, 5_110, 20_000} {
		assert.Equal(t, []int{112, 150, 1_500, 1_500}, []int{at(75, gap), at(100, gap), at(1_000, gap), at(1_001, gap)})
	}
	// no cushion standing: untouched, whatever its edge says; a zero ramp stays zero
	assert.Equal(t, 1_000, gapSkimBps(1_000, false, top, top-20_000))
	assert.Equal(t, 0, at(0, 20_000))
}

// hookTakes must be SingletonFloorEngine._takeFees: four rates off one leg, each rounded down, summed.
func TestHookTakes(t *testing.T) {
	total, err := hookTakes(true, uint256.NewInt(10_001), 100, &testRates)
	require.NoError(t, err)
	assert.Equal(t, uint64(100+50+25+25), total.Uint64())

	// a sell takes the sell side's creator and holder rates
	total, err = hookTakes(false, uint256.NewInt(10_001), 300, &testRates)
	require.NoError(t, err)
	assert.Equal(t, uint64(300+75+0+25), total.Uint64())

	// each part rounds down on its own: 3 * floor(399 * 25 / 10,000) = 0, not floor(399 * 75 / 10,000) = 2
	r := Rates{CreatorBuy: 25, HolderBuy: 25, TeamFee: 25}
	total, err = hookTakes(true, uint256.NewInt(399), 0, &r)
	require.NoError(t, err)
	assert.Zero(t, total.Uint64())

	_, err = hookTakes(true, new(uint256.Int).SetAllOne(), 1, &testRates)
	assert.ErrorIs(t, err, ErrFeeOverflow)
}

// swapFee is the hook's 0.25% override composed with the PoolManager's protocol fee for the swap's direction, as
// ProtocolFeeLibrary.calculateSwapFee: protocolFee + lpFee - protocolFee * lpFee / 1e6.
func TestSwapFee(t *testing.T) {
	assert.Equal(t, uniswapv4.FeeAmount(2_500), swapFee(0, true))
	assert.Equal(t, uniswapv4.FeeAmount(2_500), swapFee(0, false))
	const packed = 1_000 | 300<<12 // zeroForOne 0.1%, oneForZero 0.03%
	assert.Equal(t, uniswapv4.FeeAmount(1_000+2_500-2), swapFee(packed, true))
	assert.Equal(t, uniswapv4.FeeAmount(300+2_500-0), swapFee(packed, false))
}
