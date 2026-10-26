package sqlite

import (
	"database/sql"
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"ripperr/internal/model/storage"
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

	c := New(db, testBox(t))
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

func TestAddCheckLogKeepStreak(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	entry := &storage.CheckLogEntry{URLHash: "h1", ItemName: "Release A", Status: "error", Error: "first"}
	for want := 1; want <= 2; want++ {
		if streak, err := c.AddCheckLog(ctx, entry); err != nil || streak != want {
			t.Fatalf("error: streak=%d err=%v, want %d nil", streak, err, want)
		}
	}
	entry.KeepStreak = true
	entry.Error = "tracker unavailable"
	if streak, err := c.AddCheckLog(ctx, entry); err != nil || streak != 2 {
		t.Fatalf("keep: streak=%d err=%v, want 2 nil", streak, err)
	}
	var status, errText string
	if err := c.db.QueryRowContext(ctx, "SELECT status, error FROM check_log WHERE url_hash = ?", "h1").Scan(&status, &errText); err != nil {
		t.Fatal(err)
	}
	if status != "error" || errText != "tracker unavailable" {
		t.Fatalf("row = %q %q, want error \"tracker unavailable\"", status, errText)
	}
	entry.KeepStreak = false
	if streak, err := c.AddCheckLog(ctx, entry); err != nil || streak != 3 {
		t.Fatalf("error after keep: streak=%d err=%v, want 3 nil", streak, err)
	}
	entry.Status = "unchanged"
	if streak, err := c.AddCheckLog(ctx, entry); err != nil || streak != 0 {
		t.Fatalf("non-error: streak=%d err=%v, want 0 nil", streak, err)
	}

	fresh := &storage.CheckLogEntry{URLHash: "h2", ItemName: "Release B", Status: "error", KeepStreak: true}
	if streak, err := c.AddCheckLog(ctx, fresh); err != nil || streak != 0 {
		t.Fatalf("fresh keep: streak=%d err=%v, want 0 nil", streak, err)
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
	// check_log сам держит имя уникальным, поэтому старую проигравшую строку вставляем
	// напрямую, мимо upsert и самоочистки в AddCheckLog
	if _, err := c.db.ExecContext(ctx, `INSERT INTO check_log (url_hash, item_name, status) VALUES (?, ?, 'updated')`, "old-hash", "Same Release"); err != nil {
		t.Fatal(err)
	}

	if err := c.PurgeDuplicateNames(ctx, map[string][]string{"same release": {"winner"}}); err != nil {
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

func TestPurgeDuplicateNamesCyrillic(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	at := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	for _, s := range []struct{ hash, name string }{{"w", "Сериал"}, {"l", "Сериал"}, {"variant", "сериал"}} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: s.hash, Name: s.name}); err != nil {
			t.Fatal(err)
		}
		if err := c.AddReleaseTime(ctx, s.hash, at, 8); err != nil {
			t.Fatal(err)
		}
	}
	// check_log держит имя уникальным, поэтому проигравшую строку вставляем мимо AddCheckLog
	if _, err := c.db.ExecContext(ctx, `INSERT INTO check_log (url_hash, item_name, status) VALUES ('l', 'Сериал', 'updated')`); err != nil {
		t.Fatal(err)
	}

	if err := c.PurgeDuplicateNames(ctx, map[string][]string{"сериал": {"w"}}); err != nil {
		t.Fatal(err)
	}

	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.URLHash] = true
	}
	if got["l"] || !got["w"] || got["variant"] {
		t.Fatalf("history rows = %v, want only w", got)
	}
	if rt := releaseUnix(t, c, "l"); len(rt) != 0 {
		t.Fatalf("loser release_times = %v, want none", rt)
	}
	var n int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM check_log WHERE url_hash = 'l'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("loser check_log rows = %d, want 0", n)
	}
}

func TestPurgeDuplicateNamesFoldsKelvinSign(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	// знак Кельвина Go сворачивает в "k", так что "Kino" с ним это вариант регистра того же имени
	for _, s := range []struct{ hash, name string }{{"w", "Kino"}, {"other", "Kino"}} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: s.hash, Name: s.name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.PurgeDuplicateNames(ctx, map[string][]string{storage.FoldName("Kino"): {"w"}}); err != nil {
		t.Fatal(err)
	}
	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].URLHash != "w" {
		t.Fatalf("history rows = %+v, want only w", items)
	}
}

