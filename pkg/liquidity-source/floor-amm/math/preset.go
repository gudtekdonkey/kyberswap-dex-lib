package floormath

// preset.rs: the preset row (governance-written DATA, copied into a launch for life), its one check spec_ok and the
// conversions a launch's create reads. ⛔ No row is a literal here: a live launch's row is read from the contract (the
// tracker), and the goldens' seed rows from golden/schedule.json's preset_rows bytes (PresetFromBytes).

import "encoding/binary"

const (
	presetTotalWhole            = 1_000_000_000
	presetTailBps               = 100
	presetTailWhole             = presetTotalWhole * presetTailBps / 10_000
	presetMaxSkimFarBps         = 1_000
	presetMaxSkimSellAtFloorBps = 1_500
	presetMinSkimSellAtFloorBps = 100
	presetMinSkimBuyFarBps      = 1
	presetMaxCreatorBps         = 500
	presetMaxPlatformBps        = 100
	presetMaxCreatorBuyBps      = 5_000
	teamSlotHardMaxBps          = 1_000
	presetMaxExtraBps           = 500
	presetMaxCreatorAllocBps    = 500
	// MaxAllocReleases is MAX_ALLOC_RELEASES: the Token Option is released in at most 8 steps.
	MaxAllocReleases = 8
	// presetMaxAllocReleaseSecs is the longest clock between two releases (365 days).
	presetMaxAllocReleaseSecs = 31_536_000
	// milestonesMaxBps is MILESTONES_MAX_BPS: the three Marketcap Milestones together, at most, on a row.
	milestonesMaxBps = 1_500
	// MilestonesLiveMaxBps is MILESTONES_LIVE_MAX_BPS: the three Marketcap Milestones together, at most, on a trade of a
	// launch opened by Create (equal to milestonesMaxBps, so a no-op on an admissible row).
	MilestonesLiveMaxBps = 1_500
	// GenesisMsLiveMaxBps is GENESIS_MS_LIVE_MAX_BPS: the genesis launch's own bound on the three together.
	GenesisMsLiveMaxBps = 650
	// PresetLen is PRESET_LEN: PresetSpec::to_bytes' length (the curves appended at 430).
	PresetLen = 486
	// PresetLenV3 is a row before the fee and Milestone curves (its fees read flat, no Milestones).
	PresetLenV3 = 430
	// PresetLenV2 is a row before the Token Option's releases (one release, no clock).
	PresetLenV2 = 409
	// PresetLenV1 is a v1 launch's row (before the appended fields).
	PresetLenV1 = 373
)

// ExtraLeg is preset::ExtraLeg.
type ExtraLeg struct {
	Bps  uint16
	Dest [32]byte
}

// FloorPoint is one of floor_points_usd: (a market cap in whole US dollars, bps).
type FloorPoint struct {
	Usd U
	Bps uint16
}

// PresetSpec is preset::PresetSpec (field order = to_bytes order).
type PresetSpec struct {
	ID                                                         uint8
	RaiseUsdCents, SaleWhole, PolWhole, HeldWhole, WindowWhole U
	CreatorBuyMaxBps                                           uint16
	SnipeSlots                                                 uint32
	WindowMaxPerTxBps                                          uint16
	CushionBps, PolBps                                         [2]uint16
	RampCpFromUsd, FullAtUsd                                   U
	Floor0Bps                                                  uint16
	RampFromUsd                                                U
	FloorPointsUsd                                             [4]FloorPoint
	CutBps, SkimBuyFarBps, SkimSellFarBps                      uint16
	SkimBuyAtFloorBps, SkimSellAtFloorBps                      uint16
	CreatorBuyBps, CreatorSellBps, PlatformBps, TeamSlotMaxBps uint16
	ExtraLegs                                                  [MaxExtraLegs]ExtraLeg
	// FloorTokenBps is the leg to $FLOOR on every trade's quote leg.
	FloorTokenBps uint16
	// CreatorAllocBps is the creator's free allocation, bps of the total supply, locked until CreatorUnlockUsd.
	CreatorAllocBps uint16
	// CreatorUnlockUsd is the market cap (price × total supply, whole US dollars) that unlocks the allocation.
	CreatorUnlockUsd U
	// UnlockBackingUsd is the pots' money (F + C, whole US dollars) the unlock also needs; 0 = none.
	UnlockBackingUsd U
	// AllocReleaseCount, AllocReleaseBps and AllocReleaseSecs: the allocation is released in AllocReleaseCount steps
	// (1 ..= 8), each AllocReleaseBps[i] of it, AllocReleaseSecs apart.
	AllocReleaseCount uint8
	AllocReleaseBps   [MaxAllocReleases]uint16
	AllocReleaseSecs  uint32
	// CurveFromUsd and CurveToUsd span the curves: every fee and Marketcap Milestone is linear in log2(FDV) between the
	// two FDVs (whole US dollars at the declared quote price), flat outside. The fee fields above are each curve's END.
	CurveFromUsd, CurveToUsd U
	// The fees' START rates (buy, sell); start = end is a flat fee. The at-floor skims are not curved.
	SkimFarStartBps, CreatorStartBps     [2]uint16
	PlatformStartBps, FloorTokenStartBps uint16
	// The Marketcap Milestones (START, END), bps of each window / schedule / tail range buy, inside the price: the Team Slot's (the
	// creator's with no slot), the platform's and $FLOOR's.
	TeamMsBps, PlatformMsBps, FloorMsBps [2]uint16
}

