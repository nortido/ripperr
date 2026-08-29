package sqlite

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"trackerr/internal/model/storage"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	c := New(db)
	if err := c.Bootstrap(t.Context()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return c
}

func hashesOf(entries []storage.CheckLogEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.URLHash
	}
	sort.Strings(out)
	return out
}

func TestAddCheckLogErrorStreak(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	entry := &storage.CheckLogEntry{URLHash: "h1", ItemName: "Release A", Status: "error"}
	if streak, err := c.AddCheckLog(ctx, entry); err != nil || streak != 1 {
		t.Fatalf("1st error: streak=%d err=%v, want 1 nil", streak, err)
	}
	if streak, err := c.AddCheckLog(ctx, entry); err != nil || streak != 2 {
		t.Fatalf("2nd error: streak=%d err=%v, want 2 nil", streak, err)
	}
	entry.Status = "unchanged"
	if streak, err := c.AddCheckLog(ctx, entry); err != nil || streak != 0 {
		t.Fatalf("non-error: streak=%d err=%v, want 0 nil", streak, err)
	}

	var count int
	if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM check_log WHERE url_hash = ?", "h1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows for h1 = %d, want 1 (re-check updates in place)", count)
	}
}

func TestAddCheckLogSameNameDifferentHashReplaces(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h1", ItemName: "Release A", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h2", ItemName: "Release A", Status: "updated"}); err != nil {
		t.Fatal(err)
	}

	entries, total, err := c.ListCheckLog(ctx, storage.CheckLogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(entries) != 1 || entries[0].URLHash != "h2" {
		t.Fatalf("entries=%+v total=%d, want single h2 row", entries, total)
	}
}

func TestListCheckLogFilters(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	seed := []struct {
		hash, name, category string
		tracker              int
		status               string
		errorChecks          int
	}{
		{"h1", "Alpha Movie", "movies", 1, "updated", 1},
		{"h2", "Beta Show", "tv", 2, "error", 1},
		{"h3", "Gamma Movie", "movies", 1, "unchanged", 1},
		{"h4", "Delta Show", "tv", 3, "error", 2},
	}
	for _, s := range seed {
		if err := c.AddItem(ctx, &storage.Item{URLHash: s.hash, TrackerTypeID: s.tracker, Category: s.category, Name: s.name}); err != nil {
			t.Fatal(err)
		}
		for range s.errorChecks {
			if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: s.hash, ItemName: s.name, TrackerTypeID: s.tracker, Status: s.status}); err != nil {
				t.Fatal(err)
			}
		}
	}

	tests := []struct {
		name   string
		filter storage.CheckLogFilter
		want   []string
	}{
		{"by status", storage.CheckLogFilter{Status: "error", Limit: 10}, []string{"h2", "h4"}},
		{"by name substring case insensitive", storage.CheckLogFilter{Name: "movie", Limit: 10}, []string{"h1", "h3"}},
		{"by tracker", storage.CheckLogFilter{TrackerID: 1, Limit: 10}, []string{"h1", "h3"}},
		{"by category via history join", storage.CheckLogFilter{Category: "tv", Limit: 10}, []string{"h2", "h4"}},
		{"broken status filter using BrokenStreak", storage.CheckLogFilter{Status: "broken", BrokenStreak: 2, Limit: 10}, []string{"h4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, total, err := c.ListCheckLog(ctx, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if total != len(tt.want) {
				t.Errorf("total = %d, want %d", total, len(tt.want))
			}
			if got := hashesOf(entries); !slices.Equal(got, tt.want) {
				t.Errorf("hashes = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("limit and offset page while total reflects the full filtered count", func(t *testing.T) {
		page1, total, err := c.ListCheckLog(ctx, storage.CheckLogFilter{Limit: 2, Offset: 0})
		if err != nil {
			t.Fatal(err)
		}
		page2, total2, err := c.ListCheckLog(ctx, storage.CheckLogFilter{Limit: 2, Offset: 2})
		if err != nil {
			t.Fatal(err)
		}
		if total != 4 || total2 != 4 {
			t.Fatalf("total=%d total2=%d, want 4 and 4", total, total2)
		}
		if len(page1) != 2 || len(page2) != 2 {
			t.Fatalf("page1=%d page2=%d, want 2 and 2", len(page1), len(page2))
		}
	})
}

func TestListCheckLogSortRecheckNullsLast(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	future := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := c.AddItem(ctx, &storage.Item{URLHash: "with-recheck", Name: "with-recheck", RecheckAt: &future}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "no-recheck", Name: "no-recheck"}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"with-recheck", "no-recheck"} {
		if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: h, ItemName: h, Status: "unchanged"}); err != nil {
			t.Fatal(err)
		}
	}

	entries, _, err := c.ListCheckLog(ctx, storage.CheckLogFilter{SortBy: "recheck", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].URLHash != "with-recheck" || entries[1].URLHash != "no-recheck" {
		t.Fatalf("order = %v, want [with-recheck no-recheck] (row without recheck_at sorts last)", hashesOf(entries))
	}
}

