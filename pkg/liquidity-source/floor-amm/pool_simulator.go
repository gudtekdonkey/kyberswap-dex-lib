package flooramm

import (
	"encoding/hex"
	"math/big"

	"github.com/goccy/go-json"
	"github.com/holiman/uint256"
	"github.com/samber/lo"

	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/entity"
	floormath "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/floor-amm/math"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/source/pool"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/util/bignumber"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/valueobject"
)

// PoolSimulator quotes one FLOOR Program launch: the token against its quote, on the FloorAmm contract itself (no
// pool). A buy and a sell are floor-math's reference trade (`floor_math::launch`), run on a copy of the launch.
type PoolSimulator struct {
	pool.Pool

	launch     floormath.Launch
	liveSupply uint256.Int
	floorAmm   string
	launchID   uint64
	isNative   bool
}

var _ = pool.RegisterFactory0(DexType, NewPoolSimulator)

func NewPoolSimulator(ep entity.Pool) (*PoolSimulator, error) {
	var se StaticExtra
	if err := json.Unmarshal([]byte(ep.StaticExtra), &se); err != nil {
		return nil, err
	}
	var ex Extra
	if err := json.Unmarshal([]byte(ep.Extra), &ex); err != nil {
		return nil, err
	}
	// the quote is Tokens[0] (the wrapped native for a native launch: 18 decimals)
	var quoteDecimals uint8 = 18
	if len(ep.Tokens) > 0 && !se.IsNative {
		quoteDecimals = ep.Tokens[0].Decimals
	}
	l, err := restore(&se, &ex, quoteDecimals)
	if err != nil {
		return nil, err
	}
	return &PoolSimulator{
		Pool: pool.Pool{Info: pool.PoolInfo{
			Address:     ep.Address,
			Exchange:    ep.Exchange,
			Type:        ep.Type,
			Tokens:      lo.Map(ep.Tokens, func(t *entity.PoolToken, _ int) string { return t.Address }),
			Reserves:    lo.Map(ep.Reserves, func(r string, _ int) *big.Int { return bignumber.NewBig(r) }),
			BlockNumber: ep.BlockNumber,
		}},
		launch:     l,
		liveSupply: *ex.LiveSupply,
		floorAmm:   se.FloorAmm,
		launchID:   se.LaunchID,
		isNative:   se.IsNative,
	}, nil
}

func restore(se *StaticExtra, ex *Extra, quoteDecimals uint8) (floormath.Launch, error) {
	for _, v := range []*uint256.Int{ex.S, ex.H, ex.L, ex.CurveFloorX64, ex.P0X64, ex.Floor, ex.Cushion, ex.BidSqrtX64,
		ex.BidLiq, ex.Supply, ex.LiveSupply, ex.WindowLeft, ex.Inventory, ex.LotTokens, ex.LotCost, ex.Tail} {
		if v == nil {
			return floormath.Launch{}, ErrInvalidSnapshot
		}
	}
	specB, err := hex.DecodeString(se.Spec)
	if err != nil {
		return floormath.Launch{}, err
	}
	schB, err := hex.DecodeString(se.Schedule)
	if err != nil {
		return floormath.Launch{}, err
	}
	bins := make([]floormath.LotBin, 0, len(ex.LotBins))
	for _, b := range ex.LotBins {
		if b.Tokens == nil || b.Cost == nil {
			return floormath.Launch{}, ErrInvalidSnapshot
		}
		bins = append(bins, floormath.LotBin{Key: b.Key, Tokens: *b.Tokens, Cost: *b.Cost})
	}
	spec, ok1 := floormath.PresetFromBytes(specB)
	sch, ok2 := floormath.ScheduleFromBytes(schB)
	if !ok1 || !ok2 {
		return floormath.Launch{}, ErrInvalidSnapshot
	}
	return floormath.Restore(&floormath.OnChain{
		Spec: spec, Schedule: sch,
		Books: floormath.State{S: *ex.S, H: *ex.H, L: *ex.L, FloorX64: *ex.CurveFloorX64},
		P0X64: *ex.P0X64, Raise: orZero(ex.Raise),
		Floor: *ex.Floor, Cushion: *ex.Cushion, BidSqrtX64: *ex.BidSqrtX64, BidLiq: *ex.BidLiq,
		Supply: *ex.Supply, WindowLeft: *ex.WindowLeft, Inventory: *ex.Inventory,
		LotTokens: *ex.LotTokens, LotCost: *ex.LotCost, Tail: *ex.Tail,
		CreatorAlloc: orZero(ex.CreatorAlloc), CreatorUnlockMcap: orZero(ex.CreatorUnlockMcap),
		CreatorUnlockBacking: orZero(ex.CreatorUnlockBacking),
		AllocReleasesDone:    ex.AllocReleasesDone, AllocNextReleaseAt: ex.AllocNextReleaseAt,
		Ended: ex.Ended, Graduated: ex.Graduated, SlotRateBps: ex.SlotRateBps, SlotBoughtPpm: ex.SlotBoughtPpm,
		SlotWeights: ex.SlotWeights, CreatorRefs: ex.CreatorRefs,
		// every launch token has 18 decimals; the math runs at min(18, max(6, the quote's))
		CreatedBlock: ex.CreatedBlock, TokenDecimals: 18, QuoteDecimals: quoteDecimals,
		// the launch's Milestone bound (launchMsLiveMaxBps); 0, a contract without the view, restores as the bound every
		// created launch carries
		MsLiveMaxBps:     ex.MsLiveMaxBps,
		QuoteUsdCentsX64: orZero(se.QuoteUsdCentsX64),
		TailLiq:          orZero(ex.TailLiq), TailSqrtX64: orZero(ex.TailSqrtX64), TailSqrtEndX64: orZero(ex.TailSqrtEndX64),
		TailMoney: orZero(ex.TailMoney),
		LotBins:   bins,
	})
}

