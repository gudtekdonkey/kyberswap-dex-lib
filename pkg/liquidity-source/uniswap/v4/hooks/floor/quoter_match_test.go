package floor

import (
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/goccy/go-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/entity"
	uniswapv3 "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/uniswap/v3"
	uniswapv4 "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/uniswap/v4"
	poolpkg "github.com/KyberNetwork/kyberswap-dex-lib/pkg/source/pool"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/valueobject"
)

// The simulator must agree with Uniswap's V4Quoter (Base 0x0d5e0F971ED27FBfF6c2837bf31316121532048D, which runs the
// real SingletonFloorHook through PoolManager.unlock) to the wei. Each base-*.json file is one Cook launch created,
// sold out and graduated on a fork of Base, read at six pool states (fresh, after a 3 ETH buy, after a ratchet, after
// a big buy above the cushion, after selling half the bag, after a deep sell into the bid): the PoolManager's slot0,
// liquidity, every tick bitmap word and every initialised tick's liquidityNet (StateView), the hook's poolState(id)
// and stairs(), and the quoter's answer for 15 buy and sell sizes (null: the quoter reverted). Three layouts: native
// ETH at currency0, USDC at currency0, USDC at currency1. 270 quotes in all. The live-*.json files are each chain's live
// graduated FLOOR pool (Ethereum, BSC, Robinhood Chain, Arc, Base; native quote), read at one block with the quoter's
// answer for 20 sizes at that block, 100 quotes in all; Arc's quote is its native USDC.
// Pure fixtures, no network: these run in CI.

const (
	hookAddr = "0xe44886ffe0a894c6c5900d77a51c7d49732c60cc"
	baseWETH = "0x4200000000000000000000000000000000000006"
	// The launch token's address is not part of the snapshot and plays no part in pricing; these stand in for it,
	// on the side of the quote the pool key puts it.
	tokenBelowQuote = "0x1111111111111111111111111111111111111111"
	tokenAboveQuote = "0xf111111111111111111111111111111111111111"
)

type pair struct {
	K int
	V string
}

func (p *pair) UnmarshalJSON(b []byte) error {
	var raw [2]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[0], &p.K); err != nil {
		return err
	}
	return json.Unmarshal(raw[1], &p.V)
}

type snapshot struct {
	Pool struct {
		SqrtPriceX96 string `json:"sqrtPriceX96"`
		Tick         int    `json:"tick"`
		Liquidity    string `json:"liquidity"`
		TickSpacing  int    `json:"tickSpacing"`
		ProtocolFee  uint32 `json:"protocolFee"`
		Bitmap       []pair `json:"bitmap"`
		LiquidityNet []pair `json:"liquidityNet"`
	} `json:"pool"`
	QuoteIsCurrency1 bool `json:"quoteIsCurrency1"`
	FloorTick        int  `json:"floorTick"`
	Rates            struct {
		SkimBuyFarBps, SkimSellFarBps, SkimBuyAtFloorBps, SkimSellAtFloorBps   int64
		CreatorBuyBps, CreatorSellBps, HolderBuyBps, HolderSellBps, TeamFeeBps int64
	} `json:"rates"`
	Cushion struct {
		CushionLower int    `json:"cushionLower"`
		CushionLiq   string `json:"cushionLiq"`
	} `json:"cushion"`
	GapSkim bool `json:"gapSkim"`
}

type expectedQuote struct {
	Side      string  `json:"side"`
	AmountIn  string  `json:"amountIn"`
	AmountOut *string `json:"amountOut"`
}

type fixtureCase struct {
	State    string          `json:"state"`
	Block    string          `json:"block"`
	PoolID   string          `json:"poolId"`
	Snapshot snapshot        `json:"snapshot"`
	Expected []expectedQuote `json:"expected"`
}

type fixture struct {
	ChainID int           `json:"chainId"`
	Label   string        `json:"label"`
	Quote   string        `json:"quote"`
	Cases   []fixtureCase `json:"cases"`
}

func loadFixtures(t *testing.T, pattern string, n int) []fixture {
	t.Helper()
	files, err := filepath.Glob(pattern)
	require.NoError(t, err)
	require.Len(t, files, n)
	fixtures := make([]fixture, 0, len(files))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		var fx fixture
		require.NoError(t, json.Unmarshal(raw, &fx))
		fixtures = append(fixtures, fx)
	}
	return fixtures
}

