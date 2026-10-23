package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"ripperr/internal/model/storage"
	"ripperr/internal/secrets"
)

const (
	legacyIntegrations = `{"qbit":{"password":"hunter2-xyz"}}`
	legacyAuth         = `{"username":"admin","salt":"s4lt-xyz","hash":"h4sh-xyz","secret":"hm4c-xyz"}`
	legacySchedule     = `{"interval":"1h"}`
	legacyCredentials  = "login_username=u&login_password=p4ss-xyz"
	legacyCookies      = "bb_session=cookie-xyz"
)

func boxFrom(t *testing.T, first byte) *secrets.Box {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = first + byte(i)
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return box
}

func testBox(t *testing.T) *secrets.Box {
	return boxFrom(t, 0x00)
}

func otherBox(t *testing.T) *secrets.Box {
	return boxFrom(t, 0x20)
}

func legacyClient(t *testing.T) (*Client, *sql.DB) {
	t.Helper()
	db := newRawDB(t)
	c := New(db, testBox(t))
	ctx := t.Context()
	if err := c.migrate(ctx, migrations[:1]); err != nil {
		t.Fatalf("migrate to 1: %v", err)
	}
	for _, s := range []struct {
		key, value string
		at         int
	}{
		{SettingIntegrations, legacyIntegrations, 1700000000},
		{SettingAuth, legacyAuth, 1700000001},
		{SettingSchedule, legacySchedule, 1700000002},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)`, s.key, []byte(s.value), s.at); err != nil {
			t.Fatalf("seed setting %s: %v", s.key, err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO trackers (tracker_type_id, tracker_name, domain_name, login_url, download_url, credentials, cookies, publicity, updated_at)
		 VALUES (7, 'Tracker7', 'seven.example', '/login', '/dl', ?, ?, 'private', 1700000003)`,
		legacyCredentials, legacyCookies); err != nil {
		t.Fatalf("seed tracker 7: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO trackers (tracker_type_id, tracker_name, domain_name, credentials, cookies, updated_at)
		 VALUES (8, 'Tracker8', 'eight.example', 'only-creds-xyz', NULL, 1700000004)`); err != nil {
		t.Fatalf("seed tracker 8: %v", err)
	}
	return c, db
}

func migratedClient(t *testing.T) (*Client, *sql.DB) {
	t.Helper()
	c, db := legacyClient(t)
	if err := c.Bootstrap(t.Context()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return c, db
}

func snapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var out []string
	for _, q := range []string{
		`SELECT quote(key) || ',' || quote(value) || ',' || quote(updated_at) FROM settings ORDER BY key`,
		`SELECT quote(tracker_type_id) || ',' || quote(tracker_name) || ',' || quote(domain_name) || ',' || quote(login_url) || ',' ||
		        quote(download_url) || ',' || quote(credentials) || ',' || quote(cookies) || ',' || quote(publicity) || ',' || quote(updated_at)
		 FROM trackers ORDER BY tracker_type_id`,
	} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatalf("snapshot scan: %v", err)
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("snapshot rows: %v", err)
		}
		_ = rows.Close()
	}
	return out
}

func rawSetting(t *testing.T, db *sql.DB, key string) (string, bool) {
	t.Helper()
	var v []byte
	err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("raw setting %s: %v", key, err)
	}
	return string(v), true
}

func TestSettingSealedAtRest(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	if err := c.SaveSetting(ctx, SettingIntegrations, json.RawMessage(legacyIntegrations)); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}
	raw, _ := rawSetting(t, c.db, SettingIntegrations)
	if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, "hunter2-xyz") {
		t.Errorf("raw value = %q, want an enc:v1: envelope", raw)
	}
	got, ok, err := c.GetSetting(ctx, SettingIntegrations)
	if err != nil || !ok || string(got) != legacyIntegrations {
		t.Errorf("GetSetting = %s ok=%v err=%v, want %s", got, ok, err, legacyIntegrations)
	}
}

func TestTrackerSecretsSealedAtRest(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	tr := &storage.Tracker{TypeID: 7, Name: "Tracker7", Domain: "seven.example", Credentials: legacyCredentials, Cookies: legacyCookies}
	if err := c.AddTracker(ctx, tr); err != nil {
		t.Fatalf("AddTracker: %v", err)
	}
	checkRaw := func(secret ...string) {
		t.Helper()
		var cred, cook, name, domain string
		if err := c.db.QueryRow(`SELECT credentials, cookies, tracker_name, domain_name FROM trackers WHERE tracker_type_id = 7`).Scan(&cred, &cook, &name, &domain); err != nil {
			t.Fatalf("raw tracker: %v", err)
		}
		for _, v := range []string{cred, cook} {
			if !strings.HasPrefix(v, "enc:v1:") {
				t.Errorf("raw secret column = %q, want an enc:v1: envelope", v)
			}
			for _, s := range secret {
				if strings.Contains(v, s) {
					t.Errorf("raw secret column holds %q", s)
				}
			}
		}
		if name != "Tracker7" || domain != "seven.example" {
			t.Errorf("tracker_name, domain_name = %q, %q, want plaintext", name, domain)
		}
	}
	checkRaw("p4ss-xyz", "cookie-xyz")

	tr.Credentials, tr.Cookies = "login_password=new-xyz", "bb_session=new-cookie-xyz"
	if err := c.UpdateTracker(ctx, tr); err != nil {
		t.Fatalf("UpdateTracker: %v", err)
	}
	checkRaw("new-xyz", "new-cookie-xyz")
	got, err := c.GetTracker(ctx, 7)
	if err != nil || !got.Exists || got.Credentials != tr.Credentials || got.Cookies != tr.Cookies || got.Name != "Tracker7" {
		t.Errorf("GetTracker = %+v err=%v, want updated secrets", got, err)
	}
}

func TestSealedValuesBoundToRow(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	if err := c.SaveSetting(ctx, SettingIntegrations, json.RawMessage(legacyIntegrations)); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveSetting(ctx, SettingSchedule, json.RawMessage(legacySchedule)); err != nil {
		t.Fatal(err)
	}
	if err := c.AddTracker(ctx, &storage.Tracker{TypeID: 7, Credentials: legacyCredentials, Cookies: legacyCookies}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`UPDATE settings SET value = (SELECT value FROM settings WHERE key = 'integrations') WHERE key = 'schedule'`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`UPDATE trackers SET credentials = cookies WHERE tracker_type_id = 7`); err != nil {
		t.Fatal(err)
	}
	if got, _, err := c.GetSetting(ctx, SettingSchedule); err == nil {
		t.Errorf("GetSetting(schedule) with the integrations envelope = %s, want error, got none", got)
	}
	if got, err := c.GetTracker(ctx, 7); err == nil {
		t.Errorf("GetTracker with cookies in credentials = %+v, want error, got none", got)
	}
}

func TestUnsealedValueRejected(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()
	if _, err := c.db.Exec(`INSERT INTO settings (key, value) VALUES ('integrations', ?)`, []byte(legacyIntegrations)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(`INSERT INTO trackers (tracker_type_id, tracker_name, domain_name, login_url, download_url, credentials, cookies, publicity) VALUES (7, '', '', '', '', ?, ?, '')`, legacyCredentials, legacyCookies); err != nil {
		t.Fatal(err)
	}
	got, _, err := c.GetSetting(ctx, SettingIntegrations)
	if err == nil {
		t.Errorf("GetSetting plaintext row: want error, got value %s", got)
	} else if strings.Contains(err.Error(), "hunter2-xyz") {
		t.Errorf("error leaks the value: %v", err)
	}
	tr, err := c.GetTracker(ctx, 7)
	if err == nil {
		t.Errorf("GetTracker plaintext row: want error, got value %+v", tr)
	} else if strings.Contains(err.Error(), "p4ss-xyz") || strings.Contains(err.Error(), "cookie-xyz") {
		t.Errorf("error leaks the value: %v", err)
	}
}

func TestNilBoxFailsClosed(t *testing.T) {
	db := newRawDB(t)
	c := New(db, nil)
	ctx := t.Context()
	if err := c.Bootstrap(ctx); !errors.Is(err, ErrNoKey) {
		t.Errorf("Bootstrap with nil box err = %v, want ErrNoKey", err)
	}
	if got := userVersion(t, db); got != 1 {
		t.Errorf("user_version = %d, want 1", got)
	}
	if err := c.SaveSetting(ctx, SettingIntegrations, json.RawMessage(legacyIntegrations)); !errors.Is(err, ErrNoKey) {
		t.Errorf("SaveSetting with nil box err = %v, want ErrNoKey", err)
	}
	if err := c.AddTracker(ctx, &storage.Tracker{TypeID: 7, Credentials: legacyCredentials}); !errors.Is(err, ErrNoKey) {
		t.Errorf("AddTracker with nil box err = %v, want ErrNoKey", err)
	}
	var n int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM settings) + (SELECT COUNT(*) FROM trackers)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("rows written with nil box = %d, want 0", n)
	}
}

func TestMigrationEncryptsLegacyRows(t *testing.T) {
	c, db := legacyClient(t)
	ctx := t.Context()
	box := testBox(t)
	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
	var plain int
	if err := db.QueryRow(`SELECT COUNT(*) FROM settings WHERE CAST(value AS TEXT) NOT LIKE 'enc:v1:%'`).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	if plain != 0 {
		t.Errorf("unsealed settings rows = %d, want 0", plain)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM trackers WHERE credentials NOT LIKE 'enc:v1:%' OR cookies NOT LIKE 'enc:v1:%'`).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	if plain != 0 {
		t.Errorf("unsealed trackers columns = %d, want 0", plain)
	}
	for key, want := range map[string]string{SettingIntegrations: legacyIntegrations, SettingAuth: legacyAuth, SettingSchedule: legacySchedule} {
		got, ok, err := c.GetSetting(ctx, key)
		if err != nil || !ok || string(got) != want {
			t.Errorf("GetSetting(%s) = %s ok=%v err=%v, want %s", key, got, ok, err, want)
		}
	}
	tr, err := c.GetTracker(ctx, 7)
	if err != nil || tr.Credentials != legacyCredentials || tr.Cookies != legacyCookies || tr.Name != "Tracker7" {
		t.Errorf("GetTracker(7) = %+v err=%v", tr, err)
	}
	var cred string
	var cook sql.NullString
	if err := db.QueryRow(`SELECT credentials, cookies FROM trackers WHERE tracker_type_id = 8`).Scan(&cred, &cook); err != nil {
		t.Fatal(err)
	}
	if cook.Valid {
		t.Errorf("tracker 8 cookies = %q, want NULL kept", cook.String)
	}
	if got, err := box.Open(cred, "trackers/8/credentials"); err != nil || string(got) != "only-creds-xyz" {
		t.Errorf("tracker 8 credentials open = %q err=%v", got, err)
	}
	var stamps []int
	rows, err := db.Query(`SELECT updated_at FROM settings WHERE key IN ('integrations', 'auth', 'schedule') UNION ALL SELECT updated_at FROM trackers`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s int
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		stamps = append(stamps, s)
	}
	_ = rows.Close()
	slices.Sort(stamps)
	if !slices.Equal(stamps, []int{1700000000, 1700000001, 1700000002, 1700000003, 1700000004}) {
		t.Errorf("updated_at = %v, want untouched", stamps)
	}
	check, ok := rawSetting(t, db, "secrets_check")
	if got, err := box.Open(check, "settings/secrets_check"); !ok || err != nil || string(got) != "ripperr secrets key check v1" {
		t.Errorf("key-check row present=%v open=%q err=%v", ok, got, err)
	}
	if marker, ok := rawSetting(t, db, "secrets_vacuum_pending"); !ok || !secrets.IsSealed(marker) {
		t.Errorf("vacuum marker present=%v sealed=%v, want a sealed row", ok, secrets.IsSealed(marker))
	}
}

