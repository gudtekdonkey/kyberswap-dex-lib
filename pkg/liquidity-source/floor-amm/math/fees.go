package floormath

// fees.rs: the one fee set — the take on a quote leg and the take on the spent leg (fill-and-refund), the Team Slot's
// split of a fill (split_fill), and the creator fee's routing (creator_legs, split_creator, split_members: the reference
// trade credits the creator fee's unbought slot share and the members' dust to F).

// skimRampLog2X64 is SKIM_RAMP_LOG2_X64 (log2 1.2, truncated): the skim's ramp spans a price 1.2× the floor.
var skimRampLog2X64 = D("4852128366996249510")

// MaxExtraLegs is MAX_EXTRA_LEGS.
const MaxExtraLegs = 4

// Rates is fees::Rates, copied from the preset row at create.
type Rates struct {
	SkimBuyFarBps, SkimSellFarBps, SkimBuyAtFloorBps, SkimSellAtFloorBps uint16
	CreatorBuyBps, CreatorSellBps, PlatformBps, CutBps                   uint16
	ExtraBps                                                             [MaxExtraLegs]uint16
	// FloorTokenBps is the leg to $FLOOR (booked to cut_owed).
	FloorTokenBps uint16
	// The curved fees' START rates (the fields above are the END) and the three Marketcap Milestones (START, END), copied
	// from the row; ratesAt turns them into one trade's flat rates.
	SkimFarStartBps, CreatorStartBps     [2]uint16
	PlatformStartBps, FloorTokenStartBps uint16
	TeamMsBps, PlatformMsBps, FloorMsBps [2]uint16
}

// curveOne is fees::CURVE_ONE: a curve position in parts per billion (0 at or under the START's FDV, curveOne at or past
// the END's).
const curveOne = 1_000_000_000

// liveMilestones is fees::live_milestones: liveMilestonesTo at MilestonesLiveMaxBps.
func liveMilestones(team, platform, floor uint16) (uint16, uint16, uint16) {
	return liveMilestonesTo(team, platform, floor, MilestonesLiveMaxBps)
}

// liveMilestonesTo is fees::live_milestones_to: the three Milestones scaled down together (each ⌊x · max ÷ Σ⌋, their
// split kept) when their sum passes a launch's bound max; unchanged under it.
func liveMilestonesTo(team, platform, floor, max uint16) (uint16, uint16, uint16) {
	sum := uint64(team) + uint64(platform) + uint64(floor)
	if sum <= uint64(max) {
		return team, platform, floor
	}
	sc := func(x uint16) uint16 { return uint16(uint64(x) * uint64(max) / sum) }
	return sc(team), sc(platform), sc(floor)
}

// curveBps is fees::curve_bps: a + (b − a) · t in bps at t ppb of curveOne (clamped), rounded UP (up: the fees, against
// the trader) or DOWN (the Milestones).
func curveBps(a, b uint16, t uint64, up bool) uint16 {
	if t > curveOne {
		t = curveOne
	}
	lo, hi, tt := a, b, t
	if a > b {
		lo, hi, tt = b, a, curveOne-t
	}
	d := uint64(hi-lo) * tt
	v := uint64(lo) + d/curveOne
	if up && d%curveOne != 0 {
		v++
	}
	return uint16(v)
}

// ratesAt is Rates::at: ONE trade's rates at curve position t — every curved fee and Milestone flattened to its value
// there (start = end). The fees round UP and the Milestones DOWN; a far buy skim with a non-zero END never pays under
// 1 bp. The at-floor skims and the extra legs are not curved. Then liveMilestones.
func ratesAt(r *Rates, t uint64) Rates { return ratesAtCapped(r, t, MilestonesLiveMaxBps) }

