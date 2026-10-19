package floormath

import "testing"

// After the schedule's end the bought-back lot's ask can TIE the tail range's price (the cushion's top at 1.00 × the frozen
// price): the lot then sells (at or under the range's price), and a range capped at the lot's ask that cannot move hands
// over to the lot, so no buy is refunded for want of tokens. Bluechip on a 9-decimal quote at $150: 50 quote in, a third
// sold back, then one buy past the end and three more.
func TestTieAfterTheEndNeverFreezesABuy(t *testing.T) {
	rows := seedRows(t)
	spec := rows[2]
	cents := lsh(n(15_000), 64)
	l, _, err := Create(&spec, cents, 9, 6, 0, [3]bool{}, zero)
	if err != nil {
		t.Fatal(err)
	}
	unit := n(1_000_000_000)
	now := uint64(spec.SnipeSlots)
	if _, err := l.Buy(mul(n(50), unit), now, l.Supply); err != nil {
		t.Fatal(err)
	}
	so, _ := l.SOut()
	if _, err := l.Sell(div(so, n(3)), l.Supply); err != nil {
		t.Fatal(err)
	}
	r, err := l.Buy(sub(D("10000000000000000000"), one), now, l.Supply)
	if err != nil || !r.EndedNow {
		t.Fatalf("the buy past the end: %+v %v", r, err)
	}
	if isZ(add(r.FlatLotOut, r.FlatTailOut)) {
		t.Fatalf("the buy that ended the schedule took nothing after the end: %+v", r)
	}
	for i := 0; i < 3; i++ {
		b, err := l.Buy(D("1000000000000000000"), now, l.Supply)
		if err != nil || isZ(b.Out) || !isZ(b.Refund) {
			t.Fatalf("buy %d after the end: out %s refund %s %v", i, b.Out.Dec(), b.Refund.Dec(), err)
		}
	}
}

// A capped range buy whose curve price is under f pays the least prices (f on what it keeps, and the line) for ALL its
// tokens, so it reaches its cap in one step instead of creeping toward it: with a big offer, √p after is the cap and the
// range kept at least f a token.
func TestCappedRangeBuyUnderTheFloorReachesItsCap(t *testing.T) {
	tokens := D("10000000000000")
	liq, se, err := tailOpen(tokens, div(one64, n(1_000)))
	if err != nil {
		t.Fatal(err)
	}
	sOut := D("1000000000000")
	floor := div(sOut, n(500)) // f at twice the curve's price
	sqrtCap := add(se, div(se, n(100)))
	b, err := tailBuy(liq, se, tokens, D("1000000000000000"), one64, floor, sOut, zero, sqrtCap, max128)
	if err != nil || isZ(b.Out) {
		t.Fatalf("%+v %v", b, err)
	}
	if !eq(b.SqrtAfter, sqrtCap) {
		t.Fatalf("√p after %s, want the cap %s (%+v)", b.SqrtAfter.Dec(), sqrtCap.Dec(), b)
	}
	if lt(mul(b.Kept, sOut), mul(b.Out, floor)) {
		t.Fatalf("under f: %+v", b)
	}
}
