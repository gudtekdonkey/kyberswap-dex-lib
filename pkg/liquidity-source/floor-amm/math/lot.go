package floormath

// lot.rs: THE BOUGHT-BACK LOT AT EACH TOKEN'S COST. The tokens the pots buy back on sells are kept by what EACH cost and
// resold the cheapest first at their own cost (÷ u, no margin): a dip buyer pays where the sell ended, like an AMM.
//
// Storage is bounded, the same on every chain: LotBins bins on a log-price grid of 32 bins an octave (key =
// ⌊log2(price · 2^64) · 32⌋, price Q64.64 quote base units per token base unit), sorted by key, each the tokens it holds
// and what they cost. A bin resells at its own average cost rounded up (lotAskX64).
//
//   - A sell's tokens come at a continuum of prices: the cushion curve's walk (falling from its top), the cushion at the
//     running floor (rising), then f. addSell splits each stage exactly at the grid's edges (edgeX64) into at most
//     LotPieces pieces (lotPieces: from the cheapest end 1, 1, 2, 4, 8, … bins, the last takes the rest). The pieces sum
//     to the stage's tokens and quote exactly (the last piece takes the rounding).
//   - A piece whose key is held joins that bin; a new key is inserted in order; with every bin in use the two bins
//     closest in key merge (ties: the dearest pair). A merged bin resells at its average: never under what it cost.

// LotBins is lot::LOT_BINS: the lot's bins.
const LotBins = 16

// lotBinsPerOctaveLog2 is lot::LOT_BINS_PER_OCTAVE_LOG2: 2^5 = 32 bins an octave (a bin spans 2^(1/32), 2.19 %).
const lotBinsPerOctaveLog2 = 5

// LotPieces is lot::LOT_PIECES: the most pieces one stage of one sell is split into.
const LotPieces = 8

// LotBin is lot::LotBin: a bin's grid key, the tokens it holds and what the pots paid for them (quote base units).
type LotBin struct {
	Key    uint32
	Tokens U
	Cost   U
}

// Lot is lot::Lot: Len bins in use, sorted by key, the cheapest first; the rest zero.
type Lot struct {
	Len  uint8
	Bins [LotBins]LotBin
}

// lotKeyOf is lot::key_of: ⌊log2(price_x64) · 32⌋ (the raw integer's log2, so every key is ≥ 0); 0 for a 0 price.
func lotKeyOf(priceX64 U) uint32 {
	l, ok := log2X64(priceX64)
	if !ok {
		return 0
	}
	return uint32(u64(rsh(l, 64-lotBinsPerOctaveLog2)))
}

// lotEdgeX64 is lot::edge_x64: the price 2^(key ÷ 32) as a raw Q64.64 integer, rounded down; saturates at u128 max.
func lotEdgeX64(key uint32) U {
	y := lsh(n(uint64(key)), 64-lotBinsPerOctaveLog2)
	in := uint(u64(rsh(y, 64)))
	m, ok := exp2X64(and(y, mask64)) // [1, 2) in Q64.64
	if !ok {
		m = max128
	}
	if in >= 64 {
		sh := in - 64
		if sh >= lz(m) {
			return max128
		}
		return lsh(m, sh)
	}
	return rsh(m, 64-in)
}

// lotPieces is lot::pieces: a stage's pieces over the grid's bins kLo ..= kHi as offsets from kLo, o[0] = 0 < o[1] < … <
// o[n] = kHi − kLo + 1, the interior bounds 1, 2, 4, 8, … (at most LotPieces pieces, the last takes the rest).
func lotPieces(kLo, kHi uint32) ([LotPieces + 1]uint32, int) {
	span := uint32(1)
	if kHi > kLo {
		span = kHi - kLo + 1
	}
	var o [LotPieces + 1]uint32
	cnt := 1
	b := uint32(1)
	for b < span && cnt < LotPieces {
		o[cnt] = b
		cnt++
		b *= 2
	}
	o[cnt] = span
	return o, cnt
}

// lotPieceKey is lot::piece_key: piece j's key, its lower bin or under the middle of its bins.
func lotPieceKey(kLo uint32, o []uint32, j int) uint32 {
	return kLo + o[j] + (o[j+1]-o[j]-1)/2
}

