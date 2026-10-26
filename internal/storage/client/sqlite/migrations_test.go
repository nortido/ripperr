package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ripperr/internal/model/storage"
)

func newRawDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	return v
}

func TestBootstrapStampsUserVersion(t *testing.T) {
	db := newRawDB(t)
	c := New(db, testBox(t))
	if err := c.Bootstrap(t.Context()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
}

func TestMigrateSkipsAlreadyApplied(t *testing.T) {
	db := newRawDB(t)
	c := New(db, testBox(t))
	if err := c.Bootstrap(t.Context()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// последняя миграция падает при повторе. Если версия уже записана, второй раз ее выполнять нельзя
	boom := append(slices.Clone(migrations), migration{name: "boom", stmts: []string{"CREATE TABLE boom (id INTEGER PRIMARY KEY)"}})
	if err := c.migrate(t.Context(), boom); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations)+1 {
		t.Fatalf("user_version = %d, want %d", got, len(migrations)+1)
	}
	// повторный прогон не должен упасть на CREATE TABLE boom без IF NOT EXISTS
	if err := c.migrate(t.Context(), boom); err != nil {
		t.Fatalf("migrate rerun: %v", err)
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	db := newRawDB(t)
	c := New(db, testBox(t))
	if _, err := db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatalf("set version: %v", err)
	}
	if err := c.Bootstrap(t.Context()); err == nil {
		t.Fatal("want error on schema newer than binary, got nil")
	}
}

func TestMigrateRollsBackFailedStep(t *testing.T) {
	db := newRawDB(t)
	c := New(db, testBox(t))
	bad := []migration{
		{name: "bad", stmts: []string{
			"CREATE TABLE good (id INTEGER PRIMARY KEY)",
			"THIS IS NOT SQL",
		}},
	}
	if err := c.migrate(t.Context(), bad); err == nil {
		t.Fatal("want error, got nil")
	}
	if got := userVersion(t, db); got != 0 {
		t.Fatalf("user_version = %d, want 0 after rollback", got)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'good'").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatal("table good survived a failed migration")
	}
}

func TestMigrateRollsBackFailedApply(t *testing.T) {
	db := newRawDB(t)
	c := New(db, testBox(t))
	errApply := errors.New("apply failed")
	bad := []migration{
		{name: "bad apply", stmts: []string{"CREATE TABLE t (id INTEGER PRIMARY KEY)"},
			apply: func(ctx context.Context, _ *Client, tx *sql.Tx) error {
				if _, err := tx.ExecContext(ctx, "INSERT INTO t (id) VALUES (1)"); err != nil {
					return err
				}
				return errApply
			}},
	}
	if err := c.migrate(t.Context(), bad); !errors.Is(err, errApply) {
		t.Fatalf("migrate err = %v, want errApply", err)
	}
	if got := userVersion(t, db); got != 0 {
		t.Fatalf("user_version = %d, want 0 after rollback", got)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 't'").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatal("table t survived a failed apply")
	}
}

func TestMigrateOverLegacyDB(t *testing.T) {
	db := newRawDB(t)
	c := New(db, testBox(t))
	// база до версионирования: таблицы есть, а user_version = 0
	for _, stmt := range migrations[0].stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy setup: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO check_log (url_hash, item_name, status) VALUES ('h1', 'Release One', 'updated')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := userVersion(t, db); got != 0 {
		t.Fatalf("legacy user_version = %d, want 0", got)
	}
	if err := c.Bootstrap(t.Context()); err != nil {
		t.Fatalf("bootstrap over legacy: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM check_log").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("check_log rows = %d, want 1 (data survived)", n)
	}
}

func TestApplyMigrationSkipsWhenVersionAlreadyReached(t *testing.T) {
	db := newRawDB(t)
	c := New(db, testBox(t))
	// как будто другой процесс накатил шаг, пока мы ждали блокировку записи
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatalf("set version: %v", err)
	}
	poison := migration{name: "poison", stmts: []string{"THIS IS NOT SQL"}}
	if err := c.applyMigration(t.Context(), 1, poison); err != nil {
		t.Fatalf("applyMigration: %v, want silent skip", err)
	}
	if got := userVersion(t, db); got != 1 {
		t.Fatalf("user_version = %d, want 1", got)
	}
}