func bi(t *testing.T, s string) *big.Int {
	t.Helper()
	v, ok := new(big.Int).SetString(s, 10)
	require.True(t, ok, s)
	return v
}

// hookExtraOf runs the snapshot's poolState and stairs answers through extraFrom, the same conversion Track makes.
func hookExtraOf(t *testing.T, s *snapshot, quote string) Extra {
	t.Helper()
	require.True(t, s.GapSkim, "every FLOOR set on the five chains carries the gap buy skim")
	state := poolStateRPC{
		PoolBound:          true,
		TickSpacing:        big.NewInt(int64(s.Pool.TickSpacing)),
		FloorTick:          big.NewInt(int64(s.FloorTick)),
		SkimBuyFarBps:      big.NewInt(s.Rates.SkimBuyFarBps),
		SkimSellFarBps:     big.NewInt(s.Rates.SkimSellFarBps),
		SkimBuyAtFloorBps:  big.NewInt(s.Rates.SkimBuyAtFloorBps),
		SkimSellAtFloorBps: big.NewInt(s.Rates.SkimSellAtFloorBps),
		CreatorBuyBps:      big.NewInt(s.Rates.CreatorBuyBps),
		CreatorSellBps:     big.NewInt(s.Rates.CreatorSellBps),
		HolderBuyBps:       big.NewInt(s.Rates.HolderBuyBps),
		HolderSellBps:      big.NewInt(s.Rates.HolderSellBps),
		TeamFeeBps:         big.NewInt(s.Rates.TeamFeeBps),
		QuoteIsCurrency1:   s.QuoteIsCurrency1,
	}
	state.QuoteToken = common.HexToAddress(quote)
	if s.QuoteIsCurrency1 {
		state.PoolKey.Currency0, state.PoolKey.Currency1 = common.HexToAddress(tokenBelowQuote), state.QuoteToken
	} else {
		state.PoolKey.Currency0, state.PoolKey.Currency1 = state.QuoteToken, common.HexToAddress(tokenAboveQuote)
	}
	stairs := stairsRPC{CushionLower: big.NewInt(int64(s.Cushion.CushionLower)), CushionLiq: bi(t, s.Cushion.CushionLiq)}
	extra, err := extraFrom(&state, &stairs, &slot0RPC{ProtocolFee: big.NewInt(int64(s.Pool.ProtocolFee))})
	require.NoError(t, err)
	return extra
}

// poolTokens returns the pool's two currencies in key order, the launch token and the quote as the simulator names
// them (native ETH is WETH in the pool's token list, flagged native in StaticExtra).
func poolTokens(fx *fixture, quoteIsCurrency1 bool) (tokens [2]string, token, quote string, nativeQuote bool) {
	quote = fx.Quote
	if nativeQuote = new(big.Int).SetBytes(common.HexToAddress(quote).Bytes()).Sign() == 0; nativeQuote {
		quote = baseWETH
	}
	if quoteIsCurrency1 {
		return [2]string{tokenBelowQuote, quote}, tokenBelowQuote, quote, nativeQuote
	}
	token = tokenAboveQuote
	return [2]string{quote, token}, token, quote, nativeQuote
}

