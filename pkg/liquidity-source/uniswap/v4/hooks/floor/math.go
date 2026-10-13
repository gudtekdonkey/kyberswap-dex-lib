package floor

import (
	"github.com/holiman/uint256"

	uniswapv4 "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/uniswap/v4"
)

// bandTicks mirrors SingletonFloorEngine._bandTicks: BID_BAND_TICKS rounded up to the pool's spacing (120 at 60).
func bandTicks(tickSpacing int) int {
	return (bidBandTicks + tickSpacing - 1) / tickSpacing * tickSpacing
}

// marketTick mirrors SingletonFloorEngine._market: the pool's raw tick in the engine frame. A quote at currency1 maps
// to -tick - 1 (not -tick).
func marketTick(rawTick int, quoteIsCurrency1 bool) int {
	if quoteIsCurrency1 {
		return -rawTick - 1
	}
	return rawTick
}

// skimBpsAt mirrors FloorHookMath.skimBps: the skim in bps at distance ticks from the bid band's dear edge
// (dearEdge - current, engine frame). A buy ramps from its at-floor rate at the band to its far rate SKIM_RAMP_TICKS
// away; a sell ramps from its at-floor rate down to its far rate, and is flat when its far rate is at or above its
// at-floor rate.
func skimBpsAt(isBuy bool, distance int, r *Rates) int {
	closeness := 0
	if distance <= 0 {
		closeness = bpsDenominator
	} else if distance < skimRampTicks {
		closeness = bpsDenominator - distance*bpsDenominator/skimRampTicks
	}
	if isBuy {
		far, at := int(r.SkimBuyFar), int(r.SkimBuyAtFloor)
		if far >= at {
			return far - (far-at)*closeness/bpsDenominator
		}
		return far + (at-far)*closeness/bpsDenominator
	}
	far, at := int(r.SkimSellFar), int(r.SkimSellAtFloor)
	if far >= at {
		return far
	}
	return far + (at-far)*closeness/bpsDenominator
}

// gapSkimBps mirrors SingletonFloorEngine._gapSkim (FloorPolicy.STAIR_GAP_SKIM = 2, the linear ramp): a buy's ramped
// skim scaled by how far the price stands above the standing cushion's top, gap = cushionLower - current (engine
// frame). Untouched at or under 2,232 ticks, 1.5x at and past 5,109, linear (floored) between; a scaled skim is
// capped at 1,500 bps. No standing cushion: untouched.
func gapSkimBps(bps int, hasCushion bool, cushionLower, current int) int {
	if !hasCushion {
		return bps
	}
	gap := cushionLower - current
	var m int
	if gap >= gapSkimTicks {
		m = gapSkimMultBps
	} else if gap > gapSkimFromTicks {
		m = bpsDenominator + (gapSkimMultBps-bpsDenominator)*(gap-gapSkimFromTicks)/(gapSkimTicks-gapSkimFromTicks)
	} else {
		return bps
	}
	return min(bps*m/bpsDenominator, gapSkimMaxBps)
}

// hookTakes mirrors SingletonFloorEngine._takeFees: skim, creator fee, holder tax and platform fee, each
// leg * bps / 10,000 rounded down, summed.
func hookTakes(isBuy bool, leg *uint256.Int, skimBps int, r *Rates) (*uint256.Int, error) {
	creator, holder := r.CreatorSell, r.HolderSell
	if isBuy {
		creator, holder = r.CreatorBuy, r.HolderBuy
	}
	var total, part, bps uint256.Int
	for _, rate := range [4]uint64{uint64(skimBps), uint64(creator), uint64(holder), uint64(r.TeamFee)} {
		if rate == 0 {
			continue
		}
		bps.SetUint64(rate)
		if _, overflow := part.MulOverflow(leg, &bps); overflow {
			return nil, ErrFeeOverflow
		}
		part.Div(&part, uint256.NewInt(bpsDenominator))
		total.Add(&total, &part)
	}
	return &total, nil
}

// swapFee is the fee the swap is charged in pips: the hook's LP fee override composed with the PoolManager's
// protocol fee for the direction, as ProtocolFeeLibrary.calculateSwapFee does.
func swapFee(protocolFee uint32, zeroForOne bool) uniswapv4.FeeAmount {
	pf := protocolFee & 0xfff
	if !zeroForOne {
		pf = protocolFee >> 12 & 0xfff
	}
	if pf == 0 {
		return lpFeePips
	}
	return uniswapv4.FeeAmount(pf + lpFeePips - pf*lpFeePips/uint32(uniswapv4.FeeMax))
}
