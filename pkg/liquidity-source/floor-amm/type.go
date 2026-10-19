package flooramm

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/holiman/uint256"

	floormath "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/floor-amm/math"
)

// StaticExtra: what a launch fixes for life.
type StaticExtra struct {
	FloorAmm string `json:"floorAmm"`
	LaunchID uint64 `json:"launchId"`
	// IsNative: the quote is the native coin (`NATIVE = address(0)`, msg.value); Tokens[0] is then the wrapped native.
	IsNative bool `json:"isNative"`
	// Spec is the preset row copied at create (PresetSpec::to_bytes, 486 B, hex; an earlier launch's row padded).
	Spec string `json:"spec"`
	// Schedule is the launch's precomputed schedule (Schedule::to_bytes, 664 B, hex), decoded from launchMarket's.
	Schedule string `json:"schedule"`
	// QuoteUsdCentsX64 is the quote's declared price at create (launchQuoteInfo): the fee and Marketcap Milestone
	// curves' span is fixed from it.
	QuoteUsdCentsX64 *uint256.Int `json:"quoteUsdCentsX64,omitempty"`
}

// Extra: the launch's market at the tracked block, in the views' units (IFloorAmm v1.3a): launchState's token amounts
// in chain units and p0 per chain unit, launchMarket's books and curve in math units; Restore converts.
type Extra struct {
	S             *uint256.Int `json:"s"`
	H             *uint256.Int `json:"h"`
	L             *uint256.Int `json:"l"`
	CurveFloorX64 *uint256.Int `json:"curveFloorX64"`
	P0X64         *uint256.Int `json:"p0X64"`
	Raise         *uint256.Int `json:"raise,omitempty"` // a scaled launch's p0 is recomputed from it
	Floor         *uint256.Int `json:"floor"`
	Cushion       *uint256.Int `json:"cushion"`
	BidSqrtX64    *uint256.Int `json:"bidSqrtX64"`
	BidLiq        *uint256.Int `json:"bidLiq"`
	Supply        *uint256.Int `json:"supply"`     // the stored supply (only falls)
	LiveSupply    *uint256.Int `json:"liveSupply"` // totalSupply() − balanceOf(dead) (burns count)
	WindowLeft    *uint256.Int `json:"windowLeft"`
	Inventory     *uint256.Int `json:"inventory"`
	LotTokens     *uint256.Int `json:"lotTokens"`
	LotCost       *uint256.Int `json:"lotCost"`
	Tail          *uint256.Int `json:"tail"`
	// CreatorAlloc is what is still held of the creator's free allocation (creatorAlloc's tokens: the releases not yet
	// done); CreatorUnlockMcap and CreatorUnlockBacking are its unlock's marks, AllocReleasesDone and
	// AllocNextReleaseAt its releases' (tokenOptionState). No trade reads them.
	CreatorAlloc         *uint256.Int `json:"creatorAlloc,omitempty"`
	CreatorUnlockMcap    *uint256.Int `json:"creatorUnlockMcap,omitempty"`
	CreatorUnlockBacking *uint256.Int `json:"creatorUnlockBacking,omitempty"`
	// AllocReleasesDone and AllocNextReleaseAt are the Token Option's releases so far and the next one's time
	// (tokenOptionState). No quote reads them (a release is not a swap).
	AllocReleasesDone  uint8  `json:"allocReleasesDone,omitempty"`
	AllocNextReleaseAt uint64 `json:"allocNextReleaseAt,omitempty"`
	Ended              bool   `json:"ended"`
	Graduated          bool   `json:"graduated"`
	SlotRateBps        uint16 `json:"slotRateBps"`
	SlotBoughtPpm      uint32 `json:"slotBoughtPpm"`
	// The members' weights and the creator's paid referrer tiers (the creator fee's routing). The interface has no view
	// of them: the tracker leaves them zero, which moves only the post-trade F of a Team Slot / referred
	// launch (the backing and the members' dust), never a quote's out; the tracker re-reads the state after every trade.
	SlotWeights  [floormath.MaxMembers]uint32 `json:"slotWeights"`
	CreatorRefs  [3]bool                      `json:"creatorRefs"`
	CreatedBlock uint64                       `json:"createdBlock"`
	// The tail range after the schedule's end (launchTail, math units): its liquidity, √p · 2^64 now and at the frozen
	// price, and the quote it holds. Absent before the end, or on a contract without the view.
	TailLiq        *uint256.Int `json:"tailLiq,omitempty"`
	TailSqrtX64    *uint256.Int `json:"tailSqrtX64,omitempty"`
	TailSqrtEndX64 *uint256.Int `json:"tailSqrtEndX64,omitempty"`
	TailMoney      *uint256.Int `json:"tailMoney,omitempty"`
	// MsLiveMaxBps is the launch's bound on the three Marketcap Milestones together (launchMsLiveMaxBps). Absent (0) on a
	// contract without the view: the bound every created launch carries.
	MsLiveMaxBps uint16 `json:"msLiveMaxBps,omitempty"`
	// LotBins are the bought-back lot's bins (key, tokens in MATH units, cost), the cheapest first; their totals must be
	// LotTokens (in math units) and LotCost, else the snapshot is refused. The contract has no view of them yet, so the
	// tracker leaves them absent: a launch whose lot holds tokens then quotes as if every bin waited (the lot sells
	// nothing in the quote, its buys go to the schedule or the range).
	LotBins []LotBinExtra `json:"lotBins,omitempty"`
}