func max16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}

func min16(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}

// msTopBps is PresetSpec::ms_top_bps: the three Milestones' largest total (each curve's larger end, summed).
func (p *PresetSpec) msTopBps() uint64 {
	return uint64(max16(p.TeamMsBps[0], p.TeamMsBps[1])) + uint64(max16(p.PlatformMsBps[0], p.PlatformMsBps[1])) +
		uint64(max16(p.FloorMsBps[0], p.FloorMsBps[1]))
}

// curvesFlat: every curved fee's START equals its END and no Marketcap Milestone — ratesAt is the same at every
// position on the curve.
func (p *PresetSpec) curvesFlat() bool {
	return p.SkimFarStartBps == [2]uint16{p.SkimBuyFarBps, p.SkimSellFarBps} &&
		p.CreatorStartBps == [2]uint16{p.CreatorBuyBps, p.CreatorSellBps} &&
		p.PlatformStartBps == p.PlatformBps && p.FloorTokenStartBps == p.FloorTokenBps &&
		p.TeamMsBps == [2]uint16{} && p.PlatformMsBps == [2]uint16{} && p.FloorMsBps == [2]uint16{}
}

// slotRoom is preset::slot_room: the headroom the open leaves under 100 % — max(TeamSlotMaxBps, msTopBps).
func (p *PresetSpec) slotRoom() uint16 {
	if t := p.msTopBps(); t > uint64(p.TeamSlotMaxBps) {
		return uint16(t)
	}
	return p.TeamSlotMaxBps
}

// presetMaxUsd is preset::PRESET_MAX_USD: the most any USD field of a row may say ($10^18).
const presetMaxUsd = 1_000_000_000_000_000_000

func (p *PresetSpec) totalWhole() U { return add(p.SaleWhole, add(p.PolWhole, p.HeldWhole)) }

func (p *PresetSpec) tailWhole() U { return div(mul(p.totalWhole(), n(presetTailBps)), n(10_000)) }

func (p *PresetSpec) creatorAllocWhole() U {
	return div(mul(p.totalWhole(), n(uint64(p.CreatorAllocBps))), n(10_000))
}

// allocReleasesBps is PresetSpec::alloc_releases_bps: the release sizes' bps together (ok false when a release in
// range is 0, one past it is not, or the count is out of 1 ..= MaxAllocReleases).
func (p *PresetSpec) allocReleasesBps() (uint32, bool) {
	k := int(p.AllocReleaseCount)
	if k == 0 || k > MaxAllocReleases {
		return 0, false
	}
	var t uint32
	for i, b := range p.AllocReleaseBps {
		if (i < k) == (b == 0) {
			return 0, false
		}
		t += uint32(b)
	}
	return t, true
}

// PadPresetRow reads an earlier launch's row as the current one, as it was built: a 373-byte v1 row gains zero fields
// (no $FLOOR leg, no allocation), a 409-byte row one release of the whole allocation with no clock, and a 430-byte
// row flat fees (each curve's START = its END, over a $1 → $2 span) with no Marketcap Milestones.
func PadPresetRow(b []byte) []byte {
	if len(b) == PresetLenV1 {
		b = append(append([]byte{}, b...), make([]byte, PresetLenV2-PresetLenV1)...)
	}
	if len(b) == PresetLenV2 {
		tail := make([]byte, PresetLenV3-PresetLenV2)
		tail[0] = 1
		binary.LittleEndian.PutUint16(tail[1:], 10_000)
		b = append(append([]byte{}, b...), tail...)
	}
	if len(b) == PresetLenV3 {
		p, ok := presetFromBytes(b)
		if !ok {
			return b
		}
		p.CurveFromUsd, p.CurveToUsd = n(1), n(2)
		b = p.ToBytes()
	}
	return b
}

