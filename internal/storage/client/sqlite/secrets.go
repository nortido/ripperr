package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"ripperr/internal/model/storage"
	"ripperr/internal/secrets"
)

const SecretsVersion = 2

const (
	trackerCredentials = "credentials"
	trackerCookies     = "cookies"

	keyCheckSetting = "secrets_check"
	keyCheckValue   = "ripperr secrets key check v1"
	// это значение лежит в базах, зашифрованных до переименования: принимаем и перезапечатываем
	oldKeyCheckValue    = "trackerr secrets key check v1"
	vacuumMarkerSetting = "secrets_vacuum_pending"
)

var (
	ErrNoKey        = errors.New("secrets: no key")
	ErrKeyMismatch  = errors.New("secrets: key does not match this database")
	ErrNotEncrypted = errors.New("secrets: database is not encrypted")

	errKeyCheckMissing = errors.New("secrets: key check row is missing")
)

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type secretCell struct {
	label  string
	aad    string
	value  []byte
	null   bool
	text   bool
	update string
	id     any
}

func settingAAD(key string) string {
	return "settings/" + key
}

func trackerAAD(id int, column string) string {
	return "trackers/" + strconv.Itoa(id) + "/" + column
}

func (c *Client) seal(plain []byte, aad string) (string, error) {
	if c.box == nil {
		return "", ErrNoKey
	}
	return c.box.Seal(plain, aad), nil
}

func (c *Client) open(envelope, aad string) ([]byte, error) {
	if c.box == nil {
		return nil, ErrNoKey
	}
	return c.box.Open(envelope, aad)
}

func (c *Client) openSetting(key, envelope string) ([]byte, error) {
	plain, err := c.open(envelope, settingAAD(key))
	if err != nil {
		return nil, fmt.Errorf("settings %s: %w", key, err)
	}
	return plain, nil
}

func (c *Client) openTracker(id int, column, envelope string) (string, error) {
	plain, err := c.open(envelope, trackerAAD(id, column))
	if err != nil {
		return "", fmt.Errorf("tracker %d %s: %w", id, column, err)
	}
	return string(plain), nil
}

func (c *Client) sealTracker(t *storage.Tracker) (cookies, credentials string, err error) {
	if cookies, err = c.seal([]byte(t.Cookies), trackerAAD(t.TypeID, trackerCookies)); err != nil {
		return
	}
	credentials, err = c.seal([]byte(t.Credentials), trackerAAD(t.TypeID, trackerCredentials))
	return
}

func SchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func (c *Client) checkKey(ctx context.Context, q rowQuerier) (old bool, err error) {
	// имя строки и ее AAD лежат в зашифрованных базах, их не менять никогда. Значение
	// сменилось один раз: старое принимается и перезапечатывается в ResealKeyCheck
	var raw []byte
	err = q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, keyCheckSetting).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errKeyCheckMissing
	}
	if err != nil {
		return false, err
	}
	plain, err := c.open(string(raw), settingAAD(keyCheckSetting))
	switch {
	case errors.Is(err, secrets.ErrOpen):
		return false, ErrKeyMismatch
	case err != nil:
		return false, fmt.Errorf("settings %s: %w", keyCheckSetting, err)
	case string(plain) == oldKeyCheckValue:
		return true, nil
	case string(plain) != keyCheckValue:
		return false, ErrKeyMismatch
	}
	return false, nil
}

func (c *Client) VerifySecrets(ctx context.Context) error {
	version, err := SchemaVersion(ctx, c.db)
	if err != nil {
		return err
	}
	if version < SecretsVersion {
		return nil
	}
	if c.box == nil {
		return ErrNoKey
	}
	_, err = c.checkKey(ctx, c.db)
	return err
}

