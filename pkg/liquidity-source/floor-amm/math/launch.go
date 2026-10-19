package floormath

// launch.rs: THE REFERENCE TRADE — a launch's whole market state and create / buy / sell / burn in the reference's
// order (the take before any credit, fill-and-refund, graduation, the end's inventory to the tail). The bought-back lot
// is kept by what EACH token cost (lot.go: bins on a log-price grid) and resold the cheapest bin first, each at its own
// ask, whenever that is under the next price: the window's ask in the window, then the books' price (or where the
// schedule's fill would start). The cushion's top is 1.00 × price · u′ and comes down to it at the start of every buy
// before the end; once a buy sells from the lot every later credit in it is capped at the lot's top. At the schedule's
// end the tail opens as ONE range from the frozen price to an infinite price (tail.go): a buy after the end takes the
// cheapest bin while its ask is at or under the range's price as a buy pays it, then the range; a sell after the end goes
// to the range first. A buy's take is charged at the cheaper of each curved buy fee's rate before the trade and where it
// ends; what that frees buys on once. The adapter quotes a swap by running Buy / Sell on a copy of the launch.

import "errors"

// Launch is launch::Launch.
type Launch struct {
	Spec       PresetSpec
	Rates      Rates
	Terms      Terms
	Sch        Schedule
	St         State
	P0X64      U
	Pots       Pots
	Supply     U
	WindowLeft U
	Inventory  U
	LotTokens  U
	LotCost    U
	Tail       U
	// CreatorAlloc is what is still held of the creator's free allocation (LOCKED until its releases); 0 once all
	// are released.
	CreatorAlloc U
	// CreatorUnlockMcap is the market cap that unlocks it, quote base units (the row's unlock at the declared price).
	CreatorUnlockMcap U
	// CreatorUnlockBacking is the pots' money (F + C) the unlock also needs, quote base units.
	CreatorUnlockBacking U
	// AllocReleasesDone is how many of the allocation's releases are done; AllocNextReleaseAt when the next is due
	// (unix seconds; 0 before the first, which waits only on the lines, and once every release is done).
	AllocReleasesDone  uint8
	AllocNextReleaseAt uint64
	Ended              bool
	Graduated          bool
	SlotRateBps        uint16
	SlotBoughtPpm      uint32
	// SlotWeights are the members' weights (ppb, fees::weights); CreatorRefs the creator's paid referrer tiers.
	SlotWeights [MaxMembers]uint32
	CreatorRefs [3]bool
	CreatedSlot uint64
	// TokenDecimals are the MATH decimals (mathDecimals: the token's, capped at max(6, the quote's)); TokenScale is
	// 10^(chain − math): Buy / Sell / Burn take and return chain units, the state is in math units.
	TokenDecimals uint8
	TokenScale    U
	// CurveL is log2 (Q64.64) of the curves' START and END FDV in quote base units (curveLogs, fixed at create).
	CurveL [2]U
	// MsLiveMaxBps is this launch's bound on the three Marketcap Milestones together (MilestonesLiveMaxBps at create,
	// GenesisMsLiveMaxBps on the genesis launch), fixed for life; every rate read caps at it.
	MsLiveMaxBps uint16
	// The tail range opened at the schedule's end (tailOpen): its liquidity L (0 before the end), its √p · 2^64 now and at
	// the frozen price, and the quote it holds (in the launch's vault beside F and C). Its tokens are Tail.
	TailLiq, TailSqrtX64, TailSqrtEndX64, TailMoney U
	// Lot is the bought-back lot by what EACH token cost (lot.go), resold the cheapest first; LotTokens / LotCost stay
	// its totals.
	Lot Lot
}

// Routing is launch::Routing: where the creator fee and the Team Slot's take went (the program's bookkeeping; the
// backing and the members' dust are credited to F by the trade).
type Routing struct {
	CreatorRefs                              [3]U
	ToCreator, CreatorToSlot, CreatorBacking U
	SlotMembers                              [MaxMembers]U
	SlotDust                                 U
}

// BuyReceipt is launch::BuyReceipt.
type BuyReceipt struct {
	Leg, Spent, Refund                                                                       U
	Take                                                                                     Take
	Swept, Out                                                                               U
	WindowOut, WindowPay, ScheduleOut, SchedulePay, LotOut, FlatLotOut, FlatTailOut, FlatPay U
	FillCut, FillSlot, Dust                                                                  U
	Routing                                                                                  Routing
	GraduatedNow                                                                             bool
	GraduationPriceX64                                                                       U
	EndedNow                                                                                 bool
	// LotPay is what the lot's tokens (LotOut, sold first, not a part of ScheduleOut) took.
	LotPay U
	// TailPay is what the tail range took (part of FlatPay); TailToFloor the part of what it kept that went to F.
	TailPay, TailToFloor U
	// MsPlatform and MsFloor are the Platform and $FLOOR Marketcap Milestones (the Team Milestone is FillSlot), out of
	// the window's, the schedule's and the tail range's fills.
	MsPlatform, MsFloor U
}

// AllocRelease is launch::AllocRelease: what one release of the allocation did.
type AllocRelease struct {
	// ReleaseIndex is 0-based (when Tokens is 0, nothing was left: the count done).
	ReleaseIndex uint8
	Tokens       U
	// Members is Tokens split pro rata by the Team Slot's weights at this release, by place (place 0 the creator).
	Members [MaxMembers]U
	// Released is what has been released so far, this release included; NextReleaseAt 0 once none is left.
	Released      U
	NextReleaseAt uint64
}

// TokenOptionState is launch::TokenOptionState: the allocation's releases in one read.
type TokenOptionState struct {
	Total, Released            U
	ReleasesDone, ReleaseCount uint8
	NextReleaseIndex           uint8
	NextReleaseAt              uint64
	NextReleaseTokens          U
	LinesMet, Due              bool
}

// SellReceipt is launch::SellReceipt.
type SellReceipt struct {
	Tokens, Gross, FromCushion, FromFloor U
	Take                                  Take
	Payout, Swept                         U
	Routing                               Routing
	// KeptToPlatform is the seller's own share of the kept skim (kept · tokens ÷ S_out), to the platform bucket.
	KeptToPlatform U
	// TailTokens are the tokens the tail range took back first (chain units, as Tokens) and
	// TailFromFloor / TailFromMoney their pay from F and from the range's money (in Gross).
	TailTokens, TailFromFloor, TailFromMoney U
}

