package pg

import (
	"sort"
	"testing"
	"time"
)

// ponytail: in-memory stand-in for upsert/dedupe SQL semantics (no PG in unit tests with -mod=vendor)

type checkRow struct {
	id          int64
	checkedAt   time.Time
	urlHash     string
	itemName    string
	tid         int
	status      string
	errorMsg    string
	errorStreak int
}

func upsertRow(rows map[string]checkRow, nextID *int64, hash, name, url string, tid int, status, errMsg string, at time.Time) map[string]checkRow {
	if rows == nil {
		rows = make(map[string]checkRow)
	}
	streak := 0
	if status == "error" {
		streak = rows[hash].errorStreak + 1
	}
	*nextID++
	rows[hash] = checkRow{
		id: *nextID, checkedAt: at, urlHash: hash, itemName: name,
		tid: tid, status: status, errorMsg: errMsg, errorStreak: streak,
	}
	return rows
}

func dedupeRows(rows []checkRow) []checkRow {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].urlHash != rows[j].urlHash {
			return rows[i].urlHash < rows[j].urlHash
		}
		if !rows[i].checkedAt.Equal(rows[j].checkedAt) {
			return rows[i].checkedAt.After(rows[j].checkedAt)
		}
		return rows[i].id > rows[j].id
	})
	seen := make(map[string]struct{})
	out := make([]checkRow, 0, len(rows))
	for _, r := range rows {
		if _, ok := seen[r.urlHash]; ok {
			continue
		}
		seen[r.urlHash] = struct{}{}
		out = append(out, r)
	}
	return out
}

func TestAddCheckLogUpsertSemantics(t *testing.T) {
	var nextID int64
	rows := make(map[string]checkRow)
	t1 := time.Now().Add(-time.Hour)
	t2 := time.Now()
	rows = upsertRow(rows, &nextID, "hash1", "old", "", 1, "unchanged", "", t1)
	rows = upsertRow(rows, &nextID, "hash1", "new", "", 2, "error", "boom", t2)
	if len(rows) != 1 {
		t.Fatalf("len = %d, want 1", len(rows))
	}
	r := rows["hash1"]
	if r.itemName != "new" || r.tid != 2 || r.status != "error" || r.errorMsg != "boom" {
		t.Fatalf("row = %+v", r)
	}
	if !r.checkedAt.Equal(t2) {
		t.Fatalf("checkedAt not updated")
	}
}

func TestAddCheckLogErrorStreak(t *testing.T) {
	var nextID int64
	rows := make(map[string]checkRow)
	now := time.Now()

	rows = upsertRow(rows, &nextID, "hash1", "n", "", 1, "error", "e1", now)
	if got := rows["hash1"].errorStreak; got != 1 {
		t.Fatalf("streak after 1st error = %d, want 1", got)
	}
	rows = upsertRow(rows, &nextID, "hash1", "n", "", 1, "error", "e2", now)
	if got := rows["hash1"].errorStreak; got != 2 {
		t.Fatalf("streak after 2nd error = %d, want 2", got)
	}
	rows = upsertRow(rows, &nextID, "hash1", "n", "", 1, "error", "e3", now)
	if got := rows["hash1"].errorStreak; got != 3 {
		t.Fatalf("streak after 3rd error = %d, want 3", got)
	}
	rows = upsertRow(rows, &nextID, "hash1", "n", "", 1, "unchanged", "", now)
	if got := rows["hash1"].errorStreak; got != 0 {
		t.Fatalf("streak after non-error status = %d, want 0", got)
	}
}

func TestDedupeCheckLogSemantics(t *testing.T) {
	now := time.Now()
	input := []checkRow{
		{id: 1, checkedAt: now.Add(-2 * time.Hour), urlHash: "h1", itemName: "old"},
		{id: 2, checkedAt: now.Add(-1 * time.Hour), urlHash: "h1", itemName: "new"},
		{id: 3, checkedAt: now, urlHash: "h2", itemName: "only"},
	}
	got := dedupeRows(input)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	byHash := make(map[string]string)
	for _, r := range got {
		byHash[r.urlHash] = r.itemName
	}
	if byHash["h1"] != "new" || byHash["h2"] != "only" {
		t.Fatalf("got = %+v", byHash)
	}
}
