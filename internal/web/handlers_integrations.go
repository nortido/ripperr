package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"trackerr/internal/config"
	"trackerr/internal/manager/client/qbittorrent"
	"trackerr/internal/model"
	"trackerr/internal/settings"
)

func (s *Server) handleGetIntegrations(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	writeJSON(w, http.StatusOK, json.RawMessage(settings.IntegrationsJSON(&cfg)))
}

func (s *Server) handlePutIntegrations(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err = settings.MergeIntegrations(r.Context(), s.db, body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestIntegration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string          `json:"kind"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := s.effective()
	if len(req.Config) > 0 && string(req.Config) != "null" {
		var err error
		cfg, err = mergeTestIntegrationsConfig(cfg, req.Kind, req.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	switch req.Kind {
	case "qbit":
		_, err := qbittorrent.New(model.ManagerConfig{
			Host:     cfg.Manager.QbitTorrent.Host,
			Username: cfg.Manager.QbitTorrent.Username,
			Password: cfg.Manager.QbitTorrent.Password,
		})
		s.testResult(w, err)
	case "flaresolverr":
		s.testResult(w, testFlaresolverr(cfg))
	case "proxy":
		s.testResult(w, testProxy(cfg))
	default:
		writeError(w, http.StatusBadRequest, "kind must be qbit, flaresolverr, or proxy")
	}
}

func mergeTestIntegrationsConfig(cfg config.App, kind string, raw json.RawMessage) (config.App, error) {
	switch kind {
	case "qbit":
		var c struct {
			Host     string `json:"host"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return cfg, err
		}
		if c.Host != "" {
			norm, err := settings.EnsureScheme(c.Host, "http")
			if err != nil {
				return cfg, err
			}
			cfg.Manager.QbitTorrent.Host = norm
		}
		cfg.Manager.QbitTorrent.Username = c.Username
		cfg.Manager.QbitTorrent.Password = c.Password
	case "flaresolverr":
		var c struct {
			URL       string `json:"url"`
			TimeoutMS int    `json:"timeout_ms"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return cfg, err
		}
		if c.URL != "" {
			norm, err := settings.EnsureScheme(c.URL, "http")
			if err != nil {
				return cfg, err
			}
			cfg.Flaresolverr.Host = norm
		}
		if c.TimeoutMS > 0 {
			cfg.Flaresolverr.MaxTimeout = c.TimeoutMS
		}
	case "proxy":
		var c struct {
			DownloadProxy string `json:"download_proxy"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return cfg, err
		}
		if err := settings.ValidateDownloadProxy(c.DownloadProxy); err != nil {
			return cfg, err
		}
		cfg.Runner.DownloadProxy = c.DownloadProxy
	default:
		return cfg, fmt.Errorf("kind must be qbit, flaresolverr, or proxy")
	}
	return cfg, nil
}

func (s *Server) testResult(w http.ResponseWriter, err error) {
	if err != nil {
		writeJSON(w, http.StatusOK, testResultResp{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, testResultResp{OK: true})
}

type qbitCategoriesClient interface {
	GetCategories(ctx context.Context) ([]string, error)
	List(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error)
	ForceRedownload(ctx context.Context, torrentHash string) error
}

// ponytail: per-request factory — no long-lived shared qbit client in HTTP handlers
var qbitClientFactory = func(cfg model.ManagerConfig) (qbitCategoriesClient, error) {
	return qbittorrent.New(cfg)
}

func (s *Server) handleGetCategories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cfg := s.effective()
	qbc, err := qbitClientFactory(model.ManagerConfig{
		Host:     cfg.Manager.QbitTorrent.Host,
		Username: cfg.Manager.QbitTorrent.Username,
		Password: cfg.Manager.QbitTorrent.Password,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	cats, err := qbc.GetCategories(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if cats == nil {
		cats = []string{}
	}
	writeJSON(w, http.StatusOK, categoriesResp{Categories: cats})
}

func testFlaresolverr(cfg config.App) error {
	base := cfg.Flaresolverr.Host
	if base == "" {
		return fmt.Errorf("flaresolverr url is empty")
	}
	payload := `{"cmd":"request.get","url":"https://example.com","maxTimeout":` + strconv.Itoa(cfg.Flaresolverr.MaxTimeout) + `}`
	req, err := http.NewRequest(http.MethodPost, base, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("flaresolverr returned %d", resp.StatusCode)
	}
	return nil
}

func testProxy(cfg config.App) error {
	proxyURL := cfg.Runner.DownloadProxy
	if proxyURL == "" {
		return fmt.Errorf("proxy url is empty")
	}
	if err := settings.ValidateDownloadProxy(proxyURL); err != nil {
		return err
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
	}
	resp, err := client.Get("https://example.com")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}