// LotBinExtra is one bin of the bought-back lot: its grid key, the tokens it holds (math units) and what they cost.
type LotBinExtra struct {
	Key    uint32       `json:"key"`
	Tokens *uint256.Int `json:"tokens"`
	Cost   *uint256.Int `json:"cost"`
}

// SwapInfo: the trade's receipt and the launch after it (UpdateBalance consumes it, never recomputes).
type SwapInfo struct {
	IsBuy    bool   `json:"isBuy"`
	LaunchID uint64 `json:"launchId"`
	FloorAmm string `json:"floorAmm"`
	// Refund: the part of a buy no stage took (fill-and-refund; an ERC-20 quote is never pulled for it).
	Refund       string `json:"refund,omitempty"`
	GraduatedNow bool   `json:"graduatedNow,omitempty"`
	EndedNow     bool   `json:"endedNow,omitempty"`

	after *floormath.Launch
}

// MetaInfo: what the executor needs.
type MetaInfo struct {
	BlockNumber uint64 `json:"blockNumber"`
	FloorAmm    string `json:"floorAmm"`
	LaunchID    uint64 `json:"launchId"`
	IsNative    bool   `json:"isNative"`
}

// launchView is IFloorAmm.LaunchView as go-ethereum decodes it.
type launchView struct {
	Token           common.Address
	Quote           common.Address
	Creator         common.Address
	PresetId        uint8
	PresetVersion   uint32
	PresetHash      [32]byte
	Graduated       bool
	Ended           bool
	CreatedAt       uint64
	CreatedBlock    uint64
	CreatorUnlockAt uint64
	GraduatedBlock  uint64
	Supply          *big.Int
	SOut            *big.Int
	WindowLeft      *big.Int
	Inventory       *big.Int
	LotTokens       *big.Int
	LotCost         *big.Int
	TailTokens      *big.Int
	CreatorLocked   *big.Int
	Floor           *big.Int
	Cushion         *big.Int
	P0X64           *big.Int
	PriceX64        *big.Int
	FloorX64        *big.Int
	Raise           *big.Int
	WindowTokens    *big.Int
	WindowRaised    *big.Int
	SlotRateBps     uint16
	SlotBoughtPpm   uint32
}

// creatorAlloc is creatorAlloc's outputs: what is still held of the creator's free allocation and its unlock's marks.
type creatorAlloc struct {
	Tokens        *big.Int
	UnlockMcap    *big.Int
	UnlockBacking *big.Int
	Unlockable    bool
	Unlocked      bool
}

// tokenOptionState is IFloorAmm.TokenOptionState: the allocation's releases (tokens in chain units).
type tokenOptionState struct {
	Total             *big.Int
	Released          *big.Int
	ReleasesDone      uint8
	ReleaseCount      uint8
	NextReleaseIndex  uint8
	NextReleaseAt     *big.Int
	NextReleaseTokens *big.Int
	LinesMet          bool
	Due               bool
}

// launchMarket is launchMarket's outputs: the books (s, h, l, curveFloorX64), the cushion curve and the schedule.
type launchMarket struct {
	Book       [4]*big.Int
	BidSqrtX64 *big.Int
	BidLiq     *big.Int
	Schedule   []byte
}

// launchTail is launchTail's outputs: the tail range after the schedule's end (math units; all 0 before it). PriceX64
// is derived from SqrtX64 and not stored.
type launchTail struct {
	Liq        *big.Int
	SqrtX64    *big.Int
	SqrtEndX64 *big.Int
	Money      *big.Int
	PriceX64   *big.Int
}

// launchQuoteInfo is launchQuoteInfo's outputs: whether the launch's quote was listed at create, and the USD price
// (cents a whole quote, Q64.64) its raise used.
type launchQuoteInfo struct {
	Listed           bool
	QuoteUsdCentsX64 *big.Int
}

// quoteEntry is the registry's QuoteEntry (quoteEntry(token)).
type quoteEntry struct {
	Listed      bool
	Enabled     bool
	Decimals    uint8
	CreateFee   *big.Int
	UsdCentsX64 *big.Int
	Launches    uint64
	UpdatedAt   uint64
}

// quoteEntryOut wraps quoteEntry's one tuple output (go-ethereum copies a single output into a struct's first field).
type quoteEntryOut struct {
	Entry quoteEntry
}

// quoteVetted is the lister's policy: a launch is listed as a pool only when its quote is in the registry, at create
// (info.Listed) or now (entry.Listed). An unlisted quote is any ERC-20 its creator priced; nothing has vetted its
// transfer behaviour, so such a launch is not routed unless routeUnlisted is set.
func quoteVetted(info launchQuoteInfo, entry *quoteEntry, routeUnlisted bool) bool {
	return routeUnlisted || info.Listed || (entry != nil && entry.Listed)
}
