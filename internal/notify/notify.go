package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"trackerr/internal/helper"
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
	sendTimeout   = 20 * time.Second
	maxErrorBody  = 300
	maxCaption    = 1024
	maxMessage    = 4096
	maxPhotoBytes = 10 << 20
	// хостинги постеров отдают картинку только браузероподобным клиентам
	posterUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// var, а не const: notify_test.go укорачивает это, чтобы не спать по-настоящему
var notifyRetryDelay = 30 * time.Second

// var, а не const: notify_test.go направляет это на сервер httptest
var telegramAPIURL = "https://api.telegram.org/bot%s/sendMessage"

// var, а не const: notify_test.go направляет это на сервер httptest
var telegramPhotoURL = "https://api.telegram.org/bot%s/sendPhoto"

// var, а не const: notify_test.go направляет это на сервер httptest
var telegramGetUpdatesURL = "https://api.telegram.org/bot%s/getUpdates"

type Config struct {
	TelegramToken  string
	TelegramChatID string
	WebhookURL     string
	Enabled        bool
}

type Event struct {
	Kind     string
	Text     string
	Subject  string
	URL      string
	ImageURL string
}

// в telegram Subject уходит ссылкой на страницу трекера, в вебхуке и в остальных
// каналах он просто дописывается к тексту отдельной строкой
func (e Event) plain() string {
	if e.Subject == "" {
		return e.Text
	}
	return e.Text + "\n" + e.Subject
}

func UpdatedEvent(name, url, imageURL string) Event {
	return Event{
		Kind:     KindUpdated,
		Text:     "Раздача обновлена и перекачана в qBittorrent:",
		Subject:  name,
		URL:      url,
		ImageURL: imageURL,
	}
}

func BrokenEvent(name, url string, streak int) Event {
	return Event{
		Kind:    KindBroken,
		Text:    fmt.Sprintf("%d ошибок подряд, запись отправлена в игнор и больше не проверяется:", streak),
		Subject: name,
		URL:     url,
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

func New(cfg Config, proxyURL string) *Notifier {
	return &Notifier{cfg: cfg, hc: newHTTPClient(proxyURL)}
}

// api.telegram.org в РФ недоступен напрямую, поэтому уведомления ходят через тот же
// прокси, что и трекеры; пустой адрес оставляет прямое соединение
func newHTTPClient(proxyURL string) *http.Client {
	return &http.Client{Timeout: sendTimeout, Transport: helper.ProxyTransport(proxyURL)}
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
	if ev.ImageURL != "" {
		// картинку Telegram скачивает сам и без наших кук: постер за хотлинк-защитой
		// или с приватного трекера отдаст ошибку, и тогда шлём обычное сообщение,
		// чтобы уведомление не пропало вовсе
		err := n.sendTelegramPhoto(ctx, ev)
		if err == nil {
			return nil
		}
		logger.Warn("notify: telegram sendPhoto: " + err.Error())
	}
	text, parseMode := telegramText(ev, maxMessage)
	payload := map[string]any{
		"chat_id":                  n.cfg.TelegramChatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return n.post(ctx, fmt.Sprintf(telegramAPIURL, n.cfg.TelegramToken), body)
}

// имя раздачи уходит ссылкой на её страницу, чтобы не дублировать адрес отдельной
// строкой. Разметка только там, где есть и имя, и ссылка: без parse_mode скобки и
// подчёркивания в именах релизов не пришлось бы экранировать, а в режиме HTML
// экранирования требуют лишь & < >, чего html.EscapeString и добивается.
// Лимит режет имя до сборки разметки, иначе обрезка порвала бы тег ссылки; сам
// telegram считает длину по видимому тексту, а не по разметке
func telegramText(ev Event, limit int) (text, parseMode string) {
	if ev.Subject == "" || ev.URL == "" {
		text = ev.plain()
		if ev.URL != "" {
			text += "\n" + ev.URL
		}
		return helper.Substr(text, 0, limit), ""
	}
	subject := ev.Subject
	if room := limit - len([]rune(ev.Text)) - 1; room < len([]rune(subject)) {
		subject = helper.Substr(subject, 0, room)
	}
	return html.EscapeString(ev.Text) + "\n" +
		`<a href="` + html.EscapeString(ev.URL) + `">` + html.EscapeString(subject) + `</a>`, "HTML"
}

func (n *Notifier) sendTelegramPhoto(ctx context.Context, ev Event) error {
	caption, parseMode := telegramText(ev, maxCaption)
	// до части картиночных хостингов у Telegram нет доступа (fastpic на ссылку от
	// него отвечает ошибкой), поэтому сначала пробуем забрать байты сами и отдать
	// файлом, и только если картинка не скачалась, отдаём ссылку: бывает и
	// наоборот, когда домен трекера закрыт для нас, но открыт для Telegram
	img, err := n.fetchImage(ctx, ev.ImageURL)
	if err == nil {
		return n.postPhotoUpload(ctx, img, caption, parseMode)
	}
	logger.Warn("notify: poster download: " + err.Error())

	payload := map[string]any{
		"chat_id": n.cfg.TelegramChatID,
		"photo":   ev.ImageURL,
		"caption": caption,
	}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return n.post(ctx, fmt.Sprintf(telegramPhotoURL, n.cfg.TelegramToken), body)
}

func (n *Notifier) fetchImage(ctx context.Context, imageURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	// хостинги постеров отдают картинку только браузероподобным клиентам
	req.Header.Set("User-Agent", posterUserAgent)
	res, err := n.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: status %d", imageURL, res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		return nil, fmt.Errorf("%s: content-type %q is not an image", imageURL, ct)
	}
	// на один больше лимита: так видно, что картинка его превысила, а не легла впритык
	img, err := io.ReadAll(io.LimitReader(res.Body, maxPhotoBytes+1))
	if err != nil {
		return nil, err
	}
	if len(img) > maxPhotoBytes {
		return nil, fmt.Errorf("%s: larger than %d bytes", imageURL, maxPhotoBytes)
	}
	return img, nil
}

func (n *Notifier) postPhotoUpload(ctx context.Context, img []byte, caption, parseMode string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("chat_id", n.cfg.TelegramChatID); err != nil {
		return err
	}
	if err := mw.WriteField("caption", caption); err != nil {
		return err
	}
	if parseMode != "" {
		if err := mw.WriteField("parse_mode", parseMode); err != nil {
			return err
		}
	}
	part, err := mw.CreateFormFile("photo", "poster.jpg")
	if err != nil {
		return err
	}
	if _, err = part.Write(img); err != nil {
		return err
	}
	if err = mw.Close(); err != nil {
		return err
	}
	return n.postBody(ctx, fmt.Sprintf(telegramPhotoURL, n.cfg.TelegramToken), mw.FormDataContentType(), buf.Bytes())
}

func ResolveChatID(ctx context.Context, token, proxyURL string) (string, error) {
	hc := newHTTPClient(proxyURL)
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
		"text":  ev.plain(),
		"url":   ev.URL,
		"time":  time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return n.post(ctx, n.cfg.WebhookURL, body)
}

func (n *Notifier) post(ctx context.Context, url string, body []byte) error {
	return n.postBody(ctx, url, "application/json", body)
}

func (n *Notifier) postBody(ctx context.Context, url, contentType string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
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