// InUse is the bins in use.
func (l *Lot) InUse() []LotBin { return l.Bins[:l.Len] }

// insert is Lot::insert: tokens that cost cost at grid key key — into the bin with that key, else a new bin in order;
// with every bin in use the two closest in key merge (ties: the dearest pair) into the lower key. Nothing with no tokens.
func (l *Lot) insert(key uint32, tokens, cost U) error {
	if isZ(tokens) {
		return nil
	}
	cnt := int(l.Len)
	i := 0
	for i < cnt && l.Bins[i].Key < key {
		i++
	}
	if i < cnt && l.Bins[i].Key == key {
		t, err := addC(l.Bins[i].Tokens, tokens)
		if err != nil {
			return err
		}
		c, err := addC(l.Bins[i].Cost, cost)
		if err != nil {
			return err
		}
		l.Bins[i].Tokens, l.Bins[i].Cost = t, c
		return nil
	}
	// the bins with the new one in place, one more than the storage holds
	var t [LotBins + 1]LotBin
	copy(t[:i], l.Bins[:i])
	t[i] = LotBin{Key: key, Tokens: tokens, Cost: cost}
	copy(t[i+1:cnt+1], l.Bins[i:cnt])
	m := cnt + 1
	if m > LotBins {
		j := 0
		for k := 1; k < m-1; k++ {
			if t[k+1].Key-t[k].Key <= t[j+1].Key-t[j].Key {
				j = k
			}
		}
		var err error
		if t[j].Tokens, err = addC(t[j].Tokens, t[j+1].Tokens); err != nil {
			return err
		}
		if t[j].Cost, err = addC(t[j].Cost, t[j+1].Cost); err != nil {
			return err
		}
		for k := j + 1; k < m-1; k++ {
			t[k] = t[k+1]
		}
		m--
		t[m] = LotBin{}
	}
	copy(l.Bins[:], t[:LotBins])
	l.Len = uint8(m)
	return nil
}

// take is Lot::take: k tokens leave bin i with the cost that leaves with them (lotCostOut: all of it with the last token,
// else ⌈cost · k ÷ tokens⌉); an emptied bin is removed (the bins above move down).
func (l *Lot) take(i int, k U) (U, error) {
	b := l.Bins[i]
	cost, err := lotCostOut(b.Cost, b.Tokens, k)
	if err != nil {
		return zero, err
	}
	if ge(k, b.Tokens) {
		cnt := int(l.Len)
		for j := i; j < cnt-1; j++ {
			l.Bins[j] = l.Bins[j+1]
		}
		l.Bins[cnt-1] = LotBin{}
		l.Len--
	} else {
		l.Bins[i].Tokens = sub(b.Tokens, k)
		l.Bins[i].Cost = sub(b.Cost, cost)
	}
	return cost, nil
}

// addSell is Lot::add_sell: a sell's tokens by what each cost — the curve's walk and the cushion at the running floor
// split at the grid's edges, the floor's tokens at f in one bin.
func (l *Lot) addSell(f *SellFill) error {
	if err := l.splitCurve(f.CurveTokens, f.CurveQuote, f.CurveFromX64, f.CurveToX64, f.CurveLiq); err != nil {
		return err
	}
	if err := l.splitRun(f.RunTokens, f.RunQuote, f.RunFloor, f.RunS); err != nil {
		return err
	}
	if !isZ(f.FloorTokens) {
		// a price only keys the bin (its cost is booked exactly): past u128 it saturates to the dearest key, so the last
		// holder's sell of one unit carrying all of F is never refused
		p, err := mulDivDown(f.FromFloor, one64, f.FloorTokens)
		if err != nil {
			p = max128
		}
		return l.insert(lotKeyOf(p), f.FloorTokens, f.FromFloor)
	}
	return nil
}

