package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	"ripperr/internal/secrets"
	"ripperr/internal/storage/client/sqlite"
)

type OpenInfo struct {
	KeySource string
	Migrated  bool
	Vacuumed  bool
	VacuumErr error

	KeyCheckResealed bool
}

type keyError struct {
	msg string
	err error
}

func (e *keyError) Error() string { return e.msg }

func (e *keyError) Unwrap() error { return e.err }

func Open(ctx context.Context, conn *sql.DB, keyDir string, lookupEnv func(string) (string, bool)) (*Service, OpenInfo, error) {
	var info OpenInfo
	version, err := sqlite.SchemaVersion(ctx, conn)
	if err != nil {
		return nil, info, err
	}
	// новый ключ допустим только до шифрования: для зашифрованной базы он не подойдет,
	// а созданный файл спрятал бы потерю настоящего ключа
	box, source, err := loadBox(keyDir, lookupEnv, version < sqlite.SecretsVersion)
	if err != nil {
		return nil, info, err
	}
	info.KeySource = source
	svc := New(conn, box)
	if err := svc.VerifySecrets(ctx); err != nil {
		return nil, info, mismatchError(err, source)
	}
	if err := svc.Bootstrap(ctx); err != nil {
		return nil, info, err
	}
	// другой процесс мог мигрировать базу с другим ключом между SchemaVersion и Bootstrap
	if err := svc.VerifySecrets(ctx); err != nil {
		return nil, info, mismatchError(err, source)
	}
	if info.KeyCheckResealed, err = svc.ResealKeyCheck(ctx); err != nil {
		return nil, info, mismatchError(err, source)
	}
	info.Migrated = version < sqlite.SecretsVersion
	// VACUUM проверяем на каждом старте: маркер переживает упавший VACUUM
	// или падение процесса между коммитом миграции и VACUUM
	info.Vacuumed, info.VacuumErr = svc.VacuumIfPending(ctx)
	return svc, info, nil
}

func DecryptSecrets(ctx context.Context, conn *sql.DB, keyDir string, lookupEnv func(string) (string, bool)) error {
	version, err := sqlite.SchemaVersion(ctx, conn)
	if err != nil {
		return err
	}
	if version < sqlite.SecretsVersion {
		return sqlite.ErrNotEncrypted
	}
	box, source, err := loadBox(keyDir, lookupEnv, false)
	if err != nil {
		return err
	}
	return mismatchError(sqlite.New(conn, box).DecryptSecrets(ctx), source)
}

func loadBox(keyDir string, lookupEnv func(string) (string, bool), create bool) (*secrets.Box, string, error) {
	key, source, err := secrets.LoadKey(keyDir, lookupEnv, create)
	if errors.Is(err, secrets.ErrKeyNotFound) {
		path := filepath.Join(keyDir, secrets.KeyFileName)
		return nil, "", &keyError{"secrets: database is encrypted and has no key, set " + secrets.EnvKey + " or restore " + path, err}
	}
	if err != nil {
		// LoadKey пишет "<source>: secrets: ...", второй префикс в строке лога лишний
		return nil, "", &keyError{"secrets: " + strings.Replace(err.Error(), ": secrets: ", ": ", 1), err}
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		return nil, "", err
	}
	return box, source, nil
}

func mismatchError(err error, source string) error {
	if errors.Is(err, sqlite.ErrKeyMismatch) {
		return &keyError{"secrets: key from " + source + " does not match this database", err}
	}
	return err
}