// Create is Launch::create: the row checked, the books opened on the sale, the creator's referrer tiers set
// (before the creator's capped buy, so it pays them), the creator's capped buy.
func Create(spec *PresetSpec, quoteUsdCentsX64 U, quoteDecimals, tokenDecimals uint8, createdSlot uint64, creatorRefs [3]bool, creatorQuote U) (Launch, *BuyReceipt, error) {
	if !SpecOk(spec) {
		return Launch{}, nil, ErrPresetInvalid
	}
	terms, raise, err := presetOf(spec, quoteUsdCentsX64, quoteDecimals)
	if err != nil {
		return Launch{}, nil, err
	}
	md := mathDecimals(tokenDecimals, quoteDecimals)
	scale, err := unit(tokenDecimals - md)
	if err != nil {
		return Launch{}, nil, err
	}
	tokenDecimals = md
	tok, err := unit(tokenDecimals)
	if err != nil {
		return Launch{}, nil, err
	}
	sale, err := saleTokens(spec, tokenDecimals)
	if err != nil {
		return Launch{}, nil, err
	}
	om, err := openMcap(raise, sale, zero)
	if err != nil {
		return Launch{}, nil, err
	}
	sch, err := NewSchedule(&terms, om)
	if err != nil {
		return Launch{}, nil, err
	}
	pol, err := mulC(spec.PolWhole, tok)
	if err != nil {
		return Launch{}, nil, err
	}
	st, err := openSaleFor(&sch, raise, sale, pol, spec.slotRoom())
	if err != nil {
		return Launch{}, nil, err
	}
	supply, err := mulC(spec.totalWhole(), tok)
	if err != nil {
		return Launch{}, nil, err
	}
	window, err := windowTokens(spec, tokenDecimals)
	if err != nil {
		return Launch{}, nil, err
	}
	tail := mul(spec.tailWhole(), tok)
	alloc := mul(spec.creatorAllocWhole(), tok)
	unlockCents, err := mulC(spec.CreatorUnlockUsd, n(100))
	if err != nil {
		return Launch{}, nil, err
	}
	unlock, err := UsdToQuote(unlockCents, quoteUsdCentsX64, quoteDecimals)
	if err != nil {
		return Launch{}, nil, err
	}
	backingCents, err := mulC(spec.UnlockBackingUsd, n(100))
	if err != nil {
		return Launch{}, nil, err
	}
	backing, err := UsdToQuote(backingCents, quoteUsdCentsX64, quoteDecimals)
	if err != nil {
		return Launch{}, nil, err
	}
	p0, err := openPriceX64(raise, sale)
	if err != nil {
		return Launch{}, nil, err
	}
	curveL, err := curveLogs(spec, quoteUsdCentsX64, quoteDecimals)
	if err != nil {
		return Launch{}, nil, err
	}
	l := Launch{
		Spec: *spec, Rates: RatesOf(spec), MsLiveMaxBps: MilestonesLiveMaxBps, Terms: terms, Sch: sch, St: st, P0X64: p0, Supply: supply,
		WindowLeft: window, Inventory: sub(sub(sub(supply, window), tail), alloc), Tail: tail, Graduated: isZ(window),
		CreatorAlloc: alloc, CreatorUnlockMcap: unlock, CreatorUnlockBacking: backing,
		CreatorRefs: creatorRefs, CreatedSlot: createdSlot, TokenDecimals: tokenDecimals, TokenScale: scale, CurveL: curveL,
	}
	if isZ(creatorQuote) {
		return l, nil, nil
	}
	c, err := creatorBuyMaxTokens(spec, tokenDecimals)
	if err != nil {
		return Launch{}, nil, err
	}
	live, err := l.chain(supply)
	if err != nil {
		return Launch{}, nil, err
	}
	r, err := l.buyCapped(creatorQuote, live, &c)
	if err != nil {
		return Launch{}, nil, err
	}
	return l, &r, nil
}

// Held is every token the program holds: the window, the inventory, the lot, the tail and the creator's locked free
// allocation (the creator's locked BUY is outside).
func (l *Launch) Held() U {
	return add(add(add(l.WindowLeft, l.Inventory), add(l.LotTokens, l.Tail)), l.CreatorAlloc)
}

// CreatorAllocUnlocked is trade::creator_alloc_unlocked: the market cap ⌊price · supply ÷ 2^64⌋ reached unlockMcap AND
// the pots' money (F + C) reached unlockBacking.
func CreatorAllocUnlocked(priceX64, supply, unlockMcap, band, unlockBacking U) bool {
	return ge(rsh(mul(priceX64, supply), 64), unlockMcap) && ge(band, unlockBacking)
}

// AllocTotal is Launch::alloc_total: the whole allocation at create, math units.
func (l *Launch) AllocTotal() (U, error) {
	un, err := unit(l.TokenDecimals)
	if err != nil {
		return zero, err
	}
	return mulC(l.Spec.creatorAllocWhole(), un)
}

// allocLinesMet: the market cap at the price a buyer pays now (LiveBuyPriceX64) × the stored supply, and F + C.
func (l *Launch) allocLinesMet() (bool, error) {
	price, err := l.LiveBuyPriceX64()
	if err != nil {
		return false, err
	}
	return CreatorAllocUnlocked(price, l.Supply, l.CreatorUnlockMcap, add(l.Pots.Floor, l.Pots.Cushion), l.CreatorUnlockBacking), nil
}

// AllocReleaseDue is Launch::alloc_release_due: nil, or NotDue while the next release's clock runs, or Locked while
// the market cap or the backing line is not met (every release re-checks both). nil too when nothing is left.
func (l *Launch) AllocReleaseDue(now uint64) error {
	if isZ(l.CreatorAlloc) {
		return nil
	}
	if l.AllocReleasesDone > 0 && now < l.AllocNextReleaseAt {
		return ErrNotDue
	}
	met, err := l.allocLinesMet()
	if err != nil {
		return err
	}
	if !met {
		return ErrLocked
	}
	return nil
}

func (l *Launch) allocReleaseCount() uint8 {
	if l.Spec.AllocReleaseCount == 0 {
		return 1
	}
	return l.Spec.AllocReleaseCount
}

// releasedAfterNext is what has been released once the next release is done (the last ends on the total).
func (l *Launch) releasedAfterNext(total U) (U, error) {
	k := l.AllocReleasesDone
	if k+1 >= l.allocReleaseCount() {
		return total, nil
	}
	return allocReleasedAfter(total, l.Spec.AllocReleaseBps[:], int(k)+1)
}

// TokenOptionState is Launch::token_option_state: the allocation's releases at `now`; moves nothing.
func (l *Launch) TokenOptionState(now uint64) (TokenOptionState, error) {
	total, err := l.AllocTotal()
	if err != nil {
		return TokenOptionState{}, err
	}
	met, err := l.allocLinesMet()
	if err != nil {
		return TokenOptionState{}, err
	}
	s := TokenOptionState{Total: total, Released: sub(total, l.CreatorAlloc), ReleasesDone: l.AllocReleasesDone,
		ReleaseCount: l.allocReleaseCount(), LinesMet: met}
	if !isZ(l.CreatorAlloc) {
		to, err := l.releasedAfterNext(total)
		if err != nil {
			return TokenOptionState{}, err
		}
		next := zero
		if gt(to, s.Released) {
			next = sub(to, s.Released)
		}
		s.NextReleaseIndex = l.AllocReleasesDone
		s.NextReleaseAt = l.AllocNextReleaseAt
		s.NextReleaseTokens = umin(next, l.CreatorAlloc)
		s.Due = l.AllocReleaseDue(now) == nil
	}
	return s, nil
}

// UnlockCreatorAlloc is Launch::unlock_creator_alloc: ONE release of the allocation a call. The first is due once the
// market cap (the books' price × the stored supply) reaches CreatorUnlockMcap AND F + C reaches CreatorUnlockBacking;
// each later one AllocReleaseSecs after the one before it (NotDue before), re-checking both lines (Locked). The
// release's tokens leave the program's hold (they join S_out, so f falls) and are split pro rata by the Team Slot's
// weights now. Nothing left: Tokens 0.
func (l *Launch) UnlockCreatorAlloc(now uint64) (AllocRelease, error) {
	total, err := l.AllocTotal()
	if err != nil {
		return AllocRelease{}, err
	}
	if isZ(l.CreatorAlloc) {
		return AllocRelease{ReleaseIndex: l.AllocReleasesDone, Released: total}, nil
	}
	if err := l.AllocReleaseDue(now); err != nil {
		return AllocRelease{}, err
	}
	k := l.AllocReleasesDone
	released, err := l.releasedAfterNext(total)
	if err != nil {
		return AllocRelease{}, err
	}
	before := sub(total, l.CreatorAlloc)
	if lt(released, before) {
		return AllocRelease{}, ErrOverflow
	}
	nTok := umin(sub(released, before), l.CreatorAlloc)
	members, err := splitRelease(nTok, l.SlotWeights)
	if err != nil {
		return AllocRelease{}, err
	}
	l.CreatorAlloc = sub(l.CreatorAlloc, nTok)
	l.AllocReleasesDone = k + 1
	l.AllocNextReleaseAt = 0
	if !isZ(l.CreatorAlloc) {
		l.AllocNextReleaseAt, _ = allocNextReleaseAt(k+1, l.allocReleaseCount(), now, l.Spec.AllocReleaseSecs)
	}
	return AllocRelease{ReleaseIndex: k, Tokens: nTok, Members: members, Released: sub(total, l.CreatorAlloc),
		NextReleaseAt: l.AllocNextReleaseAt}, nil
}

