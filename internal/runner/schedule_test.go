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