// splitCurve is Lot::split_curve: t tokens for q walked down the curve from √sb to √sn at liq, piece by piece from the
// top, each piece's tokens L·(1 ÷ √lo − 1 ÷ √hi) and quote L·(√hi − √lo) (rounded down, clamped to what is left); the
// last (cheapest) piece takes the rest.
func (l *Lot) splitCurve(t, q, sb, sn, liq U) error {
	if isZ(t) {
		return nil
	}
	kLo := lotKeyOf(priceOfSqrt(sn))
	o, cnt := lotPieces(kLo, lotKeyOf(priceOfSqrt(sb)))
	ct, cq := zero, zero
	for j := cnt - 1; j >= 1; j-- {
		se := umin(umax(sqrtX64(lotEdgeX64(kLo+o[j])), sn), sb)
		// the tokens and quote from the top down to this edge
		at := t
		if !isZ(se) {
			v, err := mulDivDown(liq, sub(sb, se), se)
			if err != nil {
				return err
			}
			w, err := mulDivDown(v, one64, sb)
			if err != nil {
				return err
			}
			at = umin(w, t)
		}
		v, err := mulDivDown(liq, sub(sb, se), one64)
		if err != nil {
			return err
		}
		aq := umax(umin(v, q), cq)
		if gt(at, ct) {
			if err := l.insert(lotPieceKey(kLo, o[:], j), sub(at, ct), sub(aq, cq)); err != nil {
				return err
			}
			ct, cq = at, aq
		}
	}
	return l.rest(lotPieceKey(kLo, o[:], 0), sub(t, ct), sub(q, cq))
}

// splitRun is Lot::split_run: t tokens for q bought by the cushion at the running floor F ÷ (S − x), rising from F ÷ S,
// piece by piece from the bottom, each piece's tokens up to an edge e, S − ⌈F ÷ e⌉, its quote the trapezoid of its two end
// prices (rounded down, clamped); the last (dearest) piece takes the rest.
func (l *Lot) splitRun(t, q, floor, s U) error {
	if isZ(t) {
		return nil
	}
	// the running floor's prices only key the pieces: past u128 (or a 0 divisor) they saturate
	pLo, err := mulDivDown(floor, one64, s)
	if err != nil {
		pLo = max128
	}
	kLo := lotKeyOf(pLo)
	pHi, err := mulDivDown(floor, one64, sub(s, t))
	if err != nil {
		pHi = max128
	}
	o, cnt := lotPieces(kLo, lotKeyOf(pHi))
	ct, cq := zero, zero
	pA := pLo
	for j := 1; j < cnt; j++ {
		e := umax(lotEdgeX64(kLo+o[j]), one)
		// the tokens until the running floor reaches e: S − ⌈F · 2^64 ÷ e⌉
		fe := divCeil(mul(floor, one64), e)
		if !fits(fe) {
			fe = max128
		}
		at := umin(satSub(s, fe), t)
		if gt(at, ct) {
			pq, err := mulDivDown(sub(at, ct), add(rsh(pA, 1), rsh(e, 1)), one64)
			if err != nil {
				return err
			}
			pq = umin(pq, sub(q, cq))
			if err := l.insert(lotPieceKey(kLo, o[:], j-1), sub(at, ct), pq); err != nil {
				return err
			}
			ct = at
			cq = add(cq, pq)
		}
		pA = e
	}
	return l.rest(lotPieceKey(kLo, o[:], cnt-1), sub(t, ct), sub(q, cq))
}

// rest is a stage's last piece: t tokens for q at key; with no token left (rounding) its quote joins the bin at or under
// key (the dearest such, else the cheapest; nothing with no bin).
func (l *Lot) rest(key uint32, t, q U) error {
	if !isZ(t) {
		return l.insert(key, t, q)
	}
	if isZ(q) || l.Len == 0 {
		return nil
	}
	cnt := int(l.Len)
	i := 0
	for i+1 < cnt && l.Bins[i+1].Key <= key {
		i++
	}
	c, err := addC(l.Bins[i].Cost, q)
	if err != nil {
		return err
	}
	l.Bins[i].Cost = c
	return nil
}

// totals is the bins' tokens and cost (Launch.LotTokens / LotCost always equal them).
func (l *Lot) totals() (U, U) {
	t, c := zero, zero
	for _, b := range l.InUse() {
		t, c = add(t, b.Tokens), add(c, b.Cost)
	}
	return t, c
}