// ratesAtCapped is Rates::at_capped: ratesAt with the Milestones' bound max together (liveMilestonesTo), a launch's own.
func ratesAtCapped(r *Rates, t uint64, max uint16) Rates {
	c := func(a, b uint16, up bool) uint16 { return curveBps(a, b, t, up) }
	bf := c(r.SkimFarStartBps[0], r.SkimBuyFarBps, true)
	if r.SkimBuyFarBps > 0 && bf < presetMinSkimBuyFarBps {
		bf = presetMinSkimBuyFarBps
	}
	sf := c(r.SkimFarStartBps[1], r.SkimSellFarBps, true)
	cb := c(r.CreatorStartBps[0], r.CreatorBuyBps, true)
	cs := c(r.CreatorStartBps[1], r.CreatorSellBps, true)
	pl := c(r.PlatformStartBps, r.PlatformBps, true)
	ft := c(r.FloorTokenStartBps, r.FloorTokenBps, true)
	tm, pm, fm := liveMilestonesTo(c(r.TeamMsBps[0], r.TeamMsBps[1], false), c(r.PlatformMsBps[0], r.PlatformMsBps[1], false),
		c(r.FloorMsBps[0], r.FloorMsBps[1], false), max)
	return Rates{
		SkimBuyFarBps: bf, SkimSellFarBps: sf, SkimBuyAtFloorBps: r.SkimBuyAtFloorBps, SkimSellAtFloorBps: r.SkimSellAtFloorBps,
		CreatorBuyBps: cb, CreatorSellBps: cs, PlatformBps: pl, CutBps: r.CutBps, ExtraBps: r.ExtraBps, FloorTokenBps: ft,
		SkimFarStartBps: [2]uint16{bf, sf}, CreatorStartBps: [2]uint16{cb, cs}, PlatformStartBps: pl, FloorTokenStartBps: ft,
		TeamMsBps: [2]uint16{tm, tm}, PlatformMsBps: [2]uint16{pm, pm}, FloorMsBps: [2]uint16{fm, fm},
	}
}

// cheaperBuy is Rates::cheaper_buy: a BUY's take at the cheaper of two flattened ratesAt — the trade's place before it (r)
// and where it ends (end) — leg by leg: the far buy skim, the creator fee, the Platform fee and the $FLOOR fee. Every
// curve is monotone in the FDV, so that is the least rate on the buy's path and no piece of a split buy pays under it:
// one buy is never charged more than the same money in pieces. The sell legs, the at-floor skims, the extra legs and the
// Milestones are r's.
func (r *Rates) cheaperBuy(end *Rates) Rates {
	c := *r
	c.SkimBuyFarBps = min(r.SkimBuyFarBps, end.SkimBuyFarBps)
	c.CreatorBuyBps = min(r.CreatorBuyBps, end.CreatorBuyBps)
	c.PlatformBps = min(r.PlatformBps, end.PlatformBps)
	c.FloorTokenBps = min(r.FloorTokenBps, end.FloorTokenBps)
	return c
}

// milestones is Rates::milestones: the three Milestones' flat rates on a flattened ratesAt (team, platform, $FLOOR).
func (r *Rates) milestones() [3]uint16 {
	return [3]uint16{r.TeamMsBps[1], r.PlatformMsBps[1], r.FloorMsBps[1]}
}

// RatesOf is Rates::of.
func RatesOf(p *PresetSpec) Rates {
	return Rates{
		SkimBuyFarBps: p.SkimBuyFarBps, SkimSellFarBps: p.SkimSellFarBps,
		SkimBuyAtFloorBps: p.SkimBuyAtFloorBps, SkimSellAtFloorBps: p.SkimSellAtFloorBps,
		CreatorBuyBps: p.CreatorBuyBps, CreatorSellBps: p.CreatorSellBps,
		PlatformBps: p.PlatformBps, CutBps: p.CutBps,
		ExtraBps:        [MaxExtraLegs]uint16{p.ExtraLegs[0].Bps, p.ExtraLegs[1].Bps, p.ExtraLegs[2].Bps, p.ExtraLegs[3].Bps},
		FloorTokenBps:   p.FloorTokenBps,
		SkimFarStartBps: p.SkimFarStartBps, CreatorStartBps: p.CreatorStartBps,
		PlatformStartBps: p.PlatformStartBps, FloorTokenStartBps: p.FloorTokenStartBps,
		TeamMsBps: p.TeamMsBps, PlatformMsBps: p.PlatformMsBps, FloorMsBps: p.FloorMsBps,
	}
}

