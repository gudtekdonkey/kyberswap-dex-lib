package floor

import (
	"math/big"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/goccy/go-json"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/entity"
	uniswapv4 "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/uniswap/v4"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/valueobject"
)

// tracked is a USDC-at-currency1 pool as Track keeps it, with the pool's tick at -370540 (engine tick 370539).
func tracked() *Hook {
	h := &Hook{Hook: &uniswapv4.BaseHook{}, Extra: Extra{
		TickSpacing: 60, QuoteIsCurrency1: true, FloorTick: 375660, Rates: testRates,
		CushionLower: 375060, HasCushion: true,
	}}
	h.tick, h.hasTick = -370540, true
	return h
}

// The registered factory rebuilds the hook from the tracked extra and the pool's tick, and the address's permission
// bits select beforeSwap and afterSwap with both return deltas (0x20cc: beforeInitialize | beforeSwap | afterSwap |
// beforeSwapReturnDelta | afterSwapReturnDelta).
func TestFactory_RegisteredAndPermissions(t *testing.T) {
	addr := common.HexToAddress("0xe44886ffe0a894c6C5900d77A51c7d49732C60Cc")
	extra := tracked().Extra
	hX, err := json.Marshal(extra)
	require.NoError(t, err)
	hook, ok := uniswapv4.GetHook(addr, &uniswapv4.HookParam{
		HookExtra: uniswapv4.HookExtra(hX),
		Pool:      &entity.Pool{Extra: `{"liquidity":1,"sqrtPriceX96":1,"tickSpacing":60,"tick":-370540,"ticks":[]}`},
	})
	require.True(t, ok)
	h, isFloor := hook.(*Hook)
	require.True(t, isFloor)
	assert.Equal(t, extra, h.Extra)
	assert.Equal(t, -370540, h.tick)
	assert.True(t, h.hasTick)
	assert.Equal(t, string(valueobject.ExchangeUniswapV4Floor), h.GetExchange())
	assert.True(t, h.CanBeforeSwap(addr))
	assert.True(t, h.CanAfterSwap(addr))

	const (
		beforeInitialize      = 1 << 13
		beforeSwap            = 1 << 7
		afterSwap             = 1 << 6
		beforeSwapReturnDelta = 1 << 3
		afterSwapReturnDelta  = 1 << 2
	)
	flags := (uint16(addr[18])<<8 | uint16(addr[19])) & 0x3fff
	assert.Equal(t, uint16(beforeInitialize|beforeSwap|afterSwap|beforeSwapReturnDelta|afterSwapReturnDelta), flags)

	// a pool whose extra carries no tick is never priced
	hook, _ = uniswapv4.GetHook(addr, &uniswapv4.HookParam{HookExtra: uniswapv4.HookExtra(hX)})
	_, err = hook.BeforeSwap(&uniswapv4.BeforeSwapParams{CalcOut: true, AmountSpecified: big.NewInt(1e6)})
	assert.ErrorIs(t, err, ErrPoolNotTracked)
}

func TestBeforeSwap_Refusals(t *testing.T) {
	h := tracked()
	_, err := h.BeforeSwap(&uniswapv4.BeforeSwapParams{CalcOut: false, AmountSpecified: big.NewInt(1e6)})
	assert.ErrorIs(t, err, ErrExactOutNotSupported, "the hook reverts ExactOutNotSupported")

	_, err = (&Hook{Hook: &uniswapv4.BaseHook{}}).BeforeSwap(&uniswapv4.BeforeSwapParams{CalcOut: true,
		AmountSpecified: big.NewInt(1e6)})
	assert.ErrorIs(t, err, ErrPoolNotTracked)

	_, err = h.BeforeSwap(&uniswapv4.BeforeSwapParams{CalcOut: true, AmountSpecified: big.NewInt(0)})
	assert.ErrorIs(t, err, uniswapv4.ErrInvalidAmountIn)

	h.bidReplaced = true
	_, err = h.BeforeSwap(&uniswapv4.BeforeSwapParams{CalcOut: true, AmountSpecified: big.NewInt(1e6)})
	assert.ErrorIs(t, err, ErrBidReplacePending)
}