// LiveBuyPriceX64 is Launch::live_buy_price_x64: the price a buyer pays for the next token now — the books' price (in
// the window the window's ask; after the end the tail range's ask, TailAskX64), or the cheapest bin's ask if lower.
func (l *Launch) LiveBuyPriceX64() (U, error) {
	var next U
	var err error
	switch {
	case l.Ended:
		if next, err = l.TailAskX64(); err != nil {
			return zero, err
		}
	case !isZ(l.WindowLeft):
		// the window's ask with the backing line, quote-free (no discount)
		f, err := l.FloorX64()
		if err != nil {
			return zero, err
		}
		s, err := l.SOut()
		if err != nil {
			return zero, err
		}
		slot, err := l.slotSchedule()
		if err != nil {
			return zero, err
		}
		if next, err = l.windowAskX64(f, s, &Take{}, one, slot.U); err != nil {
			return zero, err
		}
	default:
		if next, err = l.St.PriceX64(); err != nil {
			return zero, err
		}
	}
	a, err := l.LotAskX64()
	if err != nil {
		return zero, err
	}
	if a != nil {
		return umin(*a, next), nil
	}
	return next, nil
}

// slotSchedule is Launch::slot_schedule: the schedule with the three Marketcap Milestones at the launch's FDV now.
func (l *Launch) slotSchedule() (Schedule, error) {
	r, err := l.ratesNow()
	if err != nil {
		return Schedule{}, err
	}
	return l.msSchedule(r.milestones())
}

// windowAskX64 is Launch::window_ask_x64: the window's ask for a buy (take0 on leg) — p₀ raised to the floor's line while
// the backing line stays under it (the window flat in normal flow); once that line binds, at least the lot's backing
// line (discounted only by the take that leaves the pots), so a buy split in pieces never gets more.
func (l *Launch) windowAskX64(f, sOut U, take0 *Take, leg, u2 U) (U, error) {
	wp, err := windowPriceX64(l.P0X64, f, u2)
	if err != nil {
		return zero, err
	}
	band := satAdd(l.Pots.Floor, l.Pots.Cushion)
	bl, err := backingLineX64(band, sOut, take0.Total, leg, u2)
	if err != nil {
		return zero, err
	}
	if !gt(bl, wp) {
		return wp, nil
	}
	ll, err := lotBackingLineX64(band, sOut, take0.Total, take0.Kept, leg, u2)
	if err != nil {
		return zero, err
	}
	return umax(wp, ll), nil
}

// LotAskX64 is Launch::lot_ask_x64: the lot's quote-free ask now, its cheapest bin's that asks; nil with none.
func (l *Launch) LotAskX64() (*U, error) {
	s, err := l.SOut()
	if err != nil {
		return nil, err
	}
	rb := l.rangeBidX64()
	for _, b := range l.Lot.InUse() {
		a, err := lotAskX64(&l.Pots, s, b.Cost, b.Tokens, l.Sch.U, zero, zero, one, rb)
		if err != nil {
			return nil, err
		}
		if a != nil {
			return a, nil
		}
	}
	return nil, nil
}

// cheapestBin is Launch::cheapest_bin: the first bin that asks for this buy (take0 on leg) and its ask; ok false with none.
func (l *Launch) cheapestBin(sOut, u U, take0 *Take, leg U) (int, U, bool, error) {
	rb := l.rangeBidX64()
	for i, b := range l.Lot.InUse() {
		a, err := lotAskX64(&l.Pots, sOut, b.Cost, b.Tokens, u, take0.Total, take0.Kept, leg, rb)
		if err != nil {
			return 0, zero, false, err
		}
		if a != nil {
			return i, *a, true, nil
		}
	}
	return 0, zero, false, nil
}

// rangeBidX64 is Launch::range_bid_x64: the tail range's price while it stands above its frozen price (a sell gets it
// from the range first), else 0 — a lower bound of every bin's ask.
func (l *Launch) rangeBidX64() U {
	if l.Ended && gt(l.TailSqrtX64, l.TailSqrtEndX64) {
		return l.TailPriceX64()
	}
	return zero
}

// TailAskX64 is Launch::tail_ask_x64: what the next token of the range costs a buyer (before the fees on top) — its
// price ÷ u′, raised to f on what it keeps (⌈f · 2^64 ÷ u′⌉) and the quote-free backing line; 0 before the end.
func (l *Launch) TailAskX64() (U, error) {
	if !l.Ended {
		return zero, nil
	}
	slot, err := l.slotSchedule()
	if err != nil {
		return zero, err
	}
	u := slot.U
	s, err := l.SOut()
	if err != nil {
		return zero, err
	}
	f, err := l.FloorX64()
	if err != nil {
		return zero, err
	}
	fLine := zero
	if !isZ(f) {
		fLine = divUpX64Sat(f, u)
	}
	line, err := backingLineX64(satAdd(l.Pots.Floor, l.Pots.Cushion), s, zero, one, u)
	if err != nil {
		return zero, err
	}
	// the curve's price is what the range keeps a token: a buyer pays it ÷ u′ (the Milestones inside the price)
	px := divUpX64Sat(l.TailPriceX64(), u)
	return umax(umax(px, fLine), line), nil
}

// divUpX64Sat is ⌈v · 2^64 ÷ max(d, 1)⌉, saturating at u128 max.
func divUpX64Sat(v, d U) U {
	r := divCeil(mul(v, one64), umax(d, one))
	if !fits(r) {
		return max128
	}
	return r
}

// TailPriceX64 is Launch::tail_price_x64: the tail range's price now (0 before the end).
func (l *Launch) TailPriceX64() U {
	if !l.Ended {
		return zero
	}
	return tailPriceX64(l.TailSqrtX64)
}

// TailOwed is Launch::tail_owed: what the tail range's curve owes a seller down to the frozen price.
func (l *Launch) TailOwed() (U, error) { return tailOwed(l.TailLiq, l.TailSqrtX64, l.TailSqrtEndX64) }

// SOut is S_out at the stored supply.
func (l *Launch) SOut() (U, error) { return sOutOf(l.Supply, l.Held()) }

// FloorX64 is the published floor F ÷ S_out.
func (l *Launch) FloorX64() (U, error) {
	s, err := l.SOut()
	if err != nil {
		return zero, err
	}
	return FloorX64(l.Pots.Floor, s)
}

// msSchedule is the schedule with the three Marketcap Milestones ms (u′ = u − team − platform − $FLOOR): what window
// and schedule money is kept at.
func (l *Launch) msSchedule(ms [3]uint16) (Schedule, error) {
	return l.Sch.WithSlotCut(slotCutQ(ms[0]+ms[1]+ms[2], ppm))
}

// curveLogs is launch::curve_logs: log2 of the row's curve span in quote base units (Q64.64) at the declared quote
// price — Zero if either end is under one base unit, PresetInvalid unless the END's is above the START's.
func curveLogs(p *PresetSpec, quoteUsdCentsX64 U, quoteDecimals uint8) ([2]U, error) {
	q := func(usd U) (U, error) {
		c, err := mulC(usd, n(100))
		if err != nil {
			return zero, err
		}
		v, err := UsdToQuote(c, quoteUsdCentsX64, quoteDecimals)
		if err != nil {
			return zero, err
		}
		lg, ok := log2X64(v)
		if !ok {
			return zero, ErrZero
		}
		return lg, nil
	}
	a, err := q(p.CurveFromUsd)
	if err != nil {
		return [2]U{}, err
	}
	b, err := q(p.CurveToUsd)
	if err != nil {
		return [2]U{}, err
	}
	if le(b, a) {
		return [2]U{}, ErrPresetInvalid
	}
	return [2]U{a, b}, nil
}

// curveTAt is Launch::curve_t_at: where the launch sits on its curves at the books' price, ppb of curveOne — the FDV
// ⌊price · supply ÷ 2^64⌋ (the stored live supply; saturating), log2 of it against CurveL.
func (l *Launch) curveTAt(priceX64 U) uint64 {
	fdv := umin(rsh(mul(priceX64, l.Supply), 64), max128)
	lg, ok := log2X64(fdv)
	if !ok {
		return 0
	}
	a, b := l.CurveL[0], l.CurveL[1]
	if le(lg, a) || le(b, a) {
		return 0
	}
	if ge(lg, b) {
		return curveOne
	}
	return u64(div(mul(sub(lg, a), n(curveOne)), sub(b, a)))
}

// curveT is Launch::curve_t: where the launch sits on its curves now — at the books' price, or after the schedule's end
// at the tail range's price (the books are frozen there).
func (l *Launch) curveT() (uint64, error) {
	if l.Ended {
		return l.curveTAt(l.TailPriceX64()), nil
	}
	p, err := l.St.PriceX64()
	if err != nil {
		return 0, err
	}
	return l.curveTAt(p), nil
}

