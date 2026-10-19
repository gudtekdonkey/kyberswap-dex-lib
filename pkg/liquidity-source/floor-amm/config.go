package flooramm

import "github.com/KyberNetwork/kyberswap-dex-lib/pkg/valueobject"

type Config struct {
	DexID    string              `json:"dexID"`
	ChainID  valueobject.ChainID `json:"chainID"`
	FloorAmm string              `json:"floorAmm"` // the one FloorAmm proxy of the chain (one CREATE2 address)
	// NewPoolLimit is the most launches one listing pass adds.
	NewPoolLimit int `json:"newPoolLimit"`
	// RouteUnlisted also lists launches whose quote is not in the registry (any ERC-20, priced by its creator). Off by
	// default: nothing has vetted such a quote token's transfer behaviour.
	RouteUnlisted bool `json:"routeUnlisted"`
}
