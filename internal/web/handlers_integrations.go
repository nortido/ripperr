package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"trackerr/internal/config"
	"trackerr/internal/helper"
	"trackerr/internal/manager"
	"trackerr/internal/manager/client/qbittorrent"
	"trackerr/internal/manager/client/transmission"
	"trackerr/internal/model"
	"trackerr/internal/notify"
	"trackerr/internal/settings"
)

func (s *Server) handleGetIntegrations(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	writeJSON(w, http.StatusOK, json.RawMessage(settings.IntegrationsJSON(&cfg)))
}

// ponytail: переменные пакета, чтобы тесты подменяли Telegram без реального вызова, тот же приём, что и managerClientFactory
var telegramChatIDResolver = notify.ResolveChatID

var sendNotification = func(ctx context.Context, cfg notify.Config, proxyURL string, ev notify.Event) error {
	return notify.New(cfg, proxyURL).Send(ctx, ev)
}

func (s *Server) handlePutIntegrations(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	body, err = s.resolveTelegramChatID(ctx, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err = settings.MergeIntegrations(ctx, s.db, body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resolveTelegramChatID(ctx context.Context, body json.RawMessage) (json.RawMessage, error) {
	var patch struct {
		Notifications struct {
			Telegram struct {
				BotToken *string `json:"bot_token"`
			} `json:"telegram"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(body, &patch); err != nil {
		return body, nil // пусть ошибку JSON покажет MergeIntegrations
	}
	token := patch.Notifications.Telegram.BotToken
	if token == nil {
		return body, nil
	}
	if *token == "" {
		// токен очищен: выбрасываем любой chat id, вычисленный для старого
		return setTelegramChatID(body, "")
	}
	cfg := s.effective()
	if *token == cfg.Notify.TelegramToken && cfg.Notify.TelegramChatID != "" {
		return body, nil
	}
	chatID, err := telegramChatIDResolver(ctx, *token, cfg.Runner.DownloadProxy)
	if err != nil {
		return nil, err
	}
	return setTelegramChatID(body, chatID)
}

func setTelegramChatID(body json.RawMessage, chatID string) (json.RawMessage, error) {
	// обходим сырой патч как map[string]json.RawMessage, а не зеркальной
	// структурой, чтобы поля, которые мы тут не трогаем (qbit, flaresolverr, proxy,
	// webhook, enabled...), оставались нетронутыми даже по мере изменения формы патча
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, err
	}
	var notifications map[string]json.RawMessage
	if raw, ok := top["notifications"]; ok {
		if err := json.Unmarshal(raw, &notifications); err != nil {
			return nil, err
		}
	} else {
		notifications = map[string]json.RawMessage{}
	}
	var telegram map[string]json.RawMessage
	if raw, ok := notifications["telegram"]; ok {
		if err := json.Unmarshal(raw, &telegram); err != nil {
			return nil, err
		}
	} else {
		telegram = map[string]json.RawMessage{}
	}
	idJSON, err := json.Marshal(chatID)
	if err != nil {
		return nil, err
	}
	telegram["chat_id"] = idJSON
	telegramJSON, err := json.Marshal(telegram)
	if err != nil {
		return nil, err
	}
	notifications["telegram"] = telegramJSON
	notificationsJSON, err := json.Marshal(notifications)
	if err != nil {
		return nil, err
	}
	top["notifications"] = notificationsJSON
	return json.Marshal(top)
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
		// проверяет именно qbit-карточку, даже когда активен transmission: обе
		// карточки настраиваются и проверяются независимо друг от друга
		_, err := qbittorrent.New(qbitManagerConfigFrom(cfg))
		s.testResult(w, err)
	case "transmission":
		_, err := transmission.New(transmissionManagerConfigFrom(cfg))
		s.testResult(w, err)
	case "flaresolverr":
		s.testResult(w, testFlaresolverr(cfg))
	case "proxy":
		s.testResult(w, testProxy(cfg))
	case "notify":
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if cfg.Notify.TelegramToken != "" && cfg.Notify.TelegramChatID == "" {
			chatID, err := telegramChatIDResolver(ctx, cfg.Notify.TelegramToken, cfg.Runner.DownloadProxy)
			if err != nil {
				s.testResult(w, err)
				return
			}
			cfg.Notify.TelegramChatID = chatID
		}
		s.testResult(w, sendNotification(ctx, notify.Config(cfg.Notify), cfg.Runner.DownloadProxy, notify.Event{Kind: notify.KindTest, Text: "trackerr: connectivity test"}))
	default:
		writeError(w, http.StatusBadRequest, "kind must be qbit, transmission, flaresolverr, proxy, or notify")
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
	case "transmission":
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
			cfg.Manager.Transmission.Host = norm
		}
		cfg.Manager.Transmission.Username = c.Username
		cfg.Manager.Transmission.Password = c.Password
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
	case "notify":
		var c struct {
			Telegram struct {
				BotToken string `json:"bot_token"`
				ChatID   string `json:"chat_id"`
			} `json:"telegram"`
			Webhook struct {
				URL string `json:"url"`
			} `json:"webhook"`
			Enabled bool `json:"enabled"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return cfg, err
		}
		// chat id вычисляет сервер, форма его не присылает: при том же токене берём
		// сохранённый, при смене токена сбрасываем, чтобы он определился заново
		if c.Telegram.BotToken != cfg.Notify.TelegramToken {
			cfg.Notify.TelegramChatID = ""
		}
		if c.Telegram.ChatID != "" {
			cfg.Notify.TelegramChatID = c.Telegram.ChatID
		}
		cfg.Notify.TelegramToken = c.Telegram.BotToken
		cfg.Notify.WebhookURL = c.Webhook.URL
		cfg.Notify.Enabled = c.Enabled
	default:
		return cfg, fmt.Errorf("kind must be qbit, transmission, flaresolverr, proxy, or notify")
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

type managerClient interface {
	GetCategories(ctx context.Context) ([]string, error)
	List(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error)
	ForceRedownload(ctx context.Context, torrentHash string) error
}

// ponytail: фабрика на каждый запрос, никакого долгоживущего общего клиента загрузок в HTTP-обработчиках
var managerClientFactory = func(kind string, cfg model.ManagerConfig) (managerClient, error) {
	return manager.NewClient(kind, cfg)
}

func qbitManagerConfigFrom(cfg config.App) model.ManagerConfig {
	return model.ManagerConfig{
		Host:     cfg.Manager.QbitTorrent.Host,
		Username: cfg.Manager.QbitTorrent.Username,
		Password: cfg.Manager.QbitTorrent.Password,
	}
}

func transmissionManagerConfigFrom(cfg config.App) model.ManagerConfig {
	return model.ManagerConfig{
		Host:     cfg.Manager.Transmission.Host,
		Username: cfg.Manager.Transmission.Username,
		Password: cfg.Manager.Transmission.Password,
	}
}

func (s *Server) managerConfig(cfg config.App) model.ManagerConfig {
	// BlackholeDir здесь не выставляется: конструктор клиента делает по нему MkdirAll, когда он непустой,
	// а проверка связи или загрузка категорий не должны создавать этот каталог
	// побочным эффектом. handleRedownload выставляет его явно после этого вызова.
	if manager.NormalizeClient(cfg.Manager.Client) == manager.ClientTransmission {
		return transmissionManagerConfigFrom(cfg)
	}
	return qbitManagerConfigFrom(cfg)
}

func (s *Server) handleGetCategories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cfg := s.effective()
	mc, err := managerClientFactory(cfg.Manager.Client, s.managerConfig(cfg))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	cats, err := mc.GetCategories(ctx)
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
	client := &http.Client{Timeout: 30 * time.Second, Transport: helper.DirectTransport()}
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
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: helper.ProxyTransport(proxyURL),
	}
	resp, err := client.Get("https://example.com")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}
