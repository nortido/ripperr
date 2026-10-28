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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// 4096 (rs, один record) - 86 (header) - 1 (delimiter) - 16 (GCM tag)
const MaxPayload = 3993

const (
	recordSize = 4096
	jwtHeader  = `{"typ":"JWT","alg":"ES256"}`
)

var b64url = base64.RawURLEncoding

type Keys struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

type Device struct {
	ID        string `json:"id"`
	Endpoint  string `json:"endpoint"`
	P256dh    string `json:"p256dh"`
	Auth      string `json:"auth"`
	Label     string `json:"label"`
	Origin    string `json:"origin"`
	CreatedAt int64  `json:"created_at"`
}

func GenerateKeys() (Keys, error) {
	sk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Keys{}, err
	}
	priv, err := sk.Bytes()
	if err != nil {
		return Keys{}, err
	}
	pub, err := sk.PublicKey.Bytes()
	if err != nil {
		return Keys{}, err
	}
	return Keys{Public: b64url.EncodeToString(pub), Private: b64url.EncodeToString(priv)}, nil
}

func NewRequest(ctx context.Context, keys Keys, d Device, payload []byte, now time.Time) (*http.Request, error) {
	// url.Error печатает весь URL, а endpoint не должен попадать в ошибки и логи
	u, err := url.Parse(d.Endpoint)
	if err != nil {
		return nil, errors.New("webpush: endpoint is not a valid URL")
	}
	if u.Scheme != "https" || u.Hostname() == "" {
		return nil, errors.New("webpush: endpoint must be an https URL with a host")
	}
	signer, err := parseKeys(keys)
	if err != nil {
		return nil, err
	}
	uaPublic, err := b64url.DecodeString(d.P256dh)
	if err != nil {
		return nil, fmt.Errorf("webpush: p256dh: %w", err)
	}
	auth, err := b64url.DecodeString(d.Auth)
	if err != nil {
		return nil, fmt.Errorf("webpush: auth: %w", err)
	}
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	body, err := encrypt(payload, uaPublic, auth, as, salt)
	if err != nil {
		return nil, err
	}
	jwt, err := vapidJWT(signer, u.Scheme+"://"+u.Host, d.Origin, now.Add(12*time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", "86400")
	// без него Android в Doze копит push до пробуждения
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+keys.Public)
	return req, nil
}

func parseKeys(keys Keys) (*ecdsa.PrivateKey, error) {
	priv, err := b64url.DecodeString(keys.Private)
	if err != nil {
		return nil, fmt.Errorf("webpush: vapid private key: %w", err)
	}
	sk, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), priv)
	if err != nil {
		return nil, fmt.Errorf("webpush: vapid private key: %w", err)
	}
	pub, err := b64url.DecodeString(keys.Public)
	if err != nil {
		return nil, fmt.Errorf("webpush: vapid public key: %w", err)
	}
	// k= уходит в заголовок как есть: при несовпадении с приватным push service отвечает 403
	derived, err := sk.PublicKey.Bytes()
	if err != nil || !bytes.Equal(derived, pub) {
		return nil, errors.New("webpush: vapid public key does not match private key")
	}
	return sk, nil
}

func encrypt(plaintext, uaPublic, authSecret []byte, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(plaintext) > MaxPayload {
		return nil, fmt.Errorf("webpush: payload %d bytes, max %d", len(plaintext), MaxPayload)
	}
	if len(authSecret) != 16 {
		return nil, fmt.Errorf("webpush: auth secret %d bytes, want 16", len(authSecret))
	}
	if len(salt) != 16 {
		return nil, fmt.Errorf("webpush: salt %d bytes, want 16", len(salt))
	}
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("webpush: p256dh: %w", err)
	}
	secret, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := as.PublicKey().Bytes()

	keyInfo := make([]byte, 0, 14+2*len(asPublic))
	keyInfo = append(keyInfo, "WebPush: info\x00"...)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)
	ikm, err := hkdf.Key(sha256.New, secret, authSecret, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, 21+len(asPublic)+len(plaintext)+1+gcm.Overhead())
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, byte(len(asPublic)))
	out = append(out, asPublic...)
	headerLen := len(out)
	// 0x02 = разделитель последнего record (RFC 8188), без паддинга
	out = append(out, plaintext...)
	out = append(out, 0x02)
	return gcm.Seal(out[:headerLen], nonce, out[headerLen:], nil), nil
}

func vapidJWT(sk *ecdsa.PrivateKey, aud, sub string, exp int64) (string, error) {
	claims, err := json.Marshal(struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}{aud, exp, sub})
	if err != nil {
		return "", err
	}
	unsigned := b64url.EncodeToString([]byte(jwtHeader)) + "." + b64url.EncodeToString(claims)
	h := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, sk, h[:])
	if err != nil {
		return "", err
	}
	// ES256 в JWS это r||s по 32 байта, а не DER
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return unsigned + "." + b64url.EncodeToString(sig), nil
}