// Take is fees::Take: the take on one quote leg.
type Take struct {
	SkimBps                            uint16
	Skim, Cut, Kept, Creator, Platform U
	Extra                              [MaxExtraLegs]U
	// FloorToken goes to cut_owed, then into the $FLOOR launch's F.
	FloorToken U
	// Total is skim + creator + platform + floor token + Σ extra: what the trader pays on the leg.
	Total U
}

func part(leg U, bps uint16) (U, error) { return toU128(div(mul(leg, n(uint64(bps))), bpsU)) }

// partUp is fees::part_up: ⌈leg · bps ÷ 10⁴⌉.
func partUp(leg U, bps uint16) (U, error) { return toU128(divCeil(mul(leg, n(uint64(bps))), bpsU)) }

func closenessBps(price, floor U) uint16 {
	if isZ(floor) {
		return 0
	}
	if le(price, floor) {
		return BPS
	}
	lp, _ := log2X64(price)
	lf, _ := log2X64(floor)
	d := satSub(lp, lf)
	if ge(d, skimRampLog2X64) {
		return 0
	}
	far := div(mul(d, bpsU), skimRampLog2X64)
	if !fits(far) {
		far = bpsU
	}
	return uint16(BPS - u64(umin(far, bpsU)))
}

func skimBpsAt(r *Rates, isBuy bool, price, floor U) uint16 {
	c := uint32(closenessBps(price, floor))
	const b = uint32(BPS)
	bf, sf, ba, sa := uint32(r.SkimBuyFarBps), uint32(r.SkimSellFarBps), uint32(r.SkimBuyAtFloorBps), uint32(r.SkimSellAtFloorBps)
	var v uint32
	switch {
	case isBuy && bf >= ba:
		v = bf - (bf-ba)*c/b
	case isBuy:
		v = bf + (ba-bf)*c/b
	case sf >= sa:
		v = sf
	default:
		v = sf + (sa-sf)*c/b
	}
	return uint16(v)
}

// take is fees::take on quote leg `leg` at the books' price and f BEFORE the trade. Every trader leg rounds UP
// (part_up), bounded by what is left of the leg in the order skim, creator, platform, $FLOOR, extras; the cut rounds
// up too, at most the skim; total = leg − what is left.
func take(r *Rates, leg U, isBuy bool, price, floor U) (Take, error) {
	var t Take
	t.SkimBps = skimBpsAt(r, isBuy, price, floor)
	left := leg
	up := func(bps uint16) (U, error) {
		v, err := partUp(leg, bps)
		if err != nil {
			return zero, err
		}
		v = umin(v, left)
		left = sub(left, v)
		return v, nil
	}
	var err error
	if t.Skim, err = up(t.SkimBps); err != nil {
		return Take{}, err
	}
	cb := r.CreatorSellBps
	if isBuy {
		cb = r.CreatorBuyBps
	}
	if t.Creator, err = up(cb); err != nil {
		return Take{}, err
	}
	if t.Platform, err = up(r.PlatformBps); err != nil {
		return Take{}, err
	}
	if t.FloorToken, err = up(r.FloorTokenBps); err != nil {
		return Take{}, err
	}
	for i := 0; i < MaxExtraLegs; i++ {
		if t.Extra[i], err = up(r.ExtraBps[i]); err != nil {
			return Take{}, err
		}
	}
	c, err := partUp(t.Skim, r.CutBps)
	if err != nil {
		return Take{}, err
	}
	t.Cut = umin(c, t.Skim)
	t.Kept = sub(t.Skim, t.Cut)
	t.Total = sub(leg, left)
	return t, nil
}

