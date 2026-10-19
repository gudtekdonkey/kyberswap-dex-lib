package floormath

// schedule.rs: revision 3's cushion schedule — what the trade path reads (Schedule::new / with_slot_cut, open_sale, the
// walk, fill_to_room_m, raise_to_backing_m, raise_price_to_m, cp_targets). The memo of schedule.rs (pure answers reused)
// is not ported: the same bits.

var (
	invLn2X64 = D("26613026195688644983") // INV_LN2_X64
	ln2X64    = D("12786308645202655659") // LN2_X64
	l64       = lsh(n(64), 64)            // 64.0 in Q64.64
)

const maxPoints = 8

// RetireRoundUp is RETIRE_ROUND_UP (raw Q64.64 units).
var retireRoundUp = n(4)

// backingMarginBps is BACKING_MARGIN_BPS.
var backingMarginBps = n(10_300)

func mulQ(a, b U) (U, error) { return toU128(rsh(mul(a, b), 64)) }

func divQ(a, b U) (U, error) {
	if isZ(b) {
		return zero, ErrZero
	}
	return toU128(div(lsh(a, 64), b))
}

func mulDiv(a, b, c U) (U, error) { return mulDivDown(a, b, c) }

func log2Z(v U) (U, error) {
	r, ok := log2X64(v)
	if !ok {
		return zero, ErrZero
	}
	return r, nil
}

// pow2 is schedule::pow2: 2^l as an integer, rounded down.
func pow2(l U) (U, error) {
	ip := uint(u64(rsh(l, 64)))
	g, ok := exp2X64(and(l, mask64))
	if !ok {
		return zero, ErrOverflow
	}
	if ip >= 64 {
		sh := ip - 64
		if sh >= lz(g) {
			return zero, ErrOverflow
		}
		return lsh(g, sh), nil
	}
	return rsh(g, 64-ip), nil
}

// scaleUp is x · 2^y for y ≥ 0 in Q64.64, rounded down.
func scaleUp(x, y U) (U, error) {
	ip := uint(uint32(u64(rsh(y, 64))))
	g, ok := exp2X64(and(y, mask64))
	if !ok {
		return zero, ErrOverflow
	}
	v, err := toU128(rsh(mul(x, g), 64))
	if err != nil {
		return zero, err
	}
	if ip >= lz(v) && !isZ(v) {
		return zero, ErrOverflow
	}
	if ip >= 256 {
		return zero, nil
	}
	return lsh(v, ip), nil
}

// Point is a schedule share at a market cap: (quote base units, bps).
type Point struct {
	Mcap U
	Bps  uint16
}

// Terms is schedule::Terms.
type Terms struct {
	CushionBps  [2]uint16
	PolBps      [2]uint16
	RampCpFrom  U
	FullAt      U
	Floor0Bps   uint16
	RampFrom    U
	FloorPoints [4]Point
	CutBps      uint16
}

// Schedule is schedule::Schedule.
type Schedule struct {
	N   int
	L   [maxPoints]U
	Cp  [maxPoints]U
	F   [maxPoints]U
	Gcp [maxPoints]U
	Gf  [maxPoints]U
	U   U
}

func bpsQ(b uint16) U { return div(mul(n(uint64(b)), one64), bpsU) }

type knot struct{ l, v U }

func lerp(knots []knot, l U) (U, error) {
	if le(l, knots[0].l) {
		return knots[0].v, nil
	}
	for i := 0; i+1 < len(knots); i++ {
		a, va, b, vb := knots[i].l, knots[i].v, knots[i+1].l, knots[i+1].v
		if le(l, b) {
			if eq(b, a) {
				return vb, nil
			}
			d, err := mulDiv(sub(vb, va), sub(l, a), sub(b, a))
			if err != nil {
				return zero, err
			}
			return add(va, d), nil
		}
	}
	return knots[len(knots)-1].v, nil
}

// NewSchedule is Schedule::new.
func NewSchedule(t *Terms, openMcap U) (Schedule, error) {
	var s Schedule
	if uint64(t.CutBps) >= BPS {
		return s, ErrVaultShareOutOfRange
	}
	u := bpsQ(uint16(BPS - uint64(t.CutBps)))
	lOpen, err := log2Z(openMcap)
	if err != nil {
		return s, err
	}
	lFrom, err := log2Z(umax(t.RampCpFrom, openMcap))
	if err != nil {
		return s, err
	}
	lFull, err := log2Z(umax(t.FullAt, openMcap))
	if err != nil {
		return s, err
	}
	lFull = umax(lFull, lFrom)
	if t.CushionBps[1] < t.CushionBps[0] || t.PolBps[1] < t.PolBps[0] {
		return s, ErrRampOutOfRange
	}
	cp0 := add(bpsQ(t.CushionBps[0]), bpsQ(t.PolBps[0]))
	cp1 := add(bpsQ(t.CushionBps[1]), bpsQ(t.PolBps[1]))
	// a ramp that is full at the open opens at the full share
	cOpen := cp0
	if le(lFull, lOpen) {
		cOpen = cp1
	}
	cpKnots := []knot{{lOpen, cOpen}, {lFrom, cp0}, {lFull, cp1}}
	fKnots := make([]knot, 5)
	if fKnots[0].l, err = log2Z(t.RampFrom); err != nil {
		return s, err
	}
	fKnots[0].v = bpsQ(t.Floor0Bps)
	for i, p := range t.FloorPoints {
		if fKnots[i+1].l, err = log2Z(p.Mcap); err != nil {
			return s, err
		}
		fKnots[i+1].v = bpsQ(p.Bps)
		if le(fKnots[i+1].l, fKnots[i].l) || lt(fKnots[i+1].v, fKnots[i].v) {
			return s, ErrRampOutOfRange
		}
	}
	ls := []U{lOpen, cpKnots[1].l, cpKnots[2].l, fKnots[0].l, fKnots[1].l, fKnots[2].l, fKnots[3].l, fKnots[4].l}
	for i := 1; i < len(ls); i++ { // insertion sort (sort_unstable: equal keys are equal values)
		for j := i; j > 0 && lt(ls[j], ls[j-1]); j-- {
			ls[j], ls[j-1] = ls[j-1], ls[j]
		}
	}
	s.U = u
	for _, x := range ls {
		if lt(x, lOpen) || (s.N > 0 && eq(x, s.L[s.N-1])) {
			continue
		}
		s.L[s.N] = x
		if s.Cp[s.N], err = lerp(cpKnots, x); err != nil {
			return s, err
		}
		if s.F[s.N], err = lerp(fKnots, x); err != nil {
			return s, err
		}
		s.N++
	}
	for i := 0; i < s.N; i++ {
		if i+1 < s.N {
			dl := sub(s.L[i+1], s.L[i])
			if s.Gcp[i], err = divQ(sub(s.Cp[i+1], s.Cp[i]), dl); err != nil {
				return s, err
			}
			if s.Gf[i], err = divQ(sub(s.F[i+1], s.F[i]), dl); err != nil {
				return s, err
			}
		}
		if err = s.risesAt(i, u); err != nil {
			return s, err
		}
	}
	return s, nil
}

