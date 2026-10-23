package runner

import (
	"testing"
	"time"
)

func TestNextRunsWithin24h(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, loc)

	two := NextRunsWithin(now, 7, 2, 24*time.Hour, nil)
	if len(two) != 2 {
		t.Fatalf("2 passes/day: got %d slots, want 2: %v", len(two), two)
	}
	if two[0].Hour() != 19 || two[1].Day() != 16 || two[1].Hour() != 7 {
		t.Fatalf("unexpected slots: %v", two)
	}

	many := NextRunsWithin(now, 7, 24, 24*time.Hour, nil)
	if len(many) < 2 || len(many) > 24 {
		t.Fatalf("24 passes/day: got %d slots, want 2..24: %v", len(many), many)
	}

	one := NextRunsWithin(now, 7, 1, 24*time.Hour, nil)
	if len(one) != 1 {
		t.Fatalf("1 pass/day: got %d slots, want 1", len(one))
	}
	if one[0].Day() != 16 || one[0].Hour() != 7 {
		t.Fatalf("unexpected slot: %v", one[0])
	}
}

func TestNextRunsWithinAlwaysAtLeastOne(t *testing.T) {
	now := time.Date(2026, 3, 15, 6, 59, 0, 0, time.Local)
	for _, perDay := range []int{1, 2, 3, 4, 6, 8, 12, 24} {
		runs := NextRunsWithin(now, 7, perDay, 24*time.Hour, nil)
		if len(runs) < 1 {
			t.Fatalf("list_refresh_per_day=%d: expected at least 1 slot, got %v", perDay, runs)
		}
	}
}

func TestSlotFloor(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 3, day, hour, minute, 0, 0, msk) }
	cases := []struct {
		name          string
		start, perDay int
		in, want      time.Time
	}{
		{"start 0, 12/day, inside", 0, 12, at(15, 22, 6), at(15, 22, 0)},
		{"start 0, 12/day, on slot", 0, 12, at(15, 22, 0), at(15, 22, 0)},
		{"start 7, 12/day, evening", 7, 12, at(15, 22, 6), at(15, 21, 0)},
		{"start 7, 12/day, before anchor", 7, 12, at(15, 5, 30), at(15, 5, 0)},
		{"start 7, 1/day, before anchor", 7, 1, at(15, 6, 0), at(14, 7, 0)},
		{"perDay 0", 7, 0, at(15, 22, 6), at(15, 22, 6)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := slotFloor(c.in, c.start, c.perDay, msk); !got.Equal(c.want) {
				t.Errorf("slotFloor(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
