package webpush

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

type fakeSettings struct {
	mu         sync.Mutex
	rows       map[string]json.RawMessage
	gets       int
	saves      int
	saveErr    error
	afterGet   func()
	beforeSave func()
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{rows: map[string]json.RawMessage{}}
}

func (f *fakeSettings) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	f.mu.Lock()
	f.gets++
	v, ok := f.rows[key]
	hook := f.afterGet
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return bytes.Clone(v), ok, nil
}

func (f *fakeSettings) SaveSetting(_ context.Context, key string, value json.RawMessage) error {
	f.mu.Lock()
	f.saves++
	hook := f.beforeSave
	err := f.saveErr
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.rows[key] = bytes.Clone(value)
	f.mu.Unlock()
	return nil
}

func (f *fakeSettings) set(fn func(f *fakeSettings)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

func (f *fakeSettings) saveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saves
}

func (f *fakeSettings) row() (json.RawMessage, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.rows[SettingKey]
	return v, ok
}

type subFixture struct {
	sub    Subscription
	p256dh []byte
	auth   []byte
}

func newSub(t *testing.T, endpoint string) subFixture {
	t.Helper()
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	var s Subscription
	s.Endpoint = endpoint
	s.Keys.P256dh = base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes())
	s.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	return subFixture{sub: s, p256dh: ua.PublicKey().Bytes(), auth: auth}
}

func newKeyedStore(t *testing.T) (*fakeSettings, *Store, string) {
	t.Helper()
	f := newFakeSettings()
	s := NewStore(f)
	pub, err := s.PublicKey(context.Background())
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	return f, s, pub
}

const testOrigin = "https://track.example"