// risesAt: k + k′ ÷ ln 2 < u at interval i's top.
func (s *Schedule) risesAt(i int, u U) error {
	kTop := add(s.Cp[i], s.F[i])
	if i+1 < s.N {
		kTop = add(s.Cp[i+1], s.F[i+1])
	}
	slope, err := mulQ(add(s.Gcp[i], s.Gf[i]), invLn2X64)
	if err != nil {
		return err
	}
	if ge(add(kTop, slope), u) {
		return ErrRatioOutOfRange
	}
	return nil
}

// WithSlotCut is Schedule::with_slot_cut: u′ = u − slot_q.
func (s *Schedule) WithSlotCut(slotQ U) (Schedule, error) {
	if isZ(slotQ) {
		return *s, nil
	}
	if lt(s.U, slotQ) || eq(s.U, slotQ) {
		return Schedule{}, ErrVaultShareOutOfRange
	}
	u := sub(s.U, slotQ)
	for i := 0; i < s.N; i++ {
		if err := s.risesAt(i, u); err != nil {
			return Schedule{}, err
		}
	}
	out := *s
	out.U = u
	return out, nil
}

func (s *Schedule) seg(l U) int {
	i := 0
	for i+1 < s.N && ge(l, s.L[i+1]) {
		i++
	}
	return i
}

// at is (cp, cp′, f, f′) at l.
func (s *Schedule) at(l U) (cp, gcp, f, gf U, err error) {
	i := s.seg(l)
	d := satSub(l, s.L[i])
	a, err := mulQ(s.Gcp[i], d)
	if err != nil {
		return
	}
	b, err := mulQ(s.Gf[i], d)
	if err != nil {
		return
	}
	return add(s.Cp[i], a), s.Gcp[i], add(s.F[i], b), s.Gf[i], nil
}

// Shares is (cp, f) at l.
func (s *Schedule) Shares(l U) (U, U, error) {
	cp, _, f, _, err := s.at(l)
	return cp, f, err
}

func (s *Schedule) nextPoint(l U) (U, bool) {
	if s.N > 0 && lt(l, s.L[0]) {
		return s.L[0], true
	}
	i := s.seg(l)
	if i+1 < s.N && gt(s.L[i+1], l) {
		return s.L[i+1], true
	}
	return zero, false
}

func (s *Schedule) line(l U) (U, U, U) {
	if s.N > 0 && lt(l, s.L[0]) {
		return add(s.Cp[0], s.F[0]), zero, zero
	}
	i := s.seg(l)
	return add(s.Cp[i], s.F[i]), add(s.Gcp[i], s.Gf[i]), sub(l, s.L[i])
}

func (s *Schedule) dlog2sA(l0, l1 U) (U, error) {
	ka, g, e0 := s.line(l0)
	d, err := subC(l1, l0)
	if err != nil {
		return zero, err
	}
	gi, err := mulQ(g, invLn2X64)
	if err != nil {
		return zero, err
	}
	lin, err := mulQ(add(ka, gi), d)
	if err != nil {
		return zero, err
	}
	dd, err := mulQ(d, add(add(e0, e0), d))
	if err != nil {
		return zero, err
	}
	quad, err := mulQ(g, dd)
	if err != nil {
		return zero, err
	}
	return divQ(add(lin, rsh(quad, 1)), s.U)
}

// solveAM is Schedule::solve_a_m: (l, pow2(l) when its last money check took it).
func (s *Schedule) solveAM(lo, hi, h U) (U, *U, error) {
	lh, err := log2Z(h)
	if err != nil {
		return zero, nil, err
	}
	l := lo
	for it := 0; it < 6; it++ {
		ka, g, e := s.line(l)
		ge_, err := mulQ(g, e)
		if err != nil {
			return zero, nil, err
		}
		k := add(ka, ge_)
		gk, err := mulQ(g, invLn2X64)
		if err != nil {
			return zero, nil, err
		}
		log2k, err := log2Z(k)
		if err != nil {
			return zero, nil, err
		}
		// g = l + log2 k − 64 − log2 h, signed
		pos, neg := add(l, log2k), add(l64, lh)
		if ge(pos, neg) {
			break
		}
		r, err := divQ(gk, k)
		if err != nil {
			return zero, nil, err
		}
		step, err := divQ(sub(neg, pos), add(one64, r))
		if err != nil {
			return zero, nil, err
		}
		if isZ(step) {
			break
		}
		l = umin(add(l, step), hi)
	}
	back := lsh(n(1), 20)
	for {
		if le(l, lo) {
			return l, nil, nil
		}
		cp, _, f, _, err := s.at(l)
		if err != nil {
			return zero, nil, err
		}
		if m, err := pow2(l); err == nil {
			if x, err := mulQ(add(cp, f), m); err == nil && le(x, h) {
				return l, &m, nil
			}
		}
		l = umax(satSub(l, back), lo)
		back = lsh(back, 1)
	}
}

func splitCp(t *Terms, cp U) (U, U, error) {
	c0, c1 := bpsQ(t.CushionBps[0]), bpsQ(t.CushionBps[1])
	cp0, cp1 := add(c0, bpsQ(t.PolBps[0])), add(c1, bpsQ(t.PolBps[1]))
	c := c0
	if gt(cp1, cp0) && gt(cp, cp0) {
		d, err := mulDiv(sub(c1, c0), umin(sub(cp, cp0), sub(cp1, cp0)), sub(cp1, cp0))
		if err != nil {
			return zero, zero, err
		}
		c = add(c0, d)
	}
	c = umin(c, cp)
	return c, sub(cp, c), nil
}

// State is schedule::State: the books.
type State struct {
	S, H, L, FloorX64 U
}

// PriceX64 is State::price_x64.
func (st *State) PriceX64() (U, error) {
	m, err := pow2(st.L)
	if err != nil {
		return zero, err
	}
	return divQ(m, st.S)
}

