package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return b
}

func TestEncryptRFC8291Vector(t *testing.T) {
	plaintext := b64(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	asPublic := b64(t, "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8")
	asPrivate := b64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	uaPublic := b64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	salt := b64(t, "DGv6ra1nlYgDCS1FRnbzlw")
	auth := b64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	header := b64(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8")
	ciphertext := b64(t, "8pfeW0KbunFT06SuDKoJH9Ql87S1QUrdirN6GcG7sFz1y1sqLgVi1VhjVkHsUoEsbI_0LpXMuGvnzQ")

	as, err := ecdh.P256().NewPrivateKey(asPrivate)
	if err != nil {
		t.Fatal(err)
	}
	got, err := encrypt(plaintext, uaPublic, auth, as, salt)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte{}, header...), ciphertext...)
	if len(want) != 144 {
		t.Fatalf("vector length %d, want 144", len(want))
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("body mismatch\n got %x\nwant %x", got, want)
	}
	if !bytes.Equal(got[16:20], []byte{0, 0, 0x10, 0}) || got[20] != 0x41 || !bytes.Equal(got[21:86], asPublic) {
		t.Fatalf("header fields wrong: %x", got[:86])
	}
}

func testDecrypt(t *testing.T, body []byte, ua *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("body too short: %d", len(body))
	}
	salt := body[:16]
	if rs := binary.BigEndian.Uint32(body[16:20]); rs != 4096 {
		t.Fatalf("rs %d", rs)
	}
	idlen := int(body[20])
	keyid := body[21 : 21+idlen]
	asPub, err := ecdh.P256().NewPublicKey(keyid)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := ua.ECDH(asPub)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), keyid...)
	ikm, err := hkdf.Key(sha256.New, secret, auth, string(info), 32)
	if err != nil {
		t.Fatal(err)
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		t.Fatalf("no 0x02 delimiter: %x", plain)
	}
	return plain[:len(plain)-1]
}

type testDevice struct {
	d    Device
	ua   *ecdh.PrivateKey
	auth []byte
}

func newTestDevice(t *testing.T, endpoint string) testDevice {
	t.Helper()
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return testDevice{
		d: Device{
			Endpoint: endpoint,
			P256dh:   base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()),
			Auth:     base64.RawURLEncoding.EncodeToString(auth),
			Origin:   "https://track.example",
		},
		ua:   ua,
		auth: auth,
	}
}

func readBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const fcmEndpoint = "https://fcm.googleapis.com/fcm/send/abc123"