func newSim(t *testing.T, fx *fixture, c *fixtureCase) (sim *uniswapv4.PoolSimulator, token, quote string) {
	t.Helper()
	s := &c.Snapshot
	tokens, token, quote, nativeQuote := poolTokens(fx, s.QuoteIsCurrency1)

	// Every initialised tick is in the bitmap and has a liquidityNet entry, and nothing else.
	var bitmapTicks []int
	for _, w := range s.Pool.Bitmap {
		word := bi(t, w.V)
		for bit := 0; bit < 256; bit++ {
			if word.Bit(bit) == 1 {
				bitmapTicks = append(bitmapTicks, (w.K*256+bit)*s.Pool.TickSpacing)
			}
		}
	}
	ticks := make([]uniswapv4.Tick, 0, len(s.Pool.LiquidityNet))
	netTicks := make([]int, 0, len(s.Pool.LiquidityNet))
	for _, p := range s.Pool.LiquidityNet {
		net := bi(t, p.V)
		gross := new(big.Int).Abs(net) // the walk reads only liquidityNet; any non-zero gross marks the tick initialised
		if gross.Sign() == 0 {
			gross.SetInt64(1)
		}
		ticks = append(ticks, uniswapv4.Tick{Index: p.K, LiquidityGross: gross, LiquidityNet: net})
		netTicks = append(netTicks, p.K)
	}
	slices.Sort(bitmapTicks)
	require.Equal(t, bitmapTicks, netTicks, "bitmap and liquidityNet disagree")

	hookExtra, err := json.Marshal(hookExtraOf(t, s, fx.Quote))
	require.NoError(t, err)
	extra, err := json.Marshal(uniswapv4.Extra{
		Extra: &uniswapv3.Extra{
			Liquidity:    bi(t, s.Pool.Liquidity),
			SqrtPriceX96: bi(t, s.Pool.SqrtPriceX96),
			TickSpacing:  uint64(s.Pool.TickSpacing),
			Tick:         big.NewInt(int64(s.Pool.Tick)),
			Ticks:        ticks,
		},
		HookExtra: hookExtra,
	})
	require.NoError(t, err)
	staticExtra, err := json.Marshal(uniswapv4.StaticExtra{
		IsNative:     [2]bool{nativeQuote, false},
		Fee:          0x800000, // the dynamic-fee flag every FLOOR pool key carries
		TickSpacing:  int32(s.Pool.TickSpacing),
		HooksAddress: common.HexToAddress(hookAddr),
	})
	require.NoError(t, err)

	sim, err = uniswapv4.NewPoolSimulator(entity.Pool{
		Address:  c.PoolID,
		Exchange: string(valueobject.ExchangeUniswapV4Floor),
		Type:     uniswapv4.DexType,
		Tokens:   []*entity.PoolToken{{Address: tokens[0], Swappable: true}, {Address: tokens[1], Swappable: true}},
		// reserves only cap swap sizes in the simulator; the pool's real balances sit in the PoolManager
		Reserves:    entity.PoolReserves{"1" + zeros(45), "1" + zeros(45)},
		StaticExtra: string(staticExtra),
		Extra:       string(extra),
		BlockNumber: mustUint(t, c.Block),
	}, valueobject.ChainID(fx.ChainID))
	require.NoError(t, err)
	return sim, token, quote
}

func zeros(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}

func mustUint(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := strconv.ParseUint(s, 10, 64)
	require.NoError(t, err)
	return v
}

func quoteOf(sim *uniswapv4.PoolSimulator, e *expectedQuote, token, quote string, amountIn *big.Int,
) (*poolpkg.CalcAmountOutResult, error) {
	tokenIn, tokenOut := quote, token
	if e.Side == "sell" {
		tokenIn, tokenOut = token, quote
	}
	return sim.CalcAmountOut(poolpkg.CalcAmountOutParams{
		TokenAmountIn: poolpkg.TokenAmount{Token: tokenIn, Amount: amountIn},
		TokenOut:      tokenOut,
	})
}

