package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"ripperr/internal/helper/tlscapture"
	"ripperr/internal/helper/tlsclient"
	"ripperr/internal/model/proxy"
	"ripperr/internal/system/logger"
)

const (
	identityRetry  = time.Hour
	uaProbeTimeout = 10 * time.Second
	selfCheckLimit = 30 * time.Second
)

type identityEntry struct {
	id       tlscapture.Identity
	failedAt time.Time
}

// кэш общий на процесс: у тест-логина свой клиент proxy, но тот же Flare и тот же UA
var (
	identityMu    sync.Mutex
	identityCache = map[string]identityEntry{}
	captureGate   = make(chan struct{}, 1)
)

func (f *client) ChromeIdentity(ctx context.Context) (tlscapture.Identity, error) {
	ua, err := f.flareUserAgent(ctx)
	if err != nil {
		return tlscapture.Identity{}, err
	}
	key := f.conf.Flaresolverr.URL + "\x00" + ua
	if id, ok := cachedIdentity(key); ok {
		return id, nil
	}
	select {
	case captureGate <- struct{}{}:
		defer func() { <-captureGate }()
	case <-ctx.Done():
		return fallbackIdentity(ua), nil
	}
	if id, ok := cachedIdentity(key); ok {
		return id, nil
	}
	return f.resolveIdentity(ctx, key, ua), nil
}

func (f *client) resolveIdentity(ctx context.Context, key, ua string) tlscapture.Identity {
	opts, err := receiverOptions(f.conf.Flaresolverr)
	if err != nil {
		return f.captureFailed(ctx, key, ua, err)
	}
	rcv, err := tlscapture.Listen(opts)
	if err != nil {
		return f.captureFailed(ctx, key, ua, err)
	}
	defer rcv.Close()

	flareFP, err := rcv.Capture(ctx, f.openViaFlare)
	if err != nil {
		return f.captureFailed(ctx, key, ua, err)
	}
	id := tlscapture.Identity{UserAgent: ua, Profile: tlsclient.ChromeProfile(ua), Fingerprint: flareFP}
	if captured := flareFP.Headers["user-agent"]; captured != "" {
		id.UserAgent = captured
	}

	builtin := tlsclient.Config{Profile: id.Profile, Direct: true, NoRedirects: true}
	ours := f.selfCheck(ctx, rcv, builtin)
	if flareFP.Matches(ours) {
		id.Mode = tlscapture.ModeBuiltin
		storeIdentity(key, id, time.Time{})
		return id
	}
	custom := builtin
	custom.Fingerprint = flareFP
	theirs := f.selfCheck(ctx, rcv, custom)
	if ctx.Err() != nil {
		return f.captureFailed(ctx, key, ua, ctx.Err())
	}
	if flareFP.Matches(theirs) {
		id.Mode = tlscapture.ModeCustom
		storeIdentity(key, id, time.Time{})
		return id
	}
	id.Mode = tlscapture.ModeFlare
	logger.Warn(fmt.Sprintf("tls fingerprint mismatch: flaresolverr JA4 %s, built-in %s, custom %s; pages go through flaresolverr",
		ja4Of(flareFP), ja4Of(ours), ja4Of(theirs)))
	storeIdentity(key, id, time.Time{})
	return id
}

func (f *client) selfCheck(ctx context.Context, rcv *tlscapture.Receiver, cfg tlsclient.Config) *tlscapture.Fingerprint {
	cctx, cancel := context.WithTimeout(ctx, selfCheckLimit)
	defer cancel()
	// ошибка пробы или отсутствие захвата = отпечаток не совпал
	fp, _ := rcv.Capture(cctx, func(c context.Context, target string) error {
		return f.probe(c, cfg, target)
	})
	return fp
}

func (f *client) openViaFlare(ctx context.Context, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	res, err := f.Send(req, &proxy.Options{OnlyCookies: true}, proxy.TypeFlare)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func (f *client) captureFailed(ctx context.Context, key, ua string, cause error) tlscapture.Identity {
	logger.Warn(fmt.Sprintf("tls capture via flaresolverr failed, using the built-in profile (see TLS_CAPTURE_HOST, TLS_CAPTURE_PORT): %s", cause))
	id := fallbackIdentity(ua)
	// отмена контекста или занятый слот не повод запоминать неудачу на час
	if ctx.Err() == nil {
		storeIdentity(key, id, time.Now())
	}
	return id
}

func (f *client) flareUserAgent(ctx context.Context) (string, error) {
	u, err := url.Parse(f.conf.Flaresolverr.URL)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("flaresolverr url %q has no host", f.conf.Flaresolverr.URL)
	}
	ctx, cancel := context.WithTimeout(ctx, uaProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.Scheme+"://"+u.Host+"/", nil)
	if err != nil {
		return "", err
	}
	res, err := f.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("flaresolverr status %d", res.StatusCode)
	}
	var body struct {
		UserAgent string `json:"userAgent"`
	}
	if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.UserAgent == "" {
		return "", errors.New("flaresolverr returned no userAgent")
	}
	return body.UserAgent, nil
}

func receiverOptions(conf proxy.ItemConfig) (tlscapture.Options, error) {
	if conf.CaptureHost != "" {
		return tlscapture.Options{ListenAddr: ":" + strconv.Itoa(conf.CapturePort), AdvertiseHost: conf.CaptureHost}, nil
	}
	host, err := tlscapture.AdvertiseHost(conf.URL)
	if err != nil {
		return tlscapture.Options{}, err
	}
	return tlscapture.Options{ListenAddr: net.JoinHostPort(host, strconv.Itoa(conf.CapturePort)), AdvertiseHost: host}, nil
}

func fallbackIdentity(ua string) tlscapture.Identity {
	return tlscapture.Identity{UserAgent: ua, Profile: tlsclient.ChromeProfile(ua), Mode: tlscapture.ModeBuiltin}
}

func ja4Of(fp *tlscapture.Fingerprint) string {
	if fp == nil {
		return "none"
	}
	s, err := tlscapture.JA4(fp.ClientHello)
	if err != nil {
		return "unparsed"
	}
	return s
}

func cachedIdentity(key string) (tlscapture.Identity, bool) {
	identityMu.Lock()
	defer identityMu.Unlock()
	e, ok := identityCache[key]
	if !ok || (!e.failedAt.IsZero() && time.Since(e.failedAt) >= identityRetry) {
		return tlscapture.Identity{}, false
	}
	return e.id, true
}

func storeIdentity(key string, id tlscapture.Identity, failedAt time.Time) {
	identityMu.Lock()
	defer identityMu.Unlock()
	identityCache[key] = identityEntry{id: id, failedAt: failedAt}
}
