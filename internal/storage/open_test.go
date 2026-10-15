package storage

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ripperr/internal/secrets"
	"ripperr/internal/storage/client/sqlite"
)

const (
	liveSecret    = "live-secret-ABC"
	remnantSecret = "remnant-secret-XYZ"
	liveValue     = `{"qbit":{"password":"` + liveSecret + `"}}`
	newKeyCheck   = "ripperr secrets key check v1"
	// свой литерал, а не константа из sqlite: опечатка в ней не должна проходить тесты
	oldKeyCheck = "trackerr secrets key check v1"
)

func noEnv(string) (string, bool) { return "", false }

func envKey(value string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if name == secrets.EnvKey {
			return value, true
		}
		return "", false
	}
}

func otherKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
}

func dbFile(dir string) string { return filepath.Join(dir, "ripperr.db") }

func keyFile(dir string) string { return filepath.Join(dir, secrets.KeyFileName) }

func openConn(t *testing.T, dir string) *sql.DB {
	t.Helper()
	// DSN как в cmd/main.go connectDB
	dsn := dbFile(dir) + "?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)&_txlock=immediate"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.PingContext(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return conn
}

func exec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func legacyDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	conn := openConn(t, dir)
	exec(t, conn, `CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL DEFAULT (unixepoch()))`)
	exec(t, conn, `CREATE TABLE IF NOT EXISTS trackers (tracker_type_id INTEGER PRIMARY KEY, tracker_name TEXT, domain_name TEXT, login_url TEXT, download_url TEXT, credentials TEXT, cookies TEXT, publicity TEXT, updated_at INTEGER)`)
	exec(t, conn, `PRAGMA user_version = 1`)
	exec(t, conn, `PRAGMA secure_delete = OFF`)
	exec(t, conn, `INSERT INTO settings (key, value, updated_at) VALUES ('old', ?, 100)`, []byte(`{"p":"`+remnantSecret+`"}`))
	exec(t, conn, `INSERT INTO settings (key, value, updated_at) VALUES ('integrations', ?, 200)`, []byte(liveValue))
	exec(t, conn, `INSERT INTO trackers (tracker_type_id, tracker_name, domain_name, credentials, cookies, updated_at) VALUES (7, 'rt', 'rt.org', 'login_password=p4ss', NULL, 300)`)
	exec(t, conn, `DELETE FROM settings WHERE key = 'old'`)
	if !fileContains(t, dbFile(dir), remnantSecret) {
		t.Fatalf("precondition: %s not in the db file", remnantSecret)
	}
	return dir, conn
}

func fileContains(t *testing.T, path, s string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return bytes.Contains(data, []byte(s))
}