// ratesAt is Launch::rates_at: the rates at curve position t, the Milestones capped at the launch's own bound.
func (l *Launch) ratesAt(t uint64) Rates { return ratesAtCapped(&l.Rates, t, l.MsLiveMaxBps) }

// ratesNow is Launch::rates_now: the launch's rates for a trade now (ratesAt at curveT).
func (l *Launch) ratesNow() (Rates, error) {
	t, err := l.curveT()
	if err != nil {
		return Rates{}, err
	}
	return l.ratesAt(t), nil
}

// scheduleFill is one keeperless schedule fill of the inventory alone (the lot sells first, outside it) at the
// Milestones' schedule slot: the fill, the tokens on offer and the room; ok false if it fails (a failing fill sells
// nothing and the rest is refunded: a buy never reverts on the curve's own state).
func (l *Launch) scheduleFill(slot *Schedule, rem, budget, sOut U) (f Fill, have, room U, ok bool) {
	have = l.Inventory
	room = umin(have, budget)
	f, ok, err := fillKeeperless(slot, &l.St, rem, room, add(l.Pots.Floor, l.Pots.Cushion), sOut, l.Pots.Floor, zero)
	if err != nil {
		ok = false
	}
	return
}

func (l *Launch) sync(live U) (U, error) {
	l.Supply = syncSupply(l.Supply, live)
	s, err := l.SOut()
	if err != nil {
		return zero, err
	}
	return l.sweep(s)
}

// sweep is Launch::sweep: with nothing outside, F + C and the tail range's money (it backs nobody then) go to the
// platform bucket. What was swept.
func (l *Launch) sweep(sOut U) (U, error) {
	p, swept, err := sweepIfEmpty(&l.Pots, sOut)
	if err != nil {
		return zero, err
	}
	l.Pots = p
	if isZ(sOut) && !isZ(l.TailMoney) {
		if swept, err = addC(swept, l.TailMoney); err != nil {
			return zero, err
		}
		l.TailMoney = zero
	}
	return swept, nil
}

func (l *Launch) room(st *State) (U, error) {
	c, p, err := cpTargets(&l.Sch, &l.Terms, st)
	if err != nil {
		return zero, err
	}
	return satSub(add(c, p), l.Pots.Cushion), nil
}

// Buy is Launch::buy: `leg` quote at slot / block `nowSlot`, the token's live supply `liveSupply`. Refuses nothing: what
// no stage takes is refunded (BuyReceipt.Refund).
func (l *Launch) Buy(leg U, nowSlot uint64, liveSupply U) (BuyReceipt, error) {
	since := uint64(0)
	if nowSlot > l.CreatedSlot {
		since = nowSlot - l.CreatedSlot
	}
	c, err := snipeCap(&l.Spec, l.TokenDecimals, since)
	if err != nil {
		return BuyReceipt{}, err
	}
	return l.buyCapped(leg, liveSupply, c)
}

// buyCapped is Launch::buy_capped: liveSupply and the receipt's token fields in chain units, capT in math units.
func (l *Launch) buyCapped(leg, liveSupply U, capT *U) (BuyReceipt, error) {
	k := l.scale()
	r, err := l.buyMath(leg, div(liveSupply, k), capT)
	if err != nil || eq(k, one) {
		return r, err
	}
	for _, v := range []*U{&r.Out, &r.WindowOut, &r.ScheduleOut, &r.LotOut, &r.FlatLotOut, &r.FlatTailOut} {
		if *v, err = mulC(*v, k); err != nil {
			return BuyReceipt{}, err
		}
	}
	return r, nil
}

// mathDecimals is launch::math_decimals: min(td, max(6, qd)).
func mathDecimals(td, qd uint8) uint8 {
	if qd < 6 {
		qd = 6
	}
	if td < qd {
		return td
	}
	return qd
}

func (l *Launch) scale() U { return umax(l.TokenScale, one) }

// chain is a math amount in chain units.
func (l *Launch) chain(v U) (U, error) { return mulC(v, l.scale()) }

func (l *Launch) buyMath(leg, liveSupply U, capT *U) (BuyReceipt, error) {
	r := BuyReceipt{Leg: leg}
	var err error
	if r.Swept, err = l.sync(liveSupply); err != nil {
		return BuyReceipt{}, err
	}
	// (F + C) / S_out past u128 (FloorX64 saturates): no token is priceable, the buy is refunded whole
	s0, err := l.SOut()
	if err != nil {
		return BuyReceipt{}, err
	}
	fc, err := FloorX64(add(l.Pots.Floor, l.Pots.Cushion), s0)
	if err != nil {
		return BuyReceipt{}, err
	}
	if eq(fc, max128) {
		r.Refund = leg
		return r, nil
	}
	// 1. the take on the whole leg at the books' price and f BEFORE the trade; nothing credited yet
	price0, err := l.St.PriceX64()
	if err != nil {
		return BuyReceipt{}, err
	}
	f0, err := l.FloorX64()
	if err != nil {
		return BuyReceipt{}, err
	}
	// one trade, one place on the curves: the fees and the Milestones at the FDV BEFORE the trade
	rates, err := l.ratesNow()
	if err != nil {
		return BuyReceipt{}, err
	}
	ms := rates.milestones()
	take0, err := take(&rates, leg, true, price0, f0)
	if err != nil {
		return BuyReceipt{}, err
	}
	rem := sub(leg, take0.Total)
	budget := max128
	if capT != nil {
		budget = *capT
	}
	sOut, err := l.SOut()
	if err != nil {
		return BuyReceipt{}, err
	}
	slot, err := l.msSchedule(ms)
	if err != nil {
		return BuyReceipt{}, err
	}
	u := l.Sch.U
	// the bid's top at what the schedule keeps (u′)
	uTop := slot.U
	if !l.Ended {
		bp, err := l.St.PriceX64()
		if err != nil {
			return BuyReceipt{}, err
		}
		bt, err := bidTopX64(bp, uTop)
		if err != nil {
			return BuyReceipt{}, err
		}
		if l.Pots, err = capTop(&l.Pots, sOut, bt); err != nil {
			return BuyReceipt{}, err
		}
	}
	windowWas := l.WindowLeft
	// once a buy sells from the lot, every later credit in it lifts the curve at most to the lot's own credit top (lotTop)
	var lotTop *U
	// 2. the window, flat at its ask: the lot's bins whose ask is under it sell FIRST (the cheapest first)
	if !isZ(l.WindowLeft) && !isZ(rem) && !isZ(budget) && !isZ(l.LotTokens) {
		f, err := FloorX64(l.Pots.Floor, sOut)
		if err != nil {
			return BuyReceipt{}, err
		}
		ask, err := l.windowAskX64(f, sOut, &take0, leg, slot.U)
		if err != nil {
			return BuyReceipt{}, err
		}
		if err := l.lotWalk(ask, nil, &rem, &budget, &sOut, &take0, leg, u, uTop, &lotTop, &r); err != nil {
			return BuyReceipt{}, err
		}
	}
	if !isZ(l.WindowLeft) && !isZ(rem) && !isZ(budget) {
		f, err := FloorX64(l.Pots.Floor, sOut)
		if err != nil {
			return BuyReceipt{}, err
		}
		ask, err := l.windowAskX64(f, sOut, &take0, leg, slot.U)
		if err != nil {
			return BuyReceipt{}, err
		}
		ff, err := flatFill(ask, rem, windowRoom(l.WindowLeft, &budget), slot.U, l.Pots.Floor, sOut)
		if err != nil {
			return BuyReceipt{}, err
		}
		if !isZ(ff.Out) {
			bp, err := l.St.PriceX64()
			if err != nil {
				return BuyReceipt{}, err
			}
			bt, err := bidTopX64(bp, uTop)
			if err != nil {
				return BuyReceipt{}, err
			}
			top, err := flatCreditTop(bt, ask, slot.U)
			if err != nil {
				return BuyReceipt{}, err
			}
			if lotTop != nil {
				top = umin(top, *lotTop)
			}
			rm, err := l.room(&l.St)
			if err != nil {
				return BuyReceipt{}, err
			}
			pots, err := credit(&l.Pots, sOut, ff.Out, ff.Kept, rm, top)
			if err != nil {
				return BuyReceipt{}, err
			}
			sl, fl, pl, ct, err := splitMilestones(ff.Pay, ff.Cut, ms)
			if err != nil {
				return BuyReceipt{}, err
			}
			r.MsFloor, r.MsPlatform = add(r.MsFloor, fl), add(r.MsPlatform, pl)
			l.Pots = pots
			l.WindowLeft = sub(l.WindowLeft, ff.Out)
			sOut = add(sOut, ff.Out)
			rem = sub(rem, ff.Pay)
			budget = sub(budget, ff.Out)
			r.WindowOut, r.WindowPay = ff.Out, ff.Pay
			r.FillCut, r.FillSlot = add(r.FillCut, ct), add(r.FillSlot, sl)
		}
	}
	if graduates(windowWas, r.WindowOut) {
		l.Graduated = true
		r.GraduatedNow = true
		if r.GraduationPriceX64, err = l.St.PriceX64(); err != nil {
			return BuyReceipt{}, err
		}
	}
	// 3.–5. the lot's bins under the books, the schedule, after the end the bins and the tail range. The take is CHARGED at
	// the cheaper of each curved buy fee's rate before the trade and where the curve's first fill ENDS (every curve is
	// monotone in the FDV, so no piece of a split pays under it), fixed ONCE inside that fill's own re-fill (charge.cheaper):
	// what it frees buys in the same re-fill as the Milestones' (never a second pass)
	ch := charge{base: rates, rates: rates, take: take0, leg: leg, price0: price0, f0: f0}
	if rem, _, sOut, err = l.curveStages(&take0, leg, &slot, ms, u, uTop, rem, budget, sOut, &lotTop, &ch, &r); err != nil {
		return BuyReceipt{}, err
	}
	// 6. fill-and-refund: the CHARGED take on the SPENT leg only
	net := sub(sub(leg, ch.take.Total), rem)
	spent, tk := leg, ch.take
	if !isZ(rem) {
		if spent, tk, err = legForNet(&ch.rates, net, true, price0, f0); err != nil {
			return BuyReceipt{}, err
		}
	}
	r.Spent, r.Refund, r.Take = spent, sub(leg, spent), tk
	r.Dust = sub(sub(spent, tk.Total), net)
	r.Out = add(add(add(r.WindowOut, r.LotOut), r.ScheduleOut), add(r.FlatLotOut, r.FlatTailOut))
	// a buy that gets no token is refunded in full and pays no fee
	if isZ(r.Out) {
		r.Spent, r.Refund, r.Take, r.Dust = zero, leg, Take{}, zero
		tk = Take{}
	}
	// 7. only now the kept skim, the rounding dust, the creator fee's unbought slot share and the members' dust to F
	if r.Routing, err = l.route(tk.Creator, r.FillSlot); err != nil {
		return BuyReceipt{}, err
	}
	if k := add(add(tk.Kept, r.Dust), add(r.Routing.CreatorBacking, r.Routing.SlotDust)); !isZ(k) {
		if l.Pots, err = credit(&l.Pots, sOut, zero, k, zero, zero); err != nil {
			return BuyReceipt{}, err
		}
	}
	return r, nil
}