// priceX64M is State::price_x64_m: PriceX64 from m = pow2(st.L) in hand (it MUST be; the same bits).
func (st *State) priceX64M(m U) (U, error) { return divQ(m, st.S) }

func openMcap(raise, sale, pol U) (U, error) { return mulDiv(raise, add(sale, pol), sale) }

func open(sch *Schedule, raise, sale, pol U) (State, error) {
	if isZ(pol) {
		return State{}, ErrRatioOutOfRange
	}
	s := add(sale, pol)
	h, err := mulQ(raise, sch.U)
	if err != nil {
		return State{}, err
	}
	m, err := openMcap(raise, sale, pol)
	if err != nil {
		return State{}, err
	}
	l, err := log2Z(m)
	if err != nil {
		return State{}, err
	}
	cp, _, f, _, err := sch.at(l)
	if err != nil {
		return State{}, err
	}
	above, err := mulQ(cp, m)
	if err != nil {
		return State{}, err
	}
	if gt(above, h) {
		return State{}, ErrRampOutOfRange
	}
	a, err := divQ(sub(h, above), s)
	if err != nil {
		return State{}, err
	}
	fm, err := mulQ(f, m)
	if err != nil {
		return State{}, err
	}
	b, err := divQ(fm, s)
	if err != nil {
		return State{}, err
	}
	return State{S: s, H: h, L: l, FloorX64: umax(a, b)}, nil
}

// openSale is schedule::open_sale: the books on the sale only.
func openSale(sch *Schedule, raise, sale, pol U) (State, error) {
	wide, err := open(sch, raise, sale, pol)
	if err != nil {
		return State{}, err
	}
	m, err := openMcap(raise, sale, zero)
	if err != nil {
		return State{}, err
	}
	l, err := log2Z(m)
	if err != nil {
		return State{}, err
	}
	st, _, err := rederiveM(sch, &State{S: sale, L: l, FloorX64: wide.FloorX64})
	return st, err
}

// openSlotMarginBps is schedule::OPEN_SLOT_MARGIN_BPS: the margin openSaleFor keeps past the row's largest Team Slot cut.
const openSlotMarginBps = 50

// openSaleFor is schedule::open_sale_for: openSale with the curve floor capped at (u − cp − (slotMaxBps + 50 bps)) ·
// mcap ÷ sale, so every admissible Team Slot rate can still raise the price. Where the cap does not bind it is openSale.
func openSaleFor(sch *Schedule, raise, sale, pol U, slotMaxBps uint16) (State, error) {
	wide, err := openSale(sch, raise, sale, pol)
	if err != nil {
		return State{}, err
	}
	m, err := openMcap(raise, sale, zero)
	if err != nil {
		return State{}, err
	}
	cp, _, _, _, err := sch.at(wide.L)
	if err != nil {
		return State{}, err
	}
	head := uint32(slotMaxBps) + openSlotMarginBps
	if head > 0xffff {
		head = 0xffff
	}
	h := bpsQ(uint16(head))
	if lt(sch.U, cp) || le(sub(sch.U, cp), h) {
		return State{}, ErrRampOutOfRange
	}
	q, err := mulQ(sub(sub(sch.U, cp), h), m)
	if err != nil {
		return State{}, err
	}
	capX64, err := divQ(q, sale)
	if err != nil {
		return State{}, err
	}
	if le(wide.FloorX64, capX64) {
		return wide, nil
	}
	st, _, err := rederiveM(sch, &State{S: sale, L: wide.L, FloorX64: capX64})
	return st, err
}

func phi(floor, s, l U) (U, error) {
	m, err := pow2(l)
	if err != nil {
		return zero, err
	}
	return toU128(div(mul(floor, s), m))
}

func lOfB(sch *Schedule, floor, h, s, lGuess U) (U, error) {
	fs, err := mulQ(floor, s)
	if err != nil {
		return zero, err
	}
	rest, err := subC(h, fs)
	if err != nil {
		return zero, err
	}
	l := lGuess
	cp, gcp, _, _, err := sch.at(l)
	if err != nil {
		return zero, err
	}
	q, err := divQ(rest, cp)
	if err != nil {
		return zero, err
	}
	target, err := log2Z(q)
	if err != nil {
		return zero, err
	}
	gi, err := mulQ(gcp, invLn2X64)
	if err != nil {
		return zero, err
	}
	r, err := divQ(gi, cp)
	if err != nil {
		return zero, err
	}
	dg := add(one64, r)
	if ge(target, l) {
		d, err := divQ(sub(target, l), dg)
		if err != nil {
			return zero, err
		}
		return add(l, d), nil
	}
	d, err := divQ(sub(l, target), dg)
	if err != nil {
		return zero, err
	}
	return sub(l, d), nil
}

func dsDh(sch *Schedule, floor, h, s, l U) (U, error) {
	cp, _, _, _, err := sch.at(l)
	if err != nil {
		return zero, err
	}
	fs, err := mulQ(floor, s)
	if err != nil {
		return zero, err
	}
	rest, err := subC(h, fs)
	if err != nil {
		return zero, err
	}
	ur, err := mulQ(sch.U, rest)
	if err != nil {
		return zero, err
	}
	return mulDiv(cp, s, ur)
}

func stepB(sch *Schedule, floor, h, s, l, dh U) (U, U, error) {
	half := rsh(dh, 1)
	k1, err := dsDh(sch, floor, h, s, l)
	if err != nil {
		return zero, zero, err
	}
	x, err := mulQ(k1, half)
	if err != nil {
		return zero, zero, err
	}
	s2 := add(s, x)
	l2, err := lOfB(sch, floor, add(h, half), s2, l)
	if err != nil {
		return zero, zero, err
	}
	k2, err := dsDh(sch, floor, add(h, half), s2, l2)
	if err != nil {
		return zero, zero, err
	}
	if x, err = mulQ(k2, half); err != nil {
		return zero, zero, err
	}
	s3 := add(s, x)
	l3, err := lOfB(sch, floor, add(h, half), s3, l2)
	if err != nil {
		return zero, zero, err
	}
	k3, err := dsDh(sch, floor, add(h, half), s3, l3)
	if err != nil {
		return zero, zero, err
	}
	if x, err = mulQ(k3, dh); err != nil {
		return zero, zero, err
	}
	s4 := add(s, x)
	l4, err := lOfB(sch, floor, add(h, dh), s4, l3)
	if err != nil {
		return zero, zero, err
	}
	k4, err := dsDh(sch, floor, add(h, dh), s4, l4)
	if err != nil {
		return zero, zero, err
	}
	sum := add(add(k1, lsh(k2, 1)), add(lsh(k3, 1), k4))
	if x, err = mulQ(sum, dh); err != nil {
		return zero, zero, err
	}
	s1 := add(s, div(x, n(6)))
	l1, err := lOfB(sch, floor, add(h, dh), s1, l4)
	return s1, l1, err
}

