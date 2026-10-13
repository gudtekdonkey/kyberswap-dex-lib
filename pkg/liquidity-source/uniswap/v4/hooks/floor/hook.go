// Package floor implements SingletonFloorHook (0xe44886ffe0a894c6C5900d77A51c7d49732C60Cc on Ethereum, BSC, Base,
// Robinhood Chain and Arc), the one Uniswap v4 hook every graduated FLOOR launch trades through. Its pools carry the
// dynamic-fee flag, spacing 60, and the hook overrides the LP fee to 0.25% on every swap.
//
//   - Exact input only: beforeSwap reverts ExactOutNotSupported on an exact-output swap.
//   - Buy (quote in): beforeSwap takes skim + creator + holder + platform fee off the quote paid in, each
//     floor(amountIn * bps / 10,000), the skim at the PRE-swap price (skimBpsAt) scaled by the cushion gap
//     (gapSkimBps). The pool swaps what is left.
//   - Sell (token in): the pool swaps; afterSwap takes the same four rates off the quote paid out, the skim at the
//     POST-swap price, no gap scaling.
//   - A sell that reaches the bid band re-places the bid inside the swap after the trader's amounts are settled: the
//     quote is exact, but the pool changes in a way the tick math does not describe, so the hook refuses to quote
//     the pool again until it is re-tracked (the re-place's ModifyLiquidity events trigger that).
//
// Everything the fee rule reads is per pool: the rates, floor and orientation from poolState(id), the cushion from
// execute(id, stairs()), the protocol fee from StateView. All of it changes only in transactions that emit
// PoolManager events for the pool, so the event-driven refresh keeps it current.
package floor

import (
	"context"
	"fmt"
	"math/big"

	"github.com/KyberNetwork/ethrpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/goccy/go-json"
	"github.com/holiman/uint256"

	uniswapv4 "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/uniswap/v4"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/util/bignumber"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/valueobject"
)

type Hook struct {
	uniswapv4.Hook `json:"-"`
	Extra

	// tick is the pool's current raw tick: read from the pool's extra when the hook is built, moved by
	// UpdateBalance. hasTick is false when the pool's extra carried none.
	tick    int
	hasTick bool
	// bidReplaced is set by a simulated sell that reached the bid band: the state is stale from then on.
	bidReplaced bool
}

var _ = uniswapv4.RegisterHooksFactory(func(param *uniswapv4.HookParam) uniswapv4.Hook {
	h := &Hook{Hook: &uniswapv4.BaseHook{Exchange: valueobject.ExchangeUniswapV4Floor}}
	_ = param.HookExtra.Unmarshal(&h.Extra)
	if param.Pool != nil && param.Pool.Extra != "" {
		var extra struct {
			Tick *int `json:"tick"`
		}
		if err := json.Unmarshal([]byte(param.Pool.Extra), &extra); err == nil && extra.Tick != nil {
			h.tick, h.hasTick = *extra.Tick, true
		}
	}
	return h
}, HookAddresses...)

// Track reads poolState(id), the pool's stairs() through execute(id, ...) and the PoolManager's slot0 (for the
// protocol fee) in one multicall, so all three come from one block.
func (h *Hook) Track(ctx context.Context, param *uniswapv4.HookParam) (json.RawMessage, error) {
	poolId := common.HexToHash(param.Pool.Address)
	hookAddr := hexutil.Encode(param.HookAddress[:])
	stairsCall, err := poolViewsABI.Pack("stairs")
	if err != nil {
		return nil, err
	}

	var state poolStateResp
	var stairsRaw []byte
	var slot0 slot0RPC
	if _, err = param.RpcClient.NewRequest().SetContext(ctx).SetBlockNumber(param.BlockNumber).
		SetOverrides(param.Overrides).
		AddCall(&ethrpc.Call{ABI: hookABI, Target: hookAddr, Method: "poolState",
			Params: []any{poolId}}, []any{&state}).
		AddCall(&ethrpc.Call{ABI: hookABI, Target: hookAddr, Method: "execute",
			Params: []any{poolId, stairsCall}}, []any{&stairsRaw}).
		AddCall(&ethrpc.Call{ABI: stateViewABI, Target: param.Cfg.StateViewAddress, Method: "getSlot0",
			Params: []any{poolId}}, []any{&slot0}).
		Aggregate(); err != nil {
		return nil, fmt.Errorf("failed to track floor pool: %w", err)
	}

	var stairs stairsResp
	if err = unpackStairs(stairsRaw, &stairs); err != nil {
		return nil, err
	}
	if h.Extra, err = extraFrom(&state.PoolState, &stairs.Stairs, &slot0); err != nil {
		return nil, err
	}
	return json.Marshal(h.Extra)
}