// legForNet is fees::leg_for_net: the least leg whose rest after the take covers `net`.
func legForNet(r *Rates, net U, isBuy bool, price, floor U) (U, Take, error) {
	s := skimBpsAt(r, isBuy, price, floor)
	cb := r.CreatorSellBps
	if isBuy {
		cb = r.CreatorBuyBps
	}
	legs := []uint16{s, cb, r.PlatformBps, r.FloorTokenBps, r.ExtraBps[0], r.ExtraBps[1], r.ExtraBps[2], r.ExtraBps[3]}
	var b, k uint64
	for _, x := range legs {
		b += uint64(x)
		if x > 0 {
			k++
		}
	}
	if b >= BPS {
		return zero, Take{}, ErrOverflow
	}
	keep := n(BPS - b)
	// the span [⌈net · 10⁴ ÷ keep⌉, ⌈(net + k) · 10⁴ ÷ keep⌉]
	lo, err := toU128(divCeil(mul(net, bpsU), keep))
	if err != nil {
		return zero, Take{}, err
	}
	nk, err := addC(net, n(k))
	if err != nil {
		return zero, Take{}, err
	}
	hi, err := toU128(divCeil(mul(nk, bpsU), keep))
	if err != nil {
		return zero, Take{}, err
	}
	for g := lo; le(g, hi); g = add(g, one) {
		t, err := take(r, g, isBuy, price, floor)
		if err != nil {
			return zero, Take{}, err
		}
		if ge(sub(g, t.Total), net) {
			return g, t, nil
		}
	}
	return zero, Take{}, ErrOverflow
}

const ppm = 1_000_000

// splitMilestones is fees::split_milestones: of a window, schedule or tail range fill's outOfBooks (pay − vaultIn), the three
// Marketcap Milestones' parts — ⌊pay · rate ÷ 10⁴⌋ each, in the order Team, $FLOOR, platform, each at most what is
// left — and the rest (the books' rounding, to the platform bucket). ms is (team, platform, $FLOOR).
func splitMilestones(pay, outOfBooks U, ms [3]uint16) (team, floor, platform, rest U, err error) {
	left := outOfBooks
	tk := func(bps uint16) (U, error) {
		v, err := part(pay, bps)
		if err != nil {
			return zero, err
		}
		v = umin(v, left)
		left = sub(left, v)
		return v, nil
	}
	if team, err = tk(ms[0]); err != nil {
		return
	}
	if floor, err = tk(ms[2]); err != nil {
		return
	}
	if platform, err = tk(ms[1]); err != nil {
		return
	}
	return team, floor, platform, left, nil
}

// creatorRefBps is fees::CREATOR_REF_BPS: 90 / 9 / 1 % of the creator fee's 10 %.
var creatorRefBps = [3]uint16{900, 90, 10}

// creatorLegs is fees::creator_legs (slices): each present tier's ⌊fee · bps ÷ 10⁴⌋, bounded by what is left.
func creatorLegs(fee U, present [3]bool) ([3]U, U, error) {
	var out [3]U
	left := fee
	for i := 0; i < 3; i++ {
		if !present[i] || creatorRefBps[i] == 0 {
			continue
		}
		s, err := part(fee, creatorRefBps[i])
		if err != nil {
			return out, zero, err
		}
		s = umin(s, left)
		out[i] = s
		left = sub(left, s)
	}
	return out, left, nil
}

// splitCreator is fees::split_creator: the creator fee's rest with a Team Slot, (slot, backing).
func splitCreator(rest U, boughtPpm uint32) (U, U, error) {
	bp := uint64(boughtPpm)
	if bp > ppm {
		bp = ppm
	}
	slot, err := toU128(div(mul(rest, n(bp)), n(ppm)))
	if err != nil {
		return zero, zero, err
	}
	return slot, sub(rest, slot), nil
}

// MaxMembers is fees::MAX_MEMBERS.
const MaxMembers = 5

const weightOne = 1_000_000_000

// splitMembers is fees::split_members: each member's ⌊amount · w ÷ 10⁹⌋ and the dust.
func splitMembers(amount U, w [MaxMembers]uint32) ([MaxMembers]U, U, error) {
	var parts [MaxMembers]U
	given := zero
	for i := 0; i < MaxMembers; i++ {
		wi := uint64(w[i])
		if wi > weightOne {
			wi = weightOne
		}
		v, err := toU128(div(mul(amount, n(wi)), n(weightOne)))
		if err != nil {
			return parts, zero, err
		}
		// never more than is left: the parts sum to at most the amount
		parts[i] = umin(v, sub(amount, given))
		given = add(given, parts[i])
	}
	return parts, sub(amount, given), nil
}
