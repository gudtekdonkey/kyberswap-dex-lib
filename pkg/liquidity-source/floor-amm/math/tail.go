package floormath

// tail.rs: THE TAIL RANGE. After the schedule's end the tail (1 % of the supply plus any inventory the schedule did not
// sell) is ONE constant-product range owned by the program, from the schedule's frozen price to an infinite price.
// L = ⌊tail · √frozen⌋ is fixed at the end; the curve holds ⌈L ÷ √p⌉ tokens, so it never runs out. A buy raises √p by
// what the range keeps ((kept − 1) ÷ L), floor first (⌈F·n ÷ S_out⌉ to F); a sell lowers it down to the frozen price, f a
// token from F and the rest from the range's money. Every rounding is against the trader. √p is √(price) · 2^64
// (sqrtX64), L quote base units per √p.

// tailOpen is tail::open: (L, √frozen) for `tokens` held at the frozen price.
func tailOpen(tokens, frozenX64 U) (U, U, error) {
	se := sqrtX64(frozenX64)
	liq, err := mulDivDown(tokens, se, one64)
	if err != nil {
		return zero, zero, err
	}
	return liq, se, nil
}

// tailTokensAt is tail::tokens_at: ⌈L · 2^64 ÷ √p⌉ (0 with no liquidity; at least 1 with some).
func tailTokensAt(liq, sqrt U) (U, error) {
	if isZ(liq) {
		return zero, nil
	}
	v, err := toU128(divCeil(lsh(liq, 64), umax(sqrt, one)))
	if err != nil {
		return zero, err
	}
	return umax(v, one), nil
}

// tailTokensFloor is ⌊L · 2^64 ÷ √p⌋: the tokens the curve surely holds at √p. A buy counts its tokens from here (never an
// unpaid base unit); a sell returns into ⌊L ÷ √p⌋ + k, so its √p″ is rounded up.
func tailTokensFloor(liq, sqrt U) (U, error) {
	return toU128(div(lsh(liq, 64), umax(sqrt, one)))
}

// tailOwed is tail::owed: what the curve owes a seller from √p down to the frozen price, ⌊L · (√p − √end) ÷ 2^64⌋.
func tailOwed(liq, sqrt, sqrtEnd U) (U, error) {
	return mulDivDown(liq, satSub(sqrt, sqrtEnd), one64)
}

// tailPriceX64 is tail::price_x64: the range's price, Q64.64 (rounded down, saturating).
func tailPriceX64(sqrt U) U { return priceOfSqrt(sqrt) }

// TailBuy is tail::TailBuy: what one buy took from the range.
type TailBuy struct {
	Pay, Out, Kept, Cut, ToFloor, ToMoney, SqrtAfter U
}

// TailSell is tail::TailSell: what one sell put back into the range.
type TailSell struct {
	Tokens, Gross, FromFloor, FromMoney, SqrtAfter U
}

// tailBuy is tail::buy: a buy of at most `offer` quote from the range at √p — `held` its tokens (it keeps one), u what it
// keeps (the cut-adjusted keep), floor / sOut the pots' F and the tokens outside (at least f a token), line the backing
// line (0 = none), sqrtCap a √p it stops at (max128 = none), tokenCap the most tokens. Nothing out: nothing charged.
func tailBuy(liq, sqrt, held, offer, u, floor, sOut, line, sqrtCap, tokenCap U) (TailBuy, error) {
	none := TailBuy{SqrtAfter: sqrt}
	if isZ(liq) || isZ(offer) || isZ(u) {
		return none, nil
	}
	x0, err := tailTokensFloor(liq, sqrt)
	if err != nil {
		return TailBuy{}, err
	}
	maxOut := umin(umin(tokenCap, satSub(held, one)), satSub(x0, one))
	// the money's move: √p + (kept − 1) ÷ L
	kept, err := mulDivDown(offer, u, one64)
	if err != nil {
		return TailBuy{}, err
	}
	s1 := max128
	if d, err := mulDivDown(satSub(kept, one), one64, liq); err == nil {
		if v, err := addC(sqrt, d); err == nil {
			s1 = v
		}
	}
	capped := false
	if gt(s1, sqrtCap) {
		s1 = umax(sqrtCap, sqrt)
		capped = true
	}
	t1, err := tailTokensAt(liq, s1)
	if err != nil {
		return TailBuy{}, err
	}
	nOut := satSub(x0, t1)
	if gt(nOut, maxOut) {
		nOut = maxOut
		v, err := mulDivUp(liq, one64, sub(x0, nOut))
		if err != nil {
			return TailBuy{}, err
		}
		s1 = umax(sqrt, v)
		capped = true
	}
	if isZ(nOut) {
		return none, nil
	}
	// a capped move charges what it needs: ⌈(⌈L·(√p′ − √p) ÷ 2^64⌉ + 1) · 2^64 ÷ u⌉, and at least the least prices below
	// for all its tokens (⌈⌈F·n ÷ S_out⌉ · 2^64 ÷ u⌉ and ⌈n · line ÷ 2^64⌉, saturating), so a capped move under f reaches
	// its cap in one step instead of creeping toward it
	pay := offer
	if capped {
		mv, err := mulDivUp(liq, sub(s1, sqrt), one64)
		if err != nil {
			return TailBuy{}, err
		}
		mv1, err := addC(mv, one)
		if err != nil {
			return TailBuy{}, err
		}
		v, err := mulDivUp(mv1, one64, u)
		if err != nil {
			return TailBuy{}, err
		}
		if !isZ(floor) && !isZ(sOut) {
			fn, err := mulDivUp(floor, nOut, sOut)
			if err != nil {
				return TailBuy{}, err
			}
			atF, err := mulDivUp(fn, one64, u)
			if err != nil {
				atF = max128
			}
			v = umax(v, atF)
		}
		if !isZ(line) {
			atLine, err := mulDivUp(nOut, line, one64)
			if err != nil {
				atLine = max128
			}
			v = umax(v, atLine)
		}
		pay = umin(v, offer)
	}
	if kept, err = mulDivDown(pay, u, one64); err != nil {
		return TailBuy{}, err
	}
	// the least prices: f a token from what the range keeps, the backing line from what the buyer pays
	m := nOut
	if !isZ(floor) && !isZ(sOut) {
		// a bound: past u128 it binds nothing (a huge leg fills or refunds, never an error)
		v, err := mulDivDown(kept, sOut, floor)
		if err != nil {
			v = max128
		}
		m = umin(m, v)
	}
	if !isZ(line) {
		v := div(lsh(pay, 64), line)
		if !fits(v) {
			v = max128
		}
		m = umin(m, v)
	}
	if isZ(m) {
		return none, nil
	}
	if lt(m, nOut) {
		// fewer tokens than the money's move: the curve moves by the tokens only (never down)
		v, err := mulDivUp(liq, one64, sub(x0, m))
		if err != nil {
			return TailBuy{}, err
		}
		s1 = umax(sqrt, umin(s1, v))
		nOut = m
	}
	toFloor := zero
	if !isZ(sOut) {
		v, err := mulDivUp(floor, nOut, sOut)
		if err != nil {
			return TailBuy{}, err
		}
		toFloor = umin(v, kept)
	}
	return TailBuy{Pay: pay, Out: nOut, Kept: kept, Cut: sub(pay, kept), ToFloor: toFloor, ToMoney: sub(kept, toFloor), SqrtAfter: s1}, nil
}

