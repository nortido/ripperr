package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	SettingKey = "webpush"
	MaxDevices = 20

	maxEndpoint = 2048
	maxLabel    = 60
)

var (
	ErrInvalid        = errors.New("webpush: invalid subscription")
	ErrTooManyDevices = errors.New("webpush: device limit reached")
	ErrNoKeys         = errors.New("webpush: no VAPID keys yet")
)

// Store создают на каждый запрос, на каждый reload Notifier и в breaker hook,
// а строка одна на процесс: замок общий для пакета, а не поле Store
var docMu sync.Mutex

type Settings interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}

type Store struct {
	db Settings
}

func NewStore(db Settings) *Store {
	return &Store{db: db}
}

type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

type document struct {
	VAPID   Keys     `json:"vapid"`
	Devices []Device `json:"devices"`
}

func (s *Store) read(ctx context.Context) (document, error) {
	var doc document
	raw, ok, err := s.db.GetSetting(ctx, SettingKey)
	if err != nil {
		return doc, fmt.Errorf("webpush: read settings: %w", err)
	}
	if !ok {
		return doc, nil
	}
	// текст ошибки json может процитировать кусок строки, а в ней ключи
	if json.Unmarshal(raw, &doc) != nil {
		return document{}, errors.New("webpush: stored settings row is not valid JSON")
	}
	return doc, nil
}

func (s *Store) write(ctx context.Context, doc document) error {
	if doc.Devices == nil {
		doc.Devices = []Device{}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := s.db.SaveSetting(ctx, SettingKey, raw); err != nil {
		return fmt.Errorf("webpush: save settings: %w", err)
	}
	return nil
}

func (s *Store) PublicKey(ctx context.Context) (string, error) {
	doc, err := s.read(ctx)
	if err != nil {
		return "", err
	}
	if doc.VAPID.Private != "" {
		return doc.VAPID.Public, nil
	}
	docMu.Lock()
	defer docMu.Unlock()
	doc, err = s.read(ctx)
	if err != nil {
		return "", err
	}
	if doc.VAPID.Private != "" {
		return doc.VAPID.Public, nil
	}
	keys, err := GenerateKeys()
	if err != nil {
		return "", err
	}
	doc.VAPID = keys
	if err := s.write(ctx, doc); err != nil {
		return "", err
	}
	return keys.Public, nil
}

func (s *Store) Load(ctx context.Context) (Keys, []Device, error) {
	doc, err := s.read(ctx)
	if err != nil {
		return Keys{}, nil, err
	}
	return doc.VAPID, doc.Devices, nil
}

func (s *Store) Subscribe(ctx context.Context, sub Subscription, label, origin string, now time.Time) (Device, error) {
	d, err := validate(sub, origin)
	if err != nil {
		return Device{}, err
	}
	d.Label = cleanLabel(label)
	docMu.Lock()
	defer docMu.Unlock()
	doc, err := s.read(ctx)
	if err != nil {
		return Device{}, err
	}
	if doc.VAPID.Private == "" {
		return Device{}, ErrNoKeys
	}
	i := deviceIndex(doc.Devices, d.ID)
	if i >= 0 {
		d.CreatedAt = doc.Devices[i].CreatedAt
		doc.Devices[i] = d
	} else {
		if len(doc.Devices) >= MaxDevices {
			return Device{}, ErrTooManyDevices
		}
		d.CreatedAt = now.Unix()
		doc.Devices = append(doc.Devices, d)
	}
	if err := s.write(ctx, doc); err != nil {
		return Device{}, err
	}
	return d, nil
}

func (s *Store) Remove(ctx context.Context, id string) (bool, error) {
	docMu.Lock()
	defer docMu.Unlock()
	doc, err := s.read(ctx)
	if err != nil {
		return false, err
	}
	i := deviceIndex(doc.Devices, id)
	if i < 0 {
		return false, nil
	}
	doc.Devices = append(doc.Devices[:i], doc.Devices[i+1:]...)
	if err := s.write(ctx, doc); err != nil {
		return false, err
	}
	return true, nil
}

func deviceIndex(devs []Device, id string) int {
	for i, d := range devs {
		if d.ID == id {
			return i
		}
	}
	return -1
}

func DeviceID(endpoint string) string {
	h := sha256.Sum256([]byte(endpoint))
	return b64url.EncodeToString(h[:12])
}

func invalid(field string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, field)
}

// ошибки называют только поле: endpoint и ключи дают право слать push на устройство
func validate(sub Subscription, origin string) (Device, error) {
	if len(sub.Endpoint) > maxEndpoint {
		return Device{}, invalid("endpoint is too long")
	}
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return Device{}, invalid("endpoint must be an https URL with a host")
	}
	p256dh := strings.TrimRight(sub.Keys.P256dh, "=")
	pub, err := b64url.DecodeString(p256dh)
	if err != nil || len(pub) != 65 {
		return Device{}, invalid("keys.p256dh")
	}
	if _, err := ecdh.P256().NewPublicKey(pub); err != nil {
		return Device{}, invalid("keys.p256dh")
	}
	auth := strings.TrimRight(sub.Keys.Auth, "=")
	if a, err := b64url.DecodeString(auth); err != nil || len(a) != 16 {
		return Device{}, invalid("keys.auth")
	}
	o, err := url.Parse(origin)
	if err != nil || o.Scheme != "https" || o.Hostname() == "" || o.User != nil ||
		o.Path != "" || o.RawQuery != "" || o.Fragment != "" || o.ForceQuery {
		return Device{}, invalid("origin must be an https origin")
	}
	return Device{
		ID:       DeviceID(sub.Endpoint),
		Endpoint: sub.Endpoint,
		P256dh:   p256dh,
		Auth:     auth,
		Origin:   o.Scheme + "://" + o.Host,
	}, nil
}

func cleanLabel(label string) string {
	var b strings.Builder
	n := 0
	for _, r := range label {
		if unicode.IsControl(r) {
			continue
		}
		if n == maxLabel {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

var (
	uaBrowsers = []struct{ token, name string }{
		{"Edg/", "Edge"},
		{"OPR/", "Opera"},
		{"YaBrowser/", "Yandex Browser"},
		{"SamsungBrowser/", "Samsung Internet"},
		{"Firefox/", "Firefox"},
		{"FxiOS/", "Firefox"},
		{"CriOS/", "Chrome"},
		{"Chrome/", "Chrome"},
		{"Safari/", "Safari"},
	}
	// iPhone и iPad пишут "like Mac OS X", Android пишет "Linux": порядок важен
	uaSystems = []struct{ token, name string }{
		{"iPhone", "iPhone"},
		{"iPad", "iPad"},
		{"Android", "Android"},
		{"Windows", "Windows"},
		{"Mac OS X", "macOS"},
		{"CrOS", "ChromeOS"},
		{"Linux", "Linux"},
	}
)

func Label(userAgent string) string {
	var parts []string
	for _, b := range uaBrowsers {
		if strings.Contains(userAgent, b.token) {
			parts = append(parts, b.name)
			break
		}
	}
	for _, sys := range uaSystems {
		if strings.Contains(userAgent, sys.token) {
			parts = append(parts, sys.name)
			break
		}
	}
	return strings.Join(parts, ", ")
}