func maxStepB(sch *Schedule, floor, h, s, l U) (U, error) {
	fs, err := mulQ(floor, s)
	if err != nil {
		return zero, err
	}
	rest, err := subC(h, fs)
	if err != nil {
		return zero, err
	}
	p, err := phi(floor, s, l)
	if err != nil {
		return zero, err
	}
	pu, err := divQ(p, sch.U)
	if err != nil {
		return zero, err
	}
	lean, err := subC(one64, pu)
	if err != nil {
		return zero, err
	}
	lean = umax(lean, one)
	q, err := divQ(rest, lean)
	if err != nil {
		return zero, err
	}
	return umax(rsh(q, 1), one), nil
}

// flatB is schedule::flat_b: region B on a flat interval in closed form. (h, s, l, done, at_switch).
func flatB(sch *Schedule, floor, s, l, h1, m0 U) (U, U, U, bool, bool, error) {
	fail := func(err error) (U, U, U, bool, bool, error) { return zero, zero, zero, false, false, err }
	c, _, f, _, err := sch.at(l)
	if err != nil {
		return fail(err)
	}
	u := sch.U
	uc, err := subC(u, c)
	if err != nil {
		return fail(err)
	}
	p0, err := divQ(m0, s)
	if err != nil {
		return fail(err)
	}
	ucp, err := mulQ(uc, p0)
	if err != nil {
		return fail(err)
	}
	d0, err := subC(ucp, floor)
	if err != nil {
		return fail(err)
	}
	d0 = umax(d0, one)
	fs, err := mulQ(floor, s)
	if err != nil {
		return fail(err)
	}
	a, err := mulDiv(fs, u, uc)
	if err != nil {
		return fail(err)
	}
	ds, err := mulQ(d0, s)
	if err != nil {
		return fail(err)
	}
	cds, err := mulQ(c, ds)
	if err != nil {
		return fail(err)
	}
	b, err := mulDiv(cds, one64, uc)
	if err != nil {
		return fail(err)
	}
	e1, err := divQ(u, c)
	if err != nil {
		return fail(err)
	}
	e := sub(e1, one64)
	parts := func(y U) (U, U, error) {
		x, err := scaleUp(a, y)
		if err != nil {
			return zero, zero, err
		}
		ey, err := mulQ(e1, y)
		if err != nil {
			return zero, zero, err
		}
		z, err := scaleUp(b, ey)
		return x, z, err
	}
	money := func(y U) (U, error) {
		x, z, err := parts(y)
		return add(x, z), err
	}
	books := func(y U) (U, U, U, error) {
		s1, err := scaleUp(s, y)
		if err != nil {
			return zero, zero, zero, err
		}
		ey, err := mulQ(e, y)
		if err != nil {
			return zero, zero, zero, err
		}
		grow, err := scaleUp(d0, ey)
		if err != nil {
			return zero, zero, zero, err
		}
		p1, err := divQ(add(floor, grow), uc)
		if err != nil {
			return zero, zero, zero, err
		}
		ps, err := mulQ(p1, s1)
		if err != nil {
			return zero, zero, zero, err
		}
		lg, err := log2Z(ps)
		return s1, lg, grow, err
	}
	// dl/dy = 1 + e·grow ÷ (floor + grow)
	newtonToPoint := func(z, sz, lz, grow, lb U) (U, U, U, error) {
		for it := 0; it < 6; it++ {
			if le(lz, lb) {
				break
			}
			eg, err := mulQ(e, grow)
			if err != nil {
				return zero, zero, zero, err
			}
			r, err := mulDiv(eg, one64, add(floor, grow))
			if err != nil {
				return zero, zero, zero, err
			}
			step, err := mulDiv(sub(lz, lb), one64, add(one64, r))
			if err != nil {
				return zero, zero, zero, err
			}
			if isZ(step) {
				break
			}
			z = satSub(z, step)
			if sz, lz, grow, err = books(z); err != nil {
				return zero, zero, zero, err
			}
		}
		back := lsh(n(1), 20)
		for !isZ(z) && gt(lz, lb) {
			z = satSub(z, back)
			back = lsh(back, 1)
			var err error
			if sz, lz, _, err = books(z); err != nil {
				return zero, zero, zero, err
			}
		}
		return z, sz, lz, nil
	}
	num, err := mulQ(floor, satSub(uc, f))
	if err != nil {
		return fail(err)
	}
	den, err := mulQ(f, d0)
	if err != nil {
		return fail(err)
	}
	den = umax(den, one)
	ySw := zero
	if gt(num, den) {
		q, err := divQ(num, den)
		if err != nil {
			return fail(err)
		}
		lq, err := log2Z(q)
		if err != nil {
			return fail(err)
		}
		if ySw, err = divQ(sub(lq, l64), e); err != nil {
			return fail(err)
		}
	}
	yHi := ySw
	var cached *[2]U
	if lb, ok := sch.nextPoint(l); ok {
		plb, err := pow2(lb)
		if err != nil {
			return fail(err)
		}
		cm, err := mulQ(c, plb)
		if err != nil {
			return fail(err)
		}
		if ge(h1, add(cm, fs)) {
			sd, err := mulDiv(s, d0, uc)
			if err != nil {
				return fail(err)
			}
			lsd, err := log2Z(sd)
			if err != nil {
				return fail(err)
			}
			base := satSub(lsd, l64)
			guess, err := divQ(satSub(lb, base), e1)
			if err != nil {
				return fail(err)
			}
			guess = umin(guess, ySw)
			swS, swL, swG, err := books(guess)
			if err != nil {
				return fail(err)
			}
			if gt(swL, lb) {
				z, sz, _, err := newtonToPoint(guess, swS, swL, swG, lb)
				if err != nil {
					return fail(err)
				}
				az, bz, err := parts(z)
				if err != nil {
					return fail(err)
				}
				if le(add(az, bz), h1) {
					return add(az, bz), sz, lb, false, false, nil
				}
				yHi = z
				cached = &[2]U{az, bz}
			}
		}
	}
	var ay, by U
	if cached != nil {
		ay, by = cached[0], cached[1]
	} else if ay, by, err = parts(yHi); err != nil {
		return fail(err)
	}
	var hSw U
	if eq(yHi, ySw) {
		hSw = add(ay, by)
	} else if hSw, err = money(ySw); err != nil {
		return fail(err)
	}
	y := yHi
	done := false
	if gt(hSw, h1) {
		done = true
		for it := 0; it < 8; it++ {
			hy := add(ay, by)
			if le(hy, h1) {
				break
			}
			eb, err := mulQ(e1, by)
			if err != nil {
				return fail(err)
			}
			slope, err := mulQ(ln2X64, add(ay, eb))
			if err != nil {
				return fail(err)
			}
			slope = umax(slope, one)
			step, err := mulDiv(sub(hy, h1), one64, slope)
			if err != nil {
				return fail(err)
			}
			if isZ(step) {
				break
			}
			y = satSub(y, step)
			if ay, by, err = parts(y); err != nil {
				return fail(err)
			}
		}
		back := lsh(n(1), 20)
		hy := add(ay, by)
		for !isZ(y) && gt(hy, h1) {
			y = satSub(y, back)
			back = lsh(back, 1)
			if hy, err = money(y); err != nil {
				return fail(err)
			}
		}
	}
	s1, l1, growY, err := books(y)
	if err != nil {
		return fail(err)
	}
	if lb, ok := sch.nextPoint(l); ok && gt(l1, lb) {
		z, sz, _, err := newtonToPoint(y, s1, l1, growY, lb)
		if err != nil {
			return fail(err)
		}
		mz, err := money(z)
		if err != nil {
			return fail(err)
		}
		return mz, sz, lb, false, false, nil
	}
	if !done {
		return hSw, umax(s1, s), umax(l1, l), false, true, nil
	}
	return h1, s1, l1, true, false, nil
}

