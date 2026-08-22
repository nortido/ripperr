package sqlite

import (
	"database/sql"
	"path/filepath"
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
	c := New(db)
	if err := c.Bootstrap(t.Context()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
}

func TestMigrateSkipsAlreadyApplied(t *testing.T) {
	db := newRawDB(t)
	c := New(db)
	if err := c.Bootstrap(t.Context()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// вторая миграция с заведомо ломающим оператором: если applied уже учтён,
	// она не должна выполниться повторно после того, как её версия записана
	boom := []migration{
		{name: "init", stmts: migrations[0].stmts},
		{name: "boom", stmts: []string{"CREATE TABLE boom (id INTEGER PRIMARY KEY)"}},
	}
	if err := c.migrate(t.Context(), boom); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := userVersion(t, db); got != 2 {
		t.Fatalf("user_version = %d, want 2", got)
	}
	// повторный прогон не должен упасть на CREATE TABLE boom без IF NOT EXISTS
	if err := c.migrate(t.Context(), boom); err != nil {
		t.Fatalf("migrate rerun: %v", err)
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	db := newRawDB(t)
	c := New(db)
	if _, err := db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatalf("set version: %v", err)
	}
	if err := c.Bootstrap(t.Context()); err == nil {
		t.Fatal("want error on schema newer than binary, got nil")
	}
}

func TestMigrateRollsBackFailedStep(t *testing.T) {
	db := newRawDB(t)
	c := New(db)
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

// боевая база создана до версионирования: таблицы уже стоят, user_version = 0.
// init обязан пройти по ней вхолостую и просто проставить версию.
func TestMigrateOverLegacyDB(t *testing.T) {
	db := newRawDB(t)
	c := New(db)
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

// версия перечитывается уже под блокировкой записи: если шаг успел накатить другой
// процесс, пока мы ждали, повторно выполнять его нельзя
func TestApplyMigrationSkipsWhenVersionAlreadyReached(t *testing.T) {
	db := newRawDB(t)
	c := New(db)
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
