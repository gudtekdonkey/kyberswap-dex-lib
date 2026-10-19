package flooramm

import (
	"context"
	"math/big"
	"time"

	"github.com/KyberNetwork/ethrpc"
	"github.com/goccy/go-json"
	"github.com/holiman/uint256"

	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/entity"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/source/pool"
	pooltrack "github.com/KyberNetwork/kyberswap-dex-lib/pkg/source/pool/tracker"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/util/abi"
)

var _ = pooltrack.RegisterFactoryCE0(DexType, NewPoolTracker)

type PoolTracker struct {
	config       *Config
	ethrpcClient *ethrpc.Client
}

func NewPoolTracker(config *Config, ethrpcClient *ethrpc.Client) *PoolTracker {
	return &PoolTracker{config: config, ethrpcClient: ethrpcClient}
}

// GetNewPoolState reads the launch's market, its books, the allocation's releases and the token's live supply in ONE
// multicall (one block).
// Nothing pauses a buy or a sell: there is no pause flag to track.
func (t *PoolTracker) GetNewPoolState(ctx context.Context, p entity.Pool, _ pool.GetNewPoolStateParams) (entity.Pool, error) {
	var se StaticExtra
	if err := json.Unmarshal([]byte(p.StaticExtra), &se); err != nil {
		return p, err
	}
	id := new(big.Int).SetUint64(se.LaunchID)
	var (
		// launchState returns ONE tuple: go-ethereum unpacks a single output into the destination's first field
		viewOut struct{ View launchView }
		market  launchMarket
		alloc   creatorAlloc
		// tokenOptionState returns ONE tuple too
		option                   struct{ State tokenOptionState }
		totalSupply, deadBalance *big.Int
	)
	res, err := t.ethrpcClient.NewRequest().SetContext(ctx).
		AddCall(&ethrpc.Call{ABI: floorAmmABI, Target: se.FloorAmm, Method: methodLaunchState, Params: []any{id}},
			[]any{&viewOut}).
		AddCall(&ethrpc.Call{ABI: floorAmmABI, Target: se.FloorAmm, Method: methodLaunchMarket, Params: []any{id}},
			[]any{&market}).
		AddCall(&ethrpc.Call{ABI: floorAmmABI, Target: se.FloorAmm, Method: methodCreatorAlloc, Params: []any{id}},
			[]any{&alloc}).
		AddCall(&ethrpc.Call{ABI: floorAmmABI, Target: se.FloorAmm, Method: methodTokenOption, Params: []any{id}},
			[]any{&option}).
		AddCall(&ethrpc.Call{ABI: abi.Erc20ABI, Target: p.Tokens[1].Address, Method: abi.Erc20TotalSupplyMethod},
			[]any{&totalSupply}).
		AddCall(&ethrpc.Call{ABI: abi.Erc20ABI, Target: p.Tokens[1].Address, Method: abi.Erc20BalanceOfMethod,
			Params: []any{deadAddress}}, []any{&deadBalance}).
		Aggregate()
	if err != nil {
		return p, err
	}
	view := viewOut.View
	if totalSupply == nil || deadBalance == nil || totalSupply.Cmp(deadBalance) < 0 {
		return p, ErrInvalidSnapshot
	}
	// the program's live supply: tokens sent to the dead address count as burned
	liveSupply := new(big.Int).Sub(totalSupply, deadBalance)
	u := func(b *big.Int) (*uint256.Int, error) {
		if b == nil {
			return nil, ErrInvalidSnapshot
		}
		v, overflow := uint256.FromBig(b)
		if overflow {
			return nil, ErrOverflow
		}
		return v, nil
	}
	ex := Extra{Ended: view.Ended, Graduated: view.Graduated, SlotRateBps: view.SlotRateBps,
		SlotBoughtPpm: view.SlotBoughtPpm, CreatedBlock: view.CreatedBlock}
	for _, f := range []struct {
		dst **uint256.Int
		src *big.Int
	}{
		{&ex.S, market.Book[0]}, {&ex.H, market.Book[1]}, {&ex.L, market.Book[2]}, {&ex.CurveFloorX64, market.Book[3]},
		{&ex.BidSqrtX64, market.BidSqrtX64}, {&ex.BidLiq, market.BidLiq}, {&ex.P0X64, view.P0X64}, {&ex.Raise, view.Raise},
		{&ex.Floor, view.Floor}, {&ex.Cushion, view.Cushion}, {&ex.Supply, view.Supply}, {&ex.LiveSupply, liveSupply},
		{&ex.WindowLeft, view.WindowLeft}, {&ex.Inventory, view.Inventory}, {&ex.LotTokens, view.LotTokens},
		{&ex.LotCost, view.LotCost}, {&ex.Tail, view.TailTokens},
	} {
		if *f.dst, err = u(f.src); err != nil {
			return p, err
		}
	}
	for _, f := range []struct {
		dst **uint256.Int
		src *big.Int
	}{
		{&ex.CreatorAlloc, alloc.Tokens}, {&ex.CreatorUnlockMcap, alloc.UnlockMcap},
		{&ex.CreatorUnlockBacking, alloc.UnlockBacking},
	} {
		if *f.dst, err = u(f.src); err != nil {
			return p, err
		}
	}
	if option.State.NextReleaseAt == nil || !option.State.NextReleaseAt.IsUint64() {
		return p, ErrInvalidSnapshot
	}
	ex.AllocReleasesDone, ex.AllocNextReleaseAt = option.State.ReleasesDone, option.State.NextReleaseAt.Uint64()
	// at the same block, both optional (a contract without the view, or a failed call, leaves the field absent): the
	// launch's Milestone bound (absent: the create default), and after the schedule's end the tail range (absent: the
	// range is not priced)
	var (
		msLiveMax uint16
		tail      launchTail
	)
	req := t.ethrpcClient.NewRequest().SetContext(ctx).SetBlockNumber(res.BlockNumber).
		AddCall(&ethrpc.Call{ABI: floorAmmABI, Target: se.FloorAmm, Method: methodMsLiveMax, Params: []any{id}},
			[]any{&msLiveMax})
	if view.Ended {
		req = req.AddCall(&ethrpc.Call{ABI: floorAmmABI, Target: se.FloorAmm, Method: methodLaunchTail, Params: []any{id}},
			[]any{&tail})
	}
	tr, err := req.TryAggregate()
	if err == nil && len(tr.Result) >= 1 && tr.Result[0] {
		ex.MsLiveMaxBps = msLiveMax
	}
	if view.Ended {
		if err == nil && len(tr.Result) == 2 && tr.Result[1] {
			for _, f := range []struct {
				dst **uint256.Int
				src *big.Int
			}{
				{&ex.TailLiq, tail.Liq}, {&ex.TailSqrtX64, tail.SqrtX64}, {&ex.TailSqrtEndX64, tail.SqrtEndX64},
				{&ex.TailMoney, tail.Money},
			} {
				if *f.dst, err = u(f.src); err != nil {
					return p, err
				}
			}
		}
	}
	held := new(uint256.Int).Add(ex.WindowLeft, ex.Inventory)
	held.Add(held, ex.LotTokens).Add(held, ex.Tail)
	extra, err := json.Marshal(ex)
	if err != nil {
		return p, err
	}
	// The launch's liquidity is F + C; the token side is every token the program sells (held, without the
	// creator's locked buy)
	quoteSide := new(uint256.Int).Add(ex.Floor, ex.Cushion)
	p.Extra = string(extra)
	p.Reserves = entity.PoolReserves{quoteSide.Dec(), held.Dec()}
	if res.BlockNumber != nil {
		p.BlockNumber = res.BlockNumber.Uint64()
	}
	p.Timestamp = time.Now().Unix()
	return p, nil
}