func TestListCheckLogExcludeIgnored(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	for _, h := range []string{"kept", "ignored"} {
		if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: h, ItemName: h, Status: "unchanged"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.AddIgnore(ctx, "ignored", "ignored", "http://x"); err != nil {
		t.Fatal(err)
	}

	t.Run("excludes ignored row from items and total", func(t *testing.T) {
		entries, total, err := c.ListCheckLog(ctx, storage.CheckLogFilter{ExcludeIgnored: true, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 || len(entries) != 1 || entries[0].URLHash != "kept" {
			t.Fatalf("entries=%+v total=%d, want single kept row", entries, total)
		}
	})

	t.Run("flag off still returns ignored row", func(t *testing.T) {
		entries, total, err := c.ListCheckLog(ctx, storage.CheckLogFilter{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if total != 2 || len(entries) != 2 {
			t.Fatalf("entries=%+v total=%d, want both rows", entries, total)
		}
	})

	t.Run("IgnoredOnly wins over ExcludeIgnored when both are set", func(t *testing.T) {
		entries, total, err := c.ListCheckLog(ctx, storage.CheckLogFilter{ExcludeIgnored: true, IgnoredOnly: true, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 || len(entries) != 1 || entries[0].URLHash != "ignored" {
			t.Fatalf("entries=%+v total=%d, want single ignored row (IgnoredOnly takes precedence)", entries, total)
		}
	})
}

func TestItemTimestampRoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	created := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	recheck := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	if err := c.AddItem(ctx, &storage.Item{URLHash: "h1", Name: "A", CreatedAt: &created, RecheckAt: &recheck}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "h2", Name: "B", CreatedAt: &created}); err != nil {
		t.Fatal(err)
	}

	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byHash := make(map[string]storage.Item, len(items))
	for _, it := range items {
		byHash[it.URLHash] = it
	}

	got1 := byHash["h1"]
	if got1.CreatedAt == nil || !got1.CreatedAt.Equal(created) {
		t.Errorf("h1 CreatedAt = %v, want %v", got1.CreatedAt, created)
	}
	if got1.RecheckAt == nil || !got1.RecheckAt.Equal(recheck) {
		t.Errorf("h1 RecheckAt = %v, want %v", got1.RecheckAt, recheck)
	}

	got2 := byHash["h2"]
	if got2.RecheckAt != nil {
		t.Errorf("h2 RecheckAt = %v, want nil", got2.RecheckAt)
	}
}

func TestPurgeDuplicateNames(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	if err := c.AddItem(ctx, &storage.Item{URLHash: "winner", Name: "Same Release"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "loser1", Name: "Same Release"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "loser2", Name: " same release "}); err != nil {
		t.Fatal(err)
	}
	// check_log сам обеспечивает уникальность имени, поэтому заранее существующая проигравшая строка
	// заводится там напрямую, в обход логики upsert и самоочистки в AddCheckLog
	if _, err := c.db.ExecContext(ctx, `INSERT INTO check_log (url_hash, item_name, status) VALUES (?, ?, 'updated')`, "old-hash", "Same Release"); err != nil {
		t.Fatal(err)
	}

	if err := c.PurgeDuplicateNames(ctx, map[string]string{"same release": "winner"}); err != nil {
		t.Fatal(err)
	}

	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].URLHash != "winner" {
		t.Fatalf("history rows = %+v, want only winner", items)
	}

	var count int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM check_log WHERE url_hash = 'old-hash'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("check_log old-hash rows = %d, want 0", count)
	}
}

func TestSetForceCheck(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	ok, err := c.SetForceCheck(ctx, "unknown", true)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("SetForceCheck(unknown) = true, want false")
	}

	if err := c.AddItem(ctx, &storage.Item{URLHash: "h1", Name: "A"}); err != nil {
		t.Fatal(err)
	}
	ok, err = c.SetForceCheck(ctx, "h1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Errorf("SetForceCheck(h1) = false, want true")
	}

	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].ForceCheck {
		t.Fatalf("items = %+v, want ForceCheck true", items)
	}
}

func TestForceCheckAllSkipsIgnoredWhenListDoes(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	for _, h := range []string{"h1", "h2", "h3"} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: h, Name: h}); err != nil {
			t.Fatal(err)
		}
	}
	// h1: строка в check_log есть, не игнорируется, значит форсируется
	// h2: строки в check_log нет, значит не трогаем
	// h3: строка в check_log есть, но запись игнорируется, значит не трогаем
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h1", ItemName: "h1", Status: "updated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h3", ItemName: "h3", Status: "updated"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddIgnore(ctx, "h3", "h3", ""); err != nil {
		t.Fatal(err)
	}

	_, n, err := c.ForceCheckAll(ctx, true, storage.CheckLogFilter{ExcludeIgnored: true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}

	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	forced := make(map[string]bool, len(items))
	for _, it := range items {
		forced[it.URLHash] = it.ForceCheck
	}
	if !forced["h1"] || forced["h2"] || forced["h3"] {
		t.Fatalf("forced = %+v, want only h1", forced)
	}
}

func TestSettingRoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	if _, ok, err := c.GetSetting(ctx, "missing"); err != nil || ok {
		t.Fatalf("GetSetting(missing) ok=%v err=%v, want false nil", ok, err)
	}

	want := json.RawMessage(`{"a":1}`)
	if err := c.SaveSetting(ctx, SettingSchedule, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.GetSetting(ctx, SettingSchedule)
	if err != nil || !ok || string(got) != string(want) {
		t.Fatalf("GetSetting = %s ok=%v err=%v, want %s true nil", got, ok, err, want)
	}

	want2 := json.RawMessage(`{"a":2}`)
	if err := c.SaveSetting(ctx, SettingSchedule, want2); err != nil {
		t.Fatal(err)
	}
	got2, _, err := c.GetSetting(ctx, SettingSchedule)
	if err != nil || string(got2) != string(want2) {
		t.Fatalf("GetSetting after overwrite = %s err=%v, want %s nil", got2, err, want2)
	}
}

func TestIgnoreLifecycleAndResetErrorStreak(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	if err := c.AddIgnore(ctx, "h1", "Name", "http://x"); err != nil {
		t.Fatal(err)
	}
	set, err := c.GetIgnoreSet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set["h1"]; !ok {
		t.Fatalf("ignore set = %v, want h1 present", set)
	}
	list, err := c.ListIgnore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].URLHash != "h1" {
		t.Fatalf("ListIgnore = %+v, want single h1 entry", list)
	}

	if err := c.DeleteIgnore(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if set, err = c.GetIgnoreSet(ctx); err != nil {
		t.Fatal(err)
	} else if _, ok := set["h1"]; ok {
		t.Fatalf("ignore set after delete = %v, want h1 absent", set)
	}

	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h2", ItemName: "h2", Status: "error"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h2", ItemName: "h2", Status: "error"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ResetErrorStreak(ctx, "h2"); err != nil {
		t.Fatal(err)
	}
	entries, _, err := c.ListCheckLog(ctx, storage.CheckLogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ErrorStreak != 0 {
		t.Fatalf("entries = %+v, want single row with ErrorStreak 0", entries)
	}
}

func TestForceCheckAllHonoursListFilter(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	for _, h := range []string{"h1", "h2"} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: h, Name: h}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h1", ItemName: "h1", TrackerTypeID: 3, Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h2", ItemName: "h2", TrackerTypeID: 1, Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}

	hashes, n, err := c.ForceCheckAll(ctx, true, storage.CheckLogFilter{TrackerID: 3})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}
	if len(hashes) != 1 || hashes[0] != "h1" {
		t.Fatalf("hashes = %v, want [h1]", hashes)
	}

	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	forced := make(map[string]bool, len(items))
	for _, it := range items {
		forced[it.URLHash] = it.ForceCheck
	}
	if !forced["h1"] || forced["h2"] {
		t.Fatalf("forced = %+v, want only h1", forced)
	}
}

func TestForceCheckAllCountsOnlyChangedRows(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	for _, h := range []string{"h1", "h2", "h3"} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: h, Name: h}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: h, ItemName: h, Status: "unchanged"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.SetForceCheck(ctx, "h1", true); err != nil {
		t.Fatal(err)
	}

	_, n, err := c.ForceCheckAll(ctx, true, storage.CheckLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("forced rows = %d, want 2 (h1 was already forced)", n)
	}

	if _, err = c.SetForceCheck(ctx, "h2", false); err != nil {
		t.Fatal(err)
	}
	_, n, err = c.ForceCheckAll(ctx, false, storage.CheckLogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("cleared rows = %d, want 2 (h2 was not forced)", n)
	}
}

func TestPurgeCategoriesExcept(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	seed := []struct{ hash, name, category string }{
		{"h1", "Movie A", "radarr"},
		{"h2", "Show B", "sonarr"},
		{"h3", "Album C", "lidarr"},
		{"h4", "Unknown D", ""},
	}
	for _, s := range seed {
		if err := c.AddItem(ctx, &storage.Item{URLHash: s.hash, Category: s.category, Name: s.name}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: s.hash, ItemName: s.name, Status: "unchanged"}); err != nil {
			t.Fatal(err)
		}
	}

	// пустой список это "категории не ограничены", чистить нечего
	if n, err := c.PurgeCategoriesExcept(ctx, nil); err != nil || n != 0 {
		t.Fatalf("empty allow list: n=%d err=%v, want 0 nil", n, err)
	}

	n, err := c.PurgeCategoriesExcept(ctx, []string{"radarr", "sonarr"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d, want 1 (lidarr)", n)
	}

	entries, _, err := c.ListCheckLog(ctx, storage.CheckLogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	// h4 с пустой категорией остаётся: она не выключена, она неизвестна
	if got, want := hashesOf(entries), []string{"h1", "h2", "h4"}; !slices.Equal(got, want) {
		t.Fatalf("check_log = %v, want %v", got, want)
	}
	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("history has %d rows, want 3", len(items))
	}
}
