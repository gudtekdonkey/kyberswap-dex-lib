package floormath

import (
	"encoding/json"
	"testing"
)

// golden/trade.json's lot_ask: every bin of the drawn launches' lots, its ask (lotAskX64 on ONE bin, quote-free and with a
// take / kept skim), its backing line (lotBackingLineX64), the credit top and the one-bin live buy price.
func TestLotAskGolden(t *testing.T) {
	var g struct {
		LotAsk []struct {
			Pots struct {
				Floor      string `json:"floor"`
				Cushion    string `json:"cushion"`
				BidSqrtX64 string `json:"bid_sqrt_x64"`
				Liq        string `json:"liq"`
			} `json:"pots"`
			SOut      string                     `json:"s_out"`
			LotCost   string                     `json:"lot_cost"`
			LotTokens string                     `json:"lot_tokens"`
			U         string                     `json:"u"`
			Take      string                     `json:"take"`
			Kept      string                     `json:"kept"`
			Leg       string                     `json:"leg"`
			Price     string                     `json:"price_x64"`
			CurveLive bool                       `json:"curve_live"`
			Line      map[string]string          `json:"backing_line_x64"`
			Ask       map[string]json.RawMessage `json:"ask"`
			Top       map[string]string          `json:"credit_top"`
			Live      map[string]string          `json:"live_buy_price_x64"`
		} `json:"lot_ask"`
	}
	readJSON(t, "trade.json", &g)
	if len(g.LotAsk) < 100 {
		t.Fatalf("%d lot_ask vectors", len(g.LotAsk))
	}
	asked := 0
	for i, v := range g.LotAsk {
		p := Pots{Floor: D(v.Pots.Floor), Cushion: D(v.Pots.Cushion), BidSqrtX64: D(v.Pots.BidSqrtX64), Liq: D(v.Pots.Liq)}
		sOut, cost, tokens, u, price := D(v.SOut), D(v.LotCost), D(v.LotTokens), D(v.U), D(v.Price)
		tk, kept, leg := D(v.Take), D(v.Kept), D(v.Leg)
		if live, err := curveLive(&p, sOut); err != nil || live != v.CurveLive {
			t.Fatalf("vector %d: curve_live %v %v", i, live, err)
		}
		line, err := lotBackingLineX64(satAdd(p.Floor, p.Cushion), sOut, tk, kept, leg, u)
		if err != nil || line.Dec() != v.Line["ok"] {
			t.Fatalf("vector %d: line %s %v, want %v", i, line.Dec(), err, v.Line)
		}
		ask, err := lotAskX64(&p, sOut, cost, tokens, u, tk, kept, leg, zero)
		raw, ok := v.Ask["ok"]
		if err != nil || !ok {
			t.Fatalf("vector %d: ask %v, want %s", i, err, v.Ask)
		}
		if string(raw) == "null" {
			if ask != nil {
				t.Fatalf("vector %d: ask %s, want none", i, ask.Dec())
			}
		} else {
			var want string
			if err := json.Unmarshal(raw, &want); err != nil || ask == nil || ask.Dec() != want {
				t.Fatalf("vector %d: ask %v, want %s", i, ask, raw)
			}
			asked++
			bt, err := bidTopX64(price, u)
			if err != nil {
				t.Fatal(err)
			}
			top, err := lotCreditTop(bt, *ask, u)
			if w, ok := v.Top["ok"]; !ok || err != nil || top.Dec() != w {
				t.Fatalf("vector %d: credit top %s %v, want %v", i, top.Dec(), err, v.Top)
			}
		}
		if lp, err := liveBuyPriceX64(price, &p, sOut, cost, tokens, u); err != nil || lp.Dec() != v.Live["ok"] {
			t.Fatalf("vector %d: live price %s %v, want %v", i, lp.Dec(), err, v.Live)
		}
	}
	if asked < 50 {
		t.Fatalf("only %d bins asked", asked)
	}
}

