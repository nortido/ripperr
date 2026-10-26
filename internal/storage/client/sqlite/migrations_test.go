package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
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
