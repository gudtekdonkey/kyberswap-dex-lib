package floormath

// trade.rs (the buy's pieces) and supply.rs (count burns).

// Fill is trade::Fill: one fill along the schedule.
type Fill struct {
	Pay, Out, VaultIn, Cut U
	Ended                  bool
	After                  State
}

func fill(sch *Schedule, st *State, m0 *U, offer, inventory U) (Fill, *U, error) {
	none := Fill{After: *st}
	if isZ(offer) {
		return none, nil, nil
	}
	pay, out, after, mAfter, err := fillToRoomM(sch, st, m0, offer, inventory)
	if err != nil {
		return Fill{}, nil, err
	}
	ended := lt(pay, offer)
	if isZ(out) || le(after.H, st.H) {
		none.Ended = ended
		return none, nil, nil
	}
	// the books' money is pow2(l) in WHOLE quote units, so a dust fill can add tokens faster than one unit of money and
	// the books' price ⌊2^l ÷ s⌋ would fall: such a fill sells nothing (the rest is refunded). A buy never lowers the
	// books' price
	var m0v, m1v U
	if m0 != nil {
		m0v = *m0
	} else if m0v, err = pow2(st.L); err != nil {
		return Fill{}, nil, err
	}
	if mAfter != nil {
		m1v = *mAfter
	} else if m1v, err = pow2(after.L); err != nil {
		return Fill{}, nil, err
	}
	p0, err := st.priceX64M(m0v)
	if err != nil {
		return Fill{}, nil, err
	}
	p1, err := after.priceX64M(m1v)
	if err != nil {
		return Fill{}, nil, err
	}
	if lt(p1, p0) {
		none.Ended = ended
		return none, nil, nil
	}
	vaultIn := umin(sub(after.H, st.H), pay)
	return Fill{Pay: pay, Out: out, VaultIn: vaultIn, Cut: sub(pay, vaultIn), Ended: ended, After: after}, mAfter, nil
}

// fillKeeperless is trade::fill_keeperless: (fill, ok); ok false is Rust's None (a payment no price sells a token of).
func fillKeeperless(sch *Schedule, st *State, offer, inventory, money, sOut, floor, minPrice U) (Fill, bool, error) {
	if isZ(offer) {
		f, _, err := fill(sch, st, nil, offer, inventory)
		return f, err == nil, err
	}
	g, m, ok, err := raiseToBackingM(sch, st, money, sOut, offer, one)
	if err != nil || !ok {
		return Fill{}, false, err
	}
	var mm U
	if m != nil {
		mm = *m
	} else if mm, err = pow2(g.L); err != nil {
		return Fill{}, false, err
	}
	if !isZ(minPrice) {
		if g, mm, err = raisePriceToM(sch, &g, mm, minPrice); err != nil {
			return Fill{}, false, err
		}
	}
	f, _, err := fill(sch, &g, &mm, offer, inventory)
	if err != nil {
		return Fill{}, false, err
	}
	if !isZ(floor) && !isZ(sOut) && !isZ(f.Out) {
		v, err := mulDivDown(f.VaultIn, sOut, floor)
		if err != nil {
			return Fill{}, false, err
		}
		f.Out = umin(f.Out, v)
	}
	return f, true, nil
}

// FlatFill is trade::FlatFill.
type FlatFill struct{ Pay, Out, Kept, Cut U }

func flatFill(ask, offer, have, u, floor, sOut U) (FlatFill, error) {
	if isZ(ask) {
		return FlatFill{}, ErrZero
	}
	k := div(lsh(offer, 64), ask)
	if !fits(k) {
		k = max128
	}
	k = umin(k, have)
	if !isZ(floor) && !isZ(sOut) && !isZ(k) {
		pay, err := mulDivUp(k, ask, one64)
		if err != nil {
			return FlatFill{}, err
		}
		kept, err := mulDivDown(umin(pay, offer), u, one64)
		if err != nil {
			return FlatFill{}, err
		}
		v, err := mulDivDown(kept, sOut, floor)
		if err != nil {
			return FlatFill{}, err
		}
		k = umin(k, v)
	}
	if isZ(k) {
		return FlatFill{}, nil
	}
	pay, err := mulDivUp(k, ask, one64)
	if err != nil {
		return FlatFill{}, err
	}
	pay = umin(pay, offer)
	kept, err := mulDivDown(pay, u, one64)
	if err != nil {
		return FlatFill{}, err
	}
	if !isZ(floor) && !isZ(sOut) && lt(mul(kept, sOut), mul(k, floor)) {
		return FlatFill{}, nil
	}
	return FlatFill{Pay: pay, Out: k, Kept: kept, Cut: sub(pay, kept)}, nil
}

func flatCreditTop(bidTop, ask, u U) (U, error) {
	kept, err := mulDivDown(ask, u, one64)
	if err != nil {
		return zero, err
	}
	v, err := mulDivDown(kept, bpsU, bidTopMarginBps)
	if err != nil {
		return zero, err
	}
	return umin(bidTop, v), nil
}