// charge is a buy's take as CHARGED: base the rates before the trade; rates / take what is charged — base until the
// curve's first fill fixes it ONCE at the cheaper of base and the rates where that fill ends.
type charge struct {
	base, rates     Rates
	take            Take
	leg, price0, f0 U
}

// cheaper is the charge at the cheaper of base and end (cheaperBuy): ok when it is cheaper than what is charged now and
// not yet fixed.
func (c *charge) cheaper(end *Rates) (Rates, Take, bool, error) {
	if c.rates != c.base {
		return Rates{}, Take{}, false, nil
	}
	r := c.base.cheaperBuy(end)
	if r == c.base {
		return Rates{}, Take{}, false, nil
	}
	t, err := take(&r, c.leg, true, c.price0, c.f0)
	if err != nil {
		return Rates{}, Take{}, false, err
	}
	if !lt(t.Total, c.take.Total) {
		return Rates{}, Take{}, false, nil
	}
	return r, t, true, nil
}

// curveStages is steps 3–5 of a buy once the window is sold out: the lot's bins under the books, the schedule, then after
// the end the bins and the tail range. tk is the take every ask and line prices. Returns (rem, budget, sOut) after.
func (l *Launch) curveStages(tk *Take, leg U, slot *Schedule, ms [3]uint16, u, uTop, rem, budget, sOut U, lotTop **U, ch *charge, r *BuyReceipt) (U, U, U, error) {
	var err error
	// 3. the lot's bins, the cheapest first, each at its own ask while it is under the books' price (or under where the
	// schedule's fill of what is left would start)
	if isZ(l.WindowLeft) && !l.Ended && !isZ(rem) && !isZ(budget) && !isZ(l.LotTokens) {
		bp, err := l.St.PriceX64()
		if err != nil {
			return zero, zero, zero, err
		}
		if err := l.lotWalk(bp, slot, &rem, &budget, &sOut, tk, leg, u, uTop, lotTop, r); err != nil {
			return zero, zero, zero, err
		}
	}
	// 4. the schedule: its inventory alone (the lot waits until the books pass its ask)
	if isZ(l.WindowLeft) && !l.Ended && !isZ(rem) && !isZ(budget) {
		if rem, budget, sOut, err = l.scheduleStage(slot, ms, rem, budget, sOut, uTop, *lotTop, ch, r); err != nil {
			return zero, zero, zero, err
		}
	}
	// 5. after the end: the bins and the tail range, the cheaper first (the range never runs out)
	if l.Ended && !isZ(rem) && !isZ(budget) {
		if rem, budget, sOut, err = l.afterEnd(rem, budget, sOut, tk, leg, slot, uTop, ms, lotTop, ch, r); err != nil {
			return zero, zero, zero, err
		}
	}
	return rem, budget, sOut, nil
}

// scheduleStage is step 4 of a buy: the schedule's fill of the inventory alone. A fill takes the Milestones at the FDV
// it ENDS at, so one big buy across the rising curve never pays less than the same money in pieces: filled at the rate
// before the trade; if the end's rate differs, filled again ONCE from the same state at the end's (that fill is kept;
// if its schedule or fill fails, the first stands).
func (l *Launch) scheduleStage(slot *Schedule, ms [3]uint16, rem, budget, sOut, uTop U, lotTop *U, ch *charge, r *BuyReceipt) (U, U, U, error) {
	msS, uS := ms, slot.U
	f, have, room, ok := l.scheduleFill(slot, rem, budget, sOut)
	if ok && !isZ(f.Out) {
		ap, err := f.After.PriceX64()
		if err != nil {
			return zero, zero, zero, err
		}
		// the same ONE re-fill fixes the charged take at the cheaper of the rates before and at the end, and spends what
		// that frees
		rEnd := l.ratesAt(l.curveTAt(ap))
		msEnd := rEnd.milestones()
		cr, ct, has, err := ch.cheaper(&rEnd)
		if err != nil {
			return zero, zero, zero, err
		}
		freed := zero
		if has {
			freed = sub(ch.take.Total, ct.Total)
		}
		if msEnd != ms || !isZ(freed) {
			var at *Schedule
			if msEnd != ms {
				if se, err := l.msSchedule(msEnd); err == nil {
					at = &se
				}
			} else {
				at = slot
			}
			if at != nil {
				if g, hv, rm, ok2 := l.scheduleFill(at, add(rem, freed), budget, sOut); ok2 {
					f, have, room, msS, uS = g, hv, rm, msEnd, at.U
					if has {
						ch.rates, ch.take = cr, ct
						rem = add(rem, freed)
					}
				}
			}
		}
	}
	if !ok {
		return rem, budget, sOut, nil
	}
	if !isZ(f.Out) {
		l.Inventory = sub(l.Inventory, f.Out)
		rc, err := l.room(&f.After)
		if err != nil {
			return zero, zero, zero, err
		}
		ap, err := f.After.PriceX64()
		if err != nil {
			return zero, zero, zero, err
		}
		// the top at what THIS fill kept (u′ at the rate it was charged)
		top, err := bidTopX64(ap, umin(uS, uTop))
		if err != nil {
			return zero, zero, zero, err
		}
		if lotTop != nil {
			top = umin(top, *lotTop)
		}
		if l.Pots, err = credit(&l.Pots, sOut, f.Out, f.VaultIn, rc, top); err != nil {
			return zero, zero, zero, err
		}
		sl, fl, pl, ct, err := splitMilestones(f.Pay, f.Cut, msS)
		if err != nil {
			return zero, zero, zero, err
		}
		r.MsFloor, r.MsPlatform = add(r.MsFloor, fl), add(r.MsPlatform, pl)
		l.St = f.After
		sOut = add(sOut, f.Out)
		rem = sub(rem, f.Pay)
		budget = sub(budget, f.Out)
		r.ScheduleOut, r.SchedulePay = add(r.ScheduleOut, f.Out), add(r.SchedulePay, f.Pay)
		r.FillCut, r.FillSlot = add(r.FillCut, ct), add(r.FillSlot, sl)
	}
	// the inventory bound the fill (not the snipe cap): the schedule is DONE
	if (f.Ended && eq(room, have)) || isZ(l.Inventory) {
		if err := l.end(); err != nil {
			return zero, zero, zero, err
		}
		r.EndedNow = true
	}
	return rem, budget, sOut, nil
}