func fileSum(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return sha256.Sum256(data)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func schemaVersion(t *testing.T, conn *sql.DB) int {
	t.Helper()
	v, err := sqlite.SchemaVersion(t.Context(), conn)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	return v
}

func markerRows(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var n int
	if err := conn.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM settings WHERE key = 'secrets_vacuum_pending'`).Scan(&n); err != nil {
		t.Fatalf("count marker: %v", err)
	}
	return n
}

func mustOpen(t *testing.T, conn *sql.DB, dir string, lookupEnv func(string) (string, bool)) (*Service, OpenInfo) {
	t.Helper()
	svc, info, err := Open(t.Context(), conn, dir, lookupEnv)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return svc, info
}

func rawRows(t *testing.T, conn *sql.DB) []string {
	t.Helper()
	var out []string
	for _, q := range []string{
		`SELECT key, typeof(value), quote(value), updated_at FROM settings ORDER BY key`,
		`SELECT tracker_type_id, quote(tracker_name), quote(domain_name), quote(login_url), quote(download_url), typeof(credentials), quote(credentials), typeof(cookies), quote(cookies), quote(publicity), quote(updated_at) FROM trackers ORDER BY tracker_type_id`,
	} {
		rows, err := conn.QueryContext(t.Context(), q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan: %v", err)
			}
			parts := make([]string, len(vals))
			for i, v := range vals {
				parts[i] = v.String
			}
			out = append(out, strings.Join(parts, "|"))
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		_ = rows.Close()
	}
	return out
}

func TestOpenCreatesKeyForFreshDB(t *testing.T) {
	dir := t.TempDir()
	conn := openConn(t, dir)

	_, info := mustOpen(t, conn, dir, noEnv)

	if !strings.HasSuffix(info.KeySource, "(created)") {
		t.Fatalf("KeySource = %q, want suffix (created)", info.KeySource)
	}
	st, err := os.Stat(keyFile(dir))
	if err != nil {
		t.Fatalf("key file: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, want 0600", st.Mode().Perm())
	}
	if v := schemaVersion(t, conn); v != sqlite.SecretsVersion {
		t.Fatalf("version = %d, want %d", v, sqlite.SecretsVersion)
	}
}

func TestOpenPrefersEnvKey(t *testing.T) {
	dir := t.TempDir()
	conn := openConn(t, dir)

	_, info := mustOpen(t, conn, dir, envKey(otherKey()))

	if info.KeySource != "env "+secrets.EnvKey {
		t.Fatalf("KeySource = %q", info.KeySource)
	}
	if _, err := os.Stat(keyFile(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file must not exist, stat err = %v", err)
	}
}

func TestOpenRefusesInvalidEnvKey(t *testing.T) {
	dir := t.TempDir()
	conn := openConn(t, dir)
	sum := fileSum(t, dbFile(dir))

	_, _, err := Open(t.Context(), conn, dir, envKey("not-a-key"))

	if !errors.Is(err, secrets.ErrInvalidKey) {
		t.Fatalf("err = %v, want ErrInvalidKey", err)
	}
	if want := "secrets: env " + secrets.EnvKey + ": invalid key, want base64 of 32 bytes"; err.Error() != want {
		t.Fatalf("err text = %q, want %q", err, want)
	}
	if _, err := os.Stat(keyFile(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file must not exist, stat err = %v", err)
	}
	if fileSum(t, dbFile(dir)) != sum {
		t.Fatal("db file changed")
	}
}

func TestOpenRefusesEncryptedDBWithoutKey(t *testing.T) {
	dir := t.TempDir()
	conn := openConn(t, dir)
	mustOpen(t, conn, dir, noEnv)
	if err := os.Remove(keyFile(dir)); err != nil {
		t.Fatal(err)
	}
	sum := fileSum(t, dbFile(dir))
	names := dirNames(t, dir)

	_, _, err := Open(t.Context(), conn, dir, noEnv)

	if !errors.Is(err, secrets.ErrKeyNotFound) {
		t.Fatalf("err = %v, want ErrKeyNotFound", err)
	}
	if want := "set " + secrets.EnvKey + " or restore " + keyFile(dir); !strings.HasPrefix(err.Error(), "secrets: ") || !strings.Contains(err.Error(), want) {
		t.Fatalf("err text = %q, want prefix secrets: and %q", err, want)
	}
	if _, err := os.Stat(keyFile(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file must not be created, stat err = %v", err)
	}
	if got := dirNames(t, dir); !slices.Equal(got, names) {
		t.Fatalf("dir = %v, want %v", got, names)
	}
	if fileSum(t, dbFile(dir)) != sum {
		t.Fatal("db file changed")
	}
}

func TestOpenRefusesWrongKey(t *testing.T) {
	dir := t.TempDir()
	conn := openConn(t, dir)
	mustOpen(t, conn, dir, noEnv)
	sum := fileSum(t, dbFile(dir))
	names := dirNames(t, dir)

	_, _, err := Open(t.Context(), conn, dir, envKey(otherKey()))

	if !errors.Is(err, sqlite.ErrKeyMismatch) {
		t.Fatalf("err = %v, want ErrKeyMismatch", err)
	}
	if want := "secrets: key from env " + secrets.EnvKey + " does not match this database"; err.Error() != want {
		t.Fatalf("err text = %q, want %q", err, want)
	}
	if got := dirNames(t, dir); !slices.Equal(got, names) {
		t.Fatalf("dir = %v, want %v", got, names)
	}
	if fileSum(t, dbFile(dir)) != sum {
		t.Fatal("db file changed")
	}
}

func TestOpenEncryptsLegacyDBAndPurgesRemnants(t *testing.T) {
	dir, conn := legacyDB(t)

	svc, info := mustOpen(t, conn, dir, noEnv)

	for _, s := range []string{remnantSecret, liveSecret} {
		if fileContains(t, dbFile(dir), s) {
			t.Fatalf("db file still contains %s", s)
		}
	}
	if !info.Migrated || !info.Vacuumed || info.VacuumErr != nil {
		t.Fatalf("info = %+v, want Migrated, Vacuumed, no VacuumErr", info)
	}
	got, ok, err := svc.GetSetting(t.Context(), "integrations")
	if err != nil || !ok || string(got) != liveValue {
		t.Fatalf("GetSetting = %q, %v, %v", got, ok, err)
	}
	if n := markerRows(t, conn); n != 0 {
		t.Fatalf("marker rows = %d, want 0", n)
	}
}

func TestOpenRetriesPendingVacuum(t *testing.T) {
	dir, conn := legacyDB(t)
	key, _, err := secrets.LoadKey(dir, noEnv, true)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	// падение процесса после коммита миграции и до VACUUM
	if err := sqlite.New(conn, box).Bootstrap(t.Context()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if !fileContains(t, dbFile(dir), remnantSecret) || markerRows(t, conn) != 1 {
		t.Fatal("precondition: remnant and marker must survive the migration")
	}

	_, info := mustOpen(t, conn, dir, noEnv)

	if fileContains(t, dbFile(dir), remnantSecret) {
		t.Fatal("remnant survived")
	}
	if info.Migrated || !info.Vacuumed || info.VacuumErr != nil {
		t.Fatalf("info = %+v, want not Migrated, Vacuumed", info)
	}
	if n := markerRows(t, conn); n != 0 {
		t.Fatalf("marker rows = %d, want 0", n)
	}

	_, info = mustOpen(t, conn, dir, noEnv)
	if info.Vacuumed {
		t.Fatalf("second Open: info = %+v, want not Vacuumed", info)
	}
}

func TestDecryptSecretsRoundTrip(t *testing.T) {
	dir, conn := legacyDB(t)
	before := rawRows(t, conn)
	mustOpen(t, conn, dir, noEnv)
	keyBytes, err := os.ReadFile(keyFile(dir))
	if err != nil {
		t.Fatal(err)
	}

	if err := DecryptSecrets(t.Context(), conn, dir, noEnv); err != nil {
		t.Fatalf("DecryptSecrets: %v", err)
	}

	if after := rawRows(t, conn); !reflect.DeepEqual(after, before) {
		t.Fatalf("rows after decrypt:\n%v\nwant:\n%v", after, before)
	}
	if v := schemaVersion(t, conn); v != sqlite.SecretsVersion-1 {
		t.Fatalf("version = %d, want %d", v, sqlite.SecretsVersion-1)
	}

	_, info := mustOpen(t, conn, dir, noEnv)
	if !info.Migrated {
		t.Fatalf("info = %+v, want Migrated", info)
	}
	if again, err := os.ReadFile(keyFile(dir)); err != nil || !bytes.Equal(again, keyBytes) {
		t.Fatalf("key file changed: %v", err)
	}
	var raw string
	if err := conn.QueryRowContext(t.Context(), `SELECT credentials FROM trackers WHERE tracker_type_id = 7`).Scan(&raw); err != nil || !secrets.IsSealed(raw) {
		t.Fatalf("credentials not sealed again: %q, %v", raw, err)
	}
}

func TestDecryptSecretsWithoutKey(t *testing.T) {
	dir := t.TempDir()
	conn := openConn(t, dir)
	svc, _ := mustOpen(t, conn, dir, noEnv)
	if err := svc.SaveSetting(t.Context(), "integrations", []byte(liveValue)); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}
	if err := os.Remove(keyFile(dir)); err != nil {
		t.Fatal(err)
	}
	sum := fileSum(t, dbFile(dir))
	names := dirNames(t, dir)

	err := DecryptSecrets(t.Context(), conn, dir, noEnv)

	if !errors.Is(err, secrets.ErrKeyNotFound) {
		t.Fatalf("err = %v, want ErrKeyNotFound", err)
	}
	if got := dirNames(t, dir); !slices.Equal(got, names) {
		t.Fatalf("dir = %v, want %v", got, names)
	}
	if fileSum(t, dbFile(dir)) != sum {
		t.Fatal("db file changed")
	}
}

func TestDecryptSecretsNotEncrypted(t *testing.T) {
	dir, conn := legacyDB(t)
	before := rawRows(t, conn)

	err := DecryptSecrets(t.Context(), conn, dir, noEnv)

	if !errors.Is(err, sqlite.ErrNotEncrypted) {
		t.Fatalf("err = %v, want ErrNotEncrypted", err)
	}
	if _, err := os.Stat(keyFile(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file must not be created, stat err = %v", err)
	}
	if after := rawRows(t, conn); !reflect.DeepEqual(after, before) {
		t.Fatal("rows changed")
	}
}

func fileBox(t *testing.T, dir string) *secrets.Box {
	t.Helper()
	key, _, err := secrets.LoadKey(dir, noEnv, false)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return box
}

func setKeyCheck(t *testing.T, conn *sql.DB, value string) {
	t.Helper()
	res, err := conn.ExecContext(t.Context(), `UPDATE settings SET value = ? WHERE key = 'secrets_check'`, value)
	if err != nil {
		t.Fatalf("set key check: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("set key check: %d rows, %v", n, err)
	}
}

func rawKeyCheck(t *testing.T, conn *sql.DB) string {
	t.Helper()
	var raw string
	if err := conn.QueryRowContext(t.Context(), `SELECT value FROM settings WHERE key = 'secrets_check'`).Scan(&raw); err != nil {
		t.Fatalf("read key check: %v", err)
	}
	return raw
}

func openKeyCheck(t *testing.T, conn *sql.DB, box *secrets.Box) string {
	t.Helper()
	plain, err := box.Open(rawKeyCheck(t, conn), "settings/secrets_check")
	if err != nil {
		t.Fatalf("open key check: %v", err)
	}
	return string(plain)
}

func withoutKeyCheck(rows []string) []string {
	return slices.DeleteFunc(slices.Clone(rows), func(r string) bool { return strings.HasPrefix(r, "secrets_check|") })
}

func TestOpenResealsOldKeyCheck(t *testing.T) {
	dir := t.TempDir()
	conn := openConn(t, dir)
	svc, info := mustOpen(t, conn, dir, noEnv)
	box := fileBox(t, dir)
	if info.KeyCheckResealed {
		t.Fatalf("fresh Open: info = %+v, want not KeyCheckResealed", info)
	}
	if got := openKeyCheck(t, conn, box); got != newKeyCheck {
		t.Fatalf("fresh key check = %q, want %q", got, newKeyCheck)
	}
	if err := svc.SaveSetting(t.Context(), "integrations", []byte(liveValue)); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}
	setKeyCheck(t, conn, box.Seal([]byte(oldKeyCheck), "settings/secrets_check"))
	others := withoutKeyCheck(rawRows(t, conn))

	svc, info = mustOpen(t, conn, dir, noEnv)

	if !info.KeyCheckResealed {
		t.Fatalf("info = %+v, want KeyCheckResealed", info)
	}
	if got := openKeyCheck(t, conn, box); got != newKeyCheck {
		t.Fatalf("key check = %q, want %q", got, newKeyCheck)
	}
	if got := withoutKeyCheck(rawRows(t, conn)); !reflect.DeepEqual(got, others) {
		t.Fatalf("other rows changed:\n%v\nwant:\n%v", got, others)
	}
	got, ok, err := svc.GetSetting(t.Context(), "integrations")
	if err != nil || !ok || string(got) != liveValue {
		t.Fatalf("GetSetting = %q, %v, %v", got, ok, err)
	}

	sum := fileSum(t, dbFile(dir))
	if _, info = mustOpen(t, conn, dir, noEnv); info.KeyCheckResealed {
		t.Fatalf("second Open: info = %+v, want not KeyCheckResealed", info)
	}
	if fileSum(t, dbFile(dir)) != sum {
		t.Fatal("db file changed on Open with nothing to re-seal")
	}
}

func TestOpenOldKeyCheckWrongKey(t *testing.T) {
	dir := t.TempDir()
	conn := openConn(t, dir)
	mustOpen(t, conn, dir, noEnv)
	setKeyCheck(t, conn, fileBox(t, dir).Seal([]byte(oldKeyCheck), "settings/secrets_check"))
	sum := fileSum(t, dbFile(dir))
	names := dirNames(t, dir)

	_, _, err := Open(t.Context(), conn, dir, envKey(otherKey()))

	if !errors.Is(err, sqlite.ErrKeyMismatch) {
		t.Fatalf("err = %v, want ErrKeyMismatch", err)
	}
	if got := dirNames(t, dir); !slices.Equal(got, names) {
		t.Fatalf("dir = %v, want %v", got, names)
	}
	if fileSum(t, dbFile(dir)) != sum {
		t.Fatal("db file changed")
	}
	if _, info := mustOpen(t, conn, dir, noEnv); !info.KeyCheckResealed {
		t.Fatalf("info = %+v, want KeyCheckResealed", info)
	}
}

func TestOpenRejectsForgedKeyCheck(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    func(box *secrets.Box) string
		mismatch bool
	}{
		{"old value under another AAD", func(box *secrets.Box) string {
			return box.Seal([]byte(oldKeyCheck), "settings/integrations")
		}, true},
		{"old value unsealed", func(*secrets.Box) string { return oldKeyCheck }, false},
		{"unknown value", func(box *secrets.Box) string {
			return box.Seal([]byte("ripperr secrets key check v2"), "settings/secrets_check")
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			conn := openConn(t, dir)
			mustOpen(t, conn, dir, noEnv)
			setKeyCheck(t, conn, tc.value(fileBox(t, dir)))
			sum := fileSum(t, dbFile(dir))

			_, _, err := Open(t.Context(), conn, dir, noEnv)

			if err == nil {
				t.Fatal("Open accepted a forged key check")
			}
			if tc.mismatch && !errors.Is(err, sqlite.ErrKeyMismatch) {
				t.Fatalf("err = %v, want ErrKeyMismatch", err)
			}
			if fileSum(t, dbFile(dir)) != sum {
				t.Fatal("db file changed")
			}
		})
	}
}

func TestOpenResealFailureKeepsOldValue(t *testing.T) {
	dir := t.TempDir()
	conn := openConn(t, dir)
	mustOpen(t, conn, dir, noEnv)
	setKeyCheck(t, conn, fileBox(t, dir).Seal([]byte(oldKeyCheck), "settings/secrets_check"))
	exec(t, conn, `CREATE TRIGGER block_kcv BEFORE UPDATE ON settings WHEN NEW.key = 'secrets_check' BEGIN SELECT RAISE(ABORT, 'blocked'); END`)
	raw := rawKeyCheck(t, conn)

	_, _, err := Open(t.Context(), conn, dir, noEnv)

	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("err = %v, want the trigger error", err)
	}
	if got := rawKeyCheck(t, conn); got != raw {
		t.Fatalf("key check row = %q, want %q", got, raw)
	}
	exec(t, conn, `DROP TRIGGER block_kcv`)
	if _, info := mustOpen(t, conn, dir, noEnv); !info.KeyCheckResealed {
		t.Fatalf("info = %+v, want KeyCheckResealed", info)
	}
}

func TestDecryptSecretsAcceptsOldKeyCheck(t *testing.T) {
	dir, conn := legacyDB(t)
	before := rawRows(t, conn)
	mustOpen(t, conn, dir, noEnv)
	setKeyCheck(t, conn, fileBox(t, dir).Seal([]byte(oldKeyCheck), "settings/secrets_check"))

	if err := DecryptSecrets(t.Context(), conn, dir, noEnv); err != nil {
		t.Fatalf("DecryptSecrets: %v", err)
	}

	if after := rawRows(t, conn); !reflect.DeepEqual(after, before) {
		t.Fatalf("rows after decrypt:\n%v\nwant:\n%v", after, before)
	}
	if v := schemaVersion(t, conn); v != sqlite.SecretsVersion-1 {
		t.Fatalf("version = %d, want %d", v, sqlite.SecretsVersion-1)
	}
}