func (c *Client) ResealKeyCheck(ctx context.Context) (resealed bool, err error) {
	var tx *sql.Tx
	if tx, err = c.db.BeginTx(ctx, nil); err != nil {
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return
	}
	if version < SecretsVersion {
		err = tx.Commit()
		return
	}
	if c.box == nil {
		err = ErrNoKey
		return
	}
	var old bool
	if old, err = c.checkKey(ctx, tx); err != nil {
		return
	}
	if !old {
		err = tx.Commit()
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE settings SET value = ? WHERE key = ?`,
		c.box.Seal([]byte(keyCheckValue), settingAAD(keyCheckSetting)), keyCheckSetting); err != nil {
		return
	}
	if err = tx.Commit(); err != nil {
		return
	}
	return true, nil
}

func readSecretCells(ctx context.Context, tx *sql.Tx) ([]secretCell, error) {
	var cells []secretCell
	rows, err := tx.QueryContext(ctx, `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		var value []byte
		if err = rows.Scan(&key, &value); err != nil {
			_ = rows.Close()
			return nil, err
		}
		cells = append(cells, secretCell{
			label: "settings " + key, aad: settingAAD(key), value: value,
			update: `UPDATE settings SET value = ? WHERE key = ?`, id: key,
		})
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	rows, err = tx.QueryContext(ctx, `SELECT tracker_type_id, credentials, cookies FROM trackers ORDER BY tracker_type_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int
		var credentials, cookies sql.NullString
		if err = rows.Scan(&id, &credentials, &cookies); err != nil {
			_ = rows.Close()
			return nil, err
		}
		for _, col := range []struct {
			name  string
			value sql.NullString
		}{{trackerCredentials, credentials}, {trackerCookies, cookies}} {
			cells = append(cells, secretCell{
				label: fmt.Sprintf("tracker %d %s", id, col.name), aad: trackerAAD(id, col.name),
				value: []byte(col.value.String), null: !col.value.Valid, text: true,
				update: `UPDATE trackers SET ` + col.name + ` = ? WHERE tracker_type_id = ?`, id: id,
			})
		}
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return cells, rows.Err()
}

func encryptSecrets(ctx context.Context, c *Client, tx *sql.Tx) error {
	if c.box == nil {
		return ErrNoKey
	}
	switch _, err := c.checkKey(ctx, tx); {
	case errors.Is(err, errKeyCheckMissing):
		if _, err = tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`,
			keyCheckSetting, c.box.Seal([]byte(keyCheckValue), settingAAD(keyCheckSetting))); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
		vacuumMarkerSetting, c.box.Seal(nil, settingAAD(vacuumMarkerSetting))); err != nil {
		return err
	}

	cells, err := readSecretCells(ctx, tx)
	if err != nil {
		return err
	}
	want := make(map[string][]byte, len(cells))
	for _, cell := range cells {
		if cell.null {
			continue
		}
		if secrets.IsSealed(string(cell.value)) {
			plain, err := c.box.Open(string(cell.value), cell.aad)
			if err != nil {
				return fmt.Errorf("%s: %w", cell.label, err)
			}
			want[cell.aad] = plain
			continue
		}
		want[cell.aad] = cell.value
		if _, err = tx.ExecContext(ctx, cell.update, c.box.Seal(cell.value, cell.aad), cell.id); err != nil {
			return fmt.Errorf("%s: %w", cell.label, err)
		}
	}

	if cells, err = readSecretCells(ctx, tx); err != nil {
		return err
	}
	sealed := 0
	for _, cell := range cells {
		w, ok := want[cell.aad]
		if cell.null {
			if ok {
				return fmt.Errorf("%s: verify: value became NULL", cell.label)
			}
			continue
		}
		sealed++
		plain, err := c.box.Open(string(cell.value), cell.aad)
		if err != nil || !ok || !bytes.Equal(plain, w) {
			return fmt.Errorf("%s: verify: sealed value does not match the original", cell.label)
		}
	}
	if sealed != len(want) {
		return fmt.Errorf("verify: %d sealed values, want %d", sealed, len(want))
	}
	return nil
}

func (c *Client) DecryptSecrets(ctx context.Context) (err error) {
	var tx *sql.Tx
	if tx, err = c.db.BeginTx(ctx, nil); err != nil {
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return
	}
	if version < SecretsVersion {
		err = ErrNotEncrypted
		return
	}
	if version > SecretsVersion {
		err = fmt.Errorf("schema version %d is newer than %d, decrypt it with the build that wrote it", version, SecretsVersion)
		return
	}
	if c.box == nil {
		err = ErrNoKey
		return
	}
	if _, err = c.checkKey(ctx, tx); err != nil {
		return
	}
	var cells []secretCell
	if cells, err = readSecretCells(ctx, tx); err != nil {
		return
	}
	for _, cell := range cells {
		if cell.null || cell.aad == settingAAD(keyCheckSetting) || cell.aad == settingAAD(vacuumMarkerSetting) {
			continue
		}
		var plain []byte
		if plain, err = c.box.Open(string(cell.value), cell.aad); err != nil {
			err = fmt.Errorf("%s: %w", cell.label, err)
			return
		}
		// класс хранения тот же, что пишет код без шифрования: settings как BLOB (SaveSetting),
		// трекеры как TEXT (Add/UpdateTracker). nil-срез драйвер запишет как NULL,
		// а settings.value NOT NULL
		var arg any = append([]byte{}, plain...)
		if cell.text {
			arg = string(plain)
		}
		if _, err = tx.ExecContext(ctx, cell.update, arg, cell.id); err != nil {
			err = fmt.Errorf("%s: %w", cell.label, err)
			return
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key IN (?, ?)`, keyCheckSetting, vacuumMarkerSetting); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", SecretsVersion-1)); err != nil {
		return
	}
	err = tx.Commit()
	return
}

func (c *Client) VacuumIfPending(ctx context.Context) (bool, error) {
	var pending int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = ?`, vacuumMarkerSetting).Scan(&pending); err != nil {
		return false, err
	}
	if pending == 0 {
		return false, nil
	}
	// маркер удаляется только после успешного VACUUM: упавший или прерванный VACUUM
	// оставляет его, и следующий старт пересоберет файл еще раз
	if _, err := c.db.ExecContext(ctx, "VACUUM"); err != nil {
		return false, fmt.Errorf("vacuum: %w", err)
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, vacuumMarkerSetting); err != nil {
		return false, err
	}
	return true, nil
}