func TestVacuumIfPending(t *testing.T) {
	c, db := migratedClient(t)
	ctx := t.Context()
	if done, err := c.VacuumIfPending(ctx); !done || err != nil {
		t.Fatalf("first VacuumIfPending = %v, %v, want true, nil", done, err)
	}
	if _, ok := rawSetting(t, db, "secrets_vacuum_pending"); ok {
		t.Error("marker row still present after vacuum")
	}
	if done, err := c.VacuumIfPending(ctx); done || err != nil {
		t.Errorf("second VacuumIfPending = %v, %v, want false, nil", done, err)
	}

	c2, db2 := migratedClient(t)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c2.VacuumIfPending(cancelled); err == nil {
		t.Error("VacuumIfPending with cancelled ctx: want error, got nil")
	}
	if _, ok := rawSetting(t, db2, "secrets_vacuum_pending"); !ok {
		t.Error("marker row gone after a failed vacuum, want it kept for the retry")
	}
	if done, err := c2.VacuumIfPending(ctx); !done || err != nil {
		t.Errorf("retry VacuumIfPending = %v, %v, want true, nil", done, err)
	}
	if _, ok := rawSetting(t, db2, "secrets_vacuum_pending"); ok {
		t.Error("marker row still present after the retry")
	}
}

func TestMigrationAbortsOnForeignEnvelope(t *testing.T) {
	c, db := legacyClient(t)
	foreign := otherBox(t).Seal([]byte(`{"t":1}`), "settings/trackers")
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('trackers', ?, 1700000005)`, foreign); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, db)
	if err := c.Bootstrap(t.Context()); err == nil {
		t.Error("Bootstrap over a foreign envelope: want error, got nil")
	}
	if got := userVersion(t, db); got != 1 {
		t.Errorf("user_version = %d, want 1", got)
	}
	if after := snapshot(t, db); !slices.Equal(before, after) {
		t.Errorf("rows changed by an aborted migration:\nbefore %q\nafter  %q", before, after)
	}
}

func TestMigrationKeepsOwnEnvelopes(t *testing.T) {
	c, db := migratedClient(t)
	ctx := t.Context()
	before := snapshot(t, db)
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Errorf("user_version = %d, want %d", got, len(migrations))
	}
	if after := snapshot(t, db); !slices.Equal(before, after) {
		t.Errorf("rows changed by a repeated migration:\nbefore %q\nafter  %q", before, after)
	}
	raw, _ := rawSetting(t, db, SettingIntegrations)
	if !secrets.IsSealed(raw) {
		t.Errorf("integrations raw = %q, want sealed", raw)
	}
	if got, _, err := c.GetSetting(ctx, SettingIntegrations); err != nil || string(got) != legacyIntegrations {
		t.Errorf("GetSetting = %s err=%v, want %s", got, err, legacyIntegrations)
	}
}

func TestVerifySecrets(t *testing.T) {
	c, db := legacyClient(t)
	ctx := t.Context()
	if err := c.VerifySecrets(ctx); err != nil {
		t.Errorf("VerifySecrets at version 1 = %v, want nil", err)
	}
	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := c.VerifySecrets(ctx); err != nil {
		t.Errorf("VerifySecrets right key = %v, want nil", err)
	}
	if err := New(db, otherBox(t)).VerifySecrets(ctx); !errors.Is(err, ErrKeyMismatch) {
		t.Errorf("VerifySecrets other key = %v, want ErrKeyMismatch", err)
	}
	if err := New(db, nil).VerifySecrets(ctx); !errors.Is(err, ErrNoKey) {
		t.Errorf("VerifySecrets nil box = %v, want ErrNoKey", err)
	}
	if _, err := db.Exec(`DELETE FROM settings WHERE key = 'secrets_check'`); err != nil {
		t.Fatal(err)
	}
	if err := c.VerifySecrets(ctx); err == nil {
		t.Error("VerifySecrets without the key-check row: want error, got nil")
	}
}

func TestDecryptSecretsRestoresExactRows(t *testing.T) {
	c, db := legacyClient(t)
	ctx := t.Context()
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('empty', ?, 1700000005)`, []byte{}); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, db)
	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := c.DecryptSecrets(ctx); err != nil {
		t.Fatalf("DecryptSecrets: %v", err)
	}
	if after := snapshot(t, db); !slices.Equal(before, after) {
		t.Errorf("rows after decrypt differ:\nbefore %q\nafter  %q", before, after)
	}
	if got := userVersion(t, db); got != 1 {
		t.Errorf("user_version after decrypt = %d, want 1", got)
	}
	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap after decrypt: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Errorf("user_version after re-encrypt = %d, want %d", got, len(migrations))
	}
	if raw, _ := rawSetting(t, db, SettingAuth); !secrets.IsSealed(raw) {
		t.Errorf("auth raw after re-encrypt = %q, want sealed", raw)
	}
}

