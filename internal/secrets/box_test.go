package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testKey(t *testing.T, fill byte) []byte {
	t.Helper()
	return bytes.Repeat([]byte{fill}, 32)
}

func testBox(t *testing.T, key []byte) *Box {
	t.Helper()
	b, err := NewBox(key)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := testBox(t, testKey(t, 1))
	for _, pt := range [][]byte{{}, []byte(`{"a":1}`), bytes.Repeat([]byte("x"), 64<<10)} {
		env := b.Seal(pt, "settings/integrations")
		if !strings.HasPrefix(env, "enc:v1:") || !IsSealed(env) {
			t.Fatalf("envelope without prefix: %.20q", env)
		}
		got, err := b.Open(env, "settings/integrations")
		if err != nil {
			t.Fatalf("Open(len %d): %v", len(pt), err)
		}
		if !bytes.Equal(got, pt) {
			t.Fatalf("round trip mismatch for len %d", len(pt))
		}
	}
}

func TestSealFreshNonce(t *testing.T) {
	b := testBox(t, testKey(t, 1))
	first := b.Seal([]byte("same"), "settings/x")
	if b.Seal([]byte("same"), "settings/x") == first {
		t.Fatal("two seals of the same input gave the same envelope")
	}
}

func TestOpenRejects(t *testing.T) {
	b := testBox(t, testKey(t, 1))
	env := b.Seal([]byte("secret"), "trackers/rutracker/credentials")
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(env, "enc:v1:"))
	if err != nil {
		t.Fatal(err)
	}
	flipped := bytes.Clone(raw)
	flipped[len(flipped)/2] ^= 0x01
	short := base64.StdEncoding.EncodeToString(raw[:27])

	cases := []struct {
		name     string
		box      *Box
		envelope string
		aad      string
		want     error
	}{
		{"wrong aad", b, env, "trackers/rutracker/cookies", ErrOpen},
		{"wrong key", testBox(t, testKey(t, 2)), env, "trackers/rutracker/credentials", ErrOpen},
		{"flipped byte", b, "enc:v1:" + base64.StdEncoding.EncodeToString(flipped), "trackers/rutracker/credentials", ErrOpen},
		{"short payload", b, "enc:v1:" + short, "trackers/rutracker/credentials", ErrOpen},
		{"bad base64", b, "enc:v1:!!!not-base64", "trackers/rutracker/credentials", ErrOpen},
		{"no prefix", b, "secret", "trackers/rutracker/credentials", ErrNotSealed},
		{"v2 prefix", b, "enc:v2:" + strings.TrimPrefix(env, "enc:v1:"), "trackers/rutracker/credentials", ErrNotSealed},
	}
	for _, c := range cases {
		got, err := c.box.Open(c.envelope, c.aad)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if got != nil {
			t.Errorf("%s: plaintext returned on error", c.name)
		}
	}
}

func TestNewBoxKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 24, 31, 33} {
		if _, err := NewBox(make([]byte, n)); err == nil {
			t.Errorf("NewBox with %d-byte key: no error", n)
		}
	}
}

func TestOpenGoldenEnvelope(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	// Конверт записан один раз: любое изменение формата сломает уже зашифрованные базы
	const golden = "enc:v1:zHOxZFtp/gkuNbEJ/Dr/YxbTqMxXX15gZg4T5fUTMZdGxRQScpYI1VympWATpJFNij5Nfg9o7lu/DQ=="
	got, err := testBox(t, key).Open(golden, "settings/integrations")
	if err != nil || string(got) != `{"qbit":{"password":"golden"}}` {
		t.Fatalf("got %q, err %v", got, err)
	}
}