// checkCase asserts every quote of a state; zero is what the quoter answers for a dust buy the fees eat whole, and a
// zero-output quote is an error here, as for every source. It returns how many quotes matched an amount, how many
// were refused as the quoter refused them, and how many were dust.
//
// On Arc the library names native USDC by its wrapped entry, the 6-decimal ERC-20 interface over the same balance, and
// the v4 simulator scales a native leg by 1e12 on the way in and out (valueobject.WrapNativeAmount). The fixtures are in
// the chain's own units (18 decimals), so a native input is given in the wrapped unit, a native output compared after
// the same rounding down, and a native input the wrapped unit cannot express is skipped.
func checkCase(t *testing.T, sim *uniswapv4.PoolSimulator, c *fixtureCase, chainID valueobject.ChainID, nativeQuote bool,
	token, quote string) (matched, refused, dust, skipped int) {
	t.Helper()
	for i := range c.Expected {
		e := &c.Expected[i]
		amountIn, want := bi(t, e.AmountIn), e.AmountOut
		if nativeQuote && e.Side == "buy" {
			wrapped := valueobject.WrapNativeAmount(chainID, amountIn)
			if valueobject.UnwrapNativeAmount(chainID, wrapped).Cmp(amountIn) != 0 {
				skipped++
				continue
			}
			amountIn = wrapped
		}
		if nativeQuote && e.Side == "sell" && want != nil {
			w := valueobject.WrapNativeAmount(chainID, bi(t, *want)).String()
			want = &w
		}
		res, err := quoteOf(sim, e, token, quote, amountIn)
		switch {
		case want == nil:
			assert.Error(t, err, "%s %s: the quoter reverted", e.Side, e.AmountIn)
			refused++
		case *want == "0":
			if err == nil {
				assert.Zero(t, res.TokenAmountOut.Amount.Sign(), "%s %s", e.Side, e.AmountIn)
			}
			dust++
		default:
			if assert.NoError(t, err, "%s %s", e.Side, e.AmountIn) {
				assert.Equal(t, *want, res.TokenAmountOut.Amount.String(), "%s %s", e.Side, e.AmountIn)
				assert.Zero(t, res.RemainingTokenAmountIn.Amount.Sign())
				matched++
			}
		}
	}
	return
}

func TestQuoterMatch_Fixtures(t *testing.T) {
	var matched, refused, dust, skipped int
	for _, fx := range loadFixtures(t, "testdata/*.json", 8) {
		for i := range fx.Cases {
			c := &fx.Cases[i]
			t.Run(fx.Label+"/"+c.State, func(t *testing.T) {
				sim, token, quote := newSim(t, &fx, c)
				_, _, _, native := poolTokens(&fx, c.Snapshot.QuoteIsCurrency1)
				m, r, d, k := checkCase(t, sim, c, valueobject.ChainID(fx.ChainID), native, token, quote)
				matched, refused, dust, skipped = matched+m, refused+r, dust+d, skipped+k
			})
		}
	}
	t.Logf("quotes: %d matched to the wei, %d refused as the quoter refused, %d dust (quoter 0), %d below Arc's wrapped unit",
		matched, refused, dust, skipped)
	assert.Equal(t, 370, matched+refused+dust+skipped)
	assert.Equal(t, 3, refused)
	assert.Equal(t, 2, skipped)
}

// The two buys between recorded states change only the price: no ratchet runs and no position moves. Swapping the
// same amount on the earlier state's simulator and applying it with UpdateBalance must land on the later state's
// price, tick and liquidity, and quote every one of the later state's sizes exactly -- the hook's own view of the
// tick, which the next buy's skim reads, moves with the pool.
func TestQuoterMatch_BuysAppliedWithUpdateBalance(t *testing.T) {
	for _, fx := range loadFixtures(t, "testdata/base-*.json", 3) {
		for _, step := range []struct {
			from, to         string
			native, erc20Buy string
		}{
			{"fresh", "after 3 ETH buy", "3000000000000000000", "3000000000"},
			{"after a ratchet", "after a big buy above the cushion", "12000000000000000000", "12000000000"},
		} {
			t.Run(fx.Label+"/"+step.to, func(t *testing.T) {
				from, to := caseNamed(t, &fx, step.from), caseNamed(t, &fx, step.to)
				require.Equal(t, from.Snapshot.Pool.LiquidityNet, to.Snapshot.Pool.LiquidityNet)
				require.Equal(t, hookExtraOf(t, &from.Snapshot, fx.Quote), hookExtraOf(t, &to.Snapshot, fx.Quote))

				sim, token, quote := newSim(t, &fx, from)
				amount := step.erc20Buy
				if _, _, _, native := poolTokens(&fx, from.Snapshot.QuoteIsCurrency1); native {
					amount = step.native
				}
				amountIn := bi(t, amount)
				res, err := quoteOf(sim, &expectedQuote{Side: "buy"}, token, quote, amountIn)
				require.NoError(t, err)
				sim.UpdateBalance(poolpkg.UpdateBalanceParams{
					TokenAmountIn:  poolpkg.TokenAmount{Token: quote, Amount: amountIn},
					TokenAmountOut: *res.TokenAmountOut,
					SwapInfo:       res.SwapInfo,
				})

				assert.Equal(t, to.Snapshot.Pool.SqrtPriceX96, sim.V3Pool.SqrtRatioX96.Dec())
				assert.Equal(t, to.Snapshot.Pool.Tick, sim.V3Pool.TickCurrent)
				assert.Equal(t, to.Snapshot.Pool.Liquidity, sim.V3Pool.Liquidity.Dec())
				_, _, _, native := poolTokens(&fx, to.Snapshot.QuoteIsCurrency1)
				m, r, d, k := checkCase(t, sim, to, valueobject.ChainID(fx.ChainID), native, token, quote)
				assert.Zero(t, k)
				assert.Equal(t, len(to.Expected), m+r+d)
			})
		}
	}
}