// PresetFromBytes is PresetSpec::from_bytes (little-endian, PresetLen bytes) — how the tracker reads a launch's row
// from the contract's launchSpec getter (an earlier launch's row through PadPresetRow first).
func PresetFromBytes(b []byte) (PresetSpec, bool) {
	if len(b) != PresetLen {
		return PresetSpec{}, false
	}
	return presetFromBytes(b)
}

// presetFromBytes also reads a row from before the curves (PresetLenV3): its fees FLAT (each START = its END), no
// Milestones and a 0 span.
func presetFromBytes(b []byte) (PresetSpec, bool) {
	var p PresetSpec
	if len(b) != PresetLen && len(b) != PresetLenV3 {
		return p, false
	}
	o := 0
	take := func(k int) []byte { s := b[o : o+k]; o += k; return s }
	u16 := func() uint16 { return binary.LittleEndian.Uint16(take(2)) }
	u128 := func() U {
		s := take(16)
		var be [16]byte
		for i := range be {
			be[i] = s[15-i]
		}
		var z U
		z.SetBytes(be[:])
		return z
	}
	p.ID = take(1)[0]
	p.RaiseUsdCents, p.SaleWhole, p.PolWhole, p.HeldWhole, p.WindowWhole = u128(), u128(), u128(), u128(), u128()
	p.CreatorBuyMaxBps = u16()
	p.SnipeSlots = binary.LittleEndian.Uint32(take(4))
	p.WindowMaxPerTxBps = u16()
	p.CushionBps = [2]uint16{u16(), u16()}
	p.PolBps = [2]uint16{u16(), u16()}
	p.RampCpFromUsd = u128()
	p.FullAtUsd = u128()
	p.Floor0Bps = u16()
	p.RampFromUsd = u128()
	for i := range p.FloorPointsUsd {
		p.FloorPointsUsd[i].Usd = u128()
		p.FloorPointsUsd[i].Bps = u16()
	}
	p.CutBps, p.SkimBuyFarBps, p.SkimSellFarBps = u16(), u16(), u16()
	p.SkimBuyAtFloorBps, p.SkimSellAtFloorBps = u16(), u16()
	p.CreatorBuyBps, p.CreatorSellBps, p.PlatformBps, p.TeamSlotMaxBps = u16(), u16(), u16(), u16()
	for i := range p.ExtraLegs {
		p.ExtraLegs[i].Bps = u16()
		copy(p.ExtraLegs[i].Dest[:], take(32))
	}
	p.FloorTokenBps, p.CreatorAllocBps = u16(), u16()
	p.CreatorUnlockUsd = u128()
	p.UnlockBackingUsd = u128()
	p.AllocReleaseCount = take(1)[0]
	for i := range p.AllocReleaseBps {
		p.AllocReleaseBps[i] = u16()
	}
	p.AllocReleaseSecs = binary.LittleEndian.Uint32(take(4))
	if len(b) == PresetLen {
		p.CurveFromUsd, p.CurveToUsd = u128(), u128()
		p.SkimFarStartBps = [2]uint16{u16(), u16()}
		p.CreatorStartBps = [2]uint16{u16(), u16()}
		p.PlatformStartBps, p.FloorTokenStartBps = u16(), u16()
		p.TeamMsBps = [2]uint16{u16(), u16()}
		p.PlatformMsBps = [2]uint16{u16(), u16()}
		p.FloorMsBps = [2]uint16{u16(), u16()}
	} else {
		p.SkimFarStartBps = [2]uint16{p.SkimBuyFarBps, p.SkimSellFarBps}
		p.CreatorStartBps = [2]uint16{p.CreatorBuyBps, p.CreatorSellBps}
		p.PlatformStartBps, p.FloorTokenStartBps = p.PlatformBps, p.FloorTokenBps
	}
	return p, true
}

