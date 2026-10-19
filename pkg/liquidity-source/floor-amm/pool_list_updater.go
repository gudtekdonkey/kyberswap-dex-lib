package flooramm

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"time"

	"github.com/KyberNetwork/ethrpc"
	"github.com/KyberNetwork/logger"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/goccy/go-json"
	"github.com/holiman/uint256"

	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/entity"
	floormath "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/floor-amm/math"
	poollist "github.com/KyberNetwork/kyberswap-dex-lib/pkg/source/pool/list"
	"github.com/KyberNetwork/kyberswap-dex-lib/pkg/valueobject"
)

var _ = poollist.RegisterFactoryCE(DexType, NewPoolsListUpdater)

// PoolsListUpdater discovers launches on-chain: ids are sequential from 1, `launchCount()` is the last.
type PoolsListUpdater struct {
	config       *Config
	ethrpcClient *ethrpc.Client
}

type Metadata struct {
	// Offset is the last launch id listed.
	Offset uint64 `json:"offset"`
	// Unlisted are the launches skipped because their quote was not listed; each pass re-reads their quote's registry
	// entry and lists a launch once its quote is listed.
	Unlisted []unlistedLaunch `json:"unlisted,omitempty"`
}

// unlistedLaunch is a launch held back until its quote is listed.
type unlistedLaunch struct {
	ID    uint64         `json:"id"`
	Quote common.Address `json:"quote"`
}

func NewPoolsListUpdater(config *Config, ethrpcClient *ethrpc.Client) *PoolsListUpdater {
	return &PoolsListUpdater{config: config, ethrpcClient: ethrpcClient}
}

func (u *PoolsListUpdater) GetNewPools(ctx context.Context, metadataBytes []byte) ([]entity.Pool, []byte, error) {
	var md Metadata
	if len(metadataBytes) > 0 {
		if err := json.Unmarshal(metadataBytes, &md); err != nil {
			return nil, metadataBytes, err
		}
	}
	var count *big.Int
	if _, err := u.ethrpcClient.NewRequest().SetContext(ctx).AddCall(&ethrpc.Call{
		ABI: floorAmmABI, Target: u.config.FloorAmm, Method: methodLaunchCount,
	}, []any{&count}).Call(); err != nil {
		return nil, metadataBytes, err
	}
	limit := uint64(u.config.NewPoolLimit)
	if limit == 0 {
		limit = defaultNewPoolLimit
	}
	last := max(md.Offset, min(count.Uint64(), md.Offset+limit))

	// the launches held back earlier whose quote is listed now come first, then the new ids
	entries := map[common.Address]*quoteEntry{}
	ready, still := []uint64(nil), md.Unlisted
	if len(md.Unlisted) > 0 {
		quotes := make([]common.Address, len(md.Unlisted))
		for i, l := range md.Unlisted {
			quotes[i] = l.Quote
		}
		var err error
		if entries, err = u.quoteEntries(ctx, quotes, make([]launchQuoteInfo, len(quotes)), entries); err != nil {
			return nil, metadataBytes, err
		}
		ready, still = recheckUnlisted(md.Unlisted, entries, u.config.RouteUnlisted)
	}
	ids := ready
	for id := md.Offset + 1; id <= last; id++ {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, metadataBytes, nil
	}

	k := len(ids)
	tokens, quotes := make([]common.Address, k), make([]common.Address, k)
	specs, markets, infos := make([][]byte, k), make([]launchMarket, k), make([]launchQuoteInfo, k)
	req := u.ethrpcClient.NewRequest().SetContext(ctx)
	for i := range ids {
		id := new(big.Int).SetUint64(ids[i])
		for _, c := range []struct {
			method string
			out    any
		}{
			{methodLaunchToken, &tokens[i]},
			{methodLaunchQuote, &quotes[i]},
			{methodLaunchSpec, &specs[i]},
			{methodLaunchMarket, &markets[i]},
			{methodQuoteInfo, &infos[i]},
		} {
			req.AddCall(&ethrpc.Call{ABI: floorAmmABI, Target: u.config.FloorAmm, Method: c.method,
				Params: []any{id}}, []any{c.out})
		}
	}
	if _, err := req.Aggregate(); err != nil {
		return nil, metadataBytes, err
	}

	entries, err := u.quoteEntries(ctx, quotes, infos, entries)
	if err != nil {
		return nil, metadataBytes, err
	}
	keep, skipped := vetLaunches(ids, quotes, infos, entries, u.config.RouteUnlisted)
	still = append(still, skipped...)

	pools := make([]entity.Pool, 0, k)
	for i, id := range ids {
		if !keep[i] {
			logger.Infof("floor-amm: launch %d: quote %s is not listed, held back", id, quotes[i].Hex())
			continue
		}
		sch, ok := floormath.ScheduleFromAbi(markets[i].Schedule)
		// an earlier launch's row, as it was built: a v1 row's appended fields are 0 (no $FLOOR leg, no allocation), a
		// 409-byte row releases its allocation at once, a 430-byte row's fees are flat with no Milestones
		specs[i] = floormath.PadPresetRow(specs[i])
		if _, rowOk := floormath.PresetFromBytes(specs[i]); !rowOk || !ok {
			logger.Warnf("floor-amm: launch %d: spec %d B, schedule %d B", id, len(specs[i]), len(markets[i].Schedule))
			continue
		}
		isNative := quotes[i] == (common.Address{})
		quote := hexutil.Encode(quotes[i][:])
		if isNative {
			quote = strings.ToLower(valueobject.WrappedNativeMap[u.config.ChainID])
		}
		token := hexutil.Encode(tokens[i][:])
		staticExtra, err := json.Marshal(StaticExtra{
			FloorAmm: strings.ToLower(u.config.FloorAmm), LaunchID: id, IsNative: isNative,
			Spec: hex.EncodeToString(specs[i]), Schedule: hex.EncodeToString(sch.ToBytes()),
			QuoteUsdCentsX64: quoteCents(infos[i].QuoteUsdCentsX64),
		})
		if err != nil {
			return nil, metadataBytes, err
		}
		pools = append(pools, entity.Pool{
			Address:     token, // one launch per token; `launchOf(token)` maps back
			Exchange:    u.config.DexID,
			Type:        DexType,
			Timestamp:   time.Now().Unix(),
			Reserves:    entity.PoolReserves{"0", "0"},
			Tokens:      []*entity.PoolToken{{Address: quote, Swappable: true}, {Address: token, Swappable: true}},
			Extra:       "{}",
			StaticExtra: string(staticExtra),
		})
	}
	newMd, err := json.Marshal(Metadata{Offset: last, Unlisted: still})
	if err != nil {
		return nil, metadataBytes, err
	}
	return pools, newMd, nil
}

