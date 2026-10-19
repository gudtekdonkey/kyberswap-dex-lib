package flooramm

import (
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/goccy/go-json"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"

	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/entity"
	floormath "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/floor-amm/math"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/source/pool"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/util/testutil"
)

// The simulator held to golden/launch.json's 18-decimal walks (math/testdata):
// before every buy and sell the pool is rebuilt from what the lister and the tracker store (StaticExtra + Extra), and its
// quote must be the walk's receipt — tokens out, refund, fee, payout; its UpdateBalance must land on the walk's launch.

const (
	testFloorAmm = "0x00000000000000000000000000000000f100a000"
	testQuote    = "0x00000000000000000000000000000000000e7e00"
	testToken    = "0x00000000000000000000000000000000000f1000"
)

func goldenDir() string {
	if d := os.Getenv("FLOOR_GOLDEN"); d != "" {
		return d
	}
	return filepath.Join("math", "testdata")
}

type walk struct {
	PresetID         uint8  `json:"preset_id"`
	Quote            string `json:"quote"`
	QuoteUsdCentsX64 string `json:"quote_usd_cents_x64"`
	QuoteDecimals    uint8  `json:"quote_decimals"`
	TokenDecimals    uint8  `json:"token_decimals"`
	// a raised slot maximum for the row (the books open capped, openSaleFor)
	RowTeamSlotMaxBps *uint16 `json:"row_team_slot_max_bps"`
	TeamSlot          *struct {
		RateBps     uint16    `json:"rate_bps"`
		BoughtPpm   uint32    `json:"bought_ppm"`
		Weights     [5]uint32 `json:"weights"`
		CreatorRefs [3]bool   `json:"creator_refs"`
	} `json:"team_slot"`
	Ops []map[string]any `json:"ops"`
}

func loadGoldens(t *testing.T) (map[uint8]floormath.PresetSpec, []walk) {
	t.Helper()
	read := func(name string, v any) {
		b, err := os.ReadFile(filepath.Join(goldenDir(), name))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, v))
	}
	var sj struct {
		PresetRows []struct {
			Spec struct {
				Bytes string `json:"bytes"`
			} `json:"spec"`
		} `json:"preset_rows"`
	}
	read("schedule.json", &sj)
	rows := map[uint8]floormath.PresetSpec{}
	for _, r := range sj.PresetRows {
		b, err := hex.DecodeString(r.Spec.Bytes)
		require.NoError(t, err)
		p, ok := floormath.PresetFromBytes(b)
		require.True(t, ok)
		rows[p.ID] = p
	}
	var lj struct {
		Walks []walk `json:"walks"`
	}
	read("launch.json", &lj)
	var evm []walk
	for _, w := range lj.Walks {
		if w.TokenDecimals == 18 {
			evm = append(evm, w)
		}
	}
	require.Len(t, evm, 21) // on ETH, USDC (6 dp) and USDT (18 dp), + 3 on USDC with an 800 bps slot max
	return rows, evm
}

func u(v *uint256.Int) *uint256.Int { return new(uint256.Int).Set(v) }

// chainSupply is the launch's stored supply in chain units (the math runs at min(18, max(6, the quote's)) decimals).
func chainSupply(l *floormath.Launch) uint256.Int {
	var v uint256.Int
	if l.TokenScale.IsZero() {
		return l.Supply
	}
	v.Mul(&l.Supply, &l.TokenScale)
	return v
}