// A buy (quote in; the quote is currency1, so oneForZero) takes its fees before the swap at the pre-swap skim; a sell
// takes nothing before it. Both override the LP fee to 0.25%.
func TestBeforeSwap_BuyTakesFeesSellDoesNot(t *testing.T) {
	h := tracked()
	amount := big.NewInt(1_000_000_000)

	buy, err := h.BeforeSwap(&uniswapv4.BeforeSwapParams{CalcOut: true, ZeroForOne: false, AmountSpecified: amount})
	require.NoError(t, err)
	require.NoError(t, uniswapv4.ValidateBeforeSwapResult(buy))
	// engine tick 370539, dear edge 375540: distance 5001, closeness 10000 - 3107 = 6893, skim 100 - 68 = 32;
	// gap 375060 - 370539 = 4521: m = 10000 + 5000 * 2289 / 2877 = 13978, skim 32 * 13978 / 10000 = 44
	assert.Equal(t, big.NewInt(1_000_000_000*(44+50+25+25)/10_000), buy.DeltaSpecified)
	assert.Zero(t, buy.DeltaUnspecified.Sign())
	assert.Equal(t, uniswapv4.FeeAmount(lpFeePips), buy.SwapFee)
	assert.Equal(t, int64(gasBeforeSwap+gasTakeFees), buy.Gas)

	sell, err := h.BeforeSwap(&uniswapv4.BeforeSwapParams{CalcOut: true, ZeroForOne: true, AmountSpecified: amount})
	require.NoError(t, err)
	assert.Zero(t, sell.DeltaSpecified.Sign())
	assert.Equal(t, uniswapv4.FeeAmount(lpFeePips), sell.SwapFee)
	assert.Equal(t, int64(gasBeforeSwap), sell.Gas)
}

func afterSwap(h *Hook, zeroForOne bool, amountOut int64, tick int, remaining uint64,
) (*uniswapv4.AfterSwapResult, *SwapInfo, error) {
	params := &uniswapv4.BeforeSwapParams{CalcOut: true, ZeroForOne: zeroForOne, AmountSpecified: big.NewInt(1e18)}
	before, err := h.BeforeSwap(params)
	if err != nil {
		return nil, nil, err
	}
	res, err := h.AfterSwap(&uniswapv4.AfterSwapParams{
		BeforeSwapParams: params,
		AmountIn:         params.AmountSpecified,
		AmountOut:        big.NewInt(amountOut),
		BeforeSwapResult: before,
		PoolSwapInfo: &uniswapv4.PoolSwapInfo{
			RemainingAmountIn:     uint256.NewInt(remaining),
			NextStateSqrtRatioX96: uint256.NewInt(1),
			NextStateTickCurrent:  tick,
		},
	})
	return res, before.SwapInfo.(*SwapInfo), err
}

// A sell takes its fees after the swap at the post-swap skim. One that ends in the bid band (engine tick at or past
// the dear edge, 375540 here) is flagged and costs the bid re-place's gas; applying it stops the pool quoting.
func TestAfterSwap_SellSkimAtThePostSwapPriceAndTheBand(t *testing.T) {
	h := tracked()
	// engine tick 370539 (raw -370540): distance 5001 from the dear edge, sell skim 100 + 400 * 6893 / 10000 = 375
	res, info, err := afterSwap(h, true, 1_000_000_000, -370540, 0)
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(1_000_000_000*(375+75+0+25)/10_000), res.HookFee)
	assert.Equal(t, int64(gasTakeFees), res.Gas)
	assert.Equal(t, SwapInfo{Tick: -370540}, *info)

	// engine tick 375539 is one tick short of the band
	_, info, err = afterSwap(h, true, 1_000_000_000, -375540, 0)
	require.NoError(t, err)
	assert.False(t, info.BandReached)

	// engine tick 375540 is in it: the at-floor sell skim, 500
	res, info, err = afterSwap(h, true, 1_000_000_000, -375541, 0)
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(1_000_000_000*(500+75+0+25)/10_000), res.HookFee)
	assert.Equal(t, int64(gasTakeFees+gasBidReplace), res.Gas)
	assert.Equal(t, SwapInfo{Tick: -375541, BandReached: true}, *info)

	cloned := h.CloneState().(*Hook)
	cloned.UpdateBalance(info)
	assert.Equal(t, -375541, cloned.tick)
	_, err = cloned.BeforeSwap(&uniswapv4.BeforeSwapParams{CalcOut: true, AmountSpecified: big.NewInt(1e6)})
	assert.ErrorIs(t, err, ErrBidReplacePending)
	// the original is untouched
	assert.Equal(t, -370540, h.tick)
	assert.False(t, h.bidReplaced)

	// a buy takes nothing after the swap, but its post-swap tick moves the hook's view of the pool
	res, info, err = afterSwap(h, false, 1_000_000_000, -369000, 0)
	require.NoError(t, err)
	assert.Zero(t, res.HookFee.Sign())
	h.UpdateBalance(info)
	assert.Equal(t, -369000, h.tick)
}