// curvesOk is preset::curves_ok: a real span (0 < from < to ≤ the USD bound), each START under its END's cap, the
// at-floor buy skim ≤ max(min(start, end far buy), 1 bp), the Team Milestone ≤ the row's Team Slot max, each Milestone
// and the three together ≤ milestonesMaxBps.
func curvesOk(p *PresetSpec) bool {
	sb, ss := p.SkimFarStartBps[0], p.SkimFarStartBps[1]
	cb, cs := p.CreatorStartBps[0], p.CreatorStartBps[1]
	return !isZ(p.CurveFromUsd) && lt(p.CurveFromUsd, p.CurveToUsd) && le(p.CurveToUsd, n(presetMaxUsd)) &&
		sb <= presetMaxSkimFarBps && ss <= presetMaxSkimFarBps &&
		p.SkimBuyAtFloorBps <= max16(min16(sb, p.SkimBuyFarBps), presetMinSkimBuyFarBps) &&
		cb <= presetMaxCreatorBps && cs <= presetMaxCreatorBps &&
		p.PlatformStartBps <= presetMaxPlatformBps && p.FloorTokenStartBps <= presetMaxPlatformBps &&
		max16(p.TeamMsBps[0], p.TeamMsBps[1]) <= p.TeamSlotMaxBps &&
		max16(p.PlatformMsBps[0], p.PlatformMsBps[1]) <= milestonesMaxBps &&
		max16(p.FloorMsBps[0], p.FloorMsBps[1]) <= milestonesMaxBps &&
		p.msTopBps() <= milestonesMaxBps
}

// SpecOk is preset::spec_ok: the one check every chain's add_preset / update_preset runs.
func SpecOk(p *PresetSpec) bool {
	k := &p.FloorPointsUsd
	var extra uint32
	for _, l := range p.ExtraLegs {
		extra += uint32(l.Bps)
		if l.Bps > 0 && l.Dest == [32]byte{} {
			return false
		}
	}
	// every USD field bounded so usd · 100 fits u128 (presetMaxUsd)
	releasesBps, releasesOk := p.allocReleasesBps()
	maxUsd := n(presetMaxUsd)
	usdOk := le(p.RaiseUsdCents, mul(maxUsd, n(100))) && le(p.RampCpFromUsd, maxUsd) && le(p.FullAtUsd, maxUsd) &&
		le(p.RampFromUsd, maxUsd) && le(k[3].Usd, maxUsd)
	return usdOk && p.ID != 0 &&
		eq(p.totalWhole(), n(presetTotalWhole)) &&
		eq(p.tailWhole(), n(presetTailWhole)) &&
		gt(p.HeldWhole, n(presetTailWhole)) &&
		!isZ(p.PolWhole) && !isZ(p.SaleWhole) &&
		ge(p.RaiseUsdCents, n(100)) &&
		le(p.WindowWhole, p.SaleWhole) &&
		p.CreatorBuyMaxBps <= presetMaxCreatorBuyBps &&
		uint64(p.WindowMaxPerTxBps) <= BPS &&
		p.CushionBps[0] <= p.CushionBps[1] &&
		p.PolBps[0] <= p.PolBps[1] &&
		uint64(p.CutBps) < BPS &&
		lt(p.RampFromUsd, k[0].Usd) && lt(k[0].Usd, k[1].Usd) && lt(k[1].Usd, k[2].Usd) && lt(k[2].Usd, k[3].Usd) &&
		p.Floor0Bps <= k[0].Bps && k[0].Bps <= k[1].Bps && k[1].Bps <= k[2].Bps && k[2].Bps <= k[3].Bps &&
		uint64(k[3].Bps) < BPS &&
		p.SkimBuyFarBps >= presetMinSkimBuyFarBps &&
		p.SkimBuyFarBps <= presetMaxSkimFarBps &&
		p.SkimSellFarBps <= presetMaxSkimFarBps &&
		p.SkimBuyAtFloorBps <= p.SkimBuyFarBps &&
		p.SkimSellAtFloorBps >= presetMinSkimSellAtFloorBps &&
		p.SkimSellAtFloorBps <= presetMaxSkimSellAtFloorBps &&
		p.CreatorBuyBps <= presetMaxCreatorBps &&
		p.CreatorSellBps <= presetMaxCreatorBps &&
		p.PlatformBps <= presetMaxPlatformBps &&
		p.TeamSlotMaxBps <= teamSlotHardMaxBps &&
		extra <= presetMaxExtraBps &&
		p.CutBps == 0 &&
		p.FloorTokenBps <= presetMaxPlatformBps &&
		p.CreatorAllocBps <= presetMaxCreatorAllocBps &&
		gt(p.HeldWhole, add(n(presetTailWhole), p.creatorAllocWhole())) &&
		roundTripLoses(p) &&
		le(p.UnlockBackingUsd, p.CreatorUnlockUsd) &&
		// the allocation's releases: 1 ..= MaxAllocReleases, each in range > 0, summing to 10,000 bps of it, a clock
		// between releases
		releasesOk && releasesBps == 10_000 &&
		p.AllocReleaseSecs <= presetMaxAllocReleaseSecs &&
		(p.AllocReleaseCount == 1 || p.AllocReleaseSecs > 0) &&
		// the open's cushion + POL share, the row's largest slot cut or Milestones total (slotRoom) and the 50 bps margin
		// leave room (openSaleFor)
		uint64(p.CushionBps[0])+uint64(p.PolBps[0])+uint64(p.slotRoom())+openSlotMarginBps < 10_000 &&
		curvesOk(p)
}

