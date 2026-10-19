package flooramm

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/goccy/go-json"
	"github.com/stretchr/testify/require"

	floormath "github.com/KyberNetwork/kyberswap-dex-lib/pkg/liquidity-source/floor-amm/math"
)

// launchMarket's schedule is abi.encode(FloorSchedule.Schedule): every walk's schedule, encoded by go-ethereum's ABI
// coder as that struct, decodes back to itself; a word past 128 bits or n past 8 is refused. And the tracker's struct
// unpacks launchMarket's outputs field for field.
func TestLaunchMarketDecodes(t *testing.T) {
	words8 := func(a *[8]floormath.U) [8]*big.Int {
		var o [8]*big.Int
		for i := range a {
			o[i] = a[i].ToBig()
		}
		return o
	}
	tupleT, err := abi.NewType("tuple", "", []abi.ArgumentMarshaling{
		{Name: "n", Type: "uint256"}, {Name: "l", Type: "uint256[8]"}, {Name: "cp", Type: "uint256[8]"},
		{Name: "f", Type: "uint256[8]"}, {Name: "gcp", Type: "uint256[8]"}, {Name: "gf", Type: "uint256[8]"},
		{Name: "u", Type: "uint256"},
	})
	require.NoError(t, err)
	type solSchedule struct {
		N                 *big.Int
		L, Cp, F, Gcp, Gf [8]*big.Int
		U                 *big.Int
	}
	rows, walks := loadGoldens(t)
	checked := 0
	for _, w := range walks {
		c := w.Ops[0]
		spec := rows[w.PresetID]
		l, _, err := floormath.Create(&spec, floormath.D(w.QuoteUsdCentsX64), w.QuoteDecimals, w.TokenDecimals,
			uint64(c["created_slot"].(float64)), [3]bool{}, floormath.D("0"))
		require.NoError(t, err)
		s := l.Sch
		enc, err := abi.Arguments{{Type: tupleT}}.Pack(solSchedule{N: big.NewInt(int64(s.N)), L: words8(&s.L),
			Cp: words8(&s.Cp), F: words8(&s.F), Gcp: words8(&s.Gcp), Gf: words8(&s.Gf), U: s.U.ToBig()})
		require.NoError(t, err)
		require.Len(t, enc, floormath.ScheduleAbiLen)
		got, ok := floormath.ScheduleFromAbi(enc)
		require.True(t, ok)
		require.Equal(t, s, got)
		require.Equal(t, s.ToBytes(), got.ToBytes())

		// launchMarket's outputs through the package's ABI into the tracker's struct
		book := [4]*big.Int{l.St.S.ToBig(), l.St.H.ToBig(), l.St.L.ToBig(), l.St.FloorX64.ToBig()}
		out, err := floorAmmABI.Methods[methodLaunchMarket].Outputs.Pack(book, l.Pots.BidSqrtX64.ToBig(), l.Pots.Liq.ToBig(), enc)
		require.NoError(t, err)
		var m launchMarket
		require.NoError(t, floorAmmABI.UnpackIntoInterface(&m, methodLaunchMarket, out))
		require.Equal(t, book, m.Book)
		require.Equal(t, enc, m.Schedule)

		// creatorAlloc's outputs into the tracker's struct
		ao, err := floorAmmABI.Methods[methodCreatorAlloc].Outputs.Pack(l.CreatorAlloc.ToBig(), l.CreatorUnlockMcap.ToBig(),
			l.CreatorUnlockBacking.ToBig(), false, false)
		require.NoError(t, err)
		var ca creatorAlloc
		require.NoError(t, floorAmmABI.UnpackIntoInterface(&ca, methodCreatorAlloc, ao))
		require.Equal(t, l.CreatorAlloc.ToBig(), ca.Tokens)
		require.Equal(t, l.CreatorUnlockBacking.ToBig(), ca.UnlockBacking)

		// tokenOptionState's one tuple into the tracker's struct
		to := floorAmmABI.Methods[methodTokenOption].Outputs
		oo, err := to.Pack(struct {
			Total             *big.Int
			Released          *big.Int
			ReleasesDone      uint8
			ReleaseCount      uint8
			NextReleaseIndex  uint8
			NextReleaseAt     *big.Int
			NextReleaseTokens *big.Int
			LinesMet          bool
			Due               bool
		}{l.CreatorAlloc.ToBig(), big.NewInt(0), 2, spec.AllocReleaseCount, 2, big.NewInt(1_802_592_000),
			l.CreatorAlloc.ToBig(), true, false})
		require.NoError(t, err)
		var opt struct{ State tokenOptionState }
		require.NoError(t, floorAmmABI.UnpackIntoInterface(&opt, methodTokenOption, oo))
		require.Equal(t, uint8(2), opt.State.ReleasesDone)
		require.Equal(t, spec.AllocReleaseCount, opt.State.ReleaseCount)
		require.Equal(t, uint64(1_802_592_000), opt.State.NextReleaseAt.Uint64())

		// launchMsLiveMaxBps (v1.4a): one uint16 into the tracker's field
		mm := floorAmmABI.Methods[methodMsLiveMax]
		require.Equal(t, "launchMsLiveMaxBps(uint256)", mm.Sig)
		mo, err := mm.Outputs.Pack(uint16(floormath.GenesisMsLiveMaxBps))
		require.NoError(t, err)
		var msLive uint16
		require.NoError(t, floorAmmABI.UnpackIntoInterface(&msLive, methodMsLiveMax, mo))
		require.Equal(t, uint16(floormath.GenesisMsLiveMaxBps), msLive)

		// a v1 launch's 373-byte row, padded as the lister does: the appended fields are 0, one release
		p1, ok := floormath.PresetFromBytes(floormath.PadPresetRow(spec.ToBytes()[:floormath.PresetLenV1]))
		require.True(t, ok)
		require.Equal(t, spec.SkimBuyFarBps, p1.SkimBuyFarBps)
		require.Zero(t, p1.FloorTokenBps)
		require.Zero(t, p1.CreatorAllocBps)
		require.Equal(t, uint8(1), p1.AllocReleaseCount)
		// a 409-byte row: its allocation is released at once
		p2, ok := floormath.PresetFromBytes(floormath.PadPresetRow(spec.ToBytes()[:floormath.PresetLenV2]))
		require.True(t, ok)
		require.Equal(t, spec.UnlockBackingUsd, p2.UnlockBackingUsd)
		require.Equal(t, [floormath.MaxAllocReleases]uint16{10_000}, p2.AllocReleaseBps)
		require.True(t, floormath.SpecOk(&p2))
		// a 430-byte row (before the fee and Milestone curves): its fees flat at the row's, no Milestones
		p3, ok := floormath.PresetFromBytes(floormath.PadPresetRow(spec.ToBytes()[:floormath.PresetLenV3]))
		require.True(t, ok)
		require.Equal(t, spec.AllocReleaseBps, p3.AllocReleaseBps)
		require.Equal(t, [2]uint16{spec.SkimBuyFarBps, spec.SkimSellFarBps}, p3.SkimFarStartBps)
		require.Equal(t, [2]uint16{spec.CreatorBuyBps, spec.CreatorSellBps}, p3.CreatorStartBps)
		require.Equal(t, spec.PlatformBps, p3.PlatformStartBps)
		require.Equal(t, spec.FloorTokenBps, p3.FloorTokenStartBps)
		require.Zero(t, p3.TeamMsBps[1]+p3.PlatformMsBps[1]+p3.FloorMsBps[1])
		require.True(t, floormath.SpecOk(&p3))
		// only a whole current row decodes as is
		_, ok = floormath.PresetFromBytes(spec.ToBytes()[:floormath.PresetLenV3])
		require.False(t, ok)
		checked++
	}
	require.Positive(t, checked)
	bad := make([]byte, floormath.ScheduleAbiLen)
	bad[31] = 9 // n = 9 > 8
	_, ok := floormath.ScheduleFromAbi(bad)
	require.False(t, ok)
	bad[31] = 1
	bad[32+15] = 1 // l[0] has a bit at 2^128
	_, ok = floormath.ScheduleFromAbi(bad)
	require.False(t, ok)
	_, ok = floormath.ScheduleFromAbi(bad[:100])
	require.False(t, ok)
}