// afterEnd is step 5 of a buy, after the end: the cheapest bin when its ask is at or under the range's price as a buy
// pays it (its curve price raised to f ÷ u′ and the backing line), else the tail range stopped at that bin's ask — and
// when the range cannot move under that cap, the bin sells at its ask (a bin that waits never freezes a buy); a bin that
// sells nothing hands over to the range uncapped.
func (l *Launch) afterEnd(rem, budget, sOut U, take0 *Take, leg U, slot *Schedule, uTop U, ms [3]uint16, lotTop **U, ch *charge, r *BuyReceipt) (U, U, U, error) {
	u := l.Sch.U
	fail := func(err error) (U, U, U, error) { return zero, zero, zero, err }
	rangeBuyAt := func(offer, uk, sqrtCap U) (TailBuy, error) {
		line, err := backingLineX64(satAdd(l.Pots.Floor, l.Pots.Cushion), sOut, take0.Total, leg, slot.U)
		if err != nil {
			return TailBuy{}, err
		}
		return tailBuy(l.TailLiq, l.TailSqrtX64, l.Tail, offer, uk, l.Pots.Floor, sOut, line, sqrtCap, budget)
	}
	for round := 0; round < 2*LotBins+4; round++ {
		if isZ(rem) || isZ(budget) {
			break
		}
		// the range's price as a buy PAYS it: its curve's, raised to f ÷ u′ and the backing line (tailBuy's least prices)
		k7 := divUpX64Sat(fCeilX64(l.Pots.Floor, sOut), slot.U)
		line, err := backingLineX64(satAdd(l.Pots.Floor, l.Pots.Cushion), sOut, take0.Total, leg, slot.U)
		if err != nil {
			return fail(err)
		}
		tailPx := umax(umax(l.TailPriceX64(), k7), line)
		bin, ask, has, err := l.cheapestBin(sOut, u, take0, leg)
		if err != nil {
			return fail(err)
		}
		var tb TailBuy
		tbCap := max128
		lotNow := false
		switch {
		case has && le(ask, tailPx):
			lotNow = true
		case has:
			tbCap = sqrtX64(ask)
			if tb, err = rangeBuyAt(rem, slot.U, tbCap); err != nil {
				return fail(err)
			}
			lotNow = isZ(tb.Out)
		default:
			if tb, err = rangeBuyAt(rem, slot.U, max128); err != nil {
				return fail(err)
			}
		}
		if lotNow {
			pay, out, err := l.lotSale(bin, ask, rem, budget, sOut, u, uTop, lotTop, r)
			if err != nil {
				return fail(err)
			}
			if !isZ(out) {
				sOut = add(sOut, out)
				rem = sub(rem, pay)
				budget = sub(budget, out)
				r.FlatLotOut = add(r.FlatLotOut, out)
				r.FlatPay = add(r.FlatPay, pay)
				continue
			}
			// the bin sold nothing (its flat rounding): the range, uncapped
			tbCap = max128
			if tb, err = rangeBuyAt(rem, slot.U, max128); err != nil {
				return fail(err)
			}
		}
		// the three Marketcap Milestones apply to range buys, inside the price, at the FDV the step ENDS at: filled at this
		// trade's rate, then once again at the end's if it differs (the same cap; that one kept if it buys anything); that
		// re-fill also fixes the charged take (charge.cheaper, once a buy) and spends what it frees
		msT := ms
		if !isZ(tb.Out) {
			rEnd := l.ratesAt(l.curveTAt(priceOfSqrt(tb.SqrtAfter)))
			msEnd := rEnd.milestones()
			cr, ct, has, err := ch.cheaper(&rEnd)
			if err != nil {
				return fail(err)
			}
			freed := zero
			if has {
				freed = sub(ch.take.Total, ct.Total)
			}
			if msEnd != ms || !isZ(freed) {
				var uEnd *U
				if msEnd != ms {
					if se, err := l.msSchedule(msEnd); err == nil {
						uEnd = &se.U
					}
				} else {
					uEnd = &slot.U
				}
				if uEnd != nil {
					if g, err := rangeBuyAt(add(rem, freed), *uEnd, tbCap); err == nil && !isZ(g.Out) {
						tb, msT = g, msEnd
						if has {
							ch.rates, ch.take = cr, ct
							rem = add(rem, freed)
						}
					}
				}
			}
		}
		if isZ(tb.Out) {
			break
		}
		// floor first, the rest is the range's money; the curve moved by what it kept
		if l.Pots.Floor, err = addC(l.Pots.Floor, tb.ToFloor); err != nil {
			return fail(err)
		}
		if l.TailMoney, err = addC(l.TailMoney, tb.ToMoney); err != nil {
			return fail(err)
		}
		l.TailSqrtX64 = tb.SqrtAfter
		l.Tail = sub(l.Tail, tb.Out)
		sl, fl, pl, ct, err := splitMilestones(tb.Pay, tb.Cut, msT)
		if err != nil {
			return fail(err)
		}
		r.MsFloor, r.MsPlatform = add(r.MsFloor, fl), add(r.MsPlatform, pl)
		sOut = add(sOut, tb.Out)
		rem = sub(rem, tb.Pay)
		budget = sub(budget, tb.Out)
		r.FlatTailOut = add(r.FlatTailOut, tb.Out)
		r.FlatPay = add(r.FlatPay, tb.Pay)
		r.TailPay = add(r.TailPay, tb.Pay)
		r.TailToFloor = add(r.TailToFloor, tb.ToFloor)
		r.FillCut, r.FillSlot = add(r.FillCut, ct), add(r.FillSlot, sl)
	}
	return rem, budget, sOut, nil
}

// lotWalk is Launch::lot_walk: the lot's bins, the cheapest first, each at its own ask while that is under capX64 — with
// sched (the Milestones' schedule, before the end) also while it is under the price the schedule's fill of what is left
// would start at (the books raised to the backing, and f ÷ u′; read once, the first time a bin reaches capX64; a fill
// that cannot price: every bin that asks may sell). Its tokens and pay go to LotOut / LotPay.
func (l *Launch) lotWalk(capX64 U, sched *Schedule, rem, budget, sOut *U, tk *Take, leg, u, uTop U, lotTop **U, r *BuyReceipt) error {
	var start *U
	for round := 0; round < LotBins; round++ {
		if isZ(*rem) || isZ(*budget) {
			break
		}
		bin, ask, has, err := l.cheapestBin(*sOut, u, tk, leg)
		if err != nil {
			return err
		}
		if !has {
			break
		}
		if ge(ask, capX64) {
			if sched == nil {
				break
			}
			if start == nil {
				p := max128
				if g, _, ok, err := raiseToBackingM(sched, &l.St, satAdd(l.Pots.Floor, l.Pots.Cushion), *sOut, *rem, one); err == nil && ok {
					if v, err := g.PriceX64(); err == nil {
						p = v
					}
				}
				// and the fill's least price f ÷ u′ (a fill sells no token for less)
				p = umax(p, divUpX64Sat(fCeilX64(l.Pots.Floor, *sOut), sched.U))
				start = &p
			}
			if ge(ask, *start) {
				break
			}
		}
		pay, out, err := l.lotSale(bin, ask, *rem, *budget, *sOut, u, uTop, lotTop, r)
		if err != nil {
			return err
		}
		if isZ(out) {
			break
		}
		*sOut = add(*sOut, out)
		*rem = sub(*rem, pay)
		*budget = sub(*budget, out)
		r.LotOut = add(r.LotOut, out)
		r.LotPay = add(r.LotPay, pay)
	}
	return nil
}

