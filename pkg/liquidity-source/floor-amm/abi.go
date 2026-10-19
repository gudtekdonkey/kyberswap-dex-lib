package flooramm

import (
	"bytes"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// abis/FloorAmm.json is the IFloorAmm interface: launchCount / launchToken / launchQuote / launchSpec / launchState,
// and `launchMarket(id) → (book (s, h, l, curveFloorX64), bidSqrtX64, bidLiq, schedule)`: what the simulator needs to quote
// bit for bit (the books, the cushion curve and the stored schedule, `abi.encode(FloorSchedule.Schedule)`); and
// `creatorAlloc(id)` and `tokenOptionState(id)`, the creator's held free allocation, its unlock and its releases; `launchQuoteInfo(id)` and the registry's
// `quoteEntry(quote)`, whether the launch's quote is listed; `launchTail(id)` (the tail range after the end) and
// `launchMsLiveMaxBps(id)` (the launch's bound on the three Marketcap Milestones), both read optionally.
var floorAmmABI abi.ABI

const (
	methodLaunchCount  = "launchCount"
	methodLaunchToken  = "launchToken"
	methodLaunchQuote  = "launchQuote"
	methodLaunchSpec   = "launchSpec"
	methodLaunchState  = "launchState"
	methodLaunchMarket = "launchMarket"
	methodCreatorAlloc = "creatorAlloc"
	methodTokenOption  = "tokenOptionState"
	methodLaunchTail   = "launchTail"
	methodMsLiveMax    = "launchMsLiveMaxBps"
	methodQuoteInfo    = "launchQuoteInfo"
	methodQuoteEntry   = "quoteEntry"
)

func init() {
	var err error
	if floorAmmABI, err = abi.JSON(bytes.NewReader(floorAmmJson)); err != nil {
		panic(err)
	}
}
