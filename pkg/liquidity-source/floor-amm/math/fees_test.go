package floormath

import "testing"

// The skim's ramp spans a price 1.2× the floor: the constant is log2 6 − log2 5 within a unit, closeness is full at the
// floor and 0 from 1.2× it, half way at √1.2×. And a row with a 0 far buy skim is refused.
func TestSkimRampSpansOnePointTwo(t *testing.T) {
	l6, _ := log2X64(n(6))
	l5, _ := log2X64(n(5))
	d := sub(l6, l5)
	if diff := satSub(umax(d, skimRampLog2X64), umin(d, skimRampLog2X64)); gt(diff, one) {
		t.Fatalf("ramp %s, log2 6 − log2 5 %s", skimRampLog2X64.Dec(), d.Dec())
	}
	f := div(lsh(one, 64), n(1_000))
	if c := closenessBps(f, f); c != 10_000 {
		t.Fatalf("at the floor %d", c)
	}
	if c := closenessBps(div(mul(f, n(6)), n(5)), f); c > 1 {
		t.Fatalf("at 1.2× %d", c)
	}
	if c := closenessBps(div(mul(f, n(121)), n(100)), f); c != 0 {
		t.Fatalf("at 1.21× %d", c)
	}
	// √1.2 ≈ 1.095445115
	if c := closenessBps(div(mul(f, n(1_095_445_115)), n(1_000_000_000)), f); c < 4_999 || c > 5_001 {
		t.Fatalf("half way %d", c)
	}
	for _, p := range seedRows(t) {
		q := p
		q.SkimBuyFarBps, q.SkimBuyAtFloorBps = 0, 0
		if SpecOk(&q) {
			t.Fatalf("row %d: a 0 far buy skim passed spec_ok", p.ID)
		}
	}
}
