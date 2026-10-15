package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
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