func floorLineX64(floor, u U) (U, error) {
	if isZ(floor) {
		return zero, nil
	}
	// saturates past u128 (a dust S_out)
	fu, err := mulDivUp(floor, one64, umax(u, one))
	if err != nil {
		fu = max128
	}
	v, err := mulDivUp(fu, bidMarginBps, bpsU)
	if err != nil {
		return max128, nil
	}
	return v, nil
}

// backingLineX64 is trade::backing_line_x64: a flat sale (the window, the lot) and the tail range never sell
// under what the bid's whole money holds per token outside, less this buy's own take:
// ⌈⌈⌈(F + C) · 2^64 ÷ S_out⌉ · (leg − take) ÷ leg⌉ · 2^64 ÷ u⌉. 0 with nothing outside, a 0 leg or no money.
func backingLineX64(band, sOut, tk, leg, u U) (U, error) {
	if isZ(sOut) || isZ(leg) || isZ(band) {
		return zero, nil
	}
	// each step saturates past u128 (a dust S_out)
	per := max128
	if b, err := shl64C(band); err == nil {
		if v, err := toU128(divCeil(b, sOut)); err == nil {
			per = v
		}
	}
	net, err := mulDivUp(per, sub(leg, umin(tk, leg)), leg)
	if err != nil {
		net = max128
	}
	v, err := mulDivUp(net, one64, umax(u, one))
	if err != nil {
		return max128, nil
	}
	return v, nil
}

func windowPriceX64(p0, floor, u U) (U, error) {
	fl, err := floorLineX64(floor, u)
	if err != nil {
		return zero, err
	}
	return umax(umax(p0, fl), one), nil
}

func windowRoom(left U, capT *U) U {
	if capT == nil {
		return left
	}
	return umin(left, *capT)
}

// snipeCap is trade::snipe_cap: nil = no cap.
func snipeCap(p *PresetSpec, d uint8, since uint64) (*U, error) {
	if p.SnipeSlots == 0 || p.WindowMaxPerTxBps == 0 || since >= uint64(p.SnipeSlots) {
		return nil, nil
	}
	w, err := windowTokens(p, d)
	if err != nil {
		return nil, err
	}
	base := w
	if isZ(w) {
		if base, err = saleTokens(p, d); err != nil {
			return nil, err
		}
	}
	v, err := mulC(base, n(uint64(p.WindowMaxPerTxBps)))
	if err != nil {
		return nil, err
	}
	// at least one token a transaction
	c := umax(div(v, bpsU), one)
	return &c, nil
}

func graduates(windowLeft, windowOut U) bool { return !isZ(windowLeft) && eq(windowOut, windowLeft) }

// lotMarginBps is trade::LOT_MARGIN_BPS: the bought-back lot resells at 1.00 × its reference prices (no margin).
var lotMarginBps = n(10_000)

// lotLine is ⌈⌈x · LOT_MARGIN ÷ 10⁴⌉ · 2^64 ÷ u⌉: what a buyer pays a token so the books keep x × the lot's margin.
func lotLine(x, u U) (U, error) {
	v, err := mulDivUp(x, lotMarginBps, bpsU)
	if err != nil {
		return zero, err
	}
	return mulDivUp(v, one64, umax(u, one))
}

// fCeilX64 is ⌈F · 2^64 ÷ S_out⌉, saturating (0 with nothing outside): the floor rounded UP, as a flat fill holds it
// exactly (never a token under f).
func fCeilX64(floor, sOut U) U {
	if isZ(sOut) {
		return zero
	}
	b, err := shl64C(floor)
	if err != nil {
		return max128
	}
	v, err := toU128(divCeil(b, sOut))
	if err != nil {
		return max128
	}
	return v
}

// lotAskX64 is trade::lot_ask_x64: ONE bin's ask (lotCost / lotTokens are the bin's) — max(the bin's average cost, f
// rounded up, the live bid) ÷ u (× the lot's margin), raised to the lot's own backing line lotBackingLineX64; nil with an
// empty bin or past u128 (the bin waits). take / kept / leg: the buy's take on its whole leg and the kept skim in it (0 /
// 0 / 1 for a quote-free read). rangeX64: after the end the tail range's price while it stands above its frozen price
// (Launch.rangeBidX64), else 0 — it joins the live bid.
func lotAskX64(p *Pots, sOut, lotCost, lotTokens, u, tk, kept, leg, rangeX64 U) (*U, error) {
	if isZ(lotTokens) {
		return nil, nil
	}
	// f rounded up: an ask at the published floor (rounded down) is under the exact F ÷ S_out and flatFill's exact floor check
	// would sell nothing
	f := fCeilX64(p.Floor, sOut)
	b, err := bidPriceX64(p, sOut)
	if err != nil {
		return nil, err
	}
	b = umax(b, rangeX64)
	// any term past u128 leaves the bin aside, never reverts the buy
	terms := func() (U, error) {
		avg := zero
		if !isZ(lotCost) {
			if avg, err = mulDivUp(lotCost, one64, lotTokens); err != nil {
				return zero, err
			}
		}
		la, err := lotLine(avg, u)
		if err != nil {
			return zero, err
		}
		lf, err := lotLine(f, u)
		if err != nil {
			return zero, err
		}
		lb, err := lotLine(b, u)
		if err != nil {
			return zero, err
		}
		return umax(umax(la, lf), lb), nil
	}
	t, err := terms()
	if err != nil {
		return nil, nil
	}
	line, err := lotBackingLineX64(satAdd(p.Floor, p.Cushion), sOut, tk, kept, leg, u)
	if err != nil {
		return nil, err
	}
	ask := umax(umax(t, line), one)
	if eq(ask, max128) {
		return nil, nil
	}
	return &ask, nil
}

