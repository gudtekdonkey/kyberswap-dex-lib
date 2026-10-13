package floor

import (
	"errors"

	"github.com/ethereum/go-ethereum/common"
)

// HookAddresses is SingletonFloorHook, the one hook every FLOOR launch trades through once it graduates. It is
// deployed at the same address on Ethereum, BSC, Base, Robinhood Chain and Arc.
var HookAddresses = []common.Address{
	common.HexToAddress("0xe44886ffe0a894c6C5900d77A51c7d49732C60Cc"),
}

const (
	lpFeePips = 2_500 // SingletonFloorEngine.LP_FEE: the LP fee the hook overrides onto every swap (0.25%)

	bidBandTicks  = 120    // FloorPolicy.BID_BAND_TICKS, before rounding up to the pool's spacing
	skimRampTicks = 16_094 // SingletonFloorEngine.SKIM_RAMP_TICKS

	gapSkimTicks     = 5_109  // FloorPolicy.STAIR_GAP_SKIM_TICKS: the full multiplier at and past this gap
	gapSkimFromTicks = 2_232  // FloorPolicy.STAIR_GAP_SKIM_FROM_TICKS: no scaling at and under this gap
	gapSkimMultBps   = 15_000 // FloorPolicy.STAIR_GAP_SKIM_MULT_BPS (1.5x)
	gapSkimMaxBps    = 1_500  // FloorPolicy.STAIR_GAP_SKIM_MAX_BPS

	maxPoolTeamFeeBps = 50     // FloorPolicy.MAX_POOL_TEAM_FEE_BPS
	bpsDenominator    = 10_000 // every rate is in basis points of the quote leg

	// Gas on top of the plain v4 swap (uniswapv4.DefaultGas). Every swap pays gasBeforeSwap (the engine
	// delegatecall, the TWAP write, the trader-referral gate); the swap's quote leg pays gasTakeFees (one take into
	// the pool's custody vault and the skim, creator, holder and platform bookings), in beforeSwap on a buy and in
	// afterSwap on a sell. A native 0.1 ETH pool buy measures 387,230 gas in the hook's own suite. A sell that
	// reaches the bid band re-places the bid inside the swap: gasBidReplace.
	gasBeforeSwap = 60_000
	gasTakeFees   = 200_000
	gasBidReplace = 350_000
)

var (
	ErrPoolNotTracked        = errors.New("floor: pool state not tracked")
	ErrExactOutNotSupported  = errors.New("floor: exact output is not supported")
	ErrBidReplacePending     = errors.New("floor: bid re-placed by an earlier swap, pool must be re-tracked")
	ErrPartialFill           = errors.New("floor: pool cannot fill the whole input")
	ErrInvalidPoolState      = errors.New("floor: invalid pool state")
	ErrInvalidStairs         = errors.New("floor: invalid stairs answer")
	ErrRateOutOfRange        = errors.New("floor: rate out of range")
	ErrMissingPostSwapState  = errors.New("floor: post-swap pool state missing")
	ErrFeeOverflow           = errors.New("floor: fee overflow")
	ErrQuoteOrientationDiffs = errors.New("floor: quote orientation disagrees with the pool key")
)
