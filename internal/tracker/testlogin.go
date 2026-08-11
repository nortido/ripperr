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
	"trackerr/internal/tracker/client/kinozal"
	"trackerr/internal/tracker/client/nnm"
	"trackerr/internal/tracker/client/rutor"
	"trackerr/internal/tracker/client/rutracker"
	"trackerr/internal/tracker/client/teamhd"
)

var (
	// ErrUnknownTracker is returned when the tracker key is not in the registry.
	ErrUnknownTracker = errors.New("unknown tracker key")
)

var trackerIDByKey = map[string]int{
	"rutracker": RutrackerID,
	"kinozal":   KinozalID,
	"rutor":     RutorID,
	"teamhd":    TeamhdID,
	"nnm":       NnmID,
}

type loginDB interface {
	GetTracker(ctx context.Context, trackerID int) (*trackerModel.Tracker, error)
}

// TestLogin performs an isolated login test for one tracker using effective config.
// Optional configPatch merges tracker fields (same shape as GET /api/trackers item) without saving.
// It does not touch the production client cache or breaker alert registry.
func TestLogin(ctx context.Context, cfg config.App, db loginDB, key string, configPatch json.RawMessage) error {
	tid, ok := trackerIDByKey[key]
	if !ok {
		return ErrUnknownTracker
	}
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
	switch key {
	case "rutracker":
		attrs.Credentials = cfg.Tracker.Rutracker.Credentials
		attrs.Publicity = cfg.Tracker.Rutracker.Publicity
	case "kinozal":
		attrs.Credentials = cfg.Tracker.Kinozal.Credentials
		attrs.Cookies = helper.ParseCookies(cfg.Tracker.Kinozal.Cookies)
		attrs.Publicity = cfg.Tracker.Kinozal.Publicity
	case "rutor":
		attrs.Publicity = cfg.Tracker.Rutor.Publicity
	case "teamhd":
		attrs.Cookies = helper.ParseCookies(cfg.Tracker.TeamHD.Cookies)
		attrs.Publicity = cfg.Tracker.TeamHD.Publicity
	case "nnm":
		attrs.Credentials = cfg.Tracker.NNM.Credentials
		attrs.Cookies = helper.ParseCookies(cfg.Tracker.NNM.Cookies)
		attrs.Publicity = cfg.Tracker.NNM.Publicity
	}
	return attrs
}

func buildHandlerRegistry(conf *trackerModel.TrackerConfig) map[int]client.ITracker {
	handlerRegistry := make(map[int]client.ITracker)
	if conf.Rutracker.Enabled {
		handlerRegistry[RutrackerID] = rutracker.New(conf.Rutracker)
	}
	if conf.Kinozal.Enabled {
		handlerRegistry[KinozalID] = kinozal.New(conf.Kinozal)
	}
	if conf.Rutor.Enabled {
		handlerRegistry[RutorID] = rutor.New(conf.Rutor)
	}
	}
	if conf.TeamHD.Enabled {
		handlerRegistry[TeamhdID] = teamhd.New(conf.TeamHD)
	}
	if conf.NNM.Enabled {
		handlerRegistry[NnmID] = nnm.New(conf.NNM)
	}
	return handlerRegistry
}
