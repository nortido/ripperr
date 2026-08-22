package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"time"

	"trackerr/internal/config"
	"trackerr/internal/helper"
	"trackerr/internal/helper/tlsclient"
	proxyModel "trackerr/internal/model/proxy"
	trackerModel "trackerr/internal/model/tracker"
	"trackerr/internal/proxy"
	"trackerr/internal/settings"
	"trackerr/internal/tracker/client"
	"trackerr/internal/tracker/registry"
)

var (
	// ErrUnknownTracker возвращается, когда ключа трекера нет в реестре.
	ErrUnknownTracker = errors.New("unknown tracker key")
)

type loginDB interface {
	GetTracker(ctx context.Context, trackerID int) (*trackerModel.Tracker, error)
}

// TestLogin делает изолированную проверку входа для одного трекера на действующем конфиге.
// Необязательный configPatch подмешивает поля трекера (той же формы, что элемент GET /api/trackers) без сохранения.
// Он не трогает боевой кеш клиентов и реестр оповещений предохранителя.
func TestLogin(ctx context.Context, cfg config.App, db loginDB, key string, configPatch json.RawMessage) error {
	tid := registry.ID(key)
	if tid == 0 {
		return ErrUnknownTracker
	}
	// cfg пришёл по значению, но набор трекеров это map: без своей копии разовый
	// патч из формы ушёл бы в конфиг вызывающего, вопреки "без сохранения" выше
	cfg.Tracker = cfg.Tracker.Clone()
	if len(configPatch) > 0 && string(configPatch) != "null" {
		overlay, err := settings.TrackerPatchFromJSON(configPatch)
		if err != nil {
			return err
		}
		settings.ApplyTrackerKey(&cfg, key, overlay)
	}
	trCfg := ConfigFromApp(cfg)
	reg := buildHandlerRegistry(trCfg)
	tr, ok := reg[tid]
	if !ok {
		return fmt.Errorf("tracker %s is disabled", key)
	}

	cj, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	hc := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Jar:     cj,
		Timeout: time.Duration(cfg.Runner.HttpTimeout) * time.Second,
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
		Timeout: time.Duration(cfg.Flaresolverr.MaxTimeout)*time.Millisecond + 30*time.Second,
	}, &proxyModel.Config{
		Flaresolverr: proxyModel.ItemConfig{
			URL:        cfg.Flaresolverr.Host,
			MaxTimeout: cfg.Flaresolverr.MaxTimeout,
		},
	})

	active := trackerActiveDomain(tr)
	if active == "" {
		active = selectActiveDomain(ctx, tr.GetDomain(), trackerMirrors(tr), defaultDomainAlive)
	}
	if active != tr.GetDomain() {
		tr = withActiveDomain(tr, active)
	}

	cb := helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{
		MaxFailures: cfg.Runner.CBRetries,
		Timeout:     time.Duration(cfg.Runner.CBMinutesToRetry) * time.Minute,
	})
	tc := client.New(hc, downloadHC, tr, px, cb)

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
	// настройки важнее сохранённой сессии, включая пустые: проверка должна смотреть
	// на то, что в форме, а не откатываться к кукам, оставленным в БД прошлым входом
	if it := cfg.Tracker.ByKey(key); it != nil {
		attrs.Credentials = it.Credentials
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