func inA(sch *Schedule, floor, l, s U) (bool, error) {
	_, _, f, _, err := sch.at(l)
	if err != nil {
		return false, err
	}
	p, err := phi(floor, s, l)
	if err != nil {
		return false, err
	}
	return ge(f, p), nil
}

func inAM(sch *Schedule, floor, l, s U, m *U) (bool, U, error) {
	_, _, f, _, err := sch.at(l)
	if err != nil {
		return false, zero, err
	}
	var mm U
	if m != nil {
		mm = *m
	} else if mm, err = pow2(l); err != nil {
		return false, zero, err
	}
	p, err := toU128(div(mul(floor, s), mm))
	if err != nil {
		return false, zero, err
	}
	return ge(f, p), mm, nil
}

// gap is f − φ at (l, s), an i128 as a two's-complement U.
func gap(sch *Schedule, floor, l, s U) (U, error) {
	_, _, f, _, err := sch.at(l)
	if err != nil {
		return zero, err
	}
	p, err := phi(floor, s, l)
	if err != nil {
		return zero, err
	}
	return sub(f, p), nil
}

type paused struct {
	h1, l, s, h U
	forceA      bool
	guard       uint32
	atA         bool
}

func (p *paused) books(floor U) State { return State{S: p.s, H: p.h, L: p.l, FloorX64: floor} }

// walked is Walked: done (out, after, pow2(after.l) or nil) or paused at region A.
type walked struct {
	done  bool
	out   U
	after State
	m     *U
	p     paused
}

func mintForPayment(sch *Schedule, st *State, pay U) (U, State, error) {
	w, err := walk(sch, st, pay, false, nil)
	if err != nil {
		return zero, State{}, err
	}
	if !w.done {
		return zero, State{}, ErrOverflow
	}
	return w.out, w.after, nil
}

func walk(sch *Schedule, st *State, pay U, stopAtA bool, m0 *U) (walked, error) {
	if isZ(pay) {
		return walked{done: true, out: zero, after: *st, m: m0}, nil
	}
	pu, err := mulQ(pay, sch.U)
	if err != nil {
		return walked{}, err
	}
	h1, err := addC(st.H, pu)
	if err != nil {
		return walked{}, err
	}
	return run(sch, st, paused{h1: h1, l: st.L, s: st.S, h: st.H}, stopAtA, m0)
}

func resume(sch *Schedule, st *State, p paused) (U, State, *U, error) {
	w, err := run(sch, st, p, false, nil)
	if err != nil {
		return zero, State{}, nil, err
	}
	if !w.done {
		return zero, State{}, nil, ErrOverflow
	}
	return w.out, w.after, w.m, nil
}

type carryT struct{ l, m U }

