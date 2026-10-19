package floormath

// bid.rs: the keeperless bid — the pots F / C, the stored cushion curve, a sell's fill and a credit.
// CUSHION_CAPPED_AT_TOP and CUSHION_TRACKS_PRICE are ON, as built (no `cushion-lags` feature here).

// bidMarginBps is BID_MARGIN_BPS (1.03): the floor's line (not the bid's top).
var bidMarginBps = n(10_300)

// bidTopMarginBps is BID_TOP_MARGIN_BPS (1.00): the cushion's top is what a buy keeps of the books' price.
var bidTopMarginBps = n(10_000)

// Pots is bid::Pots.
type Pots struct {
	Floor, Cushion, BidSqrtX64, Liq U
}

// SellFill is bid::SellFill. The stages' shapes let the lot record what each token cost (Lot.addSell): stage 1, the
// curve, CurveTokens for CurveQuote walked at CurveLiq from √CurveFromX64 down to √CurveToX64; stage 2, the cushion at
// the running floor RunFloor ÷ (RunS − x), RunTokens for RunQuote; stage 3 is FloorTokens / FromFloor. All 0 for a stage
// that did not run.
type SellFill struct {
	QuoteOut, CushionTokens, FromCushion, FloorTokens, FromFloor U
	After                                                        Pots
	CurveTokens, CurveQuote, CurveFromX64, CurveToX64, CurveLiq  U
	RunTokens, RunQuote, RunFloor, RunS                          U
}

func sqrtX64(p U) U { return isqrt(lsh(p, 64)) }

func priceOfSqrt(sq U) U {
	v := rsh(mul(sq, sq), 64)
	if !fits(v) {
		return max128
	}
	return v
}

// FloorX64 is bid::floor_x64: F ÷ S_out, Q64.64, rounded down; 0 with nothing outside.
func FloorX64(floor, sOut U) (U, error) {
	if isZ(sOut) {
		return zero, nil
	}
	// past u128 it saturates (a dust S_out): never a refusal
	v, err := toU128(div(lsh(floor, 64), sOut))
	if err != nil {
		return max128, nil
	}
	return v, nil
}

func bidTopX64(price, u U) (U, error) {
	kept, err := toU128(rsh(mul(price, u), 64))
	if err != nil {
		return zero, err
	}
	return mulDivDown(kept, bpsU, bidTopMarginBps)
}

func lotCostOut(cost, tokens, k U) (U, error) {
	if ge(k, tokens) {
		return cost, nil
	}
	v, err := mulDivUp(cost, k, tokens)
	if err != nil {
		return zero, err
	}
	return umin(v, cost), nil
}

func curveLive(p *Pots, sOut U) (bool, error) {
	if isZ(p.Cushion) || isZ(p.Liq) || isZ(sOut) {
		return false, nil
	}
	return gt(mul(priceOfSqrt(p.BidSqrtX64), sOut), lsh(p.Floor, 64)), nil
}

// bidPriceX64 is bid::bid_price_x64: the live bid — the curve's top while it pays, else the floor.
func bidPriceX64(p *Pots, sOut U) (U, error) {
	live, err := curveLive(p, sOut)
	if err != nil {
		return zero, err
	}
	if live {
		return priceOfSqrt(p.BidSqrtX64), nil
	}
	return FloorX64(p.Floor, sOut)
}

// capTop is bid::cap_top: a stored curve top above topX64 (the bid's top at the cut in force now) comes down to it, the
// curve's bottom and money kept; a top at or under the bottom or the floor leaves the curve dead and its money folds into
// F. Nothing above topX64: unchanged.
func capTop(p *Pots, sOut, topX64 U) (Pots, error) {
	a := *p
	st := sqrtX64(topX64)
	if isZ(a.Cushion) || isZ(a.Liq) || le(a.BidSqrtX64, st) {
		return a, nil
	}
	dc, err := mulDivDown(a.Cushion, one64, a.Liq)
	if err != nil {
		return Pots{}, err
	}
	sa := satSub(a.BidSqrtX64, dc)
	f, err := FloorX64(a.Floor, sOut)
	if err != nil {
		return Pots{}, err
	}
	sf := sqrtX64(f)
	if gt(st, satAdd(umax(sa, sf), one)) {
		a.BidSqrtX64 = st
		if a.Liq, err = mulDivDown(a.Cushion, one64, sub(st, sa)); err != nil {
			return Pots{}, err
		}
	} else {
		if a.Floor, err = addC(a.Floor, a.Cushion); err != nil {
			return Pots{}, err
		}
		a.Cushion, a.BidSqrtX64, a.Liq = zero, zero, zero
	}
	return a, nil
}

