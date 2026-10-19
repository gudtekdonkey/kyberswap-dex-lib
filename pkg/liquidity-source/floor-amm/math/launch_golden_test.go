package floormath

// Every walk of the reference math's golden/launch.json replayed receipt for receipt — each op's receipt (or error) and
// the launch's state after. The seed rows are read from golden/schedule.json's preset_rows bytes. testdata/ holds the
// 18-decimal walks; FLOOR_GOLDEN=<dir> replays the full golden set from <dir>.

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func goldenDir() string {
	if d := os.Getenv("FLOOR_GOLDEN"); d != "" {
		return d
	}
	return "testdata"
}

func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenDir(), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}

// SeedRows reads golden/schedule.json's preset_rows: id → row.
func seedRows(t *testing.T) map[uint8]PresetSpec {
	t.Helper()
	var g struct {
		PresetRows []struct {
			Spec struct {
				ID    uint8  `json:"id"`
				Bytes string `json:"bytes"`
			} `json:"spec"`
		} `json:"preset_rows"`
	}
	readJSON(t, "schedule.json", &g)
	rows := map[uint8]PresetSpec{}
	for _, r := range g.PresetRows {
		b, err := hex.DecodeString(r.Spec.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		p, ok := PresetFromBytes(b)
		if !ok || p.ID != r.Spec.ID || !SpecOk(&p) {
			t.Fatalf("preset row %d: from_bytes %v, spec_ok %v", r.Spec.ID, ok, SpecOk(&p))
		}
		if hex.EncodeToString(p.ToBytes()) != r.Spec.Bytes {
			t.Fatalf("preset row %d: to_bytes differs from the golden", p.ID)
		}
		rows[p.ID] = p
	}
	return rows
}

func q(v U) string { return v.Dec() }

func takeJSON(t Take) map[string]any {
	ex := make([]any, MaxExtraLegs)
	for i, x := range t.Extra {
		ex[i] = q(x)
	}
	return map[string]any{"skim_bps": float64(t.SkimBps), "skim": q(t.Skim), "cut": q(t.Cut), "kept": q(t.Kept),
		"creator": q(t.Creator), "platform": q(t.Platform), "extra": ex, "floor_token": q(t.FloorToken),
		"total": q(t.Total)}
}

func buyJSON(r *BuyReceipt) map[string]any {
	return map[string]any{"leg": q(r.Leg), "spent": q(r.Spent), "refund": q(r.Refund), "take": takeJSON(r.Take),
		"swept": q(r.Swept), "out": q(r.Out), "window_out": q(r.WindowOut), "window_pay": q(r.WindowPay),
		"schedule_out": q(r.ScheduleOut), "schedule_pay": q(r.SchedulePay), "lot_out": q(r.LotOut),
		"flat_lot_out": q(r.FlatLotOut), "flat_tail_out": q(r.FlatTailOut), "flat_pay": q(r.FlatPay),
		"fill_cut": q(r.FillCut), "fill_slot": q(r.FillSlot), "dust": q(r.Dust), "routing": routingJSON(r.Routing),
		"graduated_now":        r.GraduatedNow,
		"graduation_price_x64": q(r.GraduationPriceX64), "ended_now": r.EndedNow,
		"ms_platform": q(r.MsPlatform), "ms_floor": q(r.MsFloor), "lot_pay": q(r.LotPay),
		"tail_pay": q(r.TailPay), "tail_to_floor": q(r.TailToFloor)}
}

func sellJSON(r *SellReceipt) map[string]any {
	return map[string]any{"tokens": q(r.Tokens), "gross": q(r.Gross), "from_cushion": q(r.FromCushion),
		"from_floor": q(r.FromFloor), "take": takeJSON(r.Take), "payout": q(r.Payout), "swept": q(r.Swept), "kept_to_platform": q(r.KeptToPlatform),
		"routing": routingJSON(r.Routing), "tail_tokens": q(r.TailTokens), "tail_from_floor": q(r.TailFromFloor),
		"tail_from_money": q(r.TailFromMoney)}
}

func routingJSON(g Routing) map[string]any {
	refs := make([]any, 3)
	for i, x := range g.CreatorRefs {
		refs[i] = q(x)
	}
	members := make([]any, MaxMembers)
	for i, x := range g.SlotMembers {
		members[i] = q(x)
	}
	return map[string]any{"creator_refs": refs, "to_creator": q(g.ToCreator), "creator_to_slot": q(g.CreatorToSlot),
		"creator_backing": q(g.CreatorBacking), "slot_members": members, "slot_dust": q(g.SlotDust)}
}

// refsOf is a create op's creator_refs (an input, set before the creator's buy).
func refsOf(op map[string]any) [3]bool {
	var r [3]bool
	if v, ok := op["creator_refs"].([]any); ok {
		for i := range r {
			r[i] = v[i].(bool)
		}
	}
	return r
}

func stateJSON(l *Launch) map[string]any {
	opt := func(v U, err error) any {
		if err != nil {
			return nil
		}
		return q(v)
	}
	so, e1 := l.SOut()
	fl, e2 := l.FloorX64()
	lb, e3 := l.LiveBuyPriceX64()
	return map[string]any{
		"books": map[string]any{"s": q(l.St.S), "h": q(l.St.H), "l": q(l.St.L), "floor_x64": q(l.St.FloorX64)},
		"pots": map[string]any{"floor": q(l.Pots.Floor), "cushion": q(l.Pots.Cushion),
			"bid_sqrt_x64": q(l.Pots.BidSqrtX64), "liq": q(l.Pots.Liq)},
		"supply": q(l.Supply), "window_left": q(l.WindowLeft), "inventory": q(l.Inventory),
		"lot_tokens": q(l.LotTokens), "lot_cost": q(l.LotCost), "tail": q(l.Tail),
		"creator_alloc": q(l.CreatorAlloc), "ended": l.Ended,
		"alloc_releases_done": l.AllocReleasesDone, "alloc_next_release_at": l.AllocNextReleaseAt,
		"graduated": l.Graduated, "s_out": opt(so, e1), "floor_x64": opt(fl, e2), "live_buy_price_x64": opt(lb, e3),
		"tail_liq": q(l.TailLiq), "tail_sqrt_x64": q(l.TailSqrtX64), "tail_sqrt_end_x64": q(l.TailSqrtEndX64),
		"tail_money": q(l.TailMoney), "tail_price_x64": q(l.TailPriceX64()), "lot_bins": binsJSON(&l.Lot),
	}
}

// binsJSON is the lot's bins as the goldens print them: [key, tokens, cost] per bin in use.
func binsJSON(l *Lot) []any {
	out := []any{}
	for _, b := range l.InUse() {
		out = append(out, []any{b.Key, q(b.Tokens), q(b.Cost)})
	}
	return out
}

// norm round-trips a value through JSON so both sides compare as the same Go types.
func norm(v any) any {
	b, _ := json.Marshal(v)
	var o any
	_ = json.Unmarshal(b, &o)
	return o
}

func releaseJSON(r *AllocRelease) map[string]any {
	members := make([]any, MaxMembers)
	for i, x := range r.Members {
		members[i] = q(x)
	}
	return map[string]any{"ok": q(r.Tokens), "release_index": r.ReleaseIndex, "members": members,
		"released": q(r.Released), "next_release_at": r.NextReleaseAt}
}

func optionJSON(s *TokenOptionState) map[string]any {
	return map[string]any{"total": q(s.Total), "released": q(s.Released), "releases_done": s.ReleasesDone,
		"release_count": s.ReleaseCount, "next_release_index": s.NextReleaseIndex, "next_release_at": s.NextReleaseAt,
		"next_release_tokens": q(s.NextReleaseTokens), "lines_met": s.LinesMet, "due": s.Due}
}

// nowOf is an unlock op's time (unix seconds).
func nowOf(op map[string]any) uint64 {
	v, _ := op["now"].(float64)
	return uint64(v)
}

func errJSON(err error) map[string]any { return map[string]any{"err": err.Error()} }

type walkT struct {
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

func loadWalks(t *testing.T) []walkT {
	var g struct {
		Walks []walkT `json:"walks"`
	}
	readJSON(t, "launch.json", &g)
	if len(g.Walks) == 0 {
		t.Fatal("no walks")
	}
	return g.Walks
}

func str(m map[string]any, k string) string { return m[k].(string) }

// replay runs one walk and returns the first mismatch ("" when every receipt matches) and the op count.
func replay(rows map[uint8]PresetSpec, w walkT, restore bool) (string, int) {
	spec, ok := rows[w.PresetID]
	if !ok {
		return fmt.Sprintf("no row %d", w.PresetID), 0
	}
	if w.RowTeamSlotMaxBps != nil {
		spec.TeamSlotMaxBps = *w.RowTeamSlotMaxBps
	}
	var l Launch
	var live U
	for i, op := range w.Ops {
		if restore && i > 0 {
			l = roundTrip(l, w.TokenDecimals, w.QuoteDecimals, D(w.QuoteUsdCentsX64))
		}
		var got, want any = nil, norm(op["receipt"])
		switch op["op"] {
		case "create":
			lc, r, err := Create(&spec, D(w.QuoteUsdCentsX64), w.QuoteDecimals, w.TokenDecimals, uint64(op["created_slot"].(float64)), refsOf(op), D(str(op, "creator_quote")))
			if err != nil {
				return fmt.Sprintf("op %d create: %v", i, err), i
			}
			l = lc
			// the live supply is in chain units
			if live, err = l.chain(l.Supply); err != nil {
				return fmt.Sprintf("op %d create: %v", i, err), i
			}
			if w.TeamSlot != nil {
				l.SlotRateBps, l.SlotBoughtPpm = w.TeamSlot.RateBps, w.TeamSlot.BoughtPpm
				l.SlotWeights = w.TeamSlot.Weights
			}
			if r != nil {
				got = norm(buyJSON(r))
			}
		case "buy":
			live = D(str(op, "live_supply"))
			r, err := l.Buy(D(str(op, "leg")), uint64(op["slot"].(float64)), D(str(op, "live_supply")))
			if err != nil {
				got = norm(errJSON(err))
			} else {
				got = norm(buyJSON(&r))
			}
		case "sell":
			live = D(str(op, "live_supply"))
			r, err := l.Sell(D(str(op, "tokens")), D(str(op, "live_supply")))
			if err != nil {
				got = norm(errJSON(err))
			} else {
				got = norm(sellJSON(&r))
			}
		case "unlock_creator_alloc":
			r, err := l.UnlockCreatorAlloc(nowOf(op))
			if err != nil {
				got = norm(errJSON(err))
			} else {
				got = norm(releaseJSON(&r))
			}
			if want, ok := op["token_option"]; ok {
				st, err := l.TokenOptionState(nowOf(op))
				if err != nil {
					return fmt.Sprintf("op %d token_option: %v", i, err), i
				}
				if g := norm(optionJSON(&st)); !reflect.DeepEqual(g, norm(want)) {
					return fmt.Sprintf("op %d token_option:\n got  %v\n want %v", i, g, norm(want)), i
				}
			}
		case "burn":
			// the live supply before the burn is the last trade's; the receipt is the supply after
			after, err := l.Burn(live, D(str(op, "tokens")))
			if err != nil || q(after) != str(op, "live_supply_after") {
				return fmt.Sprintf("op %d burn: got %v %v want %v", i, q(after), err, op["live_supply_after"]), i
			}
			live = after
			continue
		default:
			return fmt.Sprintf("op %d: unknown %v", i, op["op"]), i
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Sprintf("op %d %v receipt:\n got  %v\n want %v", i, op["op"], got, want), i
		}
		if a, ok := op["after"]; ok {
			st := norm(stateJSON(&l))
			if !reflect.DeepEqual(st, norm(a)) {
				return fmt.Sprintf("op %d %v state after:\n got  %v\n want %v", i, op["op"], st, norm(a)), i
			}
		}
	}
	return "", len(w.Ops)
}

func TestLaunchGoldenWalks(t *testing.T) {
	rows := seedRows(t)
	walks := loadWalks(t)
	n18 := 0
	for _, w := range walks {
		w := w
		name := fmt.Sprintf("preset%d_%s_slot%v", w.PresetID, w.Quote, w.TeamSlot != nil)
		t.Run(name, func(t *testing.T) {
			if msg, k := replay(rows, w, false); msg != "" {
				t.Fatalf("%s (after %d ops): %s", name, k, msg)
			}
		})
		if w.TokenDecimals == 18 {
			n18++
		}
	}
	// on ETH, USDC (6 dp) and USDT (18 dp)
	if n18 != 21 {
		t.Fatalf("want 21 walks with an 18-decimal token, have %d", n18)
	}
}

// roundTrip is what the tracker does: the launch as the contract stores it (the row and the schedule as bytes), restored.
func roundTrip(l Launch, tokenDecimals, quoteDecimals uint8, quoteUsdCentsX64 U) Launch {
	spec, ok1 := PresetFromBytes(l.Spec.ToBytes())
	sch, ok2 := ScheduleFromBytes(l.Sch.ToBytes())
	if !ok1 || !ok2 {
		panic("bytes")
	}
	_, raise, err := presetOf(&spec, quoteUsdCentsX64, quoteDecimals)
	if err != nil {
		panic(err)
	}
	c := chainViews(l, spec, sch, raise, tokenDecimals, quoteDecimals)
	c.QuoteUsdCentsX64 = quoteUsdCentsX64
	r, err := Restore(c)
	if err != nil {
		panic(err)
	}
	return r
}

// chainViews is what the contract's views report for l (IFloorAmm v1.3a): launchState's token amounts in chain units
// (math × scale), its p0 per chain unit (math ÷ scale, floored); launchMarket's books and curve in math units.
func chainViews(l Launch, spec PresetSpec, sch Schedule, raise U, tokenDecimals, quoteDecimals uint8) *OnChain {
	k := l.scale()
	c := func(v U) U { return mul(v, k) }
	return &OnChain{Spec: spec, Schedule: sch, Books: l.St, P0X64: div(l.P0X64, k), Raise: raise, Floor: l.Pots.Floor,
		Cushion: l.Pots.Cushion, BidSqrtX64: l.Pots.BidSqrtX64, BidLiq: l.Pots.Liq, Supply: c(l.Supply),
		WindowLeft: c(l.WindowLeft), Inventory: c(l.Inventory), LotTokens: c(l.LotTokens), LotCost: l.LotCost, Tail: c(l.Tail),
		CreatorAlloc: c(l.CreatorAlloc), CreatorUnlockMcap: l.CreatorUnlockMcap,
		CreatorUnlockBacking: l.CreatorUnlockBacking, AllocReleasesDone: l.AllocReleasesDone,
		AllocNextReleaseAt: l.AllocNextReleaseAt, Ended: l.Ended, Graduated: l.Graduated, SlotRateBps: l.SlotRateBps, SlotBoughtPpm: l.SlotBoughtPpm,
		SlotWeights: l.SlotWeights, CreatorRefs: l.CreatorRefs, CreatedBlock: l.CreatedSlot, TokenDecimals: tokenDecimals, QuoteDecimals: quoteDecimals,
		TailLiq: l.TailLiq, TailSqrtX64: l.TailSqrtX64, TailSqrtEndX64: l.TailSqrtEndX64, TailMoney: l.TailMoney,
		LotBins: append([]LotBin(nil), l.Lot.InUse()...)}
}

// TestRestoredLaunchReplays: the walks again, each op on a launch restored from its stored form (what the tracker
// reads) — Restore loses nothing the trade path reads.
func TestRestoredLaunchReplays(t *testing.T) {
	rows := seedRows(t)
	for _, w := range loadWalks(t) {
		if msg, k := replay(rows, w, true); msg != "" {
			t.Fatalf("preset %d %s (after %d ops): %s", w.PresetID, w.Quote, k, msg)
		}
	}
}

// TestDipWalks: the buy side coming down — each row climbs, is dumped four times and bought back in pieces, the
// bought-back lot first at its own ask (golden launch.json `dips`), receipt for receipt, also on a restored launch.
func TestDipWalks(t *testing.T) {
	rows := seedRows(t)
	var g struct {
		Dips []walkT `json:"dips"`
	}
	readJSON(t, "launch.json", &g)
	if len(g.Dips) < 3 {
		t.Fatalf("%d dip walks", len(g.Dips))
	}
	lotFirst := 0
	for _, w := range g.Dips {
		for _, restore := range []bool{false, true} {
			if msg, k := replay(rows, w, restore); msg != "" {
				t.Fatalf("dips preset %d %s restore %v (after %d ops): %s", w.PresetID, w.Quote, restore, k, msg)
			}
		}
		for _, op := range w.Ops {
			if r, ok := op["receipt"].(map[string]any); ok && op["op"] == "buy" && r["lot_out"] != nil && r["lot_out"] != "0" {
				lotFirst++
			}
		}
	}
	if lotFirst < 50 {
		t.Fatalf("only %d buys took the lot first", lotFirst)
	}
}
