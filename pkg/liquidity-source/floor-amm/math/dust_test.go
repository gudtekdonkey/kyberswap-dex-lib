package floormath

import "testing"

// A burn that leaves a dust S_out (F·2^64 ÷ S_out past u128) never freezes a launch: a buy is refunded whole, the last
// holder's sell redeems the floor, and trading restarts.
func TestDustSOutNeverFreezesTrading(t *testing.T) {
	spec := seedRows(t)[1]
	ether := D("1000000000000000000")
	l, _, err := Create(&spec, lsh(n(240_000), 64), 18, 18, 0, [3]bool{}, zero)
	if err != nil {
		t.Fatal(err)
	}
	live := l.Supply
	r, err := l.Buy(mul(n(1_000), ether), 1_000_000, live)
	if err != nil {
		t.Fatal(err)
	}
	if live, err = l.Burn(live, sub(r.Out, n(1))); err != nil {
		t.Fatal(err)
	}
	f := l.Pots.Floor
	b, err := l.Buy(ether, 1_000_000, live)
	if err != nil {
		t.Fatalf("a buy refused after a burn to dust: %v", err)
	}
	if !isZ(b.Out) || !eq(b.Refund, ether) {
		t.Fatalf("refunded whole: out %v refund %v", &b.Out, &b.Refund)
	}
	s, err := l.Sell(n(1), live)
	if err != nil {
		t.Fatalf("the last holder's sell refused: %v", err)
	}
	if !gt(s.Payout, div(f, n(2))) {
		t.Fatalf("the last holder redeemed %v of F %v", &s.Payout, &f)
	}
	b, err = l.Buy(ether, 1_000_000, live)
	if err != nil || isZ(b.Out) {
		t.Fatalf("trading restarts: out %v err %v", &b.Out, err)
	}
}
