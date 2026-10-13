package floor

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// Rates are the pool's own fee rates in bps, copied into its state when the launch was created and never changed.
type Rates struct {
	SkimBuyFar      uint16 `json:"sbf,omitempty"`
	SkimSellFar     uint16 `json:"ssf,omitempty"`
	SkimBuyAtFloor  uint16 `json:"sba,omitempty"`
	SkimSellAtFloor uint16 `json:"ssa,omitempty"`
	CreatorBuy      uint16 `json:"cb,omitempty"`
	CreatorSell     uint16 `json:"cs,omitempty"`
	HolderBuy       uint16 `json:"hb,omitempty"`
	HolderSell      uint16 `json:"hs,omitempty"`
	TeamFee         uint16 `json:"tf,omitempty"`
}

// Extra is the hook state kept per pool. Ticks (FloorTick, CushionLower) are in the engine frame: the quote at
// currency0 for every pool, so a dearer launch token is a lower tick.
type Extra struct {
	// TickSpacing is poolState.tickSpacing; 0 means the pool was never read or is not trading yet (not graduated).
	TickSpacing      int32 `json:"tS,omitempty"`
	QuoteIsCurrency1 bool  `json:"q1,omitempty"`
	FloorTick        int32 `json:"fT,omitempty"`
	Rates            Rates `json:"r"`
	// CushionLower is stairs().cushionLower, the cushion's dear edge; HasCushion is stairs().cushionLiq != 0.
	CushionLower int32 `json:"cL,omitempty"`
	HasCushion   bool  `json:"c,omitempty"`
	// ProtocolFee is the PoolManager's packed protocol fee (slot0.protocolFee): zeroForOne in the low 12 bits.
	ProtocolFee uint32 `json:"pF,omitempty"`
}

// SwapInfo carries a quote's post-swap tick to UpdateBalance, and whether the swap was a sell that reached the bid
// band (the hook then re-places its bid inside that swap).
type SwapInfo struct {
	Tick        int  `json:"t"`
	BandReached bool `json:"b,omitempty"`
}

// poolStateRPC is the ethrpc decode target for SingletonFloorHook.poolState(id). go-ethereum copies a returned tuple
// field by field, by position, so every field of the contract's PoolState is listed, in order.
type poolStateRPC struct {
	PoolInitializer common.Address
	SkimBuyFarBps   *big.Int
	SkimSellFarBps  *big.Int
	PoolKey         struct {
		Currency0   common.Address
		Currency1   common.Address
		Fee         *big.Int
		TickSpacing *big.Int
		Hooks       common.Address
	}
	PoolBound             bool
	TickSpacing           *big.Int
	VaultEth              *big.Int
	BidEthDeployed        *big.Int
	InventoryTokens       *big.Int
	FloorTick             *big.Int
	BidLower              *big.Int
	BidLiquidity          *big.Int
	CumulativeVolumeEth   *big.Int
	UnpaidSkim            *big.Int
	NeedsRatchet          bool
	WallShape             *big.Int
	SeedLiq               *big.Int
	SeedLower             *big.Int
	SeedUpper             *big.Int
	SeedFeesSweptEth      *big.Int
	SeedFeesSweptTokens   *big.Int
	LadderUpper           *big.Int
	LadderTokensRemaining *big.Int
	LadderProceedsEth     *big.Int
	LadderSeeded          bool
	Token                 common.Address
	PlatformVault         common.Address
	PlatformPaidEth       *big.Int
	CreatorSlotCount      *big.Int
	CreatorBuyBps         *big.Int
	CreatorSellBps        *big.Int
	CreatorPendingMask    *big.Int
	RecipientClaims       common.Address
	CreatorFeesOwed       *big.Int
	CreatorFeesPaid       *big.Int
	HolderRewards         common.Address
	HolderBuyBps          *big.Int
	HolderSellBps         *big.Int
	HolderPaidEth         *big.Int
	QuoteToken            common.Address
	QuoteIsCurrency1      bool
	CustodyVault          common.Address
	SkimBuyAtFloorBps     *big.Int
	SkimSellAtFloorBps    *big.Int
	BidWidth              *big.Int
	Referrer              common.Address
	ReferralFeesPaid      *big.Int
	QuoteUnit             *big.Int
	HolderOwedEth         *big.Int
	ReferralFeesOwed      *big.Int
	TeamFeesOwed          *big.Int
	TeamFeesPaid          *big.Int
	TeamFeeBps            *big.Int
}

// stairsRPC is the decode target for the pool's stairs() view, read through execute(poolId, stairs()).
type stairsRPC struct {
	Next         uint8
	Placed       bool
	Base         *big.Int
	TailCheap    *big.Int
	TailLiq      *big.Int
	TailTokens   *big.Int
	RelistLower  *big.Int
	RelistUpper  *big.Int
	RelistTokens *big.Int
	RelistLiq    *big.Int
	CushionLower *big.Int
	CushionUpper *big.Int
	CushionLiq   *big.Int
	CushionQuote *big.Int
	CushionFed   *big.Int
	Pending      *big.Int
	Step         [16]struct {
		Cheap  *big.Int
		Liq    *big.Int
		Tokens *big.Int
	}
	BoughtTokens       *big.Int
	BoughtCost         *big.Int
	SweptQuote         *big.Int
	CushionFees        *big.Int
	CushionBasisTokens *big.Int
	CushionBasisCost   *big.Int
	CushionInPool      *big.Int
}

// poolStateResp and stairsResp hold a single returned tuple: go-ethereum copies a lone output into the first field.
type poolStateResp struct{ PoolState poolStateRPC }

type stairsResp struct{ Stairs stairsRPC }

// slot0RPC is the decode target for StateView.getSlot0(poolId).
type slot0RPC struct {
	SqrtPriceX96 *big.Int
	Tick         *big.Int
	ProtocolFee  *big.Int
	LpFee        *big.Int
}