func TestPurgeDuplicateNamesKeepsLiveCaseVariants(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	at := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	for _, s := range []struct{ hash, name string }{{"a", "Foo"}, {"b", "foo"}, {"c", "FOO"}} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: s.hash, Name: s.name}); err != nil {
			t.Fatal(err)
		}
		if err := c.AddReleaseTime(ctx, s.hash, at, 8); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO check_log (url_hash, item_name, status) VALUES ('c', 'FOO', 'updated')`); err != nil {
		t.Fatal(err)
	}

	if err := c.PurgeDuplicateNames(ctx, map[string][]string{"foo": {"a", "b"}}); err != nil {
		t.Fatal(err)
	}

	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range items {
		got[it.URLHash] = true
	}
	if !got["a"] || !got["b"] || got["c"] {
		t.Fatalf("history rows = %v, want a and b only", got)
	}
	for _, h := range []string{"a", "b"} {
		if rt := releaseUnix(t, c, h); len(rt) != 1 {
			t.Fatalf("%s release_times = %v, want one row", h, rt)
		}
	}
	if rt := releaseUnix(t, c, "c"); len(rt) != 0 {
		t.Fatalf("c release_times = %v, want none", rt)
	}
	var n int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM check_log WHERE url_hash = 'c'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("c check_log rows = %d, want 0", n)
	}
}

func TestPurgeDuplicateNamesKeepsHashLiveUnderOtherName(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	for _, s := range []struct{ hash, name string }{{"x", "foo"}, {"y", "foo"}} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: s.hash, Name: s.name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.PurgeDuplicateNames(ctx, map[string][]string{"foo": {"a"}, "bar": {"x"}}); err != nil {
		t.Fatal(err)
	}
	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].URLHash != "x" {
		t.Fatalf("history rows = %+v, want only x", items)
	}
}

func TestPurgeDuplicateNamesEmptyNameNeverPurges(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	for _, s := range []struct{ hash, name string }{{"n1", ""}, {"n2", "  "}, {"live", "Foo"}} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: s.hash, Name: s.name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.PurgeDuplicateNames(ctx, map[string][]string{"": {"live"}, "foo": {"live"}}); err != nil {
		t.Fatal(err)
	}
	items, err := c.ListItems(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("history rows = %+v, want all three kept", items)
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
	// h4 с пустой категорией остается: она не выключена, она неизвестна
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

func TestDeleteTrackerRemovesRow(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	if err := c.AddTracker(ctx, &storage.Tracker{TypeID: 1, Name: "one", Domain: "one.example", Credentials: "SEC-c1"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddTracker(ctx, &storage.Tracker{TypeID: 2, Name: "two", Domain: "two.example", Credentials: "SEC-c", Cookies: "SEC-k"}); err != nil {
		t.Fatal(err)
	}

	if err := c.DeleteTracker(ctx, 2); err != nil {
		t.Fatalf("DeleteTracker(2): %v", err)
	}
	if tr, err := c.GetTracker(ctx, 2); err != nil || tr.Exists {
		t.Fatalf("GetTracker(2) exists=%v err=%v, want false nil", tr.Exists, err)
	}
	if err := c.DeleteTracker(ctx, 99); err != nil {
		t.Fatalf("DeleteTracker(99): %v, want nil", err)
	}
	tr, err := c.GetTracker(ctx, 1)
	if err != nil || !tr.Exists || tr.Credentials != "SEC-c1" {
		t.Fatalf("GetTracker(1) exists=%v creds=%q err=%v, want true SEC-c1 nil", tr.Exists, tr.Credentials, err)
	}
}

func releaseUnix(t *testing.T, c *Client, hash string) []int64 {
	t.Helper()
	rows, err := c.db.QueryContext(t.Context(), `SELECT released_at FROM release_times WHERE url_hash = ? ORDER BY released_at DESC`, hash)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAddReleaseTimeKeepsNewest(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	base := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	if err := c.AddReleaseTime(ctx, "other", base, 8); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		if err := c.AddReleaseTime(ctx, "h1", base.AddDate(0, 0, i), 8); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := c.AddReleaseTime(ctx, "h1", base.AddDate(0, 0, 9), 8); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	var want []int64
	for i := 9; i >= 2; i-- {
		want = append(want, base.AddDate(0, 0, i).Unix())
	}
	if got := releaseUnix(t, c, "h1"); !slices.Equal(got, want) {
		t.Fatalf("h1 = %v, want %v", got, want)
	}
	if got := releaseUnix(t, c, "other"); !slices.Equal(got, []int64{base.Unix()}) {
		t.Fatalf("other = %v, want [%d]", got, base.Unix())
	}
}

func TestRecentReleaseTimes(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	base := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	for i := range 5 {
		if err := c.AddReleaseTime(ctx, "h1", base.AddDate(0, 0, i), 8); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.AddReleaseTime(ctx, "h2", base.AddDate(0, 0, 4), 8); err != nil {
		t.Fatal(err)
	}
	unix := func(ts []time.Time) []int64 {
		out := make([]int64, len(ts))
		for i, v := range ts {
			out[i] = v.Unix()
		}
		return out
	}
	got, err := c.RecentReleaseTimes(ctx, "h1", base.AddDate(0, 0, 1), 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{base.AddDate(0, 0, 4).Unix(), base.AddDate(0, 0, 3).Unix(), base.AddDate(0, 0, 2).Unix(), base.AddDate(0, 0, 1).Unix()}
	if !slices.Equal(unix(got), want) {
		t.Fatalf("since inclusive = %v, want %v", unix(got), want)
	}
	got, err = c.RecentReleaseTimes(ctx, "h1", base, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(unix(got), want[:2]) {
		t.Fatalf("limit 2 = %v, want %v", unix(got), want[:2])
	}
	got, err = c.RecentReleaseTimes(ctx, "none", base, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("unknown hash = %v err=%v, want empty nil", got, err)
	}
}

func TestDeleteItemDropsReleaseTimes(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	at := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	for _, h := range []string{"gone", "kept"} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: h, Name: h}); err != nil {
			t.Fatal(err)
		}
		if err := c.AddReleaseTime(ctx, h, at, 8); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.DeleteItem(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	if got := releaseUnix(t, c, "gone"); len(got) != 0 {
		t.Fatalf("gone = %v, want none", got)
	}
	if got := releaseUnix(t, c, "kept"); len(got) != 1 {
		t.Fatalf("kept = %v, want one row", got)
	}
}

func TestPurgeDuplicateNamesDropsReleaseTimes(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	at := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	for _, h := range []string{"winner", "loser"} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: h, Name: "Same Release"}); err != nil {
			t.Fatal(err)
		}
		if err := c.AddReleaseTime(ctx, h, at, 8); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.PurgeDuplicateNames(ctx, map[string][]string{"same release": {"winner"}}); err != nil {
		t.Fatal(err)
	}
	if got := releaseUnix(t, c, "loser"); len(got) != 0 {
		t.Fatalf("loser = %v, want none", got)
	}
	if got := releaseUnix(t, c, "winner"); len(got) != 1 {
		t.Fatalf("winner = %v, want one row", got)
	}
}

func TestPurgeCategoriesExceptDropsReleaseTimes(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	at := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	for _, s := range []struct{ hash, category string }{{"keep", "radarr"}, {"drop", "lidarr"}, {"unknown", ""}} {
		if err := c.AddItem(ctx, &storage.Item{URLHash: s.hash, Category: s.category, Name: s.hash}); err != nil {
			t.Fatal(err)
		}
		if err := c.AddReleaseTime(ctx, s.hash, at, 8); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.PurgeCategoriesExcept(ctx, []string{"radarr"}); err != nil {
		t.Fatal(err)
	}
	if got := releaseUnix(t, c, "drop"); len(got) != 0 {
		t.Fatalf("drop = %v, want none", got)
	}
	for _, h := range []string{"keep", "unknown"} {
		if got := releaseUnix(t, c, h); len(got) != 1 {
			t.Fatalf("%s = %v, want one row", h, got)
		}
	}
}

func TestListCheckLogNewReleaseHold(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	t1 := time.Now().Add(time.Hour).Truncate(time.Second)
	t2 := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	if _, err := c.db.ExecContext(ctx,
		`INSERT INTO check_log (checked_at, url_hash, item_name, status) VALUES (?, 'n1', 'n1', 'new')`, t1.Unix()); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "h1", Name: "h1", RecheckAt: &t2}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h1", ItemName: "h1", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "e1", ItemName: "e1", Status: "error"}); err != nil {
		t.Fatal(err)
	}

	entries, _, err := c.ListCheckLog(ctx, storage.CheckLogFilter{SortBy: "recheck", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, e := range entries {
		order = append(order, e.URLHash)
	}
	if !slices.Equal(order, []string{"n1", "h1", "e1"}) {
		t.Fatalf("order = %v, want [n1 h1 e1]", order)
	}
	n1 := entries[0]
	if n1.RecheckAt == nil || !n1.RecheckAt.Equal(t1) {
		t.Fatalf("n1.RecheckAt = %v, want %v", n1.RecheckAt, t1)
	}
	if !n1.CheckedAt.IsZero() {
		t.Fatalf("n1.CheckedAt = %v, want zero", n1.CheckedAt)
	}
	if entries[1].CheckedAt.IsZero() {
		t.Fatal("h1.CheckedAt is zero, only a new row without history may have it empty")
	}

	def, _, err := c.ListCheckLog(ctx, storage.CheckLogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(def) != 3 || def[0].URLHash != "n1" {
		t.Fatalf("default order = %v, want n1 first", hashesOf(def))
	}
}

func TestAddNewRelease(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	first := time.Now().Add(time.Hour).Truncate(time.Second)

	ok, err := c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "a", ItemName: "New one", ItemURL: "u", TrackerTypeID: 3}, first)
	if err != nil || !ok {
		t.Fatalf("first insert: ok=%v err=%v, want true nil", ok, err)
	}
	if ok, err = c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "a", ItemName: "New one"}, first.Add(time.Hour)); err != nil || ok {
		t.Fatalf("same hash: ok=%v err=%v, want false nil", ok, err)
	}
	if ok, err = c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "b", ItemName: " NEW ONE "}, first.Add(time.Hour)); err != nil || ok {
		t.Fatalf("same name other hash: ok=%v err=%v, want false nil", ok, err)
	}
	if err = c.AddItem(ctx, &storage.Item{URLHash: "h", Name: "Hist"}); err != nil {
		t.Fatal(err)
	}
	if ok, err = c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "h", ItemName: "Hist"}, first); err != nil || ok {
		t.Fatalf("hash with history: ok=%v err=%v, want false nil", ok, err)
	}

	var n int
	if err = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM check_log`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("check_log rows = %d err=%v, want 1", n, err)
	}
	var name, url, status string
	var at int64
	var ttid int
	if err = c.db.QueryRowContext(ctx, `SELECT checked_at, item_name, item_url, tracker_type_id, status FROM check_log WHERE url_hash = 'a'`).
		Scan(&at, &name, &url, &ttid, &status); err != nil {
		t.Fatal(err)
	}
	if at != first.Unix() || name != "New one" || url != "u" || ttid != 3 || status != "new" {
		t.Fatalf("row a = %d %q %q %d %q, want first row unchanged", at, name, url, ttid, status)
	}
}