func TestDecryptSecretsRefuses(t *testing.T) {
	c, db := legacyClient(t)
	ctx := t.Context()
	if err := c.DecryptSecrets(ctx); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("DecryptSecrets at version 1 = %v, want ErrNotEncrypted", err)
	}
	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	before := snapshot(t, db)
	if err := New(db, otherBox(t)).DecryptSecrets(ctx); !errors.Is(err, ErrKeyMismatch) {
		t.Errorf("DecryptSecrets other key = %v, want ErrKeyMismatch", err)
	}
	if after := snapshot(t, db); !slices.Equal(before, after) || userVersion(t, db) != len(migrations) {
		t.Errorf("DB changed by a refused decrypt")
	}
	newer := len(migrations) + 1
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", newer)); err != nil {
		t.Fatal(err)
	}
	if err := c.DecryptSecrets(ctx); err == nil {
		t.Errorf("DecryptSecrets at version %d: want error, got nil", newer)
	}
	if got := userVersion(t, db); got != newer {
		t.Errorf("user_version = %d, want %d", got, newer)
	}
}

func TestSecretsVersionPinned(t *testing.T) {
	if len(migrations) < SecretsVersion {
		t.Fatalf("migration %d missing", SecretsVersion)
	}
	if got := migrations[SecretsVersion-1].name; got != "encrypt secrets" {
		t.Fatalf("migration %d = %q, want encrypt secrets", SecretsVersion, got)
	}
}