// unpackStairs decodes the bytes execute returned for stairs().
func unpackStairs(raw []byte, dst *stairsResp) error {
	outputs := poolViewsABI.Methods["stairs"].Outputs
	values, err := outputs.Unpack(raw)
	if err == nil {
		err = outputs.Copy(dst, values)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidStairs, err)
	}
	return nil
}

// extraFrom keeps what the fee rule reads. A pool that is not bound yet (its launch has not graduated) keeps an
// empty Extra, which BeforeSwap refuses to price: the pool holds no liquidity then anyway.
func extraFrom(state *poolStateRPC, stairs *stairsRPC, slot0 *slot0RPC) (Extra, error) {
	if !state.PoolBound {
		return Extra{}, nil
	}
	tickSpacing, ok1 := int24Of(state.TickSpacing)
	floorTick, ok2 := int24Of(state.FloorTick)
	cushionLower, ok3 := int24Of(stairs.CushionLower)
	if !ok1 || !ok2 || !ok3 || tickSpacing <= 0 || stairs.CushionLiq == nil ||
		slot0.ProtocolFee == nil || !slot0.ProtocolFee.IsUint64() || slot0.ProtocolFee.Uint64() >= 1<<24 {
		return Extra{}, ErrInvalidPoolState
	}
	if (state.QuoteToken == state.PoolKey.Currency1) != state.QuoteIsCurrency1 {
		return Extra{}, ErrQuoteOrientationDiffs
	}

	var r Rates
	for _, f := range []struct {
		dst *uint16
		v   *big.Int
	}{
		{&r.SkimBuyFar, state.SkimBuyFarBps}, {&r.SkimSellFar, state.SkimSellFarBps},
		{&r.SkimBuyAtFloor, state.SkimBuyAtFloorBps}, {&r.SkimSellAtFloor, state.SkimSellAtFloorBps},
		{&r.CreatorBuy, state.CreatorBuyBps}, {&r.CreatorSell, state.CreatorSellBps},
		{&r.HolderBuy, state.HolderBuyBps}, {&r.HolderSell, state.HolderSellBps},
		{&r.TeamFee, state.TeamFeeBps},
	} {
		if f.v == nil || !f.v.IsUint64() || f.v.Uint64() > bpsDenominator {
			return Extra{}, ErrRateOutOfRange
		}
		*f.dst = uint16(f.v.Uint64())
	}
	// The most each side can take, at any price: a buy's skim is at most its larger endpoint or the gap skim's cap.
	maxBuySkim := max(r.SkimBuyFar, r.SkimBuyAtFloor, gapSkimMaxBps)
	maxSellSkim := max(r.SkimSellFar, r.SkimSellAtFloor)
	if r.TeamFee > maxPoolTeamFeeBps ||
		int(maxBuySkim)+int(r.CreatorBuy)+int(r.HolderBuy)+int(r.TeamFee) >= bpsDenominator ||
		int(maxSellSkim)+int(r.CreatorSell)+int(r.HolderSell)+int(r.TeamFee) >= bpsDenominator {
		return Extra{}, ErrRateOutOfRange
	}

	return Extra{
		TickSpacing:      int32(tickSpacing),
		QuoteIsCurrency1: state.QuoteIsCurrency1,
		FloorTick:        int32(floorTick),
		Rates:            r,
		CushionLower:     int32(cushionLower),
		HasCushion:       stairs.CushionLiq.Sign() != 0,
		ProtocolFee:      uint32(slot0.ProtocolFee.Uint64()),
	}, nil
}

func int24Of(v *big.Int) (int, bool) {
	if v == nil || !v.IsInt64() || v.Int64() < -(1<<23) || v.Int64() >= 1<<23 {
		return 0, false
	}
	return int(v.Int64()), true
}

func (h *Hook) ready() error {
	if h.TickSpacing <= 0 || !h.hasTick {
		return ErrPoolNotTracked
	} else if h.bidReplaced {
		return ErrBidReplacePending
	}
	return nil
}

// dearEdge is the bid band's dear edge in the engine frame (SingletonFloorEngine._dearEdge).
func (h *Hook) dearEdge() int {
	return int(h.FloorTick) - bandTicks(int(h.TickSpacing))
}

