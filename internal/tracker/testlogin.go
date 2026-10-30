package tracker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/helper"
	"ripperr/internal/helper/formcreds"
	"ripperr/internal/helper/tlsclient"
	proxyModel "ripperr/internal/model/proxy"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/proxy"
	"ripperr/internal/settings"
	"ripperr/internal/tracker/client"
	"ripperr/internal/tracker/registry"
)

var (
	ErrUnknownTracker = errors.New("unknown tracker key")
)

var loginTransport = helper.ProxyTransport

var loginTLSDoer func(tlsclient.Config) (helper.HTTPDoer, error)

type loginDB interface {
	GetTracker(ctx context.Context, trackerID int) (*trackerModel.Tracker, error)
}

func TestLogin(ctx context.Context, cfg config.App, db loginDB, key string, configPatch json.RawMessage) error {
	tid := registry.ID(key)
	if tid == 0 {
		return ErrUnknownTracker
	}
	// cfg пришел по значению, но Tracker это map. Без копии патч из формы утек бы в конфиг вызывающего
	cfg.Tracker = cfg.Tracker.Clone()
	if len(configPatch) > 0 && string(configPatch) != "null" {
		overlay, err := settings.TrackerPatchFromJSON(configPatch)
		if err != nil {
			return err
		}
		settings.ApplyTrackerKey(&cfg, key, overlay)
	}
	// выключенный трекер тоже проверяем, enabled про боевой запуск
	if it := cfg.Tracker.ByKey(key); it != nil {
		it.Enabled = true
	}
	trCfg := ConfigFromApp(cfg)
	// реестр свой, боевой кеш клиентов и оповещения предохранителя не трогаем
	reg := buildHandlerRegistry(trCfg)
	tr, ok := reg[tid]
	if !ok {
		return fmt.Errorf("tracker %s is not configured", key)
	}

	cj, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	hc := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Jar:       cj,
		Timeout:   time.Duration(cfg.Runner.HttpTimeout) * time.Second,
		Transport: loginTransport(cfg.Runner.DownloadProxy),
	}
	downloadHC, err := tlsclient.New(tlsclient.Config{
		Timeout:  time.Duration(cfg.Runner.DownloadTimeout) * time.Second,
		ProxyURL: cfg.Runner.DownloadProxy,
		Profile:  cfg.Runner.TLSProfile,
	})
	if err != nil {
		return err
	}
	px := proxy.New(&http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout:   time.Duration(cfg.Flaresolverr.MaxTimeout)*time.Millisecond + 30*time.Second,
		Transport: helper.DirectTransport(),
	}, &proxyModel.Config{
		Flaresolverr: proxyModel.ItemConfig{
			URL:         cfg.Flaresolverr.Host,
			MaxTimeout:  cfg.Flaresolverr.MaxTimeout,
			CaptureHost: cfg.Flaresolverr.CaptureHost,
			CapturePort: cfg.Flaresolverr.CapturePort,
		},
	})

	active := trackerActiveDomain(tr)
	if active == "" {
		var paused string
		if active, paused = guardedDomain(ctx, tr, domainProbeWith(hc)(tr)); paused != "" {
			return fmt.Errorf("only the unconfirmed mirror %s answers, confirm it in the tracker settings", paused)
		}
	}
	if active != tr.GetDomain() {
		tr = withActiveDomain(tr, active)
	}

	cb := helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{
		MaxFailures: cfg.Runner.CBRetries,
		Timeout:     time.Duration(cfg.Runner.CBMinutesToRetry) * time.Minute,
	})
	opts := []client.Option{client.WithTLSSession(client.NewTLSSession(loginTLSDoer))}
	if p, ok := tr.(client.FlareSessionProvider); ok && p.FlareSession() != "" {
		suffix := make([]byte, 4)
		if _, err = rand.Read(suffix); err != nil {
			return err
		}
		id := p.FlareSession() + "-test-" + hex.EncodeToString(suffix)
		opts = append(opts, client.WithFlareSession(id))
		if pt := tr.GetProxyType(); pt == proxyModel.TypeFlare || pt == proxyModel.TypeTLS {
			// ctx вызывающего мог истечь, а тестовый браузер во FlareSolverr должен закрыться
			defer func() {
				dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				_ = px.DestroyFlareSession(dctx, id)
			}()
		}
	}
	seedCloudflareClearance(cj, tr.GetHost())
	tc := client.New(hc, downloadHC, tr, px, cb, opts...)

	attrs, err := db.GetTracker(ctx, tid)
	if err != nil {
		return err
	}
	testAttrs := testLoginAttrs(cfg, key, tid, attrs)
	return tc.InitClient(ctx, testAttrs, true)
}

func testLoginAttrs(cfg config.App, key string, tid int, dbAttrs *trackerModel.Tracker) *trackerModel.Tracker {
	attrs := &trackerModel.Tracker{TypeID: tid, Exists: true}
	if dbAttrs != nil {
		*attrs = *dbAttrs
		attrs.Exists = true
	}
	// настройки главнее сохраненной сессии, даже пустые. Проверяем то, что в форме,
	// без отката к старым кукам из БД
	if it := cfg.Tracker.ByKey(key); it != nil {
		attrs.Credentials = formcreds.Expand(it.Credentials, it.Login, it.Password)
		attrs.Cookies = helper.ParseCookies(it.Cookies)
		attrs.Publicity = it.Publicity
	}
	return attrs
}

func buildHandlerRegistry(conf trackerModel.TrackerConfig) map[int]client.ITracker {
	handlerRegistry := make(map[int]client.ITracker, len(conf))
	for _, d := range registry.All() {
		t, ok := conf[d.Key]
		if !ok || !t.Enabled {
			continue
		}
		handlerRegistry[d.ID] = d.New(t)
	}
	return handlerRegistry
}

var (
	liveJarMu sync.Mutex
	liveJar   http.CookieJar
)

func setLiveJar(j http.CookieJar) {
	liveJarMu.Lock()
	defer liveJarMu.Unlock()
	liveJar = j
}

func seedCloudflareClearance(dst http.CookieJar, host string) {
	liveJarMu.Lock()
	src := liveJar
	liveJarMu.Unlock()
	u, err := url.Parse(host + "/")
	if src == nil || err != nil {
		return
	}
	// только допуск Cloudflare: вход тест обязан пройти сам, по логину и паролю
	var cf []*http.Cookie
	for _, c := range src.Cookies(u) {
		if c.Name == "cf_clearance" {
			cf = append(cf, &http.Cookie{Name: c.Name, Value: c.Value, Path: "/"})
		}
	}
	if len(cf) > 0 {
		dst.SetCookies(u, cf)
	}
}
