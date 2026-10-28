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

	"ripperr/internal/config"
	"ripperr/internal/helper"
	"ripperr/internal/manager"
	"ripperr/internal/manager/client/deluge"
	"ripperr/internal/manager/client/downloadstation"
	"ripperr/internal/manager/client/qbittorrent"
	"ripperr/internal/manager/client/transmission"
	"ripperr/internal/mirrorsync"
	"ripperr/internal/model"
	"ripperr/internal/notify"
	"ripperr/internal/settings"
	"ripperr/internal/system/logger"
	"ripperr/internal/webpush"
)

func (s *Server) handleGetIntegrations(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	writeJSON(w, http.StatusOK, json.RawMessage(settings.IntegrationsJSON(&cfg)))
}

type revealReq struct {
	Field string `json:"field"`
}

type revealResp struct {
	Value string `json:"value"`
}

func writeRevealed(w http.ResponseWriter, value string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, revealResp{Value: value})
}

func (s *Server) handleRevealIntegrationSecret(w http.ResponseWriter, r *http.Request) {
	var req revealReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cfg := s.effective()
	value, ok := settings.RevealIntegrationSecret(&cfg, req.Field)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown secret field")
		return
	}
	writeRevealed(w, value)
}

var telegramChatIDResolver = notify.ResolveChatID // тесты подменяют, чтобы не ходить в Telegram

var mirrorSourceProbe = func(ctx context.Context, cfg config.App) (int, error) {
	return mirrorsync.TestSource(ctx, mirrorsync.Client(cfg.MirrorSource, cfg.Runner.DownloadProxy, 10*time.Second), mirrorsync.SourceURLs(cfg.MirrorSource))
}

var sendNotification = func(ctx context.Context, cfg notify.Config, proxyURL string, push *webpush.Store, ev notify.Event) error {
	return notify.New(cfg, proxyURL).WithPush(push).Send(ctx, ev)
}