func TestNewRequestRoundTrip(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	td := newTestDevice(t, fcmEndpoint)
	payload := []byte(`{"title":"t","body":"b","url":"/"}`)
	now := time.Unix(1_700_000_000, 0)

	req, err := NewRequest(context.Background(), keys, td.d, payload, now)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost || req.URL.String() != fcmEndpoint {
		t.Fatalf("method %s url %s", req.Method, req.URL)
	}
	for k, v := range map[string]string{
		"Content-Encoding": "aes128gcm",
		"Content-Type":     "application/octet-stream",
		"TTL":              "86400",
		"Urgency":          "high",
	} {
		if got := req.Header.Get(k); got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
	body1 := readBody(t, req)
	if got := testDecrypt(t, body1, td.ua, td.auth); !bytes.Equal(got, payload) {
		t.Fatalf("decrypted %q", got)
	}

	req2, err := NewRequest(context.Background(), keys, td.d, payload, now)
	if err != nil {
		t.Fatal(err)
	}
	body2 := readBody(t, req2)
	if bytes.Equal(body1, body2) || bytes.Equal(body1[:16], body2[:16]) || bytes.Equal(body1[21:86], body2[21:86]) {
		t.Fatal("two requests share salt, key or body")
	}
}

func checkVAPID(t *testing.T, keys Keys, req *http.Request, wantAud string, now time.Time) {
	t.Helper()
	authz := req.Header.Get("Authorization")
	jwt, ok := strings.CutPrefix(authz, "vapid t=")
	if !ok {
		t.Fatalf("Authorization %q", authz)
	}
	jwt, k, ok := strings.Cut(jwt, ", k=")
	if !ok || k != keys.Public {
		t.Fatalf("Authorization %q", authz)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 || strings.Contains(jwt, "=") {
		t.Fatalf("jwt %q", jwt)
	}
	if h := string(b64(t, parts[0])); h != `{"typ":"JWT","alg":"ES256"}` {
		t.Fatalf("jwt header %s", h)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(b64(t, parts[1]), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Aud != wantAud || claims.Sub != "https://track.example" || claims.Exp != now.Add(12*time.Hour).Unix() {
		t.Fatalf("claims %+v", claims)
	}
	sig := b64(t, parts[2])
	if len(sig) != 64 {
		t.Fatalf("signature length %d", len(sig))
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), b64(t, keys.Public))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, h[:], r, s) {
		t.Fatal("signature does not verify")
	}
}

func TestNewRequestVAPID(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	for endpoint, aud := range map[string]string{
		fcmEndpoint:                   "https://fcm.googleapis.com",
		"https://push.example:8443/x": "https://push.example:8443",
	} {
		td := newTestDevice(t, endpoint)
		req, err := NewRequest(context.Background(), keys, td.d, []byte("x"), now)
		if err != nil {
			t.Fatal(err)
		}
		checkVAPID(t, keys, req, aud, now)
	}
}

func TestNewRequestRejects(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	ctx := context.Background()
	td := newTestDevice(t, fcmEndpoint)

	req, err := NewRequest(ctx, keys, td.d, make([]byte, MaxPayload), now)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(readBody(t, req)); n != 4096 {
		t.Fatalf("max body %d, want 4096", n)
	}
	if _, err := NewRequest(ctx, keys, td.d, make([]byte, MaxPayload+1), now); err == nil {
		t.Fatal("oversized payload accepted")
	}

	short := keys
	short.Private = base64.RawURLEncoding.EncodeToString(b64(t, keys.Private)[:31])
	bad := map[string]func() (Keys, Device){
		"p256dh 64": func() (Keys, Device) {
			d := td.d
			d.P256dh = base64.RawURLEncoding.EncodeToString(td.ua.PublicKey().Bytes()[:64])
			return keys, d
		},
		"auth 15": func() (Keys, Device) {
			d := td.d
			d.Auth = base64.RawURLEncoding.EncodeToString(td.auth[:15])
			return keys, d
		},
		"http": func() (Keys, Device) {
			d := td.d
			d.Endpoint = "http://x/y"
			return keys, d
		},
		"no host": func() (Keys, Device) {
			d := td.d
			d.Endpoint = "https:///y"
			return keys, d
		},
		"private 31": func() (Keys, Device) { return short, td.d },
	}
	other, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	mismatch := keys
	mismatch.Public = other.Public
	bad["public mismatch"] = func() (Keys, Device) { return mismatch, td.d }
	for name, f := range bad {
		k, d := f()
		if _, err := NewRequest(ctx, k, d, []byte("x"), now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	for _, ep := range []string{"https://a b/secretpath", "https://a\x7f/secretpath", "https://:443/secretpath"} {
		d := td.d
		d.Endpoint = ep
		_, err := NewRequest(ctx, keys, d, []byte("x"), now)
		if err == nil {
			t.Errorf("%q: accepted", ep)
		} else if strings.Contains(err.Error(), "secretpath") {
			t.Errorf("%q: error leaks endpoint: %v", ep, err)
		}
	}
}

func TestGenerateKeys(t *testing.T) {
	k1, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	pub := b64(t, k1.Public)
	priv := b64(t, k1.Private)
	if len(pub) != 65 || pub[0] != 0x04 || len(priv) != 32 {
		t.Fatalf("lengths pub %d priv %d", len(pub), len(priv))
	}
	sk, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), priv)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := sk.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(derived, pub) {
		t.Fatal("public does not match private")
	}
	k2, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k2 {
		t.Fatal("two calls gave the same keys")
	}
}