// A sell that reaches the bid band makes the hook re-place its bid inside the swap. Its own quote is exact (the
// fixtures' sells after "selling half the bag" are such sells), but the simulated pool no longer describes the real
// one, so once it is applied the pool refuses every quote until it is tracked again. A sell that stays above the
// band leaves the pool quotable.
func TestQuoterMatch_SellIntoTheBandStopsQuoting(t *testing.T) {
	sellInfo := func(t *testing.T, c *fixtureCase, sim *uniswapv4.PoolSimulator, token, quote string, e *expectedQuote,
	) (*poolpkg.CalcAmountOutResult, *SwapInfo) {
		res, err := quoteOf(sim, e, token, quote, bi(t, e.AmountIn))
		require.NoError(t, err)
		assert.Equal(t, *e.AmountOut, res.TokenAmountOut.Amount.String())
		info := res.SwapInfo.(uniswapv4.SwapInfo).HookSwapInfo.(*SwapInfo)
		dearEdge := c.Snapshot.FloorTick - bandTicks(c.Snapshot.Pool.TickSpacing)
		assert.Equal(t, marketTick(info.Tick, c.Snapshot.QuoteIsCurrency1) >= dearEdge, info.BandReached)
		return res, info
	}
	largestSell := func(c *fixtureCase) *expectedQuote {
		var largest *expectedQuote
		for i := range c.Expected {
			if e := &c.Expected[i]; e.Side == "sell" && e.AmountOut != nil {
				largest = e
			}
		}
		return largest
	}
	apply := func(sim *uniswapv4.PoolSimulator, token string, e *expectedQuote, res *poolpkg.CalcAmountOutResult) {
		amountIn, _ := new(big.Int).SetString(e.AmountIn, 10)
		sim.UpdateBalance(poolpkg.UpdateBalanceParams{
			TokenAmountIn:  poolpkg.TokenAmount{Token: token, Amount: amountIn},
			TokenAmountOut: *res.TokenAmountOut,
			SwapInfo:       res.SwapInfo,
		})
	}

	for _, fx := range loadFixtures(t, "testdata/base-*.json", 3) {
		t.Run(fx.Label, func(t *testing.T) {
			c := caseNamed(t, &fx, "after a big buy above the cushion")
			sim, token, quote := newSim(t, &fx, c)
			e := largestSell(c)
			res, info := sellInfo(t, c, sim, token, quote, e)
			require.False(t, info.BandReached)
			apply(sim, token, e, res)
			_, err := quoteOf(sim, &expectedQuote{Side: "buy"}, token, quote, big.NewInt(1_000_000))
			require.NoError(t, err)

			c = caseNamed(t, &fx, "after selling half the bag")
			sim, token, quote = newSim(t, &fx, c)
			e = largestSell(c)
			res, info = sellInfo(t, c, sim, token, quote, e)
			require.True(t, info.BandReached)
			apply(sim, token, e, res)
			for _, side := range []string{"buy", "sell"} {
				_, err = quoteOf(sim, &expectedQuote{Side: side}, token, quote, big.NewInt(1_000_000))
				assert.ErrorIs(t, err, ErrBidReplacePending, side)
			}
		})
	}
}

func caseNamed(t *testing.T, fx *fixture, state string) *fixtureCase {
	t.Helper()
	for i := range fx.Cases {
		if fx.Cases[i].State == state {
			return &fx.Cases[i]
		}
	}
	t.Fatalf("%s: no state %q", fx.Label, state)
	return nil
}
