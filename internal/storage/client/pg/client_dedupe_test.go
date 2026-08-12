package pg

import (
	"strings"
	"testing"
)

type nameRow struct {
	urlHash  string
	itemName string
}

func purgeDuplicateNames(rows []nameRow, winners map[string]string) []nameRow {
	if len(winners) == 0 {
		return rows
	}
	out := make([]nameRow, 0, len(rows))
	for _, r := range rows {
		norm := strings.ToLower(strings.TrimSpace(r.itemName))
		if winner, ok := winners[norm]; ok && r.urlHash != winner {
			continue
		}
		out = append(out, r)
	}
	return out
}

func TestPurgeDuplicateNamesSemantics(t *testing.T) {
	rows := []nameRow{
		{urlHash: "h1", itemName: "Release A"},
		{urlHash: "h2", itemName: "Release A"},
		{urlHash: "h3", itemName: "Release B"},
	}
	winners := map[string]string{
		"release a": "h1",
		"release b": "h3",
	}
	got := purgeDuplicateNames(rows, winners)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].urlHash != "h1" || got[1].urlHash != "h3" {
		t.Fatalf("got = %+v", got)
	}
}

func TestPurgeDuplicateNamesEmptyWinners(t *testing.T) {
	rows := []nameRow{{urlHash: "h1", itemName: "X"}}
	got := purgeDuplicateNames(rows, nil)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}