func TestNewReleaseHolds(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	at := time.Now().Add(time.Hour).Truncate(time.Second)

	if ok, err := c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "a", ItemName: "A"}, at); err != nil || !ok {
		t.Fatalf("add: ok=%v err=%v", ok, err)
	}
	holds, err := c.NewReleaseHolds(ctx)
	if err != nil || len(holds) != 1 || !holds["a"].Equal(at) {
		t.Fatalf("holds = %v err=%v, want {a: %v}", holds, err, at)
	}
	if isNew, err := c.IsNewRelease(ctx, "a"); err != nil || !isNew {
		t.Fatalf("IsNewRelease(a) = %v err=%v, want true", isNew, err)
	}
	if isNew, err := c.IsNewRelease(ctx, "zzz"); err != nil || isNew {
		t.Fatalf("IsNewRelease(zzz) = %v err=%v, want false", isNew, err)
	}

	if _, err = c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "a", ItemName: "A", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	holds, err = c.NewReleaseHolds(ctx)
	if err != nil || len(holds) != 0 {
		t.Fatalf("holds after check = %v err=%v, want empty", holds, err)
	}
	if isNew, err := c.IsNewRelease(ctx, "a"); err != nil || isNew {
		t.Fatalf("IsNewRelease after check = %v err=%v, want false", isNew, err)
	}
	entries, _, err := c.ListCheckLog(ctx, storage.CheckLogFilter{Limit: 10})
	if err != nil || len(entries) != 1 || time.Since(entries[0].CheckedAt) > time.Minute {
		t.Fatalf("checked_at after check = %v err=%v, want about now", entries, err)
	}

	// строка new с историей уже не удержание: релиз скачан
	if ok, err := c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "b", ItemName: "B"}, at); err != nil || !ok {
		t.Fatalf("add b: ok=%v err=%v", ok, err)
	}
	if err = c.AddItem(ctx, &storage.Item{URLHash: "b", Name: "B"}); err != nil {
		t.Fatal(err)
	}
	if holds, err = c.NewReleaseHolds(ctx); err != nil || len(holds) != 0 {
		t.Fatalf("holds with history = %v err=%v, want empty", holds, err)
	}
	if isNew, err := c.IsNewRelease(ctx, "b"); err != nil || isNew {
		t.Fatalf("IsNewRelease with history = %v err=%v, want false", isNew, err)
	}
}

