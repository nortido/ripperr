package helper

import (
	"testing"
	"time"

	"ripperr/internal/model"
)

func TestDedupeEntriesByNameKeepsEarliest(t *testing.T) {
	early := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []*model.Entry{
		{Name: " Release ", URL: "https://a/1", Timestamp: &late},
		{Name: "Release", URL: "https://a/2", Timestamp: &early},
	}
	got := DedupeEntriesByName(entries)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].URL != "https://a/2" {
		t.Fatalf("url = %q, want earliest entry", got[0].URL)
	}
}

func TestDedupeEntriesByNameDifferentNames(t *testing.T) {
	t1 := time.Now()
	entries := []*model.Entry{
		{Name: "A", URL: "https://a/1", Timestamp: &t1},
		{Name: "B", URL: "https://a/2", Timestamp: &t1},
	}
	got := DedupeEntriesByName(entries)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestDedupeEntriesByNameNilTimestampLoses(t *testing.T) {
	withTS := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	entries := []*model.Entry{
		{Name: "X", URL: "https://a/nil"},
		{Name: "X", URL: "https://a/dated", Timestamp: &withTS},
	}
	got := DedupeEntriesByName(entries)
	if len(got) != 1 || got[0].URL != "https://a/dated" {
		t.Fatalf("got = %+v, want dated entry", got)
	}
}

func TestDedupeEntriesByNamePreservesWinnerOrder(t *testing.T) {
	t1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	entries := []*model.Entry{
		{Name: "B", URL: "https://a/b", Timestamp: &t2},
		{Name: "A", URL: "https://a/a", Timestamp: &t1},
		{Name: "B", URL: "https://a/b2", Timestamp: &t1},
	}
	got := DedupeEntriesByName(entries)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "A" || got[0].URL != "https://a/a" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].Name != "B" || got[1].URL != "https://a/b2" {
		t.Fatalf("second = %+v", got[1])
	}
}
