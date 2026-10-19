package floormath

import (
	"testing"
)

// The bid's line every flat ask is held to (trade::backing_line_x64), against golden/trade.json's backing_line vectors:
// the window's and the post-end lot / tail asks all take it.
func TestBackingLineGolden(t *testing.T) {
	var g struct {
		BackingLine []struct {
			Band string `json:"band"`
			SOut string `json:"s_out"`
			Take string `json:"take"`
			Leg  string `json:"leg"`
			U    string `json:"u"`
			Line map[string]string
		} `json:"backing_line"`
	}
	readJSON(t, "trade.json", &g)
	if len(g.BackingLine) < 40 {
		t.Fatalf("%d backing_line vectors", len(g.BackingLine))
	}
	for i, v := range g.BackingLine {
		got, err := backingLineX64(D(v.Band), D(v.SOut), D(v.Take), D(v.Leg), D(v.U))
		if want, ok := v.Line["ok"]; ok {
			if err != nil || got.Dec() != want {
				t.Fatalf("vector %d: got %v %v want %s", i, got.Dec(), err, want)
			}
		} else if err == nil || err.Error() != v.Line["err"] {
			t.Fatalf("vector %d: got %v %v want err %s", i, got.Dec(), err, v.Line["err"])
		}
	}
}

// Every fee and Marketcap Milestone on a curve: golden/trade.json's curves — each seed row's rates at nine points along
// the curve (Rates::at) and the Milestones' split of a fill (split_milestones).
func TestCurvesGolden(t *testing.T) {
	var g struct {
		Curves []map[string]any `json:"curves"`
	}
	readJSON(t, "trade.json", &g)
	rows := seedRows(t)
	at, sp := 0, 0
	for i, c := range g.Curves {
		if s, ok := c["split"].(map[string]any); ok {
			ms := s["ms"].([]any)
			team, floor, platform, rest, err := splitMilestones(D(s["pay"].(string)), D(s["out_of_books"].(string)),
				[3]uint16{uint16(ms[0].(float64)), uint16(ms[1].(float64)), uint16(ms[2].(float64))})
			if err != nil || team.Dec() != s["team"] || floor.Dec() != s["floor"] || platform.Dec() != s["platform"] || rest.Dec() != s["rest"] {
				t.Fatalf("split %d: %v %v %v %v %v, want %v", i, team.Dec(), floor.Dec(), platform.Dec(), rest.Dec(), err, s)
			}
			sp++
			continue
		}
		p := rows[uint8(c["row"].(float64))]
		r := RatesOf(&p)
		x := ratesAt(&r, uint64(c["t"].(float64)))
		m := x.milestones()
		got := map[string]float64{"skim_buy_far_bps": float64(x.SkimBuyFarBps), "skim_sell_far_bps": float64(x.SkimSellFarBps),
			"creator_buy_bps": float64(x.CreatorBuyBps), "creator_sell_bps": float64(x.CreatorSellBps), "platform_bps": float64(x.PlatformBps),
			"floor_token_bps": float64(x.FloorTokenBps), "team_ms_bps": float64(m[0]), "platform_ms_bps": float64(m[1]), "floor_ms_bps": float64(m[2])}
		for k, v := range got {
			if c[k].(float64) != v {
				t.Fatalf("row %v t %v: %s = %v, want %v", c["row"], c["t"], k, v, c[k])
			}
		}
		at++
	}
	if at != 27 || sp != 5 {
		t.Fatalf("%d points, %d splits", at, sp)
	}
	// the general live bound is the row limit (15 %): the seed curves' top passes whole; a launch's own lower bound (the
	// genesis's 6.5 %) scales them down together
	if a, b, c := liveMilestones(1_000, 100, 100); a != 1_000 || b != 100 || c != 100 {
		t.Fatalf("the live bound: %d %d %d", a, b, c)
	}
	if a, b, c := liveMilestonesTo(1_000, 100, 100, GenesisMsLiveMaxBps); a != 541 || b != 54 || c != 54 {
		t.Fatalf("past the genesis bound: %d %d %d", a, b, c)
	}
	if a, b, c := liveMilestones(2_000, 0, 0); a != MilestonesLiveMaxBps || b != 0 || c != 0 {
		t.Fatalf("past the live bound: %d %d %d", a, b, c)
	}
	// the row check: the three Milestones together at most 15 % (Team 10 % + Platform 4 % + $FLOOR 1 %)
	b2 := rows[2]
	if b2.msTopBps() != 1_200 {
		t.Fatalf("the seeds' Milestones top: %d", b2.msTopBps())
	}
	b2.PlatformMsBps = [2]uint16{10, 401}
	if SpecOk(&b2) {
		t.Fatal("past 15 % together is refused")
	}
	b2.PlatformMsBps = [2]uint16{10, 400}
	if !SpecOk(&b2) {
		t.Fatal("15 % together exactly is a valid row")
	}
	b2.TeamMsBps, b2.PlatformMsBps = [2]uint16{100, 1_001}, [2]uint16{10, 100}
	if SpecOk(&b2) {
		t.Fatal("the Team Milestone past the row's Team Slot max is refused")
	}
}

