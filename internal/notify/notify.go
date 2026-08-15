package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"trackerr/internal/system/logger"
)

const (
	KindUpdated     = "updated"
	KindBroken      = "broken"
	KindTrackerDown = "tracker_down"
	KindTrackerUp   = "tracker_up"
	KindTest        = "test"
)

const (
	sendTimeout  = 20 * time.Second
	maxErrorBody = 300
)

// var, not const: notify_test.go points this at an httptest server
var telegramAPIURL = "https://api.telegram.org/bot%s/sendMessage"

// var, not const: notify_test.go points this at an httptest server
var telegramGetUpdatesURL = "https://api.telegram.org/bot%s/getUpdates"

type Config struct {
	TelegramToken  string
	TelegramChatID string
	WebhookURL     string
	Enabled        bool
}

type Event struct {
	Kind string
	Text string
	URL  string
}

type Notifier struct {
	cfg Config
	hc  *http.Client
}

func New(cfg Config) *Notifier {
	return &Notifier{cfg: cfg, hc: &http.Client{Timeout: sendTimeout}}
}

func (n *Notifier) HasTransport() bool {
	if n == nil {
		return false
	}
	return n.telegramReady() || n.cfg.WebhookURL != ""
}

func (n *Notifier) telegramReady() bool {
	return n.cfg.TelegramToken != "" && n.cfg.TelegramChatID != ""
}

func (n *Notifier) wants() bool {
	return n.cfg.Enabled
}

// the caller is a tracker worker or the circuit breaker: a slow telegram must not
// hold either, so delivery runs detached and only complains to the log
func (n *Notifier) Notify(ev Event) {
	if !n.HasTransport() || !n.wants() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		if err := n.Send(ctx, ev); err != nil {
			logger.Warn("notify: " + err.Error())
		}
	}()
}

func (n *Notifier) Send(ctx context.Context, ev Event) error {
	if !n.HasTransport() {
		return errors.New("no notification transport configured")
	}
	var errs []error
	if n.telegramReady() {
		if err := n.sendTelegram(ctx, ev); err != nil {
			errs = append(errs, fmt.Errorf("telegram: %w", err))
		}
	}
	if n.cfg.WebhookURL != "" {
		if err := n.sendWebhook(ctx, ev); err != nil {
			errs = append(errs, fmt.Errorf("webhook: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (n *Notifier) sendTelegram(ctx context.Context, ev Event) error {
	text := ev.Text
	if ev.URL != "" {
		text += "\n" + ev.URL
	}
	// no parse_mode: release names carry brackets and underscores that would
	// otherwise have to be escaped for markdown
	body, err := json.Marshal(map[string]any{
		"chat_id":                  n.cfg.TelegramChatID,
		"text":                     text,
		"disable_web_page_preview": true,
	})
	if err != nil {
		return err
	}
	return n.post(ctx, fmt.Sprintf(telegramAPIURL, n.cfg.TelegramToken), body)
}

func ResolveChatID(ctx context.Context, token string) (string, error) {
	hc := &http.Client{Timeout: sendTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(telegramGetUpdatesURL, token), nil)
	if err != nil {
		return "", err
	}
	res, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()

	var parsed struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      []struct {
			Message *struct {
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"result"`
	}
	if err = json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if !parsed.OK || res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		msg := parsed.Description
		if msg == "" {
			msg = res.Status
		}
		return "", fmt.Errorf("telegram getUpdates: %s (status %d)", msg, res.StatusCode)
	}

	// updates can be edited_message, channel_post, callback_query, etc.; only
	// plain messages carry the chat we want, and we want the most recent one
	var chatID int64
	found := false
	for _, u := range parsed.Result {
		if u.Message != nil {
			chatID = u.Message.Chat.ID
			found = true
		}
	}
	if !found {
		return "", errors.New("бот не получил ни одного сообщения, напишите ему /start и повторите")
	}
	return strconv.FormatInt(chatID, 10), nil
}

func (n *Notifier) sendWebhook(ctx context.Context, ev Event) error {
	body, err := json.Marshal(map[string]string{
		"event": ev.Kind,
		"text":  ev.Text,
		"url":   ev.URL,
		"time":  time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return n.post(ctx, n.cfg.WebhookURL, body)
}

func (n *Notifier) post(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := n.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= http.StatusBadRequest {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
		return fmt.Errorf("status %d: %s", res.StatusCode, bytes.TrimSpace(snippet))
	}
	_, _ = io.Copy(io.Discard, res.Body)
	return nil
}