func run(sch *Schedule, st *State, p paused, stopAtA bool, m0 *U) (walked, error) {
	h1, l, s, h, forceA, guard := p.h1, p.l, p.s, p.h, p.forceA, p.guard
	floor := st.FloorX64
	knownA := p.atA
	var carry *carryT
	if m0 != nil {
		carry = &carryT{l, *m0}
	}
	for {
		guard++
		if guard > 4096 {
			return walked{}, ErrOverflow
		}
		var mHere *U
		var aHere bool
		if forceA {
			aHere = true
		} else if knownA {
			knownA = false
			aHere = true
		} else {
			var known *U
			if carry != nil && eq(carry.l, l) {
				m := carry.m
				known = &m
			}
			carry = nil
			b, m, err := inAM(sch, floor, l, s, known)
			if err != nil {
				return walked{}, err
			}
			mHere = &m
			aHere = b
		}
		if aHere {
			if stopAtA {
				return walked{p: paused{h1: h1, l: l, s: s, h: h, forceA: forceA, guard: guard - 1, atA: true}}, nil
			}
			lb, hasTop := sch.nextPoint(l)
			if hasTop {
				cp, _, f, _, err := sch.at(lb)
				if err != nil {
					return walked{}, err
				}
				mb, err := pow2(lb)
				if err != nil {
					return walked{}, err
				}
				x, err := mulQ(add(cp, f), mb)
				if err != nil {
					return walked{}, err
				}
				if le(x, h1) {
					d, err := sch.dlog2sA(l, lb)
					if err != nil {
						return walked{}, err
					}
					if s, err = scaleUp(s, d); err != nil {
						return walked{}, err
					}
					l = lb
					carry = &carryT{lb, mb}
					continue
				}
			}
			cap64 := add(l, l64)
			hi := cap64
			if hasTop {
				hi = umin(lb, cap64)
			}
			l1, m1, err := sch.solveAM(l, hi, h1)
			if err != nil {
				return walked{}, err
			}
			l1 = umax(l1, l)
			d, err := sch.dlog2sA(l, l1)
			if err != nil {
				return walked{}, err
			}
			if s, err = scaleUp(s, d); err != nil {
				return walked{}, err
			}
			l = l1
			_, _, f, _, err := sch.at(l)
			if err != nil {
				return walked{}, err
			}
			var m U
			if m1 != nil {
				m = *m1
			} else if m, err = pow2(l); err != nil {
				return walked{}, err
			}
			fm, err := mulQ(f, m)
			if err != nil {
				return walked{}, err
			}
			fl, err := divQ(fm, s)
			if err != nil {
				return walked{}, err
			}
			mm := m
			return walked{done: true, out: sub(s, st.S), after: State{S: s, H: h1, L: l, FloorX64: umax(fl, floor)}, m: &mm}, nil
		}
		i := sch.seg(l)
		if isZ(sch.Gcp[i]) && isZ(sch.Gf[i]) {
			var mb U
			var err error
			if mHere != nil {
				mb = *mHere
			} else if mb, err = pow2(l); err != nil {
				return walked{}, err
			}
			h2, s2, l2, done, atSwitch, err := flatB(sch, floor, s, l, h1, mb)
			if err != nil {
				return walked{}, err
			}
			forceA = atSwitch
			h, s, l = h2, s2, umax(l2, l)
			if done {
				break
			}
			continue
		}
		ms, err := maxStepB(sch, floor, h, s, l)
		if err != nil {
			return walked{}, err
		}
		hNext := umin(add(h, ms), h1)
		sNext, lNext, err := stepB(sch, floor, h, s, l, sub(hNext, h))
		if err != nil {
			return walked{}, err
		}
		in, err := inA(sch, floor, lNext, sNext)
		if err != nil {
			return walked{}, err
		}
		if !in {
			h, s, l = hNext, sNext, lNext
			if eq(h, h1) {
				break
			}
			continue
		}
		// Illinois' regula falsi on the gap f − φ inside the step (signed)
		eAt := func(x U) (U, U, U, error) {
			sx, lx, err := stepB(sch, floor, h, s, l, x)
			if err != nil {
				return zero, zero, zero, err
			}
			g, err := gap(sch, floor, lx, sx)
			return g, sx, lx, err
		}
		dh := sub(hNext, h)
		a, b := zero, dh
		sb, lbb := sNext, lNext
		ga, err := gap(sch, floor, l, s)
		if err != nil {
			return walked{}, err
		}
		gb, err := gap(sch, floor, lNext, sNext)
		if err != nil {
			return walked{}, err
		}
		closeG := lsh(n(1), 44) // (ONE >> 20) as i128
		side := 0
		for it := 0; it < 12; it++ {
			if le(sub(b, a), umax(rsh(dh, 24), one)) || !sgt(gb, closeG) {
				break
			}
			if sgt(ga, minus1) {
				ga = minus1
			}
			if slt(gb, one) {
				gb = one
			}
			w, err := mulDiv(sub(b, a), neg(ga), sub(gb, ga))
			if err != nil {
				return walked{}, err
			}
			x := add(a, w)
			x = umin(umax(x, add(a, one)), sub(b, one))
			gx, sx, lx, err := eAt(x)
			if err != nil {
				return walked{}, err
			}
			if !slt(gx, zero) {
				b, gb = x, gx
				sb, lbb = sx, lx
				if side == 1 {
					ga = sdiv2(ga)
				}
				side = 1
			} else {
				a, ga = x, gx
				if side == -1 {
					gb = sdiv2(gb)
				}
				side = -1
			}
		}
		h = add(h, b)
		s, l = sb, lbb
	}
	return walked{done: true, out: sub(s, st.S), after: State{S: s, H: h1, L: l, FloorX64: floor}}, nil
}

const maxFillTrials = 5

// fillToRoomM is schedule::fill_to_room_m (the end in closed form). (pay, out, after, pow2(after.l) or nil).
func fillToRoomM(sch *Schedule, st *State, m0 *U, offer, room U) (U, U, State, *U, error) {
	trials := func() (U, U, State, *U, error) {
		p, o, a, err := fillToRoomTrials(sch, st, offer, room)
		return p, o, a, nil, err
	}
	fail := func(err error) (U, U, State, *U, error) { return zero, zero, State{}, nil, err }
	if isZ(room) || isZ(offer) {
		return trials()
	}
	s1, err := addC(st.S, room)
	if err != nil {
		return fail(err)
	}
	w, err := walk(sch, st, offer, true, m0)
	if err != nil {
		return fail(err)
	}
	if w.done {
		if le(w.out, room) {
			return offer, w.out, w.after, w.m, nil
		}
		return trials()
	}
	p := w.p
	if ge(p.s, s1) {
		return trials()
	}
	left := satSub(p.h1, p.h)
	if lt(mul(umin(lsh(left, 1), max128), p.s), mul(p.h, sub(s1, p.s))) {
		out, after, m, err := resume(sch, st, p)
		if err != nil {
			return fail(err)
		}
		if le(out, room) {
			return offer, out, after, m, nil
		}
		return trials()
	}
	pb := p.books(st.FloorX64)
	end, mEnd, err := endInA(sch, &pb, s1)
	if err != nil {
		return fail(err)
	}
	dh, err := subC(end.H, st.H)
	if err != nil {
		return fail(err)
	}
	pay, err := toU128(divCeil(lsh(dh, 64), sch.U))
	if err != nil {
		return fail(err)
	}
	if le(pay, offer) {
		pu, err := mulQ(pay, sch.U)
		if err != nil {
			return fail(err)
		}
		h, err := addC(st.H, pu)
		if err != nil {
			return fail(err)
		}
		end.H = h
		return pay, room, end, &mEnd, nil
	}
	out, after, m, err := resume(sch, st, p)
	if err != nil {
		return fail(err)
	}
	if le(out, room) {
		return offer, out, after, m, nil
	}
	return trials()
}

type trial struct {
	pay, got U
	st       State
}

