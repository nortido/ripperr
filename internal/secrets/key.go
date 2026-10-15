package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvKey      = "DB_SECRETS_KEY"
	KeyFileName = "secrets.key"
)

var (
	ErrKeyNotFound = errors.New("secrets: key not found")
	ErrInvalidKey  = errors.New("secrets: invalid key, want base64 of 32 bytes")
)

func ParseKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(key) != keySize {
		return nil, ErrInvalidKey
	}
	return key, nil
}

func LoadKey(dir string, lookupEnv func(string) (string, bool), create bool) (key []byte, source string, err error) {
	if v, ok := lookupEnv(EnvKey); ok && strings.TrimSpace(v) != "" {
		source = "env " + EnvKey
		key, err = ParseKey(v)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", source, err)
		}
		return key, source, nil
	}

	path := filepath.Join(dir, KeyFileName)
	source = "file " + path
	key, err = readKeyFile(path)
	switch {
	case err == nil:
		return key, source, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, "", fmt.Errorf("%s: %w", source, err)
	case !create:
		return nil, "", fmt.Errorf("%s: %w", source, ErrKeyNotFound)
	}

	key, created, err := createKeyFile(dir, path)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", source, err)
	}
	if created {
		source += " (created)"
	}
	return key, source, nil
}

func readKeyFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseKey(string(data))
}

func createKeyFile(dir, path string) (key []byte, created bool, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, err
	}
	key = make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, false, err
	}
	tmp, err := writeTemp(dir, base64.StdEncoding.EncodeToString(key)+"\n")
	if err != nil {
		return nil, false, err
	}
	// Link не трогает существующий файл. При гонке проигравший читает ключ победителя,
	// и secrets.key всегда записан целиком
	linkErr := os.Link(tmp, path)
	_ = os.Remove(tmp)
	created = true
	if errors.Is(linkErr, fs.ErrExist) {
		key, err = readKeyFile(path)
		if err != nil {
			return nil, false, err
		}
		created = false
	} else if linkErr != nil {
		return nil, false, linkErr
	}
	if err := syncDir(dir); err != nil {
		return nil, false, err
	}
	return key, created, nil
}

func writeTemp(dir, content string) (string, error) {
	f, err := os.CreateTemp(dir, ".secrets.key.tmp-*")
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(content)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
