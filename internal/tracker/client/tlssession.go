package client

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/helper/tlscapture"
	"ripperr/internal/helper/tlsclient"
)

type doerKey struct {
	mode    tlscapture.Mode
	profile string
	fp      *tlscapture.Fingerprint
	proxy   string
}

type TLSSession struct {
	mu      sync.Mutex
	newDoer func(tlsclient.Config) (helper.HTTPDoer, error)
	mode    tlscapture.Mode
	headers http.Header
	doer    helper.HTTPDoer
	key     doerKey
	applied bool

	solveMu         sync.Mutex
	solvedAt        time.Time
	challengeWarned atomic.Bool
}

func NewTLSSession(newDoer func(tlsclient.Config) (helper.HTTPDoer, error)) *TLSSession {
	if newDoer == nil {
		newDoer = func(cfg tlsclient.Config) (helper.HTTPDoer, error) {
			c, err := tlsclient.New(cfg)
			if err != nil {
				return nil, err
			}
			return c, nil
		}
	}
	return &TLSSession{newDoer: newDoer}
}

func WithTLSSession(s *TLSSession) Option { return func(c *client) { c.tls = s } }

func (s *TLSSession) apply(id tlscapture.Identity, base http.Header, timeout time.Duration, proxyURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	headers := identityHeaders(id, base)
	if id.Mode == tlscapture.ModeFlare {
		s.mode, s.headers, s.applied = id.Mode, headers, true
		return nil
	}
	key := doerKey{mode: id.Mode, profile: id.Profile, fp: id.Fingerprint, proxy: proxyURL}
	if s.doer != nil && s.key == key {
		s.mode, s.headers, s.applied = id.Mode, headers, true
		return nil
	}
	cfg := tlsclient.Config{
		Timeout:     timeout,
		ProxyURL:    proxyURL,
		Profile:     id.Profile,
		Direct:      true,
		NoRedirects: true,
		HeaderOrder: chromeHeaderOrder,
	}
	if fp := id.Fingerprint; fp != nil {
		if len(fp.HeaderOrder) > 0 {
			cfg.HeaderOrder = mergeHeaderOrder(fp.HeaderOrder, chromeHeaderOrder)
		}
		if id.Mode == tlscapture.ModeCustom {
			cfg.Fingerprint = fp
		}
	}
	doer, err := s.newDoer(cfg)
	if err != nil {
		return fmt.Errorf("tls session: %w", err)
	}
	s.mode, s.headers, s.doer, s.key, s.applied = id.Mode, headers, doer, key, true
	return nil
}

func (s *TLSSession) isApplied() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applied
}

func (s *TLSSession) flareOnly() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode == tlscapture.ModeFlare
}

func (s *TLSSession) page() (helper.HTTPDoer, http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doer, s.headers.Clone()
}

func (s *TLSSession) headerSet() http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.headers.Clone()
}

var identityCopied = []string{
	"user-agent", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform",
	"accept", "accept-encoding", "accept-language", "upgrade-insecure-requests",
	"sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "priority",
}

func identityHeaders(id tlscapture.Identity, base http.Header) http.Header {
	var captured map[string]string
	if id.Fingerprint != nil {
		captured = id.Fingerprint.Headers
	}
	out := http.Header{}
	for _, name := range identityCopied {
		if v := captured[name]; v != "" {
			out.Set(name, v)
		} else if v := base.Get(name); v != "" {
			out.Set(name, v)
		}
	}
	ua := out.Get("User-Agent")
	if captured["user-agent"] == "" && id.UserAgent != "" {
		ua = id.UserAgent
		out.Set("User-Agent", ua)
	}
	if captured["sec-ch-ua"] == "" {
		out.Del("sec-ch-ua")
		if major, ok := tlsclient.ChromeMajor(ua); ok {
			out.Set("sec-ch-ua", fmt.Sprintf(`"Chromium";v="%d", "Not=A?Brand";v="24", "Google Chrome";v="%d"`, major, major))
		}
	}
	if captured["sec-ch-ua-mobile"] == "" {
		out.Set("sec-ch-ua-mobile", "?0")
	}
	if captured["sec-ch-ua-platform"] == "" {
		if p := platformFromUA(ua); p != "" {
			out.Set("sec-ch-ua-platform", p)
		}
	}
	return out
}

func platformFromUA(ua string) string {
	switch {
	case strings.Contains(ua, "X11; Linux"):
		return `"Linux"`
	case strings.Contains(ua, "Windows"):
		return `"Windows"`
	case strings.Contains(ua, "Macintosh"):
		return `"macOS"`
	}
	return ""
}

func (s *TLSSession) profile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.key.profile
}