// The V4Quoter refuses a swap the pool cannot fill whole; the buy's fees were taken on the whole input.
func TestAfterSwap_PartialFillAndMissingStateRefused(t *testing.T) {
	_, _, err := afterSwap(tracked(), false, 1, -369000, 1)
	assert.ErrorIs(t, err, ErrPartialFill)

	h := tracked()
	_, err = h.AfterSwap(&uniswapv4.AfterSwapParams{
		BeforeSwapParams: &uniswapv4.BeforeSwapParams{CalcOut: true}, AmountOut: big.NewInt(1),
	})
	assert.ErrorIs(t, err, ErrMissingPostSwapState)

	_, err = h.AfterSwap(&uniswapv4.AfterSwapParams{BeforeSwapParams: &uniswapv4.BeforeSwapParams{CalcOut: false}})
	assert.ErrorIs(t, err, ErrExactOutNotSupported)
}

// fill sets every nil *big.Int reachable in v to zero, so a decode target can be ABI-encoded.
func fill(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.Type() == reflect.TypeOf(&big.Int{}) && v.IsNil() {
			v.Set(reflect.ValueOf(new(big.Int)))
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			fill(v.Field(i))
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fill(v.Index(i))
		}
	default:
	}
}

// Track's answers, encoded in the contracts' own shapes (poolState's 57 words, execute's bytes around stairs()'s 71,
// StateView's slot0), decode through the embedded ABIs exactly as ethrpc decodes them, and extraFrom keeps what the
// fee rule reads.
func TestTrack_DecodesTheContractsAnswers(t *testing.T) {
	usdc := common.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913")
	state := poolStateRPC{
		PoolBound: true, TickSpacing: big.NewInt(60), FloorTick: big.NewInt(-1200),
		SkimBuyFarBps: big.NewInt(200), SkimSellFarBps: big.NewInt(200), SkimSellAtFloorBps: big.NewInt(300),
		CreatorBuyBps: big.NewInt(75), CreatorSellBps: big.NewInt(75), HolderSellBps: big.NewInt(10),
		TeamFeeBps: big.NewInt(25), QuoteToken: usdc, QuoteIsCurrency1: true,
	}
	state.PoolKey.Currency0, state.PoolKey.Currency1 = common.HexToAddress("0x1111111111111111111111111111111111111111"), usdc
	fill(reflect.ValueOf(&state).Elem())
	stairs := stairsRPC{CushionLower: big.NewInt(-1860), CushionLiq: big.NewInt(42)}
	stairs.Step[3].Cheap = big.NewInt(-7)
	fill(reflect.ValueOf(&stairs).Elem())

	stateData, err := hookABI.Methods["poolState"].Outputs.Pack(state)
	require.NoError(t, err)
	assert.Len(t, stateData, 57*32)
	stairsData, err := poolViewsABI.Methods["stairs"].Outputs.Pack(stairs)
	require.NoError(t, err)
	assert.Len(t, stairsData, 71*32)
	executeData, err := hookABI.Methods["execute"].Outputs.Pack(stairsData)
	require.NoError(t, err)
	slot0Data, err := stateViewABI.Methods["getSlot0"].Outputs.Pack(big.NewInt(1), big.NewInt(-5), big.NewInt(1000|300<<12),
		big.NewInt(0))
	require.NoError(t, err)

	var gotState poolStateResp
	require.NoError(t, hookABI.UnpackIntoInterface(&gotState, "poolState", stateData))
	var raw []byte
	require.NoError(t, hookABI.UnpackIntoInterface(&raw, "execute", executeData))
	var gotStairs stairsResp
	require.NoError(t, unpackStairs(raw, &gotStairs))
	var slot0 slot0RPC
	require.NoError(t, stateViewABI.UnpackIntoInterface(&slot0, "getSlot0", slot0Data))
	assert.Equal(t, jsonOf(t, state), jsonOf(t, gotState.PoolState))
	assert.Equal(t, jsonOf(t, stairs), jsonOf(t, gotStairs.Stairs))

	extra, err := extraFrom(&gotState.PoolState, &gotStairs.Stairs, &slot0)
	require.NoError(t, err)
	assert.Equal(t, Extra{
		TickSpacing: 60, QuoteIsCurrency1: true, FloorTick: -1200,
		Rates:        Rates{SkimBuyFar: 200, SkimSellFar: 200, SkimSellAtFloor: 300, CreatorBuy: 75, CreatorSell: 75, HolderSell: 10, TeamFee: 25},
		CushionLower: -1860, HasCushion: true, ProtocolFee: 1000 | 300<<12,
	}, extra)

	assert.ErrorIs(t, unpackStairs(stairsData[:10*32], &gotStairs), ErrInvalidStairs)
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestExtraFrom_Refusals(t *testing.T) {
	usdc := common.HexToAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913")
	base := func() (poolStateRPC, stairsRPC, slot0RPC) {
		s := poolStateRPC{PoolBound: true, TickSpacing: big.NewInt(60), FloorTick: big.NewInt(1200), QuoteToken: usdc,
			SkimBuyFarBps: big.NewInt(200), SkimSellFarBps: big.NewInt(200), SkimSellAtFloorBps: big.NewInt(300),
			TeamFeeBps: big.NewInt(25)}
		s.PoolKey.Currency0 = usdc
		fill(reflect.ValueOf(&s).Elem())
		st := stairsRPC{}
		fill(reflect.ValueOf(&st).Elem())
		return s, st, slot0RPC{ProtocolFee: new(big.Int)}
	}

	s, st, s0 := base()
	_, err := extraFrom(&s, &st, &s0)
	require.NoError(t, err)

	// a pool whose launch has not graduated is kept untracked, not refused
	s.PoolBound = false
	extra, err := extraFrom(&s, &st, &s0)
	require.NoError(t, err)
	assert.Equal(t, Extra{}, extra)

	for name, mutate := range map[string]func(*poolStateRPC, *stairsRPC, *slot0RPC){
		"no spacing":            func(s *poolStateRPC, _ *stairsRPC, _ *slot0RPC) { s.TickSpacing.SetInt64(0) },
		"floor tick past int24": func(s *poolStateRPC, _ *stairsRPC, _ *slot0RPC) { s.FloorTick.SetInt64(1 << 23) },
		"protocol fee past 24 bits": func(_ *poolStateRPC, _ *stairsRPC, s0 *slot0RPC) {
			s0.ProtocolFee.SetInt64(1 << 24)
		},
		"orientation": func(s *poolStateRPC, _ *stairsRPC, _ *slot0RPC) { s.QuoteIsCurrency1 = true },
		"rate past 100%": func(s *poolStateRPC, _ *stairsRPC, _ *slot0RPC) {
			s.CreatorSellBps.SetInt64(10_001)
		},
		"platform fee past its cap": func(s *poolStateRPC, _ *stairsRPC, _ *slot0RPC) { s.TeamFeeBps.SetInt64(51) },
		"sell takes sum to 100%": func(s *poolStateRPC, _ *stairsRPC, _ *slot0RPC) {
			s.CreatorSellBps.SetInt64(5_000)
			s.HolderSellBps.SetInt64(4_675)
		},
	} {
		s, st, s0 := base()
		mutate(&s, &st, &s0)
		_, err := extraFrom(&s, &st, &s0)
		assert.Error(t, err, name)
	}
}