// roundTripLoses is preset::round_trip_loses: no round trip can profit on the row under the backing line (a seller who
// is not the last pays the whole sell take; the last seller's gross is at most the pots).
func roundTripLoses(p *PresetSpec) bool {
	const b = BPS
	var x uint64
	for _, l := range p.ExtraLegs {
		x += uint64(l.Bps)
	}
	// every curved leg at its least (linear between its two ends, so the least is at one of them)
	out := uint64(min16(p.PlatformBps, p.PlatformStartBps)) + uint64(min16(p.FloorTokenBps, p.FloorTokenStartBps)) + x
	skimMin := uint64(min16(min16(p.SkimSellFarBps, p.SkimFarStartBps[1]), p.SkimSellAtFloorBps))
	creator := uint64(min16(p.CreatorSellBps, p.CreatorStartBps[1]))
	tsMin := skimMin + creator + out
	ns := creator + out
	return tsMin < b && ns < b && b*(b-tsMin) < b*b && (b-out)*(b-ns) < b*b
}

// unit is 10^decimals.
func unit(d uint8) (U, error) {
	if d > 38 {
		return zero, ErrOverflow
	}
	v := one
	for i := uint8(0); i < d; i++ {
		v = mul(v, n(10))
	}
	return v, nil
}

// UsdToQuote is preset::usd_to_quote: ⌊cents · 10^decimals · 2^64 ÷ quote_usd_cents_x64⌋.
func UsdToQuote(usdCents, quoteUsdCentsX64 U, quoteDecimals uint8) (U, error) {
	if isZ(quoteUsdCentsX64) {
		return zero, ErrZero
	}
	un, err := unit(quoteDecimals)
	if err != nil {
		return zero, err
	}
	v, err := shl64C(mul(usdCents, un))
	if err != nil {
		return zero, err
	}
	return toU128(div(v, quoteUsdCentsX64))
}

// PresetRaise is preset_of's raise: what launchState's `raise` reports for a launch of p at the declared quote price.
func PresetRaise(p *PresetSpec, quoteUsdCentsX64 U, quoteDecimals uint8) (U, error) {
	_, raise, err := presetOf(p, quoteUsdCentsX64, quoteDecimals)
	return raise, err
}

func presetOf(p *PresetSpec, quoteUsdCentsX64 U, quoteDecimals uint8) (Terms, U, error) {
	m := func(usd U) (U, error) {
		c, err := mulC(usd, n(100))
		if err != nil {
			return zero, err
		}
		return UsdToQuote(c, quoteUsdCentsX64, quoteDecimals)
	}
	raw, err := m(div(p.RaiseUsdCents, n(100)))
	if err != nil {
		return Terms{}, zero, err
	}
	raise := sub(raw, mod(raw, p.SaleWhole))
	if isZ(raise) {
		return Terms{}, zero, ErrZero
	}
	t := Terms{CushionBps: p.CushionBps, PolBps: p.PolBps, Floor0Bps: p.Floor0Bps, CutBps: p.CutBps}
	if t.RampCpFrom, err = m(p.RampCpFromUsd); err != nil {
		return Terms{}, zero, err
	}
	if t.FullAt, err = m(p.FullAtUsd); err != nil {
		return Terms{}, zero, err
	}
	if t.RampFrom, err = m(p.RampFromUsd); err != nil {
		return Terms{}, zero, err
	}
	for i, k := range p.FloorPointsUsd {
		if t.FloorPoints[i].Mcap, err = m(k.Usd); err != nil {
			return Terms{}, zero, err
		}
		t.FloorPoints[i].Bps = k.Bps
	}
	return t, raise, nil
}

