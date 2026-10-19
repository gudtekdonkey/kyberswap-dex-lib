package floormath

// The schedule's own goldens (golden/schedule.json `schedules`): each schedule's points and opening books, its walk of
// buys (mint_for_payment, from the open, the books carried) and its fills to a room (fill_to_room) — the walk's region B
// with a ramping share and the trial-buy fallback, which the launch walks never reach.

import (
	"fmt"
	"testing"
)

type stJ struct {
	S     string `json:"s"`
	H     string `json:"h"`
	L     string `json:"l"`
	Floor string `json:"floor"`
}

func (s stJ) st() State { return State{S: D(s.S), H: D(s.H), L: D(s.L), FloorX64: D(s.Floor)} }

func stOf(s State) stJ { return stJ{q(s.S), q(s.H), q(s.L), q(s.FloorX64)} }

type schedJ struct {
	Name  string `json:"name"`
	Terms struct {
		CushionBps  [2]uint16 `json:"cushion_bps"`
		PolBps      [2]uint16 `json:"pol_bps"`
		RampCpFrom  string    `json:"ramp_cp_from"`
		FullAt      string    `json:"full_at"`
		Floor0Bps   uint16    `json:"floor0_bps"`
		RampFrom    string    `json:"ramp_from"`
		FloorPoints [4][2]any `json:"floor_points"`
		CutBps      uint16    `json:"cut_bps"`
	} `json:"terms"`
	Raise    string      `json:"raise"`
	Sale     string      `json:"sale"`
	Pol      string      `json:"pol"`
	OpenMcap string      `json:"open_mcap"`
	Points   [][5]string `json:"points"`
	Open     stJ         `json:"open"`
	Buys     []struct {
		Pay    string `json:"pay"`
		Minted string `json:"minted"`
		After  stJ    `json:"after"`
	} `json:"buys"`
	ToRoom []struct {
		Before stJ    `json:"before"`
		Offer  string `json:"offer"`
		Room   string `json:"room"`
		Out    struct {
			Ok *struct {
				Pay   string `json:"pay"`
				Out   string `json:"out"`
				After stJ    `json:"after"`
			} `json:"ok"`
			Err string `json:"err"`
		} `json:"out"`
	} `json:"to_room"`
}

func TestScheduleGoldens(t *testing.T) {
	var g struct {
		Schedules []schedJ `json:"schedules"`
	}
	readJSON(t, "schedule.json", &g)
	if len(g.Schedules) == 0 {
		t.Fatal("no schedules")
	}
	var nBuys, nRoom int
	for _, sj := range g.Schedules {
		sj := sj
		t.Run(sj.Name, func(t *testing.T) {
			tj := sj.Terms
			terms := Terms{CushionBps: tj.CushionBps, PolBps: tj.PolBps, RampCpFrom: D(tj.RampCpFrom), FullAt: D(tj.FullAt),
				Floor0Bps: tj.Floor0Bps, RampFrom: D(tj.RampFrom), CutBps: tj.CutBps}
			for i, p := range tj.FloorPoints {
				terms.FloorPoints[i] = Point{D(p[0].(string)), uint16(p[1].(float64))}
			}
			sch, err := NewSchedule(&terms, D(sj.OpenMcap))
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			if sch.N != len(sj.Points) {
				t.Fatalf("points: got %d want %d", sch.N, len(sj.Points))
			}
			for i, p := range sj.Points {
				got := [5]string{q(sch.L[i]), q(sch.Cp[i]), q(sch.F[i]), q(sch.Gcp[i]), q(sch.Gf[i])}
				if got != p {
					t.Fatalf("point %d: got %v want %v", i, got, p)
				}
			}
			st, err := openSale(&sch, D(sj.Raise), D(sj.Sale), D(sj.Pol))
			if err != nil || stOf(st) != sj.Open {
				t.Fatalf("open: got %v %v want %v", stOf(st), err, sj.Open)
			}
			for i, b := range sj.Buys {
				out, after, err := mintForPayment(&sch, &st, D(b.Pay))
				if err != nil || q(out) != b.Minted || stOf(after) != b.After {
					t.Fatalf("buy %d: got %s %v %v want %s %v", i, q(out), stOf(after), err, b.Minted, b.After)
				}
				st = after
				nBuys++
			}
			for i, c := range sj.ToRoom {
				before := c.Before.st()
				pay, out, after, _, err := fillToRoomM(&sch, &before, nil, D(c.Offer), D(c.Room))
				got := fmt.Sprintf("%s %s %v", q(pay), q(out), stOf(after))
				if c.Out.Ok == nil {
					if err == nil || err.Error() != c.Out.Err {
						t.Fatalf("to_room %d: got %s %v want err %s", i, got, err, c.Out.Err)
					}
				} else if want := fmt.Sprintf("%s %s %v", c.Out.Ok.Pay, c.Out.Ok.Out, c.Out.Ok.After); err != nil || got != want {
					t.Fatalf("to_room %d: got %s %v want %s", i, got, err, want)
				}
				nRoom++
			}
		})
	}
	t.Logf("%d schedules, %d buys, %d fills to a room", len(g.Schedules), nBuys, nRoom)
}
