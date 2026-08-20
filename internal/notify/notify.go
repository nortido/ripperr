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

// var, а не const: notify_test.go укорачивает это, чтобы не спать по-настоящему
var notifyRetryDelay = 30 * time.Second

// var, а не const: notify_test.go направляет это на сервер httptest
var telegramAPIURL = "https://api.telegram.org/bot%s/sendMessage"

// var, а не const: notify_test.go направляет это на сервер httptest
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

func UpdatedEvent(name, url string) Event {
	return Event{
		Kind: KindUpdated,
		Text: fmt.Sprintf("Раздача обновлена и перекачана в qBittorrent:\n%s", name),
		URL:  url,
	}
}

func BrokenEvent(name, url string, streak int) Event {
	return Event{
		Kind: KindBroken,
		Text: fmt.Sprintf("%d ошибок подряд, запись отправлена в игнор и больше не проверяется:\n%s", streak, name),
		URL:  url,
	}
}

func TrackerDownEvent(tracker, errMsg string) Event {
	return Event{
		Kind: KindTrackerDown,
		Text: fmt.Sprintf("Трекер %s недоступен: %s\nАвтоматика предохранителя приостановила проверки этого трекера.", tracker, errMsg),
	}
}

func TrackerUpEvent(tracker string) Event {
	return Event{
		Kind: KindTrackerUp,
		Text: fmt.Sprintf("Трекер %s снова доступен, проверки возобновлены.", tracker),
	}
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

// вызывающий это воркер трекера или предохранитель: медленный telegram не должен
// держать ни того ни другого, поэтому отправка идёт отдельно и только жалуется в лог
func (n *Notifier) Notify(ev Event) {
	if !n.HasTransport() || !n.wants() {
		return
	}
	go func() {
		// бюджет покрывает обе попытки плюс паузу между ними, чтобы медленная
		// первая попытка не съедала окно повтора
		ctx, cancel := context.WithTimeout(context.Background(), 2*sendTimeout+notifyRetryDelay)
		defer cancel()
		var errs []error
		if n.telegramReady() {
			if err := sendWithRetry(ctx, n.sendTelegram, ev, "telegram"); err != nil {
				errs = append(errs, err)
			}
		}
		if n.cfg.WebhookURL != "" {
			if err := sendWithRetry(ctx, n.sendWebhook, ev, "webhook"); err != nil {
				errs = append(errs, err)
			}
		}
		if err := errors.Join(errs...); err != nil {
			logger.Warn("notify: " + err.Error())
		}
	}()
}

// транспорты независимы (4xx от telegram не должен блокировать повтор вебхука),
// поэтому повтор идёт на каждый транспорт отдельно, а не перезапуском всего Send
func sendWithRetry(ctx context.Context, send func(context.Context, Event) error, ev Event, transport string) error {
	err := send(ctx, ev)
	if err == nil {
		return nil
	}
	if !isRetryable(err) {
		return fmt.Errorf("%s: %w", transport, err)
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", transport, err)
	case <-time.After(notifyRetryDelay):
	}
	if err := send(ctx, ev); err != nil {
		return fmt.Errorf("%s: second attempt: %w", transport, err)
	}
	return nil
}

func isRetryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.status >= http.StatusInternalServerError
	}
	// отсутствие статуса значит, что запрос вообще не получил HTTP-ответа
	// (дозвон/таймаут/сброс соединения), стоит второй попытки
	return true
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
	// без parse_mode: в именах релизов есть скобки и подчёркивания, которые иначе
	// пришлось бы экранировать для markdown
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

	// обновления бывают edited_message, channel_post, callback_query и прочие; только
	// обычные сообщения несут нужный чат, и нам нужно самое свежее
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
		return &statusError{status: res.StatusCode, body: string(bytes.TrimSpace(snippet))}
	}
	_, _ = io.Copy(io.Discard, res.Body)
	return nil
}

type statusError struct {
	status int
	body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("status %d: %s", e.status, e.body)
}