func meetSqrtX64(sb, liq, floor, sOut U) (U, error) {
	y, err := mulDivDown(liq, sb, one64)
	if err != nil {
		return zero, err
	}
	m, err := toU128(rsh(mul(priceOfSqrt(sb), sOut), 64))
	if err != nil {
		return zero, err
	}
	my, err := addC(m, y)
	if err != nil {
		return zero, err
	}
	// nothing outside and no liquidity left: the curve stays where it is
	if isZ(my) {
		return sb, nil
	}
	var f4 U
	fm := mul(floor, my)
	if _, over := f4.MulOverflow(&fm, ptr(n(4))); over {
		return zero, ErrOverflow
	}
	yy := mul(y, y)
	d := add(yy, f4)
	if lt(d, yy) {
		return zero, ErrOverflow
	}
	r, err := addC(isqrt(d), one)
	if err != nil {
		return zero, err
	}
	num, err := addC(y, r)
	if err != nil {
		return zero, err
	}
	my2, err := mulC(my, n(2))
	if err != nil {
		return zero, err
	}
	v, err := mulDivUp(sb, num, my2)
	if err != nil {
		return zero, err
	}
	return umin(v, sb), nil
}

func ptr(v U) *U { return &v }

func tokensBetween(liq, sb, sn U) (U, error) {
	if isZ(sn) {
		return max128, nil
	}
	t, err := mulDivDown(liq, sub(sb, sn), sn)
	if err != nil {
		return zero, err
	}
	return mulDivDown(t, one64, sb)
}

var invLn2P1 = add(invLn2X64, one)

// bidSell is bid::sell: the curve to the meet, the cushion at the running floor, then f.
func bidSell(p *Pots, sOut, x U) (SellFill, error) {
	if gt(x, sOut) {
		return SellFill{}, ErrOverflow
	}
	a := *p
	s := sOut
	rest := x
	ct, cq := zero, zero
	var c1 [5]U
	var run [4]U
	live, err := curveLive(&a, s)
	if err != nil {
		return SellFill{}, err
	}
	if !isZ(rest) && live {
		sb := a.BidSqrtX64
		sm, err := meetSqrtX64(sb, a.Liq, a.Floor, s)
		if err != nil {
			return SellFill{}, err
		}
		drop, err := mulDivDown(a.Cushion, one64, a.Liq)
		if err != nil {
			return SellFill{}, err
		}
		sa := umax(satSub(sb, drop), one)
		end, moneyOut := sm, false
		if ge(sa, sm) {
			end, moneyOut = sa, true
		}
		toEnd, err := tokensBetween(a.Liq, sb, end)
		if err != nil {
			return SellFill{}, err
		}
		var t, sn, q U
		if ge(rest, toEnd) {
			if moneyOut {
				q = a.Cushion
			} else {
				v, err := mulDivDown(a.Liq, sub(sb, end), one64)
				if err != nil {
					return SellFill{}, err
				}
				q = umin(v, a.Cushion)
			}
			t, sn = toEnd, end
		} else {
			rs, err := mulDivDown(rest, sb, one64)
			if err != nil {
				return SellFill{}, err
			}
			den, err := addC(a.Liq, rs)
			if err != nil {
				return SellFill{}, err
			}
			v, err := mulDivUp(sb, a.Liq, den)
			if err != nil {
				return SellFill{}, err
			}
			sn = umax(v, end)
			w, err := mulDivDown(a.Liq, sub(sb, sn), one64)
			if err != nil {
				return SellFill{}, err
			}
			t, q = rest, umin(w, a.Cushion)
		}
		if !isZ(a.Floor) {
			v, err := mulDivDown(q, s, a.Floor)
			if err != nil {
				return SellFill{}, err
			}
			t = umin(t, v)
		}
		if isZ(t) {
			sn, q = sb, zero
		}
		c1 = [5]U{t, q, sb, sn, a.Liq}
		a.Cushion = sub(a.Cushion, q)
		a.BidSqrtX64 = sn
		if isZ(a.Cushion) {
			a.Liq = zero
		}
		s, rest = sub(s, t), sub(rest, t)
		ct, cq = add(ct, t), add(cq, q)
	}
	top := priceOfSqrt(a.BidSqrtX64)
	var topLim *U
	if !isZ(rest) && !isZ(a.Cushion) && !isZ(a.Floor) && !isZ(top) {
		v, err := mulDivUp(a.Floor, one64, top)
		if err != nil {
			return SellFill{}, err
		}
		tl := satSub(s, v)
		topLim = &tl
	}
	if topLim != nil && isZ(*topLim) {
		a.Floor = add(a.Floor, a.Cushion)
		a.Cushion, a.Liq = zero, zero
	}
	if !isZ(rest) && !isZ(a.Cushion) && !isZ(a.Floor) {
		y, err := mulDivUp(a.Cushion, invLn2P1, a.Floor)
		if err != nil {
			return SellFill{}, err
		}
		s1 := zero
		if yi := rsh(y, 64); lt(yi, n(128)) {
			g, ok := exp2X64(and(y, mask64))
			if !ok {
				return SellFill{}, ErrOverflow
			}
			v, err := mulDivDown(s, one64, add(g, n(2)))
			if err != nil {
				return SellFill{}, err
			}
			s1 = rsh(v, uint(yi.Uint64()))
		}
		moneyCap := sub(s, umin(s1, s))
		capT := moneyCap
		if topLim != nil {
			capT = umin(*topLim, moneyCap)
		}
		topped := lt(capT, moneyCap)
		var t, q U
		lnPart := func(k U) (U, error) {
			ls, err := log2Z(s)
			if err != nil {
				return zero, err
			}
			lk, err := log2Z(sub(s, k))
			if err != nil {
				return zero, err
			}
			d := sub(ls, lk)
			v, err := mulDivDown(a.Floor, satSub(d, one), invLn2P1)
			if err != nil {
				return zero, err
			}
			return umin(v, a.Cushion), nil
		}
		if ge(rest, capT) && !topped {
			t, q = capT, a.Cushion
		} else if ge(rest, capT) {
			if q, err = lnPart(capT); err != nil {
				return SellFill{}, err
			}
			t = capT
		} else {
			if q, err = lnPart(rest); err != nil {
				return SellFill{}, err
			}
			t = rest
		}
		v, err := mulDivDown(q, s, a.Floor)
		if err != nil {
			return SellFill{}, err
		}
		t = umin(t, v)
		if isZ(t) {
			q = zero
		}
		run = [4]U{t, q, a.Floor, s}
		a.Cushion = sub(a.Cushion, q)
		if topped && ge(rest, capT) {
			a.Floor = add(a.Floor, a.Cushion)
			a.Cushion = zero
		}
		if isZ(a.Cushion) {
			a.Liq = zero
		}
		s, rest = sub(s, t), sub(rest, t)
		ct, cq = add(ct, t), add(cq, q)
	}
	fq := zero
	if !isZ(rest) {
		if fq, err = mulDivDown(a.Floor, rest, s); err != nil {
			return SellFill{}, err
		}
		a.Floor = sub(a.Floor, fq)
	}
	return SellFill{QuoteOut: add(cq, fq), CushionTokens: ct, FromCushion: cq, FloorTokens: rest, FromFloor: fq, After: a,
		CurveTokens: c1[0], CurveQuote: c1[1], CurveFromX64: c1[2], CurveToX64: c1[3], CurveLiq: c1[4],
		RunTokens: run[0], RunQuote: run[1], RunFloor: run[2], RunS: run[3]}, nil
}