// lotBackingLineX64 is trade::lot_backing_line_x64, the backing line for the lot:
// ⌈⌈⌈(F + C) · 2^64 ÷ S_out⌉ · (leg − take) ÷ (leg − take + kept)⌉ · 2^64 ÷ u⌉ — discounted only by the take that leaves
// the pots (the kept skim stays in F). 0 with nothing outside, a 0 leg or no money; each step saturates past u128.
func lotBackingLineX64(band, sOut, tk, kept, leg, u U) (U, error) {
	if isZ(sOut) || isZ(leg) || isZ(band) {
		return zero, nil
	}
	per := fCeilX64(band, sOut)
	tk = umin(tk, leg)
	net, err := mulDivUp(per, sub(leg, tk), add(sub(leg, tk), umin(kept, tk)))
	if err != nil {
		net = max128
	}
	v, err := mulDivUp(net, one64, umax(u, one))
	if err != nil {
		return max128, nil
	}
	return v, nil
}

// lotCreditTop is trade::lot_credit_top: min(the bid's top, ⌊⌊ask · u⌋ · 10⁴ ÷ LOT_MARGIN⌋).
func lotCreditTop(bidTop, ask, u U) (U, error) {
	kept, err := mulDivDown(ask, u, one64)
	if err != nil {
		return zero, err
	}
	v, err := mulDivDown(kept, bpsU, lotMarginBps)
	if err != nil {
		return zero, err
	}
	return umin(bidTop, v), nil
}

// liveBuyPriceX64 is trade::live_buy_price_x64: the lower of price and ONE bin's quote-free ask (no range bid).
func liveBuyPriceX64(price U, p *Pots, sOut, lotCost, lotTokens, u U) (U, error) {
	a, err := lotAskX64(p, sOut, lotCost, lotTokens, u, zero, zero, one, zero)
	if err != nil {
		return zero, err
	}
	if a != nil {
		return umin(*a, price), nil
	}
	return price, nil
}

// supply.rs

func syncSupply(stored, live U) U { return umin(stored, live) }

func sOutOf(supply, held U) (U, error) { return subC(supply, held) }

func sweepIfEmpty(p *Pots, sOut U) (Pots, U, error) {
	if !isZ(sOut) {
		return *p, zero, nil
	}
	rest, err := addC(p.Floor, p.Cushion)
	if err != nil {
		return Pots{}, zero, err
	}
	return Pots{}, rest, nil
}

// allocReleasedAfter is trade::alloc_released_after: the tokens released once `releases` releases have been, of an
// allocation of `total`: ⌊total · Σ_{i<releases} bps_i ÷ 10⁴⌋ (cumulative, so the releases add up to the total).
func allocReleasedAfter(total U, releaseBps []uint16, releases int) (U, error) {
	var cum uint64
	for i := 0; i < releases && i < len(releaseBps); i++ {
		cum += uint64(releaseBps[i])
	}
	return toU128(div(mul(total, n(cum)), bpsU))
}

// allocNextReleaseAt is trade::alloc_next_release_at: when the release after `releasesDone` (of `releaseCount`) is
// due — 0 before the first (it waits only on the lines), else releasedAt + releaseSecs (saturating); ok false once
// every release is done.
func allocNextReleaseAt(releasesDone, releaseCount uint8, releasedAt uint64, releaseSecs uint32) (uint64, bool) {
	if releasesDone >= releaseCount {
		return 0, false
	}
	if releasesDone == 0 {
		return 0, true
	}
	t := releasedAt + uint64(releaseSecs)
	if t < releasedAt {
		t = ^uint64(0)
	}
	return t, true
}

// splitRelease is trade::split_release: one release's tokens pro rata by the Team Slot's weights at that release,
// the rounding dust to place 0; weights summing to 0 → place 0 (the creator) takes it all. The parts sum to tokens.
func splitRelease(tokens U, w [MaxMembers]uint32) ([MaxMembers]U, error) {
	var parts [MaxMembers]U
	if w == [MaxMembers]uint32{} {
		parts[0] = tokens
		return parts, nil
	}
	parts, dust, err := splitMembers(tokens, w)
	if err != nil {
		return parts, err
	}
	if parts[0], err = addC(parts[0], dust); err != nil {
		return parts, err
	}
	return parts, nil
}