func fillToRoomTrials(sch *Schedule, st *State, offer, room U) (U, U, State, error) {
	out, after, err := mintForPayment(sch, st, offer)
	if err != nil {
		return zero, zero, State{}, err
	}
	if le(out, room) {
		return offer, out, after, nil
	}
	if isZ(room) {
		return zero, zero, *st, nil
	}
	up := trial{offer, out, after}
	var lo *trial
	prev, last := [2]U{offer, out}, [2]U{offer, out}
	for k := 1; k < maxFillTrials; k++ {
		floorPay := zero
		if lo != nil {
			floorPay = lo.pay
			if le(sub(room, lo.got), rsh(room, 20)) || le(sub(up.pay, lo.pay), n(2)) {
				break
			}
		}
		var next U
		if k == 1 {
			if next, err = toU128(divCeil(mul(up.pay, room), up.got)); err != nil {
				return zero, zero, State{}, err
			}
		} else {
			v, ok, err := powInterp(last, prev, room)
			if err == nil && ok {
				next = v
			} else {
				next = rsh(add(floorPay, up.pay), 1)
			}
		}
		if le(next, floorPay) || ge(next, up.pay) {
			next = rsh(add(floorPay, up.pay), 1)
		}
		if le(next, floorPay) || ge(next, up.pay) {
			break
		}
		g, a, err := mintForPayment(sch, st, next)
		if err != nil {
			return zero, zero, State{}, err
		}
		prev, last = last, [2]U{next, g}
		if ge(g, room) {
			up = trial{next, g, a}
			if eq(g, room) {
				return next, room, a, nil
			}
		} else if lo == nil || gt(g, lo.got) {
			lo = &trial{next, g, a}
		}
	}
	if le(sub(up.got, room), rsh(room, 20)) {
		return up.pay, room, up.st, nil
	}
	if lo != nil && le(mul(sub(room, lo.got), n(100)), room) {
		return lo.pay, lo.got, lo.st, nil
	}
	if le(mul(sub(up.got, room), n(100)), room) {
		return up.pay, room, up.st, nil
	}
	if lo != nil {
		return lo.pay, lo.got, lo.st, nil
	}
	return up.pay, room, up.st, nil
}

// powInterp is pow_interp: (value, ok) where Rust returns Ok(Some), (_, false) for Ok(None). The i128s are
// two's-complement Us.
func powInterp(a, b [2]U, room U) (U, bool, error) {
	var lg [5]U
	for i, x := range []U{a[0], a[1], b[0], b[1], room} {
		v, err := log2Z(x)
		if err != nil {
			return zero, false, err
		}
		lg[i] = v
	}
	lpa, lga, lpb, lgb, lr := lg[0], lg[1], lg[2], lg[3], lg[4]
	dp, dg := sub(lpa, lpb), sub(lga, lgb)
	if isZ(dp) || isZ(dg) || slt(dp, zero) != slt(dg, zero) {
		return zero, false, nil
	}
	num := sub(lr, lga)
	mag, err := mulDiv(sabs(num), sabs(dp), sabs(dg))
	if err != nil {
		return zero, false, err
	}
	negative := slt(num, zero) != (slt(dp, zero) != slt(dg, zero))
	var l U
	if negative {
		if lt(lpa, mag) {
			return one, true, nil
		}
		l = sub(lpa, mag)
	} else if l, err = addC(lpa, mag); err != nil {
		return one, true, nil
	}
	v, err := pow2(l)
	if err != nil {
		return zero, false, nil
	}
	return v, true, nil
}

func endInA(sch *Schedule, st *State, s1 U) (State, U, error) {
	ls1, err := log2Z(s1)
	if err != nil {
		return State{}, zero, err
	}
	ls, err := log2Z(st.S)
	if err != nil {
		return State{}, zero, err
	}
	left, err := subC(ls1, ls)
	if err != nil {
		return State{}, zero, err
	}
	l := st.L
	for it := 0; it < maxPoints+2; it++ {
		if lb, ok := sch.nextPoint(l); ok {
			d, err := sch.dlog2sA(l, lb)
			if err != nil {
				return State{}, zero, err
			}
			if le(d, left) {
				left = sub(left, d)
				l = lb
				continue
			}
		}
		ka, g, e0 := sch.line(l)
		gi, err := mulQ(g, invLn2X64)
		if err != nil {
			return State{}, zero, err
		}
		a := add(ka, gi)
		ul, err := mulQ(sch.U, left)
		if err != nil {
			return State{}, zero, err
		}
		var d U
		if isZ(g) {
			if d, err = divQ(ul, a); err != nil {
				return State{}, zero, err
			}
		} else {
			ge0, err := mulQ(g, e0)
			if err != nil {
				return State{}, zero, err
			}
			b := add(a, ge0)
			bb := mul(b, b)
			disc := add(bb, mul(lsh(g, 1), ul))
			if lt(disc, bb) { // checked_add at 2^256 (uint256 wraps)
				return State{}, zero, ErrOverflow
			}
			r, err := subC(isqrt(disc), b)
			if err != nil {
				return State{}, zero, err
			}
			if d, err = divQ(add(r, one), g); err != nil {
				return State{}, zero, err
			}
		}
		if l, err = addC(l, add(d, one)); err != nil {
			return State{}, zero, err
		}
		cp, _, f, _, err := sch.at(l)
		if err != nil {
			return State{}, zero, err
		}
		m, err := pow2(l)
		if err != nil {
			return State{}, zero, err
		}
		fm, err := mulQ(f, m)
		if err != nil {
			return State{}, zero, err
		}
		fl, err := divQ(fm, s1)
		if err != nil {
			return State{}, zero, err
		}
		h, err := mulQ(add(cp, f), m)
		if err != nil {
			return State{}, zero, err
		}
		return State{S: s1, H: h, L: l, FloorX64: umax(fl, st.FloorX64)}, m, nil
	}
	return State{}, zero, ErrOverflow
}

func rederiveM(sch *Schedule, st *State) (State, U, error) {
	m, err := pow2(st.L)
	if err != nil {
		return State{}, zero, err
	}
	cp, f, err := sch.Shares(st.L)
	if err != nil {
		return State{}, zero, err
	}
	a, err := mulQ(cp, m)
	if err != nil {
		return State{}, zero, err
	}
	fs, err := mulQ(st.FloorX64, st.S)
	if err != nil {
		return State{}, zero, err
	}
	fm, err := mulQ(f, m)
	if err != nil {
		return State{}, zero, err
	}
	h, err := addC(a, umax(fs, fm))
	if err != nil {
		return State{}, zero, err
	}
	out := *st
	out.H = h
	return out, m, nil
}

func priceOf(m, s U) (U, error) { return divQ(m, s) }

func stepOf(m U) U { return umax(rsh(div(max128, umax(m, one)), 62), retireRoundUp) }

