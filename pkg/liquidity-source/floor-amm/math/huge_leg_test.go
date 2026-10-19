package floormath

import (
	"sort"
	"testing"
)

// A buy is never REFUSED for its size: every seed row, the goldens' quote shapes, before and after the end — legs of
// 2^64 … 2^127 fill or refund, never an error.
func TestABuyOfAnySizeIsNeverRefused(t *testing.T) {
	rows := seedRows(t)
	ids := make([]int, 0, len(rows))
	for id := range rows {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	shapes := []struct {
		cents  U
		qd, td uint8
	}{{lsh(n(100), 64), 6, 6}, {lsh(n(15_000), 64), 9, 6}, {lsh(n(240_000), 64), 18, 18}, {lsh(n(100), 64), 6, 18}}
	exps := []uint{64, 96, 120, 123, 125, 127}
	for _, id := range ids {
		spec := rows[uint8(id)]
		for _, sh := range shapes {
			l, _, err := Create(&spec, sh.cents, sh.qd, sh.td, 0, [3]bool{}, zero)
			if err != nil {
				t.Fatal(err)
			}
			live := mul(l.Supply, l.scale())
			for _, e := range exps {
				c := l
				if _, err := c.Buy(lsh(one, e), 1_000_000, live); err != nil {
					t.Fatalf("row %d qd %d td %d: a buy of 2^%d before the end refused: %v", id, sh.qd, sh.td, e, err)
				}
			}
			// past the end
			un, err := unit(sh.qd + 6)
			if err != nil {
				t.Fatal(err)
			}
			leg := un
			for !l.Ended {
				if _, err := l.Buy(leg, 1_000_000, live); err != nil {
					t.Fatalf("row %d qd %d td %d: buying to the end: %v", id, sh.qd, sh.td, err)
				}
				leg = umin(add(leg, leg), max128)
			}
			for _, e := range exps {
				c := l
				if _, err := c.Buy(lsh(one, e), 1_000_000, live); err != nil {
					t.Fatalf("row %d qd %d td %d: a buy of 2^%d after the end refused: %v", id, sh.qd, sh.td, e, err)
				}
			}
		}
	}
}