func saleTokens(p *PresetSpec, d uint8) (U, error) {
	un, err := unit(d)
	if err != nil {
		return zero, err
	}
	return mulC(p.SaleWhole, un)
}

func windowTokens(p *PresetSpec, d uint8) (U, error) {
	un, err := unit(d)
	if err != nil {
		return zero, err
	}
	return mulC(p.WindowWhole, un)
}

func creatorBuyMaxTokens(p *PresetSpec, d uint8) (U, error) {
	w, err := windowTokens(p, d)
	if err != nil {
		return zero, err
	}
	v, err := mulC(w, n(uint64(p.CreatorBuyMaxBps)))
	if err != nil {
		return zero, err
	}
	return div(v, bpsU), nil
}

func openPriceX64(raise, sale U) (U, error) {
	if isZ(sale) {
		return zero, ErrZero
	}
	return toU128(divCeil(lsh(raise, 64), sale))
}

func slotCutQ(rateBps uint16, boughtPpm uint32) U {
	bp := uint64(boughtPpm)
	if bp > 1_000_000 {
		bp = 1_000_000
	}
	return div(mul(mul(n(uint64(rateBps)), n(bp)), one64), n(BPS*1_000_000))
}

// ToBytes is PresetSpec::to_bytes (PresetLen bytes, little-endian, no padding).
func (p *PresetSpec) ToBytes() []byte {
	b := make([]byte, 0, PresetLen)
	u16 := func(v uint16) { b = append(b, byte(v), byte(v>>8)) }
	u128 := func(v U) { var t [16]byte; putU128(t[:], v); b = append(b, t[:]...) }
	b = append(b, p.ID)
	for _, v := range []U{p.RaiseUsdCents, p.SaleWhole, p.PolWhole, p.HeldWhole, p.WindowWhole} {
		u128(v)
	}
	u16(p.CreatorBuyMaxBps)
	b = append(b, byte(p.SnipeSlots), byte(p.SnipeSlots>>8), byte(p.SnipeSlots>>16), byte(p.SnipeSlots>>24))
	u16(p.WindowMaxPerTxBps)
	for _, v := range []uint16{p.CushionBps[0], p.CushionBps[1], p.PolBps[0], p.PolBps[1]} {
		u16(v)
	}
	u128(p.RampCpFromUsd)
	u128(p.FullAtUsd)
	u16(p.Floor0Bps)
	u128(p.RampFromUsd)
	for _, k := range p.FloorPointsUsd {
		u128(k.Usd)
		u16(k.Bps)
	}
	for _, v := range []uint16{p.CutBps, p.SkimBuyFarBps, p.SkimSellFarBps, p.SkimBuyAtFloorBps, p.SkimSellAtFloorBps,
		p.CreatorBuyBps, p.CreatorSellBps, p.PlatformBps, p.TeamSlotMaxBps} {
		u16(v)
	}
	for _, l := range p.ExtraLegs {
		u16(l.Bps)
		b = append(b, l.Dest[:]...)
	}
	u16(p.FloorTokenBps)
	u16(p.CreatorAllocBps)
	u128(p.CreatorUnlockUsd)
	u128(p.UnlockBackingUsd)
	b = append(b, p.AllocReleaseCount)
	for _, v := range p.AllocReleaseBps {
		u16(v)
	}
	b = append(b, byte(p.AllocReleaseSecs), byte(p.AllocReleaseSecs>>8), byte(p.AllocReleaseSecs>>16), byte(p.AllocReleaseSecs>>24))
	u128(p.CurveFromUsd)
	u128(p.CurveToUsd)
	for _, v := range []uint16{p.SkimFarStartBps[0], p.SkimFarStartBps[1], p.CreatorStartBps[0], p.CreatorStartBps[1],
		p.PlatformStartBps, p.FloorTokenStartBps, p.TeamMsBps[0], p.TeamMsBps[1], p.PlatformMsBps[0], p.PlatformMsBps[1],
		p.FloorMsBps[0], p.FloorMsBps[1]} {
		u16(v)
	}
	return b
}