// The tail range (tail::open / tokens_at / buy / sell) against golden/trade.json's tail_range vectors: the range opened at
// the frozen price, its tokens at the end, a buy of `offer` (the backing line at f on odd cases) and the sell of what it
// bought, as the generator drives them.
func TestTailRangeGolden(t *testing.T) {
	type res struct {
		Ok  map[string]string `json:"ok"`
		Err string            `json:"err"`
	}
	var g struct {
		TailRange []struct {
			Frozen string `json:"frozen_x64"`
			Floor  string `json:"floor_x64"`
			U      string `json:"u"`
			Offer  string `json:"offer"`
			Tokens string `json:"tokens"`
			Range  struct {
				Err         string            `json:"err"`
				Liq         string            `json:"liq"`
				SqrtEnd     string            `json:"sqrt_end_x64"`
				TokensAtEnd map[string]string `json:"tokens_at_end"`
				Floor       string            `json:"floor"`
				Line        string            `json:"line_x64"`
				Buy         res               `json:"buy"`
				Sell        res               `json:"sell"`
			} `json:"range"`
		} `json:"tail_range"`
	}
	readJSON(t, "trade.json", &g)
	if len(g.TailRange) < 40 {
		t.Fatalf("%d tail_range vectors", len(g.TailRange))
	}
	bought := 0
	for i, v := range g.TailRange {
		frozen, f, u, offer, tokens := D(v.Frozen), D(v.Floor), D(v.U), D(v.Offer), D(v.Tokens)
		liq, se, err := tailOpen(tokens, frozen)
		if v.Range.Err != "" {
			if err == nil || err.Error() != v.Range.Err {
				t.Fatalf("vector %d: open %v, want err %s", i, err, v.Range.Err)
			}
			continue
		}
		if err != nil || liq.Dec() != v.Range.Liq || se.Dec() != v.Range.SqrtEnd {
			t.Fatalf("vector %d: open (%s, %s) %v, want (%s, %s)", i, liq.Dec(), se.Dec(), err, v.Range.Liq, v.Range.SqrtEnd)
		}
		if x, err := tailTokensAt(liq, se); err != nil || x.Dec() != v.Range.TokensAtEnd["ok"] {
			t.Fatalf("vector %d: tokens_at %s %v, want %v", i, x.Dec(), err, v.Range.TokensAtEnd)
		}
		sOut := tokens
		floor := rsh(mul(f, sOut), 64)
		if !fits(floor) {
			floor = max128
		}
		line := zero
		if i%2 == 1 {
			line = f
		}
		if floor.Dec() != v.Range.Floor || line.Dec() != v.Range.Line {
			t.Fatalf("vector %d: floor %s line %s, want %s %s", i, floor.Dec(), line.Dec(), v.Range.Floor, v.Range.Line)
		}
		b, err := tailBuy(liq, se, tokens, offer, u, floor, sOut, line, max128, max128)
		if v.Range.Buy.Err != "" {
			if err == nil || err.Error() != v.Range.Buy.Err || v.Range.Sell.Err != v.Range.Buy.Err {
				t.Fatalf("vector %d: buy %v, want err %s", i, err, v.Range.Buy.Err)
			}
			continue
		}
		wb := v.Range.Buy.Ok
		if err != nil || b.Pay.Dec() != wb["pay"] || b.Out.Dec() != wb["out"] || b.Kept.Dec() != wb["kept"] || b.Cut.Dec() != wb["cut"] ||
			b.ToFloor.Dec() != wb["to_floor"] || b.ToMoney.Dec() != wb["to_money"] || b.SqrtAfter.Dec() != wb["sqrt_after"] {
			t.Fatalf("vector %d: buy %+v %v, want %v", i, b, err, wb)
		}
		if !isZ(b.Out) {
			bought++
		}
		s, err := tailSell(liq, b.SqrtAfter, se, b.ToMoney, b.Out, add(floor, b.ToFloor), add(sOut, b.Out))
		if v.Range.Sell.Err != "" {
			if err == nil || err.Error() != v.Range.Sell.Err {
				t.Fatalf("vector %d: sell %v, want err %s", i, err, v.Range.Sell.Err)
			}
			continue
		}
		ws := v.Range.Sell.Ok
		if err != nil || s.Tokens.Dec() != ws["tokens"] || s.Gross.Dec() != ws["gross"] || s.FromFloor.Dec() != ws["from_floor"] ||
			s.FromMoney.Dec() != ws["from_money"] || s.SqrtAfter.Dec() != ws["sqrt_after"] {
			t.Fatalf("vector %d: sell %+v %v, want %v", i, s, err, ws)
		}
	}
	if bought < 10 {
		t.Fatalf("only %d range buys got tokens", bought)
	}
}
