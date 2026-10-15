package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func noEnv(string) (string, bool) { return "", false }

func envWith(v string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		if k == EnvKey {
			return v, true
		}
		return "", false
	}
}

func randKey(t *testing.T) ([]byte, string) {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k, base64.StdEncoding.EncodeToString(k)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func modTime(t *testing.T, path string) time.Time {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.ModTime()
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	for _, n := range dirNames(t, dir) {
		if strings.HasPrefix(n, ".secrets.key.tmp-") {
			t.Errorf("temp file left: %s", n)
		}
	}
}

func TestParseKey(t *testing.T) {
	k, s := randKey(t)
	got, err := ParseKey(" \n" + s + "\n\t ")
	if err != nil || !bytes.Equal(got, k) {
		t.Fatalf("valid key: got %x, err %v", got, err)
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	for _, in := range []string{"not*base64*at*all", short} {
		got, err := ParseKey(in)
		if !errors.Is(err, ErrInvalidKey) || got != nil {
			t.Errorf("ParseKey(%q): got %x, err %v, want ErrInvalidKey", in, got, err)
		}
		if err != nil && strings.Contains(err.Error(), in) {
			t.Errorf("error text leaks input: %v", err)
		}
	}
}

func TestLoadKeyEnvWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFileName)
	_, fileKey := randKey(t)
	writeFile(t, path, fileKey+"\n")
	before := modTime(t, path)
	envKey, envStr := randKey(t)

	got, src, err := LoadKey(dir, envWith(envStr), true)
	if err != nil || !bytes.Equal(got, envKey) || src != "env DB_SECRETS_KEY" {
		t.Fatalf("got %x %q %v", got, src, err)
	}
	if !bytes.Equal(readFile(t, path), []byte(fileKey+"\n")) || !modTime(t, path).Equal(before) {
		t.Fatal("key file touched")
	}
}

func TestLoadKeyInvalidEnv(t *testing.T) {
	dir := t.TempDir()
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	got, _, err := LoadKey(dir, envWith(short), true)
	if !errors.Is(err, ErrInvalidKey) || got != nil {
		t.Fatalf("got %x, err %v, want ErrInvalidKey", got, err)
	}
	if strings.Contains(err.Error(), short) {
		t.Errorf("error text leaks env value: %v", err)
	}
	if n := dirNames(t, dir); len(n) != 0 {
		t.Fatalf("files created: %v", n)
	}
}

func TestLoadKeyEmptyEnvIsUnset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFileName)
	k, s := randKey(t)
	writeFile(t, path, s+"\n")
	for _, v := range []string{"", "  \n\t"} {
		got, src, err := LoadKey(dir, envWith(v), false)
		if err != nil || !bytes.Equal(got, k) || src != "file "+path {
			t.Errorf("env %q: got %x %q %v", v, got, src, err)
		}
	}
}

func TestLoadKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFileName)
	k, s := randKey(t)
	writeFile(t, path, s+"\n")
	got, src, err := LoadKey(dir, noEnv, false)
	if err != nil || !bytes.Equal(got, k) || src != "file "+path {
		t.Fatalf("got %x %q %v", got, src, err)
	}
}

func TestLoadKeyInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFileName)
	content := base64.StdEncoding.EncodeToString(make([]byte, 16)) + "\n"
	writeFile(t, path, content)
	got, _, err := LoadKey(dir, noEnv, false)
	if !errors.Is(err, ErrInvalidKey) || got != nil {
		t.Fatalf("got %x, err %v, want ErrInvalidKey", got, err)
	}
	if !bytes.Equal(readFile(t, path), []byte(content)) {
		t.Fatal("key file changed")
	}
}

func TestLoadKeyCreates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	path := filepath.Join(dir, KeyFileName)
	got, src, err := LoadKey(dir, noEnv, true)
	if err != nil || src != "file "+path+" (created)" {
		t.Fatalf("got %q %v", src, err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 || st.Size() != 45 {
		t.Fatalf("mode %v size %d, want 0600 and 45", st.Mode().Perm(), st.Size())
	}
	content := readFile(t, path)
	fromFile, err := ParseKey(string(content))
	if err != nil || !bytes.Equal(fromFile, got) {
		t.Fatalf("file key differs from returned key: %v", err)
	}
	assertNoTemp(t, dir)

	before := modTime(t, path)
	again, src, err := LoadKey(dir, noEnv, true)
	if err != nil || !bytes.Equal(again, got) || src != "file "+path {
		t.Fatalf("second call: %q %v", src, err)
	}
	if !bytes.Equal(readFile(t, path), content) || !modTime(t, path).Equal(before) {
		t.Fatal("key file rewritten")
	}
}

func TestLoadKeyNoCreate(t *testing.T) {
	dir := t.TempDir()
	got, _, err := LoadKey(dir, noEnv, false)
	if !errors.Is(err, ErrKeyNotFound) || got != nil {
		t.Fatalf("got %x, err %v, want ErrKeyNotFound", got, err)
	}
	if n := dirNames(t, dir); len(n) != 0 {
		t.Fatalf("files created: %v", n)
	}
}

func TestLoadKeyIgnoresLeftoverTemp(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, ".secrets.key.tmp-crash")
	leftKey, s := randKey(t)
	writeFile(t, leftover, s+"\n")

	if _, _, err := LoadKey(dir, noEnv, false); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("create=false: err %v, want ErrKeyNotFound", err)
	}
	got, _, err := LoadKey(dir, noEnv, true)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, leftKey) {
		t.Fatal("leftover temp key was used")
	}
	fromFile, err := ParseKey(string(readFile(t, filepath.Join(dir, KeyFileName))))
	if err != nil || !bytes.Equal(fromFile, got) {
		t.Fatalf("secrets.key differs from returned key: %v", err)
	}
	if !bytes.Equal(readFile(t, leftover), []byte(s+"\n")) {
		t.Fatal("leftover temp changed")
	}
}

func TestLoadKeyNeverReplacesExisting(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, KeyFileName)
		k, s := randKey(t)
		writeFile(t, path, s+"\n")
		before := modTime(t, path)
		got, _, err := LoadKey(dir, noEnv, true)
		if err != nil || !bytes.Equal(got, k) {
			t.Fatalf("got %x, err %v", got, err)
		}
		if !bytes.Equal(readFile(t, path), []byte(s+"\n")) || !modTime(t, path).Equal(before) {
			t.Fatal("key file changed")
		}
	})
	t.Run("invalid", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, KeyFileName)
		writeFile(t, path, "garbage\n")
		got, _, err := LoadKey(dir, noEnv, true)
		if !errors.Is(err, ErrInvalidKey) || got != nil {
			t.Fatalf("got %x, err %v, want ErrInvalidKey", got, err)
		}
		if !bytes.Equal(readFile(t, path), []byte("garbage\n")) {
			t.Fatal("key file changed")
		}
		if n := dirNames(t, dir); len(n) != 1 || n[0] != KeyFileName {
			t.Fatalf("dir holds %v, want only %s", n, KeyFileName)
		}
	})
}

func TestLoadKeyConcurrentCreate(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	keys := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			keys[i], _, errs[i] = LoadKey(dir, noEnv, true)
		})
	}
	wg.Wait()
	fromFile, err := ParseKey(string(readFile(t, filepath.Join(dir, KeyFileName))))
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if errs[i] != nil || !bytes.Equal(keys[i], fromFile) {
			t.Errorf("goroutine %d: err %v, key matches file: %v", i, errs[i], bytes.Equal(keys[i], fromFile))
		}
	}
	assertNoTemp(t, dir)
}