// entityOf is the pool as the lister (StaticExtra) and the tracker (Extra, Reserves, BlockNumber) store it.
func entityOf(t *testing.T, l *floormath.Launch, live uint256.Int, block uint64, quoteDecimals uint8, raise, quoteUsdCentsX64 uint256.Int) entity.Pool {
	t.Helper()
	se, err := json.Marshal(StaticExtra{FloorAmm: testFloorAmm, LaunchID: 7, Spec: hex.EncodeToString(l.Spec.ToBytes()),
		Schedule: hex.EncodeToString(l.Sch.ToBytes()), QuoteUsdCentsX64: u(&quoteUsdCentsX64)})
	require.NoError(t, err)
	// the views' units (IFloorAmm v1.3a): launchState's token amounts in chain units and p0 per chain unit;
	// launchMarket's books and curve in math units
	k := scaleOf(l)
	c := func(v uint256.Int) *uint256.Int { return new(uint256.Int).Mul(&v, &k) }
	// v1.3c: p0 per chain unit rounds UP
	p0 := new(uint256.Int).Add(&l.P0X64, &k)
	p0.SubUint64(p0, 1).Div(p0, &k)
	ex, err := json.Marshal(Extra{S: u(&l.St.S), H: u(&l.St.H), L: u(&l.St.L), CurveFloorX64: u(&l.St.FloorX64),
		P0X64: p0, Raise: u(&raise), Floor: u(&l.Pots.Floor), Cushion: u(&l.Pots.Cushion), BidSqrtX64: u(&l.Pots.BidSqrtX64),
		BidLiq: u(&l.Pots.Liq), Supply: c(l.Supply), LiveSupply: u(&live), WindowLeft: c(l.WindowLeft),
		Inventory: c(l.Inventory), LotTokens: c(l.LotTokens), LotCost: u(&l.LotCost), Tail: c(l.Tail),
		CreatorAlloc: c(l.CreatorAlloc), CreatorUnlockMcap: u(&l.CreatorUnlockMcap),
		CreatorUnlockBacking: u(&l.CreatorUnlockBacking),
		AllocReleasesDone:    l.AllocReleasesDone, AllocNextReleaseAt: l.AllocNextReleaseAt,
		Ended: l.Ended, Graduated: l.Graduated, SlotRateBps: l.SlotRateBps, SlotBoughtPpm: l.SlotBoughtPpm,
		SlotWeights: l.SlotWeights, CreatorRefs: l.CreatorRefs,
		CreatedBlock: l.CreatedSlot, TailLiq: u(&l.TailLiq), TailSqrtX64: u(&l.TailSqrtX64),
		TailSqrtEndX64: u(&l.TailSqrtEndX64), TailMoney: u(&l.TailMoney), MsLiveMaxBps: l.MsLiveMaxBps,
		LotBins: binsOf(l)})
	require.NoError(t, err)
	return entity.Pool{Address: testToken, Exchange: DexType, Type: DexType, BlockNumber: block,
		Reserves: entity.PoolReserves{"0", "0"},
		Tokens:   []*entity.PoolToken{{Address: testQuote, Decimals: quoteDecimals, Swappable: true}, {Address: testToken, Decimals: 18, Swappable: true}},
		Extra:    string(ex), StaticExtra: string(se)}
}

// binsOf is the lot's bins as the extra carries them (math units).
func binsOf(l *floormath.Launch) []LotBinExtra {
	var out []LotBinExtra
	for _, b := range l.Lot.InUse() {
		out = append(out, LotBinExtra{Key: b.Key, Tokens: u(&b.Tokens), Cost: u(&b.Cost)})
	}
	return out
}

func scaleOf(l *floormath.Launch) uint256.Int {
	if l.TokenScale.IsZero() {
		return *uint256.NewInt(1)
	}
	return l.TokenScale
}

func raiseOf(t *testing.T, spec *floormath.PresetSpec, cents string, quoteDecimals uint8) uint256.Int {
	t.Helper()
	r, err := floormath.PresetRaise(spec, floormath.D(cents), quoteDecimals)
	require.NoError(t, err)
	return r
}

func dec(m map[string]any, k string) *big.Int {
	v, ok := new(big.Int).SetString(m[k].(string), 10)
	if !ok {
		panic(k)
	}
	return v
}