func TestBootstrapCreatesReleaseTimes(t *testing.T) {
	db := newRawDB(t)
	c := New(db, testBox(t))
	if err := c.Bootstrap(t.Context()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('release_times') ORDER BY cid`)
	if err != nil {
		t.Fatalf("table info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"url_hash", "released_at"}; !slices.Equal(cols, want) {
		t.Fatalf("release_times columns = %v, want %v", cols, want)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
}

func TestMigrateAddsNewCategoryColumn(t *testing.T) {
	ctx := t.Context()
	db := newRawDB(t)
	c := New(db, testBox(t))
	if err := c.migrate(ctx, migrations[:4]); err != nil {
		t.Fatalf("migrate to 4: %v", err)
	}
	if got := userVersion(t, db); got != 4 {
		t.Fatalf("user_version = %d, want 4", got)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "h1", Name: "Has history", Category: "tv"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h1", ItemName: "Has history", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "e1", ItemName: "Broken", Status: "error"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO check_log (checked_at, url_hash, item_name, status) VALUES (?, 'n0', 'New one', 'new')`, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	snapshot := func() []string {
		rows, err := db.Query(`SELECT url_hash || '|' || status || '|' || checked_at || '|' || item_name FROM check_log ORDER BY url_hash`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	if len(before) != 3 {
		t.Fatalf("seeded rows = %d, want 3", len(before))
	}

	checkColumn := func(stage string) {
		t.Helper()
		var n, notNull int
		var dflt sql.NullString
		if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX("notnull"), 0), MAX(dflt_value) FROM pragma_table_info('check_log') WHERE name = 'new_category'`).Scan(&n, &notNull, &dflt); err != nil {
			t.Fatalf("%s: table info: %v", stage, err)
		}
		if n != 1 || notNull != 0 || dflt.Valid {
			t.Fatalf("%s: new_category count=%d notnull=%d default=%v, want 1, 0, NULL", stage, n, notNull, dflt)
		}
	}

	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap from 4: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
	checkColumn("first run")
	if after := snapshot(); !slices.Equal(before, after) {
		t.Fatalf("rows changed by migration: before %v, after %v", before, after)
	}
	var nulls int
	if err := db.QueryRow(`SELECT COUNT(*) FROM check_log WHERE new_category IS NULL`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 3 {
		t.Fatalf("rows with NULL new_category = %d, want 3", nulls)
	}

	if _, err := db.Exec("PRAGMA user_version = 4"); err != nil {
		t.Fatal(err)
	}
	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap rerun: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version after rerun = %d, want %d", got, len(migrations))
	}
	checkColumn("rerun")
}

func TestAddNewCategorySkipsWithoutCheckLog(t *testing.T) {
	db := newRawDB(t)
	c := New(db, testBox(t))
	if _, err := db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL DEFAULT (unixepoch()))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 4"); err != nil {
		t.Fatal(err)
	}
	if err := c.Bootstrap(t.Context()); err != nil {
		t.Fatalf("bootstrap without check_log: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'check_log'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("check_log was created by step 5")
	}
}

func TestMigrateExactNameIndex(t *testing.T) {
	ctx := t.Context()
	db := newRawDB(t)
	c := New(db, testBox(t))
	if err := c.migrate(ctx, migrations[:5]); err != nil {
		t.Fatalf("migrate to 5: %v", err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h1", ItemName: "Foo", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	checkIndexes := func(stage string) {
		t.Helper()
		var exact, old int
		if err := db.QueryRow(`SELECT COALESCE(SUM(name = 'check_log_item_name_exact_uidx'), 0), COALESCE(SUM(name = 'check_log_item_name_uidx'), 0) FROM sqlite_master WHERE type = 'index'`).Scan(&exact, &old); err != nil {
			t.Fatalf("%s: sqlite_master: %v", stage, err)
		}
		if exact != 1 || old != 0 {
			t.Fatalf("%s: exact index=%d old index=%d, want 1 and 0", stage, exact, old)
		}
	}

	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap from 5: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
	checkIndexes("first run")
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h2", ItemName: "foo", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM check_log`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("check_log rows = %d err=%v, want 2", n, err)
	}
	_, err := db.Exec(`INSERT INTO check_log (url_hash, item_name, status) VALUES ('h3', ' Foo ', 'unchanged')`)
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint") {
		t.Fatalf("insert exact trimmed twin: err=%v, want UNIQUE constraint", err)
	}

	// 5 это обычный повтор шага, 1 это повтор после decrypt-secrets
	for _, from := range []int{5, 1} {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", from)); err != nil {
			t.Fatal(err)
		}
		if err := c.Bootstrap(ctx); err != nil {
			t.Fatalf("bootstrap rerun from %d: %v", from, err)
		}
		if got := userVersion(t, db); got != len(migrations) {
			t.Fatalf("user_version after rerun from %d = %d, want %d", from, got, len(migrations))
		}
		checkIndexes(fmt.Sprintf("rerun from %d", from))
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM check_log`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("check_log rows after reruns = %d err=%v, want 2", n, err)
	}
}

func TestMigrateAddsAppearedAt(t *testing.T) {
	ctx := t.Context()
	db := newRawDB(t)
	c := New(db, testBox(t))
	if err := c.migrate(ctx, migrations[:6]); err != nil {
		t.Fatalf("migrate to 6: %v", err)
	}
	if got := userVersion(t, db); got != 6 {
		t.Fatalf("user_version = %d, want 6", got)
	}
	rawNew := func(hash, name string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO check_log (checked_at, url_hash, item_name, status) VALUES (?, ?, ?, 'new')`, time.Now().Add(24*time.Hour).Unix(), hash, name); err != nil {
			t.Fatal(err)
		}
	}
	rawNew("n0", "New one")
	rawNew("nh", "New with history")
	if err := c.AddItem(ctx, &storage.Item{URLHash: "nh", Name: "New with history"}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddItem(ctx, &storage.Item{URLHash: "h1", Name: "Has history"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h1", ItemName: "Has history", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "e1", ItemName: "Broken", Status: "error"}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() []string {
		t.Helper()
		rows, err := db.Query(`SELECT url_hash || '|' || status || '|' || checked_at || '|' || item_name || '|' || COALESCE(new_category, '') FROM check_log ORDER BY url_hash`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	seeded := snapshot()
	if len(seeded) != 4 {
		t.Fatalf("seeded rows = %d, want 4", len(seeded))
	}
	checkColumn := func(stage string) {
		t.Helper()
		var n, notNull int
		var typ, dflt sql.NullString
		if err := db.QueryRow(`SELECT COUNT(*), MAX(type), COALESCE(MAX("notnull"), 0), MAX(dflt_value) FROM pragma_table_info('check_log') WHERE name = 'appeared_at'`).Scan(&n, &typ, &notNull, &dflt); err != nil {
			t.Fatalf("%s: table info: %v", stage, err)
		}
		if n != 1 || typ.String != "INTEGER" || notNull != 0 || dflt.Valid {
			t.Fatalf("%s: appeared_at count=%d type=%q notnull=%d default=%v, want 1, INTEGER, 0, NULL", stage, n, typ.String, notNull, dflt)
		}
	}
	appeared := func(hash string) sql.NullInt64 {
		t.Helper()
		var v sql.NullInt64
		if err := db.QueryRow(`SELECT appeared_at FROM check_log WHERE url_hash = ?`, hash).Scan(&v); err != nil {
			t.Fatalf("appeared_at of %s: %v", hash, err)
		}
		return v
	}
	checkUnstamped := func(stage string) {
		t.Helper()
		for _, h := range []string{"nh", "h1", "e1"} {
			if v := appeared(h); v.Valid {
				t.Fatalf("%s: %s.appeared_at = %d, want NULL", stage, h, v.Int64)
			}
		}
	}

	before := time.Now().Unix()
	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap from 6: %v", err)
	}
	after := time.Now().Unix()
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
	checkColumn("first run")
	if v := appeared("n0"); !v.Valid || v.Int64 < before || v.Int64 > after {
		t.Fatalf("n0.appeared_at = %v, want within [%d, %d]", v, before, after)
	}
	checkUnstamped("first run")
	if got := snapshot(); !slices.Equal(seeded, got) {
		t.Fatalf("rows changed by migration: before %v, after %v", seeded, got)
	}

	if _, err := db.Exec(`UPDATE check_log SET appeared_at = 100 WHERE url_hash = 'n0'`); err != nil {
		t.Fatal(err)
	}
	rawNew("n2", "New two")
	var n2 int64
	// 6 это обычный повтор шага, 1 это повтор после decrypt-secrets
	for _, from := range []int{6, 1} {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", from)); err != nil {
			t.Fatal(err)
		}
		rerunBefore := time.Now().Unix()
		if err := c.Bootstrap(ctx); err != nil {
			t.Fatalf("bootstrap rerun from %d: %v", from, err)
		}
		rerunAfter := time.Now().Unix()
		stage := fmt.Sprintf("rerun from %d", from)
		if got := userVersion(t, db); got != len(migrations) {
			t.Fatalf("%s: user_version = %d, want %d", stage, got, len(migrations))
		}
		checkColumn(stage)
		if v := appeared("n0"); !v.Valid || v.Int64 != 100 {
			t.Fatalf("%s: n0.appeared_at = %v, want 100", stage, v)
		}
		v := appeared("n2")
		if !v.Valid {
			t.Fatalf("%s: n2.appeared_at is NULL, want stamped", stage)
		}
		if n2 == 0 {
			if v.Int64 < rerunBefore || v.Int64 > rerunAfter {
				t.Fatalf("%s: n2.appeared_at = %d, want within [%d, %d]", stage, v.Int64, rerunBefore, rerunAfter)
			}
			n2 = v.Int64
		} else if v.Int64 != n2 {
			t.Fatalf("%s: n2.appeared_at = %d, want %d from the first rerun", stage, v.Int64, n2)
		}
		checkUnstamped(stage)
	}
}