// route is Launch::route: the creator fee to its referrers (900 / 90 / 10 bps), then with a Team Slot split_creator (the
// bought share to the slot, the rest backing), else to the creator; the slot's take (the fills' + the creator share)
// split to the members, the dust to F.
func (l *Launch) route(creator, fillSlot U) (Routing, error) {
	var g Routing
	refs, rest, err := creatorLegs(creator, l.CreatorRefs)
	if err != nil {
		return g, err
	}
	g.CreatorRefs = refs
	if l.SlotRateBps > 0 {
		if g.CreatorToSlot, g.CreatorBacking, err = splitCreator(rest, l.SlotBoughtPpm); err != nil {
			return g, err
		}
		if g.SlotMembers, g.SlotDust, err = splitMembers(add(fillSlot, g.CreatorToSlot), l.SlotWeights); err != nil {
			return g, err
		}
	} else {
		// with no Team Slot the Team Marketcap Milestone is the creator's, as a slot with no shares pays place 0
		g.ToCreator = add(rest, fillSlot)
	}
	return g, nil
}

// end: the schedule is done — its books freeze, the inventory it did not sell joins the tail, and the tail opens as ONE
// range from the frozen price to an infinite price (its L fixed now).
func (l *Launch) end() error {
	l.Ended = true
	l.Tail = add(l.Tail, l.Inventory)
	l.Inventory = zero
	frozen, err := l.St.PriceX64()
	if err != nil {
		return err
	}
	liq, se, err := tailOpen(l.Tail, frozen)
	if err != nil {
		return err
	}
	l.TailLiq, l.TailSqrtX64, l.TailSqrtEndX64 = liq, se, se
	return nil
}

// lotSale is Launch::lot_sale: bin's flat sale at ask — floor first, its cost home to the cushion (the schedule's room at
// least) while the curve is live, nothing into a dead curve; its credit top lotCreditTop, never over an earlier lot
// sale's in the same buy (*lotTop is set to it). No slot cut (u). Returns (pay, out); the caller books the tokens.
func (l *Launch) lotSale(bin int, ask, rem, budget, sOut, u, uTop U, lotTop **U, r *BuyReceipt) (U, U, error) {
	ff, err := flatFill(ask, rem, umin(l.Lot.Bins[bin].Tokens, budget), u, l.Pots.Floor, sOut)
	if err != nil {
		return zero, zero, err
	}
	if isZ(ff.Out) {
		return zero, zero, nil
	}
	cost, err := l.Lot.take(bin, ff.Out)
	if err != nil {
		return zero, zero, err
	}
	bp, err := l.St.PriceX64()
	if err != nil {
		return zero, zero, err
	}
	bt, err := bidTopX64(bp, uTop)
	if err != nil {
		return zero, zero, err
	}
	top, err := lotCreditTop(bt, ask, u)
	if err != nil {
		return zero, zero, err
	}
	if *lotTop != nil {
		top = umin(top, **lotTop)
	}
	t := top
	*lotTop = &t
	live, err := curveLive(&l.Pots, sOut)
	if err != nil {
		return zero, zero, err
	}
	rc := zero
	if live {
		r0, err := l.room(&l.St)
		if err != nil {
			return zero, zero, err
		}
		rc = umax(r0, cost)
	}
	if l.Pots, err = credit(&l.Pots, sOut, ff.Out, ff.Kept, rc, top); err != nil {
		return zero, zero, err
	}
	l.LotTokens = sub(l.LotTokens, ff.Out)
	l.LotCost = sub(l.LotCost, cost)
	r.FillCut = add(r.FillCut, ff.Cut)
	return ff.Pay, ff.Out, nil
}

// Sell is Launch::sell: `tokens` of the seller's, the live supply `liveSupply` (chain units; whole math units are sold).
func (l *Launch) Sell(tokens, liveSupply U) (SellReceipt, error) {
	k := l.scale()
	r, err := l.sellMath(div(tokens, k), div(liveSupply, k))
	if err != nil {
		return r, err
	}
	if r.Tokens, err = mulC(r.Tokens, k); err != nil {
		return SellReceipt{}, err
	}
	// the range's tokens in chain units too
	if r.TailTokens, err = mulC(r.TailTokens, k); err != nil {
		return SellReceipt{}, err
	}
	return r, nil
}

func (l *Launch) sellMath(tokens, liveSupply U) (SellReceipt, error) {
	r := SellReceipt{Tokens: tokens}
	var err error
	if r.Swept, err = l.sync(liveSupply); err != nil {
		return SellReceipt{}, err
	}
	sOut, err := l.SOut()
	if err != nil {
		return SellReceipt{}, err
	}
	if gt(tokens, sOut) {
		return SellReceipt{}, ErrNotOutside
	}
	// past u128 the floor saturates (it only picks the at-floor skim)
	f0, err := FloorX64(l.Pots.Floor, sOut)
	if err != nil {
		return SellReceipt{}, err
	}
	// after the end the tail range takes the tokens back first, down its curve to the frozen price: f a token from F, the
	// rest from the range's money; what is left goes to the cushion and the floor
	ts := TailSell{}
	if l.Ended {
		if ts, err = tailSell(l.TailLiq, l.TailSqrtX64, l.TailSqrtEndX64, l.TailMoney, tokens, l.Pots.Floor, sOut); err != nil {
			return SellReceipt{}, err
		}
	}
	pots := l.Pots
	pots.Floor = sub(pots.Floor, ts.FromFloor)
	fl, err := bidSell(&pots, sub(sOut, ts.Tokens), sub(tokens, ts.Tokens))
	if err != nil {
		return SellReceipt{}, err
	}
	gross := add(ts.Gross, fl.QuoteOut)
	// the skim ramps on the price the sell starts at: the cushion's top while it has money, else the floor itself
	pStart := f0
	if !isZ(l.Pots.Cushion) {
		pStart = umax(priceOfSqrt(l.Pots.BidSqrtX64), f0)
	}
	// the sell's fees at the FDV before it (a sell never moves the books; after the end, the range's price); no Milestones
	sr, err := l.ratesNow()
	if err != nil {
		return SellReceipt{}, err
	}
	tk, err := take(&sr, gross, false, pStart, f0)
	if err != nil {
		return SellReceipt{}, err
	}
	sAfter := sub(sOut, tokens)
	// the kept skim backs only the holders who stay (kept · sAfter ÷ sOut); the seller's own share to the platform bucket
	keptStay, err := toU128(div(mul(tk.Kept, sAfter), umax(sOut, one)))
	if err != nil {
		return SellReceipt{}, err
	}
	r.KeptToPlatform = sub(tk.Kept, keptStay)
	tk.Kept = keptStay
	l.Pots = fl.After
	if !isZ(ts.Tokens) {
		l.Tail = add(l.Tail, ts.Tokens)
		l.TailMoney = sub(l.TailMoney, ts.FromMoney)
		l.TailSqrtX64 = ts.SqrtAfter
	}
	l.LotTokens = add(l.LotTokens, add(fl.CushionTokens, fl.FloorTokens))
	l.LotCost = add(l.LotCost, add(fl.FromCushion, fl.FromFloor))
	// each token by what IT cost
	if err := l.Lot.addSell(&fl); err != nil {
		return SellReceipt{}, err
	}
	if r.Routing, err = l.route(tk.Creator, zero); err != nil {
		return SellReceipt{}, err
	}
	if k := add(tk.Kept, add(r.Routing.CreatorBacking, r.Routing.SlotDust)); !isZ(k) {
		if l.Pots, err = credit(&l.Pots, sAfter, zero, k, zero, zero); err != nil {
			return SellReceipt{}, err
		}
	}
	swept, err := l.sweep(sAfter)
	if err != nil {
		return SellReceipt{}, err
	}
	r.Swept = add(r.Swept, swept)
	r.Gross, r.FromCushion, r.FromFloor = gross, fl.FromCushion, fl.FromFloor
	r.TailTokens, r.TailFromFloor, r.TailFromMoney = ts.Tokens, ts.FromFloor, ts.FromMoney
	r.Take = tk
	r.Payout = sub(gross, tk.Total)
	return r, nil
}