func (h *Hook) isBuy(zeroForOne bool) bool {
	return zeroForOne != h.QuoteIsCurrency1
}

// BeforeSwap overrides the LP fee and, on a buy, takes the hook's four fees off the quote paid in, at the skim of
// the pre-swap price.
func (h *Hook) BeforeSwap(params *uniswapv4.BeforeSwapParams) (*uniswapv4.BeforeSwapResult, error) {
	if !params.CalcOut {
		return nil, ErrExactOutNotSupported
	} else if err := h.ready(); err != nil {
		return nil, err
	} else if params.AmountSpecified == nil || params.AmountSpecified.Sign() <= 0 {
		return nil, uniswapv4.ErrInvalidAmountIn
	}

	result := &uniswapv4.BeforeSwapResult{
		DeltaSpecified:   bignumber.ZeroBI,
		DeltaUnspecified: bignumber.ZeroBI,
		SwapFee:          swapFee(h.ProtocolFee, params.ZeroForOne),
		Gas:              gasBeforeSwap,
		SwapInfo:         &SwapInfo{},
	}
	if !h.isBuy(params.ZeroForOne) {
		return result, nil
	}

	var leg uint256.Int
	if overflow := leg.SetFromBig(params.AmountSpecified); overflow {
		return nil, ErrFeeOverflow
	}
	pre := marketTick(h.tick, h.QuoteIsCurrency1)
	skim := gapSkimBps(skimBpsAt(true, h.dearEdge()-pre, &h.Rates), h.HasCushion, int(h.CushionLower), pre)
	total, err := hookTakes(true, &leg, skim, &h.Rates)
	if err != nil {
		return nil, err
	}
	result.DeltaSpecified = total.ToBig()
	result.Gas += gasTakeFees
	return result, nil
}

// AfterSwap records the post-swap tick and, on a sell, takes the hook's four fees off the quote paid out, at the
// skim of the post-swap price. The V4Quoter refuses a swap the pool cannot fill whole, and so does this hook.
func (h *Hook) AfterSwap(params *uniswapv4.AfterSwapParams) (*uniswapv4.AfterSwapResult, error) {
	if !params.CalcOut {
		return nil, ErrExactOutNotSupported
	}
	post := params.PoolSwapInfo
	if post == nil {
		return nil, ErrMissingPostSwapState
	} else if post.RemainingAmountIn != nil && !post.RemainingAmountIn.IsZero() {
		return nil, ErrPartialFill
	}
	var info *SwapInfo
	if params.BeforeSwapResult != nil {
		info, _ = params.BeforeSwapResult.SwapInfo.(*SwapInfo)
	}
	if info == nil {
		return nil, ErrMissingPostSwapState
	}
	info.Tick = post.NextStateTickCurrent

	if h.isBuy(params.ZeroForOne) {
		return &uniswapv4.AfterSwapResult{HookFee: bignumber.ZeroBI}, nil
	}

	var leg uint256.Int
	if params.AmountOut == nil || params.AmountOut.Sign() < 0 {
		return nil, uniswapv4.ErrInvalidAmountOut
	} else if overflow := leg.SetFromBig(params.AmountOut); overflow {
		return nil, ErrFeeOverflow
	}
	postTick := marketTick(post.NextStateTickCurrent, h.QuoteIsCurrency1)
	dearEdge := h.dearEdge()
	total, err := hookTakes(false, &leg, skimBpsAt(false, dearEdge-postTick, &h.Rates), &h.Rates)
	if err != nil {
		return nil, err
	}
	gas := int64(gasTakeFees)
	if postTick >= dearEdge {
		info.BandReached = true
		gas += gasBidReplace
	}
	return &uniswapv4.AfterSwapResult{HookFee: total.ToBig(), Gas: gas}, nil
}

func (h *Hook) CloneState() uniswapv4.Hook {
	cloned := *h
	return &cloned
}

// UpdateBalance moves the hook's view of the pool's tick. After a sell that reached the bid band the hook refuses
// to quote until the pool is tracked again: the bid it re-placed is not in the simulated state.
func (h *Hook) UpdateBalance(swapInfo any) {
	info, ok := swapInfo.(*SwapInfo)
	if !ok || info == nil {
		return
	}
	h.tick = info.Tick
	if info.BandReached {
		h.bidReplaced = true
	}
}

var _ uniswapv4.Hook = (*Hook)(nil)