func raisePriceToM(sch *Schedule, st *State, m0, min U) (State, U, error) {
	p, err := priceOf(m0, st.S)
	if err != nil {
		return State{}, zero, err
	}
	if ge(p, min) {
		return *st, m0, nil
	}
	m, err := toU128(divCeil(mul(min, st.S), one64))
	if err != nil {
		return State{}, zero, err
	}
	m = umax(m, one)
	lm, err := log2Z(m)
	if err != nil {
		return State{}, zero, err
	}
	l, err := addC(lm, retireRoundUp)
	if err != nil {
		return State{}, zero, err
	}
	bump := stepOf(m)
	for {
		probe := *st
		probe.L = l
		out, mo, err := rederiveM(sch, &probe)
		if err != nil {
			return State{}, zero, err
		}
		po, err := priceOf(mo, out.S)
		if err != nil {
			return State{}, zero, err
		}
		if ge(po, min) {
			return out, mo, nil
		}
		if l, err = addC(l, bump); err != nil {
			return State{}, zero, err
		}
		bump = lsh(bump, 1)
	}
}

// backingMinPrice is backing_min_price: (price, ok); ok false is Rust's None.
func backingMinPrice(sch *Schedule, band, public, pay U) (U, bool, error) {
	if isZ(pay) {
		return zero, true, nil
	}
	gb, err := toU128(divCeil(mul(band, backingMarginBps), bpsU))
	if err != nil {
		return zero, false, err
	}
	pu, err := mulQ(pay, sch.U)
	if err != nil {
		return zero, false, err
	}
	cut := sub(pay, pu)
	if le(gb, cut) {
		return zero, true, nil
	}
	c := sub(gb, cut)
	if isZ(public) {
		return zero, false, nil
	}
	nMax := div(mul(pay, public), c)
	if !fits(nMax) {
		nMax = max128
	}
	if isZ(nMax) {
		return zero, false, nil
	}
	v := divCeil(mul(pay, one64), nMax)
	if !fits(v) {
		return zero, false, nil
	}
	return v, true, nil
}

// raiseToBackingM is raise_to_backing_m: (books, pow2 or nil, ok); ok false is Rust's None.
func raiseToBackingM(sch *Schedule, st *State, band, public, pay, minPublic U) (State, *U, bool, error) {
	if lt(public, minPublic) {
		return *st, nil, true, nil
	}
	min, ok, err := backingMinPrice(sch, band, public, pay)
	if err != nil {
		return State{}, nil, false, err
	}
	if !ok {
		return State{}, nil, false, nil
	}
	if isZ(min) {
		return *st, nil, true, nil
	}
	m0, err := pow2(st.L)
	if err != nil {
		return State{}, nil, false, err
	}
	p, err := priceOf(m0, st.S)
	if err != nil {
		return State{}, nil, false, err
	}
	if ge(p, min) {
		return *st, &m0, true, nil
	}
	out, m, err := raisePriceToM(sch, st, m0, min)
	if err != nil {
		return State{}, nil, false, err
	}
	return out, &m, true, nil
}

// cpTargets is schedule::cp_targets: (cushion, pol) at the books.
func cpTargets(sch *Schedule, t *Terms, st *State) (U, U, error) {
	m, err := pow2(st.L)
	if err != nil {
		return zero, zero, err
	}
	cp, _, err := sch.Shares(st.L)
	if err != nil {
		return zero, zero, err
	}
	c, p, err := splitCp(t, cp)
	if err != nil {
		return zero, zero, err
	}
	a, err := mulQ(c, m)
	if err != nil {
		return zero, zero, err
	}
	b, err := mulQ(p, m)
	return a, b, err
}

// ScheduleLen is SCHEDULE_LEN: Schedule::to_bytes' length (8 + 5 × 8 × 16 + 16).
const ScheduleLen = 8 + 5*maxPoints*16 + 16

func putU128(b []byte, v U) {
	for i := 0; i < 16; i++ {
		b[i] = byte(u64(rsh(v, uint(8*i))))
	}
}

func getU128(b []byte) U {
	var be [16]byte
	for i := range be {
		be[i] = b[15-i]
	}
	var z U
	z.SetBytes(be[:])
	return z
}

// ToBytes is Schedule::to_bytes: n, then the five arrays and u, little-endian.
func (s *Schedule) ToBytes() []byte {
	b := make([]byte, ScheduleLen)
	b[0] = byte(s.N)
	o := 8
	for _, arr := range []*[maxPoints]U{&s.L, &s.Cp, &s.F, &s.Gcp, &s.Gf} {
		for _, v := range arr {
			putU128(b[o:], v)
			o += 16
		}
	}
	putU128(b[o:], s.U)
	return b
}

// ScheduleAbiLen is abi.encode(FloorSchedule.Schedule)'s length: n, l[8], cp[8], f[8], gcp[8], gf[8], u — 42 words.
const ScheduleAbiLen = 32 * (2 + 5*maxPoints)

// ScheduleFromAbi decodes the EVM contract's schedule (`abi.encode(FloorSchedule.Schedule)`, launchMarket's last output):
// the same fields as Schedule::to_bytes, each a big-endian word that must fit 128 bits, n at most 8.
func ScheduleFromAbi(b []byte) (Schedule, bool) {
	var s Schedule
	if len(b) != ScheduleAbiLen {
		return s, false
	}
	word := func(k int) (U, bool) {
		var z U
		z.SetBytes(b[32*k : 32*k+32])
		return z, z.BitLen() <= 128
	}
	nw, ok := word(0)
	if !ok || !nw.IsUint64() || nw.Uint64() > maxPoints {
		return s, false
	}
	s.N = int(nw.Uint64())
	k := 1
	for _, arr := range []*[maxPoints]U{&s.L, &s.Cp, &s.F, &s.Gcp, &s.Gf} {
		for i := range arr {
			if arr[i], ok = word(k); !ok {
				return s, false
			}
			k++
		}
	}
	if s.U, ok = word(k); !ok {
		return s, false
	}
	return s, true
}

// ScheduleFromBytes is Schedule::from_bytes — how the tracker reads a launch's stored schedule. ⛔ Only bytes the
// program wrote: nothing is re-checked.
func ScheduleFromBytes(b []byte) (Schedule, bool) {
	var s Schedule
	if len(b) != ScheduleLen {
		return s, false
	}
	s.N = min(int(b[0]), maxPoints)
	o := 8
	for _, arr := range []*[maxPoints]U{&s.L, &s.Cp, &s.F, &s.Gcp, &s.Gf} {
		for i := range arr {
			arr[i] = getU128(b[o:])
			o += 16
		}
	}
	s.U = getU128(b[o:])
	return s, true
}