// credit is bid::credit, floor first: the pots after.
func credit(p *Pots, sOut, k, amount, cushionRoom, topX64 U) (Pots, error) {
	a := *p
	first := zero
	if !isZ(sOut) {
		v, err := mulDivUp(a.Floor, k, sOut)
		if err != nil {
			return Pots{}, err
		}
		first = umin(v, amount)
	}
	left := sub(amount, first)
	toC := umin(left, cushionRoom)
	rest := sub(left, toC)
	live, err := curveLive(&a, sOut)
	if err != nil {
		return Pots{}, err
	}
	if a.Floor, err = addC(a.Floor, add(first, rest)); err != nil {
		return Pots{}, err
	}
	s, err := addC(sOut, k)
	if err != nil {
		return Pots{}, err
	}
	st := sqrtX64(topX64)
	reopen := func(c U) error {
		f, err := FloorX64(a.Floor, s)
		if err != nil {
			return err
		}
		sf := sqrtX64(f)
		a.BidSqrtX64 = st
		a.Liq = zero
		// a span too thin for its L to fit in u128 leaves L 0: the money parked in C on a dead curve, never a revert
		if gt(st, satAdd(sf, one)) {
			if v, err := mulDivDown(c, one64, sub(st, sf)); err == nil {
				a.Liq = v
			}
		}
		return nil
	}
	// the floor after is read only when the cushion takes money: a kept-skim credit never needs it
	b2 := false
	if !isZ(toC) && live {
		fa, err := FloorX64(a.Floor, s)
		if err != nil {
			return Pots{}, err
		}
		b2 = le(a.BidSqrtX64, sqrtX64(fa))
	}
	if b2 {
		if a.Floor, err = addC(a.Floor, a.Cushion); err != nil {
			return Pots{}, err
		}
		a.Cushion = toC
		if err = reopen(toC); err != nil {
			return Pots{}, err
		}
	} else if !isZ(toC) {
		c2, err := addC(a.Cushion, toC)
		if err != nil {
			return Pots{}, err
		}
		if live {
			// saturates: a √b past u128 is past the top, so the cushion re-spreads below
			sb2 := max128
			if d, err := mulDivDown(toC, one64, a.Liq); err == nil {
				if v, err := addC(a.BidSqrtX64, d); err == nil {
					sb2 = v
				}
			}
			if le(sb2, st) {
				a.BidSqrtX64 = sb2
			} else {
				dc, err := mulDivDown(a.Cushion, one64, a.Liq)
				if err != nil {
					return Pots{}, err
				}
				sa := satSub(a.BidSqrtX64, dc)
				a.BidSqrtX64 = st
				a.Liq = zero
				if gt(st, sa) {
					if v, err := mulDivDown(c2, one64, sub(st, sa)); err == nil {
						a.Liq = v
					}
				}
			}
		} else if err = reopen(c2); err != nil {
			return Pots{}, err
		}
		a.Cushion = c2
	}
	return a, nil
}