func (s *Server) handlePutIntegrations(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := s.writableConfig()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, settingsUnreadableMsg)
		return
	}
	sent := body
	if body, err = settings.UnmaskIntegrationsPatch(body, &cfg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	body, err = s.resolveTelegramChatID(ctx, cfg, sent, body)
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

func (s *Server) resolveTelegramChatID(ctx context.Context, cfg config.App, sent, body json.RawMessage) (json.RawMessage, error) {
	var patch struct {
		Notifications struct {
			Telegram struct {
				BotToken *string `json:"bot_token"`
			} `json:"telegram"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(sent, &patch); err != nil {
		return body, nil // пусть ошибку JSON покажет MergeIntegrations
	}
	token := patch.Notifications.Telegram.BotToken
	if token == nil {
		return body, nil
	}
	if *token == "" {
		// токен стерли, chat id от старого больше не годится
		return setTelegramChatID(body, "")
	}
	if *token == settings.SecretMask {
		// маска значит "токен не менялся". Недостающий chat id ищем по действующему
		// токену, саму маску в резолвер не отдаем
		if cfg.Notify.TelegramToken == "" {
			return body, nil
		}
		token = &cfg.Notify.TelegramToken
	}
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
	// патч разбираем в map, не в структуру: чужие поля (qbit, flaresolverr, proxy,
	// webhook...) так доходят нетронутыми, даже если форма патча поменяется
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
		// проверяем qbit-карточку, даже если активен transmission: карточки
		// настраиваются и проверяются отдельно
		_, err := qbittorrent.New(qbitManagerConfigFrom(cfg))
		s.testResult(w, r, req.Kind, err)
	case "transmission":
		_, err := transmission.New(transmissionManagerConfigFrom(cfg))
		s.testResult(w, r, req.Kind, err)
	case "deluge":
		_, err := deluge.New(delugeManagerConfigFrom(cfg))
		s.testResult(w, r, req.Kind, err)
	case "downloadstation":
		_, err := downloadstation.New(downloadstationManagerConfigFrom(cfg))
		s.testResult(w, r, req.Kind, err)
	case "flaresolverr":
		s.testResult(w, r, req.Kind, testFlaresolverr(cfg))
	case "proxy":
		s.testResult(w, r, req.Kind, testProxy(cfg))
	case "notify":
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		var push *webpush.Store
		if ch := notifyChannel(req.Config); ch == "" || ch == "browser" {
			push = webpush.NewStore(s.db)
		}
		if cfg.Notify.TelegramToken != "" && cfg.Notify.TelegramChatID == "" {
			chatID, err := telegramChatIDResolver(ctx, cfg.Notify.TelegramToken, cfg.Runner.DownloadProxy)
			if err != nil {
				s.testResult(w, r, req.Kind, err)
				return
			}
			cfg.Notify.TelegramChatID = chatID
		}
		s.testResult(w, r, req.Kind, sendNotification(ctx, notify.Config(cfg.Notify), cfg.Runner.DownloadProxy, push, notify.TestEvent(notify.Link(cfg.Web.AppURL, "#dashboard"))))
	case "mirror_source":
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		n, err := mirrorSourceProbe(ctx, cfg)
		if err != nil {
			s.testResult(w, r, req.Kind, err)
			return
		}
		writeJSON(w, http.StatusOK, testResultResp{OK: true, Hosts: n})
	default:
		writeError(w, http.StatusBadRequest, "kind must be qbit, transmission, deluge, downloadstation, flaresolverr, proxy, notify, or mirror_source")
	}
}

func mergeTestIntegrationsConfig(cfg config.App, kind string, raw json.RawMessage) (config.App, error) {
	orig := cfg
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
	case "deluge":
		var c struct {
			Host     string `json:"host"`
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
			cfg.Manager.Deluge.Host = norm
		}
		cfg.Manager.Deluge.Password = c.Password
	case "downloadstation":
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
			cfg.Manager.DownloadStation.Host = norm
		}
		cfg.Manager.DownloadStation.Username = c.Username
		cfg.Manager.DownloadStation.Password = c.Password
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
	case "mirror_source":
		var c struct {
			Type   string `json:"type"`
			URL    string `json:"url"`
			Direct *bool  `json:"direct"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return cfg, err
		}
		ms := config.MirrorSource{Type: c.Type, URL: strings.TrimSpace(c.URL), Direct: cfg.MirrorSource.Direct}
		if c.Direct != nil {
			ms.Direct = *c.Direct
		}
		// в отличие от сохранения, пустой тип здесь ошибка: проверяем то, что выбрано в форме
		if ms.Type == "" {
			return cfg, fmt.Errorf("mirror source type is empty")
		}
		if err := settings.ValidateMirrorSource(ms); err != nil {
			return cfg, err
		}
		cfg.MirrorSource = ms
	case "notify":
		var c struct {
			Telegram struct {
				BotToken string `json:"bot_token"`
				ChatID   string `json:"chat_id"`
			} `json:"telegram"`
			Webhook struct {
				URL string `json:"url"`
			} `json:"webhook"`
			Email struct {
				Host     string `json:"host"`
				Port     int    `json:"port"`
				Security string `json:"security"`
				Username string `json:"username"`
				Password string `json:"password"`
				From     string `json:"from"`
				To       string `json:"to"`
			} `json:"email"`
			Pushover struct {
				AppToken string `json:"app_token"`
				UserKey  string `json:"user_key"`
			} `json:"pushover"`
			Channel  string  `json:"channel"`
			Enabled  bool    `json:"enabled"`
			Language *string `json:"language"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return cfg, err
		}
		cfg.Notify.TelegramToken = c.Telegram.BotToken
		cfg.Notify.WebhookURL = c.Webhook.URL
		cfg.Notify.EmailHost, cfg.Notify.EmailPort, cfg.Notify.EmailSecurity = c.Email.Host, c.Email.Port, c.Email.Security
		cfg.Notify.EmailUsername, cfg.Notify.EmailPassword = c.Email.Username, c.Email.Password
		cfg.Notify.EmailFrom, cfg.Notify.EmailTo = c.Email.From, c.Email.To
		cfg.Notify.PushoverToken, cfg.Notify.PushoverUser = c.Pushover.AppToken, c.Pushover.UserKey
		cfg.Notify.Enabled = c.Enabled
		// поля нет: остается сохраненный язык; чужое значение не проверяем, оно рендерится как en
		if c.Language != nil {
			cfg.Notify.Language = *c.Language
		}
		// маски возвращаем до сравнения токенов, иначе скрытый токен сошел бы
		// за новый и сбросил сохраненный chat id
		if err := settings.RestoreMaskedSecrets(&cfg, &orig); err != nil {
			return cfg, err
		}
		// chat id считает сервер, форма его не шлет. При том же токене берем
		// сохраненный, при новом сбрасываем, чтобы определился заново
		if cfg.Notify.TelegramToken != orig.Notify.TelegramToken {
			cfg.Notify.TelegramChatID = ""
		}
		if c.Telegram.ChatID != "" {
			cfg.Notify.TelegramChatID = c.Telegram.ChatID
		}
		if err := keepOnlyNotifyChannel(&cfg.Notify, c.Channel); err != nil {
			return cfg, err
		}
	default:
		return cfg, fmt.Errorf("kind must be qbit, transmission, deluge, downloadstation, flaresolverr, proxy, notify, or mirror_source")
	}
	if err := settings.RestoreMaskedSecrets(&cfg, &orig); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func notifyChannel(raw json.RawMessage) string {
	var c struct {
		Channel string `json:"channel"`
	}
	_ = json.Unmarshal(raw, &c)
	return c.Channel
}

func keepOnlyNotifyChannel(n *config.Notifications, channel string) error {
	if channel == "" {
		return nil
	}
	switch channel {
	case "telegram", "webhook", "email", "pushover", "browser":
	default:
		return fmt.Errorf("channel must be telegram, webhook, email, pushover, or browser")
	}
	if channel != "telegram" {
		n.TelegramToken, n.TelegramChatID = "", ""
	}
	if channel != "webhook" {
		n.WebhookURL = ""
	}
	if channel != "email" {
		n.EmailHost, n.EmailPort, n.EmailSecurity = "", 0, ""
		n.EmailUsername, n.EmailPassword, n.EmailFrom, n.EmailTo = "", "", "", ""
	}
	if channel != "pushover" {
		n.PushoverToken, n.PushoverUser = "", ""
	}
	return nil
}

func (s *Server) testResult(w http.ResponseWriter, r *http.Request, kind string, err error) {
	if err != nil {
		logTestFailure(r, "integration test failed: "+kind, err)
		writeJSON(w, http.StatusOK, testResultResp{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, testResultResp{OK: true})
}

type managerClient interface {
	GetCategories(ctx context.Context) ([]string, error)
	List(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error)
	ForceRedownload(ctx context.Context, torrentHash string, opts model.RedownloadOptions) error
}

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

func delugeManagerConfigFrom(cfg config.App) model.ManagerConfig {
	return model.ManagerConfig{
		Host:     cfg.Manager.Deluge.Host,
		Password: cfg.Manager.Deluge.Password,
	}
}

func downloadstationManagerConfigFrom(cfg config.App) model.ManagerConfig {
	return model.ManagerConfig{
		Host:     cfg.Manager.DownloadStation.Host,
		Username: cfg.Manager.DownloadStation.Username,
		Password: cfg.Manager.DownloadStation.Password,
	}
}

func (s *Server) managerConfig(cfg config.App) model.ManagerConfig {
	// BlackholeDir тут не ставим: при непустом пути конструктор клиента делает MkdirAll,
	// а проверка связи или загрузка категорий не должны создавать каталог.
	// handleRedownload ставит его сам после этого вызова
	switch manager.NormalizeClient(cfg.Manager.Client) {
	case manager.ClientTransmission:
		return transmissionManagerConfigFrom(cfg)
	case manager.ClientDeluge:
		return delugeManagerConfigFrom(cfg)
	case manager.ClientDownloadStation:
		return downloadstationManagerConfigFrom(cfg)
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

func logTestFailure(r *http.Request, what string, err error) {
	// остановку кнопкой обрывает клиент, это не сбой проверки
	if r.Context().Err() != nil {
		return
	}
	logger.Warn(what + ": " + err.Error())
}