// launchQuoteInfo and the registry's quoteEntry unpack into the lister's structs, and the lister's policy: a launch whose
// quote is unlisted (not listed at create and not in the registry now) is not listed as a pool unless routeUnlisted.
func TestUnlistedQuoteIsNotListed(t *testing.T) {
	price := new(big.Int).Lsh(big.NewInt(100), 64)
	out, err := floorAmmABI.Methods[methodQuoteInfo].Outputs.Pack(false, price)
	require.NoError(t, err)
	var info launchQuoteInfo
	require.NoError(t, floorAmmABI.UnpackIntoInterface(&info, methodQuoteInfo, out))
	require.Equal(t, launchQuoteInfo{Listed: false, QuoteUsdCentsX64: price}, info)

	type solQuoteEntry struct {
		Listed, Enabled        bool
		Decimals               uint8
		CreateFee, UsdCentsX64 *big.Int
		Launches, UpdatedAt    uint64
	}
	want := solQuoteEntry{true, true, 6, big.NewInt(10_000_000), price, 3, 1_760_000_000}
	eo, err := floorAmmABI.Methods[methodQuoteEntry].Outputs.Pack(want)
	require.NoError(t, err)
	require.Len(t, eo, 7*32)
	var eout quoteEntryOut
	require.NoError(t, floorAmmABI.UnpackIntoInterface(&eout, methodQuoteEntry, eo))
	entry := eout.Entry
	require.Equal(t, quoteEntry{true, true, 6, big.NewInt(10_000_000), price, 3, 1_760_000_000}, entry)

	listed := launchQuoteInfo{Listed: true, QuoteUsdCentsX64: price}
	unlisted := launchQuoteInfo{QuoteUsdCentsX64: price}
	require.True(t, quoteVetted(listed, nil, false), "listed at create")
	require.True(t, quoteVetted(unlisted, &entry, false), "listed in the registry since")
	require.False(t, quoteVetted(unlisted, &quoteEntry{}, false), "unlisted: not a pool")
	require.False(t, quoteVetted(unlisted, nil, false), "unlisted, no entry read: not a pool")
	require.True(t, quoteVetted(unlisted, &quoteEntry{}, true), "routeUnlisted lists it")
}