// tailSell is tail::sell: a sell of up to `tokens` into the range at √p, down to √end at most — token by token the larger
// of the curve and f: √p″ = ⌈L · 2^64 ÷ (⌊L ÷ √p⌋ + k)⌉ (√end exactly when the range is full again); ⌊F·k ÷ S_out⌋ from F
// (sOut before the sell); above √m = max(√p″, √f) the curve's pay ⌊L·(√p − √m) ÷ 2^64⌋ − 1 less ⌈F·k₁ ÷ S_out⌉ for its
// k₁ = ⌊L ÷ √m⌋ − ⌊L ÷ √p⌋ tokens from the range's money (at most what it holds); under √f a token gets f alone. A sell in
// pieces never collects more than one sell.
func tailSell(liq, sqrt, sqrtEnd, money, tokens, floor, sOut U) (TailSell, error) {
	none := TailSell{SqrtAfter: sqrt}
	if isZ(liq) || isZ(tokens) || le(sqrt, sqrtEnd) {
		return none, nil
	}
	x0, err := tailTokensFloor(liq, sqrt)
	if err != nil {
		return TailSell{}, err
	}
	xe, err := tailTokensAt(liq, sqrtEnd)
	if err != nil {
		return TailSell{}, err
	}
	k := umin(tokens, satSub(xe, x0))
	if isZ(k) {
		return none, nil
	}
	x1 := add(x0, k)
	s2 := sqrtEnd
	if lt(x1, xe) {
		v, err := mulDivUp(liq, one64, x1)
		if err != nil {
			return TailSell{}, err
		}
		s2 = umin(umax(v, sqrtEnd), sqrt)
	}
	fromFloor := zero
	if !isZ(sOut) {
		if fromFloor, err = mulDivDown(floor, k, sOut); err != nil {
			return TailSell{}, err
		}
	}
	f, err := FloorX64(floor, sOut)
	if err != nil {
		return TailSell{}, err
	}
	sf := sqrtX64(f)
	fromMoney := zero
	if gt(sqrt, sf) {
		sm := umax(s2, sf)
		// the tokens above √f: from ⌊L ÷ √p⌋ up to ⌊L ÷ √m⌋ (fewer: against the seller)
		xm, err := tailTokensFloor(liq, sm)
		if err != nil {
			return TailSell{}, err
		}
		k1 := umin(satSub(xm, x0), k)
		g1, err := mulDivDown(liq, sub(sqrt, sm), one64)
		if err != nil {
			return TailSell{}, err
		}
		g1 = satSub(g1, one)
		f1 := zero
		if !isZ(sOut) {
			if f1, err = mulDivUp(floor, k1, sOut); err != nil {
				return TailSell{}, err
			}
		}
		fromMoney = umin(satSub(g1, f1), money)
	}
	return TailSell{Tokens: k, Gross: add(fromFloor, fromMoney), FromFloor: fromFloor, FromMoney: fromMoney, SqrtAfter: s2}, nil
}
