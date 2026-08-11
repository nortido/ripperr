package runner

import (
	"testing"
	"time"
)

func TestNextRunsWithinTimezone(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	runs := NextRunsWithin(now, 7, 2, 24*time.Hour, loc)
	if len(runs) != 2 {
		t.Fatalf("got %d slots, want 2: %v", len(runs), runs)
	}
	if runs[0].Location().String() != "Europe/Moscow" {
		t.Fatalf("slot location = %s", runs[0].Location())
	}
	if runs[0].Hour() != 19 {
		t.Fatalf("first slot = %v", runs[0])
	}
}