// A launch held back because its quote is unlisted is kept in the lister's metadata and listed on a later pass once the
// registry lists its quote; a listed launch is never held back.
func TestUnlistedLaunchIsPickedUpOnceItsQuoteIsListed(t *testing.T) {
	q1, q2 := common.HexToAddress("0x1111"), common.HexToAddress("0x2222")
	price := new(big.Int).Lsh(big.NewInt(100), 64)
	ids := []uint64{1, 2, 3}
	quotes := []common.Address{{}, q1, q2}
	infos := []launchQuoteInfo{{Listed: true, QuoteUsdCentsX64: price}, {QuoteUsdCentsX64: price}, {QuoteUsdCentsX64: price}}

	// pass 1: neither q1 nor q2 is in the registry
	entries := map[common.Address]*quoteEntry{q1: {}, q2: {}}
	keep, skipped := vetLaunches(ids, quotes, infos, entries, false)
	require.Equal(t, []bool{true, false, false}, keep)
	require.Equal(t, []unlistedLaunch{{2, q1}, {3, q2}}, skipped)

	// the held-back launches survive the metadata round trip
	b, err := json.Marshal(Metadata{Offset: 3, Unlisted: skipped})
	require.NoError(t, err)
	var md Metadata
	require.NoError(t, json.Unmarshal(b, &md))
	require.Equal(t, Metadata{Offset: 3, Unlisted: skipped}, md)

	// pass 2: still unlisted, nothing ready
	ready, still := recheckUnlisted(md.Unlisted, entries, false)
	require.Empty(t, ready)
	require.Equal(t, skipped, still)

	// pass 3: governance lists q2; launch 3 is ready, launch 2 stays held back
	entries[q2] = &quoteEntry{Listed: true, Enabled: true, Decimals: 6, UsdCentsX64: price}
	ready, still = recheckUnlisted(still, entries, false)
	require.Equal(t, []uint64{3}, ready)
	require.Equal(t, []unlistedLaunch{{2, q1}}, still)
	// read again on that pass, launch 3 is now kept
	keep, skipped = vetLaunches([]uint64{3}, []common.Address{q2}, []launchQuoteInfo{infos[2]}, entries, false)
	require.Equal(t, []bool{true}, keep)
	require.Empty(t, skipped)

	// routeUnlisted lists everything at once
	ready, still = recheckUnlisted(still, map[common.Address]*quoteEntry{}, true)
	require.Equal(t, []uint64{2}, ready)
	require.Empty(t, still)
}