func orZero(v *uint256.Int) uint256.Int {
	if v == nil {
		return uint256.Int{}
	}
	return *v
}

// nowBlock is the block a routed swap lands in at the earliest: the snipe span counts blocks after the create block.
func (s *PoolSimulator) nowBlock() uint64 { return s.Info.BlockNumber + 1 }

func (s *PoolSimulator) CalcAmountOut(params pool.CalcAmountOutParams) (*pool.CalcAmountOutResult, error) {
	in, out := s.GetTokenIndex(params.TokenAmountIn.Token), s.GetTokenIndex(params.TokenOut)
	if in < 0 || out < 0 || in == out {
		return nil, ErrInvalidToken
	}
	amountIn, overflow := uint256.FromBig(params.TokenAmountIn.Amount)
	if overflow {
		return nil, ErrOverflow
	}
	l := s.launch // a copy: the quote never touches the simulator's state
	if in == 0 {
		now := s.nowBlock()
		// during the snipe span the cap applies to EACH buy call, router callers included, and several capped buys may share a transaction; the cap is in Buy, the
		// over-cap rest refunded (RemainingTokenAmountIn)
		r, err := l.Buy(*amountIn, now, s.liveSupply)
		if err != nil {
			return nil, err
		}
		if r.Out.IsZero() {
			return nil, ErrZeroOut
		}
		res := &pool.CalcAmountOutResult{
			TokenAmountOut: &pool.TokenAmount{Token: s.Info.Tokens[1], Amount: r.Out.ToBig()},
			Fee:            &pool.TokenAmount{Token: s.Info.Tokens[0], Amount: r.Take.Total.ToBig()},
			Gas:            buyGas,
			SwapInfo: &SwapInfo{IsBuy: true, LaunchID: s.launchID, FloorAmm: s.floorAmm, Refund: r.Refund.Dec(),
				GraduatedNow: r.GraduatedNow, EndedNow: r.EndedNow, after: &l},
		}
		if !r.Refund.IsZero() {
			res.RemainingTokenAmountIn = &pool.TokenAmount{Token: s.Info.Tokens[0], Amount: r.Refund.ToBig()}
		}
		return res, nil
	}
	r, err := l.Sell(*amountIn, s.liveSupply)
	if err != nil {
		return nil, err
	}
	if r.Payout.IsZero() {
		return nil, ErrZeroOut
	}
	return &pool.CalcAmountOutResult{
		TokenAmountOut: &pool.TokenAmount{Token: s.Info.Tokens[0], Amount: r.Payout.ToBig()},
		Fee:            &pool.TokenAmount{Token: s.Info.Tokens[0], Amount: r.Take.Total.ToBig()},
		Gas:            sellGas,
		SwapInfo:       &SwapInfo{LaunchID: s.launchID, FloorAmm: s.floorAmm, after: &l},
	}, nil
}

func (s *PoolSimulator) UpdateBalance(params pool.UpdateBalanceParams) {
	si, ok := params.SwapInfo.(*SwapInfo)
	if !ok || si.after == nil {
		return
	}
	s.launch = *si.after
}

// CloneState: the launch is a value (no pointers, slices or maps; the lot's bins are an array), so a struct copy is a
// deep copy.
func (s *PoolSimulator) CloneState() pool.IPoolSimulator {
	cloned := *s
	return &cloned
}

func (s *PoolSimulator) GetMetaInfo(_, _ string) any {
	return MetaInfo{BlockNumber: s.Info.BlockNumber, FloorAmm: s.floorAmm, LaunchID: s.launchID, IsNative: s.isNative}
}

// The native quote (NATIVE = address(0)) is msg.value in and a native transfer out.

func (s *PoolSimulator) SwapReceiveNativeIn(tokenIn, _ string, _ valueobject.ChainID) bool {
	return s.isNative && s.GetTokenIndex(tokenIn) == 0
}

func (s *PoolSimulator) SwapReturnNativeOut(tokenIn, _ string, _ valueobject.ChainID) bool {
	return s.isNative && s.GetTokenIndex(tokenIn) == 1
}