// recheckUnlisted splits the held-back launches into those whose quote is listed now (ready to list) and the rest.
func recheckUnlisted(pending []unlistedLaunch, entries map[common.Address]*quoteEntry,
	routeUnlisted bool) (ready []uint64, still []unlistedLaunch) {
	for _, l := range pending {
		if quoteVetted(launchQuoteInfo{}, entries[l.Quote], routeUnlisted) {
			ready = append(ready, l.ID)
		} else {
			still = append(still, l)
		}
	}
	return ready, still
}

// vetLaunches applies quoteVetted to each launch read: which to list, and which to hold back for a later pass.
func vetLaunches(ids []uint64, quotes []common.Address, infos []launchQuoteInfo,
	entries map[common.Address]*quoteEntry, routeUnlisted bool) (keep []bool, skipped []unlistedLaunch) {
	keep = make([]bool, len(ids))
	for i, id := range ids {
		if keep[i] = quoteVetted(infos[i], entries[quotes[i]], routeUnlisted); !keep[i] {
			skipped = append(skipped, unlistedLaunch{ID: id, Quote: quotes[i]})
		}
	}
	return keep, skipped
}

// quoteEntries reads the registry's entry of each quote a launch was created in unlisted (a quote may be listed since),
// adding to entries; a quote already read is not read again.
func (u *PoolsListUpdater) quoteEntries(ctx context.Context, quotes []common.Address, infos []launchQuoteInfo,
	entries map[common.Address]*quoteEntry) (map[common.Address]*quoteEntry, error) {
	if u.config.RouteUnlisted {
		return entries, nil
	}
	req := u.ethrpcClient.NewRequest().SetContext(ctx)
	calls := 0
	for i, q := range quotes {
		if infos[i].Listed || entries[q] != nil {
			continue
		}
		e := &quoteEntryOut{}
		entries[q] = &e.Entry
		req.AddCall(&ethrpc.Call{ABI: floorAmmABI, Target: u.config.FloorAmm, Method: methodQuoteEntry,
			Params: []any{q}}, []any{e})
		calls++
	}
	if calls == 0 {
		return entries, nil
	}
	if _, err := req.Aggregate(); err != nil {
		return nil, err
	}
	return entries, nil
}

// quoteCents is launchQuoteInfo's quoteUsdCentsX64 as a uint256 (nil when absent or past 256 bits).
func quoteCents(v *big.Int) *uint256.Int {
	if v == nil {
		return nil
	}
	z, overflow := uint256.FromBig(v)
	if overflow {
		return nil
	}
	return z
}