func TestSimulatorReplaysTheGoldenWalks(t *testing.T) {
	rows, walks := loadGoldens(t)
	var buys, sells, refunds, unlocks int
	for _, w := range walks {
		spec := rows[w.PresetID]
		if w.RowTeamSlotMaxBps != nil {
			spec.TeamSlotMaxBps = *w.RowTeamSlotMaxBps
		}
		var ref floormath.Launch
		var live uint256.Int
		for i, op := range w.Ops {
			name := w.Quote + "/" + strconv.Itoa(int(w.PresetID)) + "/op" + strconv.Itoa(i)
			switch op["op"] {
			case "create":
				l, _, err := floormath.Create(&spec, floormath.D(w.QuoteUsdCentsX64), w.QuoteDecimals, w.TokenDecimals,
					uint64(op["created_slot"].(float64)), refsOf(op), floormath.D(op["creator_quote"].(string)))
				require.NoError(t, err, name)
				if w.TeamSlot != nil {
					l.SlotRateBps, l.SlotBoughtPpm = w.TeamSlot.RateBps, w.TeamSlot.BoughtPpm
					l.SlotWeights = w.TeamSlot.Weights
				}
				ref, live = l, chainSupply(&l)
				continue
			case "unlock_creator_alloc":
				// not a swap: the tracker re-reads the state after it (the next op's pool is rebuilt from it)
				// one release of the allocation a call, on its clock (unix seconds)
				got, err := ref.UnlockCreatorAlloc(uint64(op["now"].(float64)))
				rc := op["receipt"].(map[string]any)
				if want, ok := rc["ok"].(string); ok {
					require.NoError(t, err, name)
					require.Equal(t, want, got.Tokens.Dec(), name)
					require.Equal(t, uint64(rc["next_release_at"].(float64)), got.NextReleaseAt, name)
					unlocks++
				} else {
					require.EqualError(t, err, rc["err"].(string), name)
				}
				continue
			case "burn":
				nl, err := ref.Burn(live, floormath.D(op["tokens"].(string)))
				require.NoError(t, err, name)
				live = nl
				continue
			}
			live = floormath.D(op["live_supply"].(string))
			rc := op["receipt"].(map[string]any)
			_, isErr := rc["err"]
			require.False(t, isErr, name) // no walk refuses an op
			block := uint64(0)
			if s, ok := op["slot"]; ok {
				block = uint64(s.(float64)) - 1 // the simulator quotes for the next block
			}
			sim, err := NewPoolSimulator(entityOf(t, &ref, live, block, w.QuoteDecimals, raiseOf(t, &spec, w.QuoteUsdCentsX64, w.QuoteDecimals), floormath.D(w.QuoteUsdCentsX64)))
			require.NoError(t, err, name)
			take := rc["take"].(map[string]any)
			if op["op"] == "buy" {
				leg := dec(op, "leg")
				res, err := sim.CalcAmountOut(pool.CalcAmountOutParams{
					TokenAmountIn: pool.TokenAmount{Token: testQuote, Amount: leg}, TokenOut: testToken})
				r, rerr := ref.Buy(floormath.D(op["leg"].(string)), block+1, live)
				require.NoError(t, rerr, name)
				if dec(rc, "out").Sign() == 0 {
					require.ErrorIs(t, err, ErrZeroOut, name)
					continue
				}
				require.NoError(t, err, name)
				require.Equal(t, dec(rc, "out"), res.TokenAmountOut.Amount, name)
				require.Equal(t, dec(take, "total"), res.Fee.Amount, name)
				if refund := dec(rc, "refund"); refund.Sign() > 0 {
					require.Equal(t, refund, res.RemainingTokenAmountIn.Amount, name)
					refunds++
				} else {
					require.Nil(t, res.RemainingTokenAmountIn, name)
				}
				sim.UpdateBalance(pool.UpdateBalanceParams{SwapInfo: res.SwapInfo})
				require.Equal(t, r.Out.Dec(), res.TokenAmountOut.Amount.String(), name)
				buys++
			} else {
				res, err := sim.CalcAmountOut(pool.CalcAmountOutParams{
					TokenAmountIn: pool.TokenAmount{Token: testToken, Amount: dec(op, "tokens")}, TokenOut: testQuote})
				_, rerr := ref.Sell(floormath.D(op["tokens"].(string)), live)
				require.NoError(t, rerr, name)
				if dec(rc, "payout").Sign() == 0 {
					require.ErrorIs(t, err, ErrZeroOut, name)
					continue
				}
				require.NoError(t, err, name)
				require.Equal(t, dec(rc, "payout"), res.TokenAmountOut.Amount, name)
				require.Equal(t, dec(take, "total"), res.Fee.Amount, name)
				sim.UpdateBalance(pool.UpdateBalanceParams{SwapInfo: res.SwapInfo})
				sells++
			}
			// Restore keeps only the Terms the trade path reads (the row's shares)
			want := ref
			want.Terms = floormath.Terms{CushionBps: ref.Terms.CushionBps, PolBps: ref.Terms.PolBps}
			require.Equal(t, want, sim.launch, name+": UpdateBalance lands on the walk's launch")
		}
	}
	t.Logf("%d buys (%d refunded in part), %d sells quoted bit for bit; %d allocation releases", buys, refunds, sells, unlocks)
	require.Positive(t, refunds)
	require.Positive(t, unlocks)
}