// golden/trade.json's lot_bins: the grid (key_of / edge_x64 / pieces / piece_key), 40 inserts with merging (the bins
// after each), and 24 drawn split_curve + split_run (the bins after).
func TestLotBinsGolden(t *testing.T) {
	var g struct {
		LotBins []struct {
			Kind      string   `json:"kind"`
			Price     string   `json:"price_x64"`
			Key       uint32   `json:"key"`
			Edge      string   `json:"edge_x64"`
			EdgeNext  string   `json:"edge_next_x64"`
			PiecesHi  uint32   `json:"pieces_hi"`
			Pieces    []uint32 `json:"pieces"`
			PieceKeys []uint32 `json:"piece_keys"`
			Tokens    string   `json:"tokens"`
			Cost      string   `json:"cost"`
			Curve     []string `json:"curve"`
			Run       []string `json:"run"`
			Bins      [][]any  `json:"bins"`
		} `json:"lot_bins"`
	}
	readJSON(t, "trade.json", &g)
	same := func(l *Lot, want [][]any) bool {
		return jsonEq(binsJSON(l), want)
	}
	var ins Lot
	kinds := map[string]int{}
	for i, v := range g.LotBins {
		kinds[v.Kind]++
		switch v.Kind {
		case "grid":
			if k := lotKeyOf(D(v.Price)); k != v.Key {
				t.Fatalf("row %d: key_of %d, want %d", i, k, v.Key)
			}
			if e := lotEdgeX64(v.Key); e.Dec() != v.Edge {
				t.Fatalf("row %d: edge %s, want %s", i, e.Dec(), v.Edge)
			}
			if e := lotEdgeX64(v.Key + 1); e.Dec() != v.EdgeNext {
				t.Fatalf("row %d: next edge %s, want %s", i, e.Dec(), v.EdgeNext)
			}
			o, cnt := lotPieces(v.Key, v.PiecesHi)
			if !jsonEq(o[:cnt+1], v.Pieces) {
				t.Fatalf("row %d: pieces %v, want %v", i, o[:cnt+1], v.Pieces)
			}
			keys := make([]uint32, cnt)
			for j := range keys {
				keys[j] = lotPieceKey(v.Key, o[:], j)
			}
			if !jsonEq(keys, v.PieceKeys) {
				t.Fatalf("row %d: piece keys %v, want %v", i, keys, v.PieceKeys)
			}
		case "insert":
			if err := ins.insert(v.Key, D(v.Tokens), D(v.Cost)); err != nil || !same(&ins, v.Bins) {
				t.Fatalf("row %d: insert %v: bins %v, want %v", i, err, binsJSON(&ins), v.Bins)
			}
		case "split":
			var l Lot
			c, r := v.Curve, v.Run
			if err := l.splitCurve(D(c[0]), D(c[1]), D(c[2]), D(c[3]), D(c[4])); err != nil {
				t.Fatalf("row %d: split_curve %v", i, err)
			}
			if err := l.splitRun(D(r[0]), D(r[1]), D(r[2]), D(r[3])); err != nil {
				t.Fatalf("row %d: split_run %v", i, err)
			}
			if !same(&l, v.Bins) {
				t.Fatalf("row %d: split bins %v, want %v", i, binsJSON(&l), v.Bins)
			}
			// the pieces sum to the stages' tokens and quote exactly
			tt, tq := l.totals()
			if !eq(tt, add(D(c[0]), D(r[0]))) || !eq(tq, add(D(c[1]), D(r[1]))) {
				t.Fatalf("row %d: totals %s / %s", i, tt.Dec(), tq.Dec())
			}
		default:
			t.Fatalf("row %d: kind %q", i, v.Kind)
		}
	}
	if kinds["grid"] != 48 || kinds["insert"] != 40 || kinds["split"] != 24 {
		t.Fatalf("rows %v", kinds)
	}
	if ins.Len != LotBins {
		t.Fatalf("the inserts end with %d bins, not every bin in use", ins.Len)
	}
	// take: a whole bin leaves with its cost and the bins above move down
	tok, c := ins.Bins[0].Tokens, ins.Bins[0].Cost
	got, err := ins.take(0, tok)
	if err != nil || !eq(got, c) || ins.Len != LotBins-1 {
		t.Fatalf("take: %s %v, len %d", got.Dec(), err, ins.Len)
	}
}

// jsonEq compares two values as their JSON forms.
func jsonEq(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}