func TestKnownReleases(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "a", ItemName: "Жук Name", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "h", Name: " Other "}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "empty", Name: "  "}); err != nil {
		t.Fatal(err)
	}
	hashes, names, err := c.KnownReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantHashes := map[string]struct{}{"a": {}, "h": {}, "empty": {}}
	wantNames := map[string]struct{}{"жук name": {}, "other": {}}
	if len(hashes) != len(wantHashes) || len(names) != len(wantNames) {
		t.Fatalf("hashes=%v names=%v, want %v and %v", hashes, names, wantHashes, wantNames)
	}
	for h := range wantHashes {
		if _, ok := hashes[h]; !ok {
			t.Fatalf("hashes = %v, missing %q", hashes, h)
		}
	}
	for n := range wantNames {
		if _, ok := names[n]; !ok {
			t.Fatalf("names = %v, missing %q", names, n)
		}
	}
}

func TestDeleteNewRelease(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	at := time.Now().Add(time.Hour).Truncate(time.Second)

	if ok, err := c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "n", ItemName: "N"}, at); err != nil || !ok {
		t.Fatalf("add n: ok=%v err=%v", ok, err)
	}
	if ok, err := c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "nh", ItemName: "NH"}, at); err != nil || !ok {
		t.Fatalf("add nh: ok=%v err=%v", ok, err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "nh", Name: "NH"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "u", ItemName: "U", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "u", Name: "U"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "e", ItemName: "E", Status: "error"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddReleaseTime(ctx, "u", at, 5); err != nil {
		t.Fatal(err)
	}

	for _, h := range []string{"n", "nh", "u", "e", "missing"} {
		if err := c.DeleteNewRelease(ctx, h); err != nil {
			t.Fatalf("DeleteNewRelease(%s): %v", h, err)
		}
	}

	count := func(q string) int {
		t.Helper()
		var n int
		if err := c.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM check_log WHERE url_hash = 'n'`); n != 0 {
		t.Fatalf("hold n rows = %d, want deleted", n)
	}
	for _, h := range []string{"nh", "u", "e"} {
		if n := count(`SELECT COUNT(*) FROM check_log WHERE url_hash = '` + h + `'`); n != 1 {
			t.Fatalf("check_log %s rows = %d, want kept", h, n)
		}
	}
	if n := count(`SELECT COUNT(*) FROM history`); n != 2 {
		t.Fatalf("history rows = %d, want 2 untouched", n)
	}
	if n := count(`SELECT COUNT(*) FROM release_times`); n != 1 {
		t.Fatalf("release_times rows = %d, want 1 untouched", n)
	}
}

func TestListedReleases(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	at := time.Now().Add(time.Hour).Truncate(time.Second)

	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h1", ItemName: "Show A", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "h1", Name: "Show A"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "n1", ItemName: "New one"}, at); err != nil || !ok {
		t.Fatalf("add n1: ok=%v err=%v", ok, err)
	}
	if ok, err := c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "nh", ItemName: "NH"}, at); err != nil || !ok {
		t.Fatalf("add nh: ok=%v err=%v", ok, err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "nh", Name: "NH"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "e1", ItemName: "Err one", Status: "error"}); err != nil {
		t.Fatal(err)
	}
	// история без строки журнала в список не попадает
	if err := c.AddItem(ctx, &storage.Item{URLHash: "hx", Name: "Only history"}); err != nil {
		t.Fatal(err)
	}

	names, fresh, err := c.ListedReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := map[string]string{"h1": "Show A", "n1": "New one", "nh": "NH", "e1": "Err one"}
	if !maps.Equal(names, wantNames) {
		t.Fatalf("names = %v, want %v", names, wantNames)
	}
	if len(fresh) != 1 {
		t.Fatalf("fresh = %v, want only n1", fresh)
	}
	if _, ok := fresh["n1"]; !ok {
		t.Fatalf("fresh = %v, want n1", fresh)
	}
}

func TestListCheckLogNewReleaseCategory(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	if err := c.AddItem(ctx, &storage.Item{URLHash: "h1", Name: "h1", Category: "tv"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h1", ItemName: "h1", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "e1", ItemName: "e1", Status: "error"}); err != nil {
		t.Fatal(err)
	}
	first := time.Now().Add(time.Hour).Truncate(time.Second)
	if ok, err := c.AddNewRelease(ctx, &storage.CheckLogEntry{URLHash: "n1", ItemName: "New one", Category: "tv"}, first); err != nil || !ok {
		t.Fatalf("AddNewRelease: ok=%v err=%v, want true nil", ok, err)
	}

	categories := func(f storage.CheckLogFilter) map[string]string {
		t.Helper()
		f.Limit = 10
		entries, _, err := c.ListCheckLog(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := make(map[string]string, len(entries))
		for _, e := range entries {
			out[e.URLHash] = e.Category
		}
		return out
	}
	want := map[string]string{"n1": "tv", "h1": "tv", "e1": ""}
	if got := categories(storage.CheckLogFilter{}); !maps.Equal(got, want) {
		t.Fatalf("categories = %v, want %v", got, want)
	}
	if got := categories(storage.CheckLogFilter{Category: "tv"}); !maps.Equal(got, map[string]string{"n1": "tv", "h1": "tv"}) {
		t.Fatalf("filter tv = %v, want h1 and n1", got)
	}
	if got := categories(storage.CheckLogFilter{Category: "movies"}); len(got) != 0 {
		t.Fatalf("filter movies = %v, want none", got)
	}

	hashes, _, err := c.ForceCheckAll(ctx, true, storage.CheckLogFilter{Category: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Sort(hashes); !slices.Equal(hashes, []string{"h1", "n1"}) {
		t.Fatalf("force-all hashes = %v, want h1 and n1", hashes)
	}

	if _, err = c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "n1", ItemName: "New one", Status: "error"}); err != nil {
		t.Fatal(err)
	}
	if got := categories(storage.CheckLogFilter{}); got["n1"] != "tv" {
		t.Fatalf("n1 after first check = %q, want tv", got["n1"])
	}

	// у строки с историей колонка new_category не читается
	if _, err = c.db.ExecContext(ctx, `UPDATE check_log SET new_category = 'movies' WHERE url_hash = 'h1'`); err != nil {
		t.Fatal(err)
	}
	if got := categories(storage.CheckLogFilter{}); got["h1"] != "tv" {
		t.Fatalf("h1 with stale new_category = %q, want tv", got["h1"])
	}
	if got := categories(storage.CheckLogFilter{Category: "movies"}); len(got) != 0 {
		t.Fatalf("filter movies after stale = %v, want none", got)
	}

	// история без категории (NULL) тоже не должна подхватывать new_category
	if err = c.AddItem(ctx, &storage.Item{URLHash: "h2", Name: "h2"}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.db.ExecContext(ctx, `UPDATE history SET category_name = NULL WHERE url_hash = 'h2'`); err != nil {
		t.Fatal(err)
	}
	if _, err = c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h2", ItemName: "h2", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.db.ExecContext(ctx, `UPDATE check_log SET new_category = 'movies' WHERE url_hash = 'h2'`); err != nil {
		t.Fatal(err)
	}
	if got := categories(storage.CheckLogFilter{}); got["h2"] != "" {
		t.Fatalf("h2 with NULL history category = %q, want empty", got["h2"])
	}
	if got := categories(storage.CheckLogFilter{Category: "movies"}); len(got) != 0 {
		t.Fatalf("filter movies for NULL history category = %v, want none", got)
	}
}