// freshSim: a WAGMI launch on an 18-decimal quote right after create, quoted at `block`. The seed rows have no snipe
// cap; this row sets one (150 blocks, 1 % of the window a call).
func freshSim(t *testing.T, block uint64) (*PoolSimulator, *big.Int) {
	rows, walks := loadGoldens(t)
	w := walks[0]
	spec := rows[w.PresetID]
	spec.SnipeSlots, spec.WindowMaxPerTxBps = 150, 100
	l, _, err := floormath.Create(&spec, floormath.D(w.QuoteUsdCentsX64), w.QuoteDecimals, 18, 1_000, [3]bool{}, floormath.D("0"))
	require.NoError(t, err)
	sim, err := NewPoolSimulator(entityOf(t, &l, chainSupply(&l), block, w.QuoteDecimals, raiseOf(t, &spec, w.QuoteUsdCentsX64, w.QuoteDecimals), floormath.D(w.QuoteUsdCentsX64)))
	require.NoError(t, err)
	oneQuote := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(w.QuoteDecimals)), nil)
	return sim, new(big.Int).Div(oneQuote, big.NewInt(100)) // 0.01 ETH
}

// Inside the snipe span every buy call is capped (several may share a route), the over-cap rest refunded; after the
// span there is no cap.
func TestSnipeSpanCapsEveryBuyCall(t *testing.T) {
	buy := func(s *PoolSimulator, amt *big.Int) (*pool.CalcAmountOutResult, error) {
		return s.CalcAmountOut(pool.CalcAmountOutParams{TokenAmountIn: pool.TokenAmount{Token: testQuote, Amount: amt}, TokenOut: testToken})
	}
	big100 := func(a *big.Int) *big.Int { return new(big.Int).Mul(a, big.NewInt(100_000)) } // 1,000 ETH: far past the cap
	sim, amt := freshSim(t, 1_010)
	// the cap: ⌊window · window_max_per_tx_bps ÷ 10⁴⌋ (the row has a window)
	capT := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	capT.Mul(capT, sim.launch.Spec.WindowWhole.ToBig())
	capT.Mul(capT, big.NewInt(int64(sim.launch.Spec.WindowMaxPerTxBps))).Div(capT, big.NewInt(10_000))
	require.Positive(t, capT.Sign())
	for i := 0; i < 2; i++ {
		res, err := buy(sim, big100(amt))
		require.NoError(t, err, "buy %d in the span", i)
		require.Equal(t, 0, res.TokenAmountOut.Amount.Cmp(capT), "buy %d: capped at the snipe cap", i)
		require.NotNil(t, res.RemainingTokenAmountIn, "buy %d: the rest refunded", i)
		sim.UpdateBalance(pool.UpdateBalanceParams{SwapInfo: res.SwapInfo})
	}
	_, err := sim.CalcAmountOut(pool.CalcAmountOutParams{TokenAmountIn: pool.TokenAmount{Token: testToken, Amount: big.NewInt(1e15)}, TokenOut: testQuote})
	require.NoError(t, err)
	// after the span: no cap
	sim, amt = freshSim(t, 1_000+150)
	res, err := buy(sim, big100(amt))
	require.NoError(t, err)
	require.Equal(t, 1, res.TokenAmountOut.Amount.Cmp(capT), "the span is over")
}

func TestCloneStateAndPurity(t *testing.T) {
	sim, amt := freshSim(t, 1_200)
	params := pool.CalcAmountOutParams{TokenAmountIn: pool.TokenAmount{Token: testQuote, Amount: amt}, TokenOut: testToken}
	testutil.TestCloneState(t, sim, params, nil)
	a, err := sim.CalcAmountOut(params)
	require.NoError(t, err)
	b, err := sim.CalcAmountOut(params)
	require.NoError(t, err)
	require.Equal(t, a.TokenAmountOut.Amount, b.TokenAmountOut.Amount, "CalcAmountOut is pure")
	_, err = sim.CalcAmountOut(pool.CalcAmountOutParams{TokenAmountIn: pool.TokenAmount{Token: testToken, Amount: big.NewInt(1)}, TokenOut: testToken})
	require.ErrorIs(t, err, ErrInvalidToken)
}