func TestSettingsEnvelopeGolden(t *testing.T) {
	c := newTestClient(t)
	// конверт записан один раз: если он перестал открываться, сломаются уже зашифрованные базы
	const golden = "enc:v1:zHOxZFtp/gkuNbEJ/Dr/YxbTqMxXX15gZg4T5fUTMZdGxRQScpYI1VympWATpJFNij5Nfg9o7lu/DQ=="
	if _, err := c.db.Exec(`INSERT INTO settings (key, value) VALUES ('integrations', ?)`, golden); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.GetSetting(t.Context(), SettingIntegrations)
	if err != nil || !ok || string(got) != `{"qbit":{"password":"golden"}}` {
		t.Fatalf("GetSetting = %s ok=%v err=%v", got, ok, err)
	}
}

func TestTrackerEnvelopeGolden(t *testing.T) {
	c := newTestClient(t)
	const golden = "enc:v1:3+iunnlIvyUiN+L3yZjzhI3wvNKfd7nZOSjpHx9CmBrqvG9bnvAIDBS8CSZIBPXRPwzzF6Gqjk70MgcD6WhYnlqTlwc="
	cookies := testBox(t).Seal(nil, "trackers/7/cookies")
	if _, err := c.db.Exec(`INSERT INTO trackers (tracker_type_id, tracker_name, domain_name, login_url, download_url, credentials, cookies, publicity)
		VALUES (7, '', '', '', '', ?, ?, '')`, golden, cookies); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetTracker(t.Context(), 7)
	if err != nil || got.Credentials != legacyCredentials || got.Cookies != "" {
		t.Fatalf("GetTracker = %+v err=%v", got, err)
	}
}

func TestDecryptSecretsKeepsReleaseTimes(t *testing.T) {
	c, db := migratedClient(t)
	ctx := t.Context()
	at := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	if err := c.AddReleaseTime(ctx, "h1", at, 8); err != nil {
		t.Fatal(err)
	}
	rows := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM release_times WHERE url_hash = 'h1' AND released_at = ?`, at.Unix()).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := c.DecryptSecrets(ctx); err != nil {
		t.Fatalf("DecryptSecrets: %v", err)
	}
	if got := userVersion(t, db); got != 1 {
		t.Errorf("user_version after decrypt = %d, want 1", got)
	}
	if n := rows(); n != 1 {
		t.Errorf("release rows after decrypt = %d, want 1", n)
	}
	if err := c.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap after decrypt: %v", err)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Errorf("user_version after bootstrap = %d, want %d", got, len(migrations))
	}
	if n := rows(); n != 1 {
		t.Errorf("release rows after bootstrap = %d, want 1", n)
	}
}