func TestStorePublicKeyOnce(t *testing.T) {
	ctx := context.Background()
	f := newFakeSettings()
	s := NewStore(f)
	k1, err := s.PublicKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := s.PublicKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 || k1 == "" {
		t.Fatalf("keys differ or empty: %q %q", k1, k2)
	}
	if n := f.saveCount(); n != 1 {
		t.Fatalf("saves = %d, want 1", n)
	}
	raw, ok := f.row()
	if !ok {
		t.Fatal("no row")
	}
	var stored struct {
		VAPID Keys `json:"vapid"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.VAPID.Public != k1 {
		t.Fatal("stored public key differs from returned")
	}
	priv, err := base64.RawURLEncoding.DecodeString(stored.VAPID.Private)
	if err != nil || len(priv) != 32 {
		t.Fatalf("private: %d bytes, err %v", len(priv), err)
	}
	if _, err := parseKeys(stored.VAPID); err != nil {
		t.Fatalf("stored pair: %v", err)
	}

	f = newFakeSettings()
	s = NewStore(f)
	start := make(chan struct{})
	var wg sync.WaitGroup
	keys := make([]string, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() {
			<-start
			keys[i], errs[i] = s.PublicKey(ctx)
		})
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errors: %v %v", errs[0], errs[1])
	}
	if keys[0] != keys[1] {
		t.Fatal("concurrent PublicKey returned different keys")
	}
	if n := f.saveCount(); n != 1 {
		t.Fatalf("concurrent saves = %d, want 1", n)
	}
}

func TestStorePublicKeyNotReturnedWhenSaveFails(t *testing.T) {
	ctx := context.Background()
	f := newFakeSettings()
	f.saveErr = errors.New("disk full")
	s := NewStore(f)
	if k, err := s.PublicKey(ctx); err == nil || k != "" {
		t.Fatalf("PublicKey = %q, %v; want error", k, err)
	}
	if _, ok := f.row(); ok {
		t.Fatal("row written despite save error")
	}
	f.set(func(f *fakeSettings) { f.saveErr = nil })
	k, err := s.PublicKey(ctx)
	if err != nil || k == "" {
		t.Fatalf("PublicKey = %q, %v", k, err)
	}
	keys, _, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if keys.Public != k {
		t.Fatal("row does not hold the returned key")
	}
}

func TestStoreSubscribeUpsert(t *testing.T) {
	ctx := context.Background()
	_, s, pub := newKeyedStore(t)
	a := newSub(t, "https://fcm.googleapis.com/fcm/send/abc123")
	t1 := time.Unix(1_700_000_000, 0)
	t2 := t1.Add(time.Hour)
	d, err := s.Subscribe(ctx, a.sub, "Chrome, macOS", testOrigin, t1)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != DeviceID(a.sub.Endpoint) || d.Origin != testOrigin || d.CreatedAt != t1.Unix() {
		t.Fatalf("device = %+v", d)
	}
	a2 := newSub(t, a.sub.Endpoint)
	d2, err := s.Subscribe(ctx, a2.sub, "Firefox, Linux", testOrigin, t2)
	if err != nil {
		t.Fatal(err)
	}
	if d2.ID != d.ID || d2.CreatedAt != t1.Unix() || d2.Label != "Firefox, Linux" ||
		d2.P256dh != a2.sub.Keys.P256dh || d2.Auth != a2.sub.Keys.Auth {
		t.Fatalf("upsert = %+v", d2)
	}
	keys, devs, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if keys.Public != pub || keys.Private == "" {
		t.Fatal("Load keys differ from PublicKey pair")
	}
	if len(devs) != 1 || devs[0] != d2 {
		t.Fatalf("devices = %+v", devs)
	}

	for endpoint, want := range map[string]string{
		"https://fcm.googleapis.com/fcm/send/abc123": "05WsUk2skTnEadK3",
		"https://web.push.apple.com/QGuQyavXutnMH":   "Sp6F7A_qSuzg-pDs",
	} {
		if got := DeviceID(endpoint); got != want {
			t.Errorf("DeviceID(%q) = %q, want %q", endpoint, got, want)
		}
	}
}

func TestStoreSubscribeCap(t *testing.T) {
	ctx := context.Background()
	_, s, _ := newKeyedStore(t)
	now := time.Unix(1_700_000_000, 0)
	subs := make([]subFixture, MaxDevices)
	for i := range subs {
		subs[i] = newSub(t, "https://push.example/d"+string(rune('a'+i)))
		if _, err := s.Subscribe(ctx, subs[i].sub, "", testOrigin, now); err != nil {
			t.Fatalf("device %d: %v", i, err)
		}
	}
	extra := newSub(t, "https://push.example/extra")
	if _, err := s.Subscribe(ctx, extra.sub, "", testOrigin, now); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("21st: %v, want ErrTooManyDevices", err)
	}
	again := newSub(t, subs[4].sub.Endpoint)
	if _, err := s.Subscribe(ctx, again.sub, "", testOrigin, now); err != nil {
		t.Fatalf("re-subscribe: %v", err)
	}
	if _, devs, _ := s.Load(ctx); len(devs) != MaxDevices {
		t.Fatalf("devices = %d", len(devs))
	}
}

func TestStoreSubscribeRejects(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	valid := newSub(t, "https://push.example/x")

	f := newFakeSettings()
	if _, err := NewStore(f).Subscribe(ctx, valid.sub, "", testOrigin, now); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("no keys: %v, want ErrNoKeys", err)
	}
	if f.saveCount() != 0 {
		t.Fatal("save without keys")
	}

	f, s, _ := newKeyedStore(t)
	b64 := base64.RawURLEncoding.EncodeToString
	offCurve := append([]byte{0x04}, make([]byte, 64)...)
	cases := map[string]func(sub *Subscription, origin *string){
		"http endpoint": func(s *Subscription, _ *string) { s.Endpoint = "http://push.example/x" },
		"userinfo":      func(s *Subscription, _ *string) { s.Endpoint = "https://user:secret@push.example/x" },
		"long endpoint": func(s *Subscription, _ *string) {
			s.Endpoint = "https://push.example/" + strings.Repeat("q", 2049-len("https://push.example/"))
		},
		"no host":         func(s *Subscription, _ *string) { s.Endpoint = "https:///x" },
		"p256dh 64":       func(s *Subscription, _ *string) { s.Keys.P256dh = b64(valid.p256dh[1:]) },
		"p256dh offcurve": func(s *Subscription, _ *string) { s.Keys.P256dh = b64(offCurve) },
		"auth 15":         func(s *Subscription, _ *string) { s.Keys.Auth = b64(valid.auth[:15]) },
		"http origin":     func(_ *Subscription, o *string) { *o = "http://192.168.1.5:6410" },
		"empty origin":    func(_ *Subscription, o *string) { *o = "" },
		"origin path":     func(_ *Subscription, o *string) { *o = "https://track.example/app" },
	}
	for name, mutate := range cases {
		sub := valid.sub
		origin := testOrigin
		mutate(&sub, &origin)
		_, err := s.Subscribe(ctx, sub, "", origin, now)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
			continue
		}
		for _, v := range []string{sub.Endpoint, sub.Keys.P256dh, sub.Keys.Auth, origin} {
			if v != "" && strings.Contains(err.Error(), v) {
				t.Errorf("%s: error %q echoes a submitted value", name, err)
			}
		}
	}
	if n := f.saveCount(); n != 1 {
		t.Fatalf("saves = %d, want 1 (key only)", n)
	}

	padded := valid.sub
	padded.Keys.P256dh = base64.URLEncoding.EncodeToString(valid.p256dh)
	padded.Keys.Auth = base64.URLEncoding.EncodeToString(valid.auth)
	if !strings.HasSuffix(padded.Keys.P256dh, "=") || !strings.HasSuffix(padded.Keys.Auth, "=") {
		t.Fatal("fixture is not padded")
	}
	d, err := s.Subscribe(ctx, padded, "", testOrigin, now)
	if err != nil {
		t.Fatalf("padded: %v", err)
	}
	if d.P256dh != valid.sub.Keys.P256dh || d.Auth != valid.sub.Keys.Auth {
		t.Fatalf("padded keys stored as %q %q, want unpadded", d.P256dh, d.Auth)
	}
}

func TestStoreRemoveAndLabel(t *testing.T) {
	ctx := context.Background()
	f, s, _ := newKeyedStore(t)
	now := time.Unix(1_700_000_000, 0)
	a := newSub(t, "https://push.example/a")
	b := newSub(t, "https://push.example/b")
	da, err := s.Subscribe(ctx, a.sub, "A", testOrigin, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Subscribe(ctx, b.sub, "B", testOrigin, now); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Remove(ctx, da.ID); !ok || err != nil {
		t.Fatalf("Remove = %v, %v", ok, err)
	}
	_, devs, err := s.Load(ctx)
	if err != nil || len(devs) != 1 || devs[0].ID == da.ID {
		t.Fatalf("after Remove: %+v, %v", devs, err)
	}
	saves := f.saveCount()
	if ok, err := s.Remove(ctx, "nope"); ok || err != nil {
		t.Fatalf("Remove(nope) = %v, %v", ok, err)
	}
	if f.saveCount() != saves {
		t.Fatal("Remove of an unknown id saved")
	}

	for ua, want := range map[string]string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36":                   "Chrome, macOS",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1": "Safari, iPhone",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148":                           "iPhone",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:131.0) Gecko/20100101 Firefox/131.0":                                                        "Firefox, Windows",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0":           "Edge, Windows",
		"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 YaBrowser/24.10.1.100 Mobile Safari/537.36":      "Yandex Browser, Android",
		"": "",
	} {
		got := Label(ua)
		if got != want {
			t.Errorf("Label(%q) = %q, want %q", ua, got, want)
		}
		if utf8.RuneCountInString(got) > 60 {
			t.Errorf("Label(%q) longer than 60 runes", ua)
		}
	}
}

func TestStoreNoLostUpdate(t *testing.T) {
	ctx := context.Background()
	f, s, _ := newKeyedStore(t)
	now := time.Unix(1_700_000_000, 0)
	a := newSub(t, "https://push.example/a")
	b := newSub(t, "https://push.example/b")
	da, err := s.Subscribe(ctx, a.sub, "A", testOrigin, now)
	if err != nil {
		t.Fatal(err)
	}

	gotGet := make(chan struct{}, 1)
	removed := make(chan error, 1)
	var once sync.Once
	f.set(func(f *fakeSettings) {
		f.beforeSave = func() {
			once.Do(func() {
				f.set(func(f *fakeSettings) {
					f.afterGet = func() {
						select {
						case gotGet <- struct{}{}:
						default:
						}
					}
				})
				go func() {
					_, err := s.Remove(ctx, da.ID)
					removed <- err
				}()
				// Remove дойдет до GetSetting только если запись не под замком
				select {
				case <-gotGet:
				case <-time.After(200 * time.Millisecond):
				}
			})
		}
	})
	db, err := s.Subscribe(ctx, b.sub, "B", testOrigin, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	_, devs, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hasA, hasB bool
	for _, d := range devs {
		hasA = hasA || d.ID == da.ID
		hasB = hasB || d.ID == db.ID
	}
	if hasA || !hasB {
		t.Fatalf("lost update: A present %v, B present %v", hasA, hasB)
	}
}