// refsOf is a create op's creator_refs.
func refsOf(op map[string]any) [3]bool {
	var r [3]bool
	if v, ok := op["creator_refs"].([]any); ok {
		for i := range r {
			r[i] = v[i].(bool)
		}
	}
	return r
}

// A launch on a 6-dp quote runs the math at 6 dp (scale 1e12): restored from the views in their units (IFloorAmm v1.3a:
// chain-unit token amounts, a floored per-chain-unit p0, math-unit books), it quotes a buy and a sell exactly as the
// reference launch does in chain units.
func TestScaledLaunchRestoresFromChainUnitViews(t *testing.T) {
	rows, walks := loadGoldens(t)
	var w *walk
	for i := range walks {
		if walks[i].QuoteDecimals == 6 && walks[i].TokenDecimals == 18 {
			w = &walks[i]
			break
		}
	}
	require.NotNil(t, w, "a 6-dp quote, 18-dp token walk")
	spec := rows[w.PresetID]
	ref, _, err := floormath.Create(&spec, floormath.D(w.QuoteUsdCentsX64), w.QuoteDecimals, 18, 0, [3]bool{}, floormath.D("0"))
	require.NoError(t, err)
	require.Equal(t, "1000000000000", ref.TokenScale.Dec())
	live := chainSupply(&ref)
	sim, err := NewPoolSimulator(entityOf(t, &ref, live, 1_000, w.QuoteDecimals, raiseOf(t, &spec, w.QuoteUsdCentsX64, w.QuoteDecimals), floormath.D(w.QuoteUsdCentsX64)))
	require.NoError(t, err)
	require.Equal(t, ref.P0X64, sim.launch.P0X64, "p0 recomputed, not the floored view")
	require.Equal(t, ref.Supply, sim.launch.Supply, "supply back in math units")
	leg := big.NewInt(1_000_000_000) // 1,000 USDC
	res, err := sim.CalcAmountOut(pool.CalcAmountOutParams{TokenAmountIn: pool.TokenAmount{Token: testQuote, Amount: leg}, TokenOut: testToken})
	require.NoError(t, err)
	r, err := ref.Buy(*uint256.MustFromBig(leg), 1_001, live)
	require.NoError(t, err)
	require.Equal(t, r.Out.ToBig(), res.TokenAmountOut.Amount, "the buy's tokens, chain units")
	sim.UpdateBalance(pool.UpdateBalanceParams{SwapInfo: res.SwapInfo})
	half := new(big.Int).Div(res.TokenAmountOut.Amount, big.NewInt(2))
	sres, err := sim.CalcAmountOut(pool.CalcAmountOutParams{TokenAmountIn: pool.TokenAmount{Token: testToken, Amount: half}, TokenOut: testQuote})
	require.NoError(t, err)
	s, err := ref.Sell(*uint256.MustFromBig(half), live)
	require.NoError(t, err)
	require.Equal(t, s.Payout.ToBig(), sres.TokenAmountOut.Amount, "the sell's payout")
}

// lotEntity: a WAGMI launch on the first walk's quote after one whole quote's buy and a sell of half of it (the lot holds
// bins), as the tracker stores it at block 5,000; its live supply and the one quote.
func lotEntity(t *testing.T) (entity.Pool, floormath.Launch, uint256.Int, uint256.Int) {
	rows, walks := loadGoldens(t)
	w := walks[0]
	spec := rows[w.PresetID]
	l, _, err := floormath.Create(&spec, floormath.D(w.QuoteUsdCentsX64), w.QuoteDecimals, 18, 1_000, [3]bool{}, floormath.D("0"))
	require.NoError(t, err)
	live := chainSupply(&l)
	oneQuote := floormath.D("1" + strings.Repeat("0", int(w.QuoteDecimals)))
	b, err := l.Buy(oneQuote, 5_000, live)
	require.NoError(t, err)
	half := new(uint256.Int).Div(&b.Out, uint256.NewInt(2))
	_, err = l.Sell(*half, live)
	require.NoError(t, err)
	require.Positive(t, int(l.Lot.Len), "the sell put tokens in the lot")
	raise := raiseOf(t, &spec, w.QuoteUsdCentsX64, w.QuoteDecimals)
	return entityOf(t, &l, live, 5_000, w.QuoteDecimals, raise, floormath.D(w.QuoteUsdCentsX64)), l, live, oneQuote
}

