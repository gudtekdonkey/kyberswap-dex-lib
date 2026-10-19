package floormath

import (
	"encoding/binary"
	"testing"
)

// The allocation's releases against golden/trade.json's alloc_releases vectors: the cumulative release, the clock and
// the pro-rata split.
func TestAllocReleasesGolden(t *testing.T) {
	var g struct {
		AllocReleases []struct {
			Kind     string              `json:"kind"`
			Count    uint8               `json:"count"`
			Bps      []uint16            `json:"bps"`
			Total    string              `json:"total"`
			Released []map[string]string `json:"released"`
			Done     uint8               `json:"releases_done"`
			At       uint64              `json:"released_at"`
			Secs     uint32              `json:"release_secs"`
			Next     *uint64             `json:"next_release_at"`
			Tokens   string              `json:"tokens"`
			Weights  [MaxMembers]uint32  `json:"weights"`
			Members  map[string][]string `json:"members"`
		} `json:"alloc_releases"`
	}
	readJSON(t, "trade.json", &g)
	kinds := map[string]int{}
	for i, v := range g.AllocReleases {
		kinds[v.Kind]++
		switch v.Kind {
		case "released_after":
			for k, want := range v.Released {
				got, err := allocReleasedAfter(D(v.Total), v.Bps, k)
				if err != nil || got.Dec() != want["ok"] {
					t.Fatalf("vector %d after %d: got %v %v want %v", i, k, got.Dec(), err, want)
				}
			}
		case "next_release_at":
			got, ok := allocNextReleaseAt(v.Done, v.Count, v.At, v.Secs)
			if ok != (v.Next != nil) || (ok && got != *v.Next) {
				t.Fatalf("vector %d: got %d %v want %v", i, got, ok, v.Next)
			}
		case "split_release":
			got, err := splitRelease(D(v.Tokens), v.Weights)
			if err != nil {
				t.Fatalf("vector %d: %v", i, err)
			}
			for k, want := range v.Members["ok"] {
				if got[k].Dec() != want {
					t.Fatalf("vector %d place %d: got %s want %s", i, k, got[k].Dec(), want)
				}
			}
		default:
			t.Fatalf("vector %d: unknown kind %s", i, v.Kind)
		}
	}
	if kinds["released_after"] == 0 || kinds["next_release_at"] == 0 || kinds["split_release"] == 0 {
		t.Fatalf("kinds %v", kinds)
	}
}

// spec_ok holds the releases (a count 1 ..= 8, every one in range > 0 and none past it, the sizes summing to 10,000, a
// clock between releases ≤ 365 days); the release fields sit at @409 / @410 / @426; an earlier row reads as one release.
func TestSpecOkHoldsTheReleases(t *testing.T) {
	for _, b := range seedRows(t) {
		with := func(f func(p *PresetSpec)) PresetSpec { p := b; f(&p); return p }
		one := with(func(p *PresetSpec) {
			p.AllocReleaseCount, p.AllocReleaseBps, p.AllocReleaseSecs = 1, [MaxAllocReleases]uint16{10_000}, 0
		})
		eight := with(func(p *PresetSpec) {
			p.AllocReleaseCount, p.AllocReleaseSecs = 8, 1
			for i := range p.AllocReleaseBps {
				p.AllocReleaseBps[i] = 1_250
			}
		})
		for _, p := range []PresetSpec{b, one, eight} {
			if !SpecOk(&p) {
				t.Fatalf("row %d: %d %v %d refused", b.ID, p.AllocReleaseCount, p.AllocReleaseBps, p.AllocReleaseSecs)
			}
		}
		for _, p := range []PresetSpec{
			with(func(p *PresetSpec) { p.AllocReleaseCount, p.AllocReleaseBps = 0, [MaxAllocReleases]uint16{} }),
			with(func(p *PresetSpec) { p.AllocReleaseCount = 9 }),
			with(func(p *PresetSpec) { p.AllocReleaseBps = [MaxAllocReleases]uint16{2_500, 2_500, 2_500, 2_499} }),
			with(func(p *PresetSpec) { p.AllocReleaseBps = [MaxAllocReleases]uint16{2_500, 2_500, 2_500, 2_501} }),
			with(func(p *PresetSpec) { p.AllocReleaseBps = [MaxAllocReleases]uint16{5_000, 0, 2_500, 2_500} }),
			with(func(p *PresetSpec) { p.AllocReleaseBps = [MaxAllocReleases]uint16{2_500, 2_500, 2_500, 2_499, 1} }),
			with(func(p *PresetSpec) { p.AllocReleaseSecs = 0 }),
			with(func(p *PresetSpec) { p.AllocReleaseSecs = presetMaxAllocReleaseSecs + 1 }),
			with(func(p *PresetSpec) {
				p.AllocReleaseCount, p.AllocReleaseBps = 1, [MaxAllocReleases]uint16{5_000, 5_000}
			}),
		} {
			if SpecOk(&p) {
				t.Fatalf("row %d: %d %v %d passed", b.ID, p.AllocReleaseCount, p.AllocReleaseBps, p.AllocReleaseSecs)
			}
		}
		p := with(func(p *PresetSpec) {
			p.AllocReleaseCount, p.AllocReleaseBps = 3, [MaxAllocReleases]uint16{1_000, 4_000, 5_000}
			p.AllocReleaseSecs = 0x0102_0304
		})
		m := p.ToBytes()
		if len(m) != PresetLen || m[409] != 3 || binary.LittleEndian.Uint16(m[410:]) != 1_000 ||
			binary.LittleEndian.Uint16(m[414:]) != 5_000 || binary.LittleEndian.Uint32(m[426:]) != 0x0102_0304 {
			t.Fatalf("row %d: the release fields' offsets", b.ID)
		}
		v2 := PadPresetRow(m[:PresetLenV2])
		q, ok := PresetFromBytes(v2)
		if !ok || q.AllocReleaseCount != 1 || q.AllocReleaseBps != [MaxAllocReleases]uint16{10_000} || q.AllocReleaseSecs != 0 ||
			q.UnlockBackingUsd != p.UnlockBackingUsd || !SpecOk(&q) {
			t.Fatalf("row %d: a 409-byte row padded: %v %+v", b.ID, ok, q)
		}
	}
}