// Burn is Launch::burn: the new live supply after a holder burns `tokens`.
func (l *Launch) Burn(liveSupply, tokens U) (U, error) {
	k := l.scale()
	o, err := sOutOf(umin(div(liveSupply, k), l.Supply), l.Held())
	if err != nil {
		return zero, err
	}
	outside, err := l.chain(o)
	if err != nil {
		return zero, err
	}
	outside = add(outside, mod(liveSupply, k))
	if gt(tokens, outside) {
		return zero, ErrNotOutside
	}
	return sub(liveSupply, tokens), nil
}

// OnChain is a launch's state as the contract's views report it (the tracker's reads), in IFloorAmm v1.3a's units:
// launchState's token amounts (Supply, WindowLeft, Inventory, LotTokens, Tail) and creatorAlloc's tokens are CHAIN units
// (math × scale) and its P0X64 is per CHAIN base unit (math ÷ scale, floored); launchMarket's Books, BidSqrtX64, BidLiq
// and Schedule are MATH units, as the reference trade stores them. Quote amounts never scale.
type OnChain struct {
	Spec                               PresetSpec
	Schedule                           Schedule
	Books                              State
	P0X64                              U
	Raise                              U // launchState's raise: P0X64 is recomputed from it on a scaled launch
	Floor, Cushion, BidSqrtX64, BidLiq U
	Supply, WindowLeft, Inventory      U
	LotTokens, LotCost, Tail           U
	CreatorAlloc, CreatorUnlockMcap    U
	CreatorUnlockBacking               U
	// AllocReleasesDone and AllocNextReleaseAt are tokenOptionState's (releases done, the next one's time).
	AllocReleasesDone  uint8
	AllocNextReleaseAt uint64
	// QuoteUsdCentsX64 is the quote's declared price at create (launchQuoteInfo): the curves' span is fixed from it.
	QuoteUsdCentsX64 U
	// MsLiveMaxBps is the launch's bound on the three Marketcap Milestones together; 0 (a contract with no view of it)
	// reads as MilestonesLiveMaxBps, every launch Create opens.
	MsLiveMaxBps     uint16
	Ended, Graduated bool
	SlotRateBps      uint16
	SlotBoughtPpm    uint32
	SlotWeights      [MaxMembers]uint32
	CreatorRefs      [3]bool // the creator's paid referrer tiers
	CreatedBlock     uint64
	// TokenDecimals and QuoteDecimals are the chain's: Restore derives the math decimals and the scale from them.
	TokenDecimals, QuoteDecimals uint8
	// The tail range (math units): L, √p · 2^64 now and at the frozen price, the quote it holds; all 0 before the end.
	TailLiq, TailSqrtX64, TailSqrtEndX64, TailMoney U
	// LotBins are the bought-back lot's bins (key, tokens in MATH units, cost), the cheapest first; their totals must be
	// LotTokens ÷ scale and LotCost. None (a contract with no view of them): an empty lot — a launch whose lot holds
	// tokens then quotes as if every bin waited (the lot sells nothing in the quote).
	LotBins []LotBin
}

// ErrStaleLot is Restore's refusal of lot bins whose totals are not the launch's lot (stale or malformed).
var ErrStaleLot = errors.New("lot bins do not match the launch's lot")

// Restore rebuilds the reference trade's Launch from the contract's state. Terms are the row's: the trade path reads
// only their cushion / POL shares (cp_targets' split_cp), and the schedule itself is the stored one. On a scaled launch
// (scale > 1) the chain-unit token amounts are divided back to math units (exact: the contract reports math × scale) and
// p0 is recomputed as the open does (open_price_x64(raise, the sale in math units)): the per-chain-unit view price is
// floored and cannot be inverted.
func Restore(c *OnChain) (Launch, error) {
	md := mathDecimals(c.TokenDecimals, c.QuoteDecimals)
	scale, err := unit(c.TokenDecimals - md)
	if err != nil {
		return Launch{}, err
	}
	p0 := c.P0X64
	toMath := func(v U) U { return v }
	if gt(scale, one) {
		sale, err := saleTokens(&c.Spec, md)
		if err != nil {
			return Launch{}, err
		}
		if p0, err = openPriceX64(c.Raise, sale); err != nil {
			return Launch{}, err
		}
		toMath = func(v U) U { return div(v, scale) }
	}
	// the fee and Milestone curves' span, as create fixed it (a flat row's curves are never read: no span needed)
	msMax := c.MsLiveMaxBps
	if msMax == 0 {
		msMax = MilestonesLiveMaxBps
	}
	lot, err := lotOf(c.LotBins, toMath(c.LotTokens), c.LotCost)
	if err != nil {
		return Launch{}, err
	}
	var curveL [2]U
	if !c.Spec.curvesFlat() {
		if curveL, err = curveLogs(&c.Spec, c.QuoteUsdCentsX64, c.QuoteDecimals); err != nil {
			return Launch{}, err
		}
	}
	return Launch{
		Spec: c.Spec, Rates: RatesOf(&c.Spec), Terms: Terms{CushionBps: c.Spec.CushionBps, PolBps: c.Spec.PolBps},
		Sch: c.Schedule, St: c.Books, P0X64: p0,
		Pots:   Pots{Floor: c.Floor, Cushion: c.Cushion, BidSqrtX64: c.BidSqrtX64, Liq: c.BidLiq},
		Supply: toMath(c.Supply), WindowLeft: toMath(c.WindowLeft), Inventory: toMath(c.Inventory),
		LotTokens: toMath(c.LotTokens), LotCost: c.LotCost, Tail: toMath(c.Tail), CreatorAlloc: toMath(c.CreatorAlloc),
		CreatorUnlockMcap: c.CreatorUnlockMcap, CreatorUnlockBacking: c.CreatorUnlockBacking,
		AllocReleasesDone: c.AllocReleasesDone, AllocNextReleaseAt: c.AllocNextReleaseAt, CurveL: curveL, MsLiveMaxBps: msMax, Ended: c.Ended,
		Graduated: c.Graduated, SlotRateBps: c.SlotRateBps, SlotBoughtPpm: c.SlotBoughtPpm,
		SlotWeights: c.SlotWeights, CreatorRefs: c.CreatorRefs, CreatedSlot: c.CreatedBlock, TokenDecimals: md, TokenScale: scale,
		TailLiq: c.TailLiq, TailSqrtX64: c.TailSqrtX64, TailSqrtEndX64: c.TailSqrtEndX64, TailMoney: c.TailMoney, Lot: lot,
	}, nil
}

// lotOf is the lot of the given bins, checked: at most LotBins, keys strictly rising, no empty bin, totals the launch's
// lot (tokens, cost). No bins: an empty lot.
func lotOf(bins []LotBin, tokens, cost U) (Lot, error) {
	var l Lot
	if len(bins) == 0 {
		return l, nil
	}
	if len(bins) > LotBins {
		return l, ErrStaleLot
	}
	for i, b := range bins {
		if isZ(b.Tokens) || (i > 0 && b.Key <= bins[i-1].Key) {
			return l, ErrStaleLot
		}
		l.Bins[i] = b
	}
	l.Len = uint8(len(bins))
	if t, c := l.totals(); !eq(t, tokens) || !eq(c, cost) {
		return Lot{}, ErrStaleLot
	}
	return l, nil
}

// InSnipeSpan is whether a buy at slot / block `now` is inside the snipe span (where each buy call is
// capped).
func (l *Launch) InSnipeSpan(now uint64) bool {
	since := uint64(0)
	if now > l.CreatedSlot {
		since = now - l.CreatedSlot
	}
	c, err := snipeCap(&l.Spec, l.TokenDecimals, since)
	return err == nil && c != nil
}