// The lot's bins and the Milestone bound ride in Extra, both optional: bins whose totals are the launch's restore the
// lot exactly; no bins restore an empty lot (the launch still quotes, the lot sells nothing in the quote); stale bins are
// refused. The bound restores as read; absent, as the create default.
func TestLotBinsAndBoundInExtra(t *testing.T) {
	ep, l, live, oneQuote := lotEntity(t)
	withExtra := func(f func(*Extra)) entity.Pool {
		var ex Extra
		require.NoError(t, json.Unmarshal([]byte(ep.Extra), &ex))
		f(&ex)
		bs, err := json.Marshal(ex)
		require.NoError(t, err)
		e := ep
		e.Extra = string(bs)
		return e
	}
	params := pool.CalcAmountOutParams{TokenAmountIn: pool.TokenAmount{Token: testQuote, Amount: oneQuote.ToBig()}, TokenOut: testToken}

	sim, err := NewPoolSimulator(ep)
	require.NoError(t, err)
	require.Equal(t, l.Lot, sim.launch.Lot)
	require.Equal(t, uint16(floormath.MilestonesLiveMaxBps), sim.launch.MsLiveMaxBps)
	a, err := sim.CalcAmountOut(params)
	require.NoError(t, err)
	ref := l
	r, err := ref.Buy(oneQuote, 5_001, live)
	require.NoError(t, err)
	require.Equal(t, r.Out.ToBig(), a.TokenAmountOut.Amount)

	// no bins: an empty lot, still a quote
	sim, err = NewPoolSimulator(withExtra(func(ex *Extra) { ex.LotBins = nil }))
	require.NoError(t, err)
	require.Zero(t, sim.launch.Lot.Len)
	ask, err := sim.launch.LotAskX64()
	require.NoError(t, err)
	require.Nil(t, ask)
	_, err = sim.CalcAmountOut(params)
	require.NoError(t, err)

	// stale bins: refused
	_, err = NewPoolSimulator(withExtra(func(ex *Extra) {
		ex.LotBins[0].Tokens = new(uint256.Int).AddUint64(ex.LotBins[0].Tokens, 1)
	}))
	require.ErrorIs(t, err, floormath.ErrStaleLot)

	// the bound as read
	sim, err = NewPoolSimulator(withExtra(func(ex *Extra) { ex.MsLiveMaxBps = floormath.GenesisMsLiveMaxBps }))
	require.NoError(t, err)
	require.Equal(t, uint16(floormath.GenesisMsLiveMaxBps), sim.launch.MsLiveMaxBps)
}

// A quote is never an error for its size: legs of 2^64 … 2^127 on every seed row of the first walk's quote, before and
// after the schedule's end, quote tokens (or nothing: ErrZeroOut), never Overflow.
func TestHugeLegsAreQuotedNotRefused(t *testing.T) {
	rows, walks := loadGoldens(t)
	w := walks[0]
	for id, spec := range rows {
		l, _, err := floormath.Create(&spec, floormath.D(w.QuoteUsdCentsX64), w.QuoteDecimals, 18, 0, [3]bool{}, floormath.D("0"))
		require.NoError(t, err)
		live := chainSupply(&l)
		raise := raiseOf(t, &spec, w.QuoteUsdCentsX64, w.QuoteDecimals)
		quote := func(stage string) {
			sim, err := NewPoolSimulator(entityOf(t, &l, live, 1_000_000, w.QuoteDecimals, raise, floormath.D(w.QuoteUsdCentsX64)))
			require.NoError(t, err)
			for _, e := range []uint{64, 96, 120, 123, 125, 127} {
				amt := new(big.Int).Lsh(big.NewInt(1), e)
				_, err := sim.CalcAmountOut(pool.CalcAmountOutParams{TokenAmountIn: pool.TokenAmount{Token: testQuote, Amount: amt}, TokenOut: testToken})
				if err != nil {
					require.ErrorIs(t, err, ErrZeroOut, "row %d %s: 2^%d", id, stage, e)
				}
			}
		}
		quote("before the end")
		leg := floormath.D("1" + strings.Repeat("0", int(w.QuoteDecimals)+6))
		for !l.Ended {
			_, err := l.Buy(leg, 1_000_000, live)
			require.NoError(t, err)
			leg.Add(&leg, &leg)
		}
		quote("after the end")
	}
}
