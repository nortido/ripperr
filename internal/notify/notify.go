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
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/metrics"
	"ripperr/internal/system/logger"
)

const (
	KindUpdated         = "updated"
	KindBroken          = "broken"
	KindTrackerDown     = "tracker_down"
	KindTrackerUp       = "tracker_up"
	KindCheckFailed     = "check_failed"
	KindMirrorSwitched  = "mirror_switched"
	KindMirrorSuggested = "mirror_suggested"
	KindTest            = "test"
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

// не const: notify_test.go укорачивает паузу, чтобы не спать по-настоящему
var notifyRetryDelay = 30 * time.Second

// эти три адреса не const: notify_test.go подменяет их на httptest
var telegramAPIURL = "https://api.telegram.org/bot%s/sendMessage"

var telegramPhotoURL = "https://api.telegram.org/bot%s/sendPhoto"

var telegramGetUpdatesURL = "https://api.telegram.org/bot%s/getUpdates"

type Config struct {
	TelegramToken    string
	TelegramChatID   string
	WebhookURL       string
	Enabled          bool
	TelegramDisabled bool
	WebhookDisabled  bool
	EmailDisabled    bool
	PushoverDisabled bool
	TelegramEvents   []string
	WebhookEvents    []string
	EmailHost        string
	EmailPort        int
	EmailSecurity    string
	EmailUsername    string
	EmailPassword    string
	EmailFrom        string
	EmailTo          string
	EmailEvents      []string
	PushoverToken    string
	PushoverUser     string
	PushoverEvents   []string
}

type Event struct {
	Kind     string
	Text     string
	Subject  string
	URL      string
	ImageURL string
	// AppPath: хеш-маршрут в интерфейсе; AppLink: полная ссылка, ее собирает Notifier
	AppPath string
	AppLink string
}

const (
	pathTrackers = "#trackers"
	appLinkLabel = "Открыть в Ripperr"
	// router.js режет name до 200 знаков
	maxLinkedName = 200
)

func dashboardPath(filter, name string) string {
	q := url.Values{}
	if filter != "" {
		q.Set("filter", filter)
	}
	if name = helper.Substr(strings.TrimSpace(name), 0, maxLinkedName); name != "" {
		q.Set("name", name)
	}
	if len(q) == 0 {
		return "#dashboard"
	}
	// Encode сортирует ключи: filter идет перед name
	return "#dashboard?" + q.Encode()
}

func Link(appURL, path string) string {
	if appURL == "" || path == "" {
		return ""
	}
	return appURL + "/" + path
}

func (e Event) plain() string {
	if e.Subject == "" {
		return e.Text
	}
	return e.Text + "\n" + e.Subject
}

func UpdatedEvent(name, url, imageURL string) Event {
	return Event{
		Kind:     KindUpdated,
		Text:     "Раздача обновлена и перекачана в torrent-клиенте:",
		Subject:  name,
		URL:      url,
		ImageURL: imageURL,
		AppPath:  dashboardPath("", name),
	}
}

func BrokenEvent(name, url string, streak int) Event {
	return Event{
		Kind:    KindBroken,
		Text:    fmt.Sprintf("Ошибок подряд: %d. Раздача отправлена в игнор и больше не проверяется:", streak),
		Subject: name,
		URL:     url,
		AppPath: dashboardPath("ignored", name),
	}
}

func TrackerDownEvent(tracker, errMsg string) Event {
	return Event{
		Kind:    KindTrackerDown,
		Text:    fmt.Sprintf("Трекер %s недоступен: %s\nПроверки этого трекера приостановлены.", tracker, errMsg),
		AppPath: pathTrackers,
	}
}

func TrackerUpEvent(tracker string) Event {
	return Event{
		Kind:    KindTrackerUp,
		Text:    fmt.Sprintf("Трекер %s снова доступен, проверки возобновлены.", tracker),
		AppPath: pathTrackers,
	}
}

func MirrorSwitchedEvent(tracker, from, to string) Event {
	return Event{
		Kind:    KindMirrorSwitched,
		Text:    fmt.Sprintf("Трекер %s: %s не отвечает, проверки переключены на %s.", tracker, from, to),
		AppPath: pathTrackers,
	}
}

func MirrorDigestEvent(lines []string) Event {
	return Event{
		Kind:    KindMirrorSuggested,
		Text:    "Зеркала трекеров: " + strings.Join(lines, "; "),
		AppPath: pathTrackers,
	}
}

func MirrorPausedEvent(tracker, host string) Event {
	return Event{
		Kind:    KindMirrorSuggested,
		Text:    fmt.Sprintf("Трекер %s на паузе: отвечает только новое зеркало %s. Подтвердите его в настройках трекера.", tracker, host),
		AppPath: pathTrackers,
	}
}

func CheckFailedEvent(name, url, errMsg string) Event {
	return Event{
		Kind:    KindCheckFailed,
		Text:    "Ошибка проверки раздачи: " + errMsg,
		Subject: name,
		URL:     url,
		AppPath: dashboardPath("", name),
	}
}

func DefaultEvents() []string {
	// check_failed тут нет: шум от каждой ошибки включают руками
	return []string{KindUpdated, KindBroken, KindTrackerDown, KindTrackerUp, KindMirrorSwitched, KindMirrorSuggested}
}

func KnownEvent(kind string) bool {
	switch kind {
	case KindUpdated, KindBroken, KindTrackerDown, KindTrackerUp, KindCheckFailed, KindMirrorSwitched, KindMirrorSuggested:
		return true
	}
	return false
}

func wants(events []string, kind string) bool {
	if kind == KindTest {
		return true
	}
	if events == nil {
		events = DefaultEvents()
	}
	return slices.Contains(events, kind)
}

type Notifier struct {
	cfg    Config
	hc     *http.Client
	appURL string
}

func New(cfg Config, proxyURL string) *Notifier {
	return &Notifier{cfg: cfg, hc: newHTTPClient(proxyURL)}
}

func (n *Notifier) WithAppURL(appURL string) *Notifier {
	n.appURL = appURL
	return n
}

func (n *Notifier) withLink(ev Event) Event {
	if ev.AppLink == "" {
		ev.AppLink = Link(n.appURL, ev.AppPath)
	}
	return ev
}

func newHTTPClient(proxyURL string) *http.Client {
	// api.telegram.org из РФ напрямую не открывается, поэтому идем через прокси трекеров.
	// Пустой адрес: прямое соединение
	return &http.Client{Timeout: sendTimeout, Transport: helper.ProxyTransport(proxyURL)}
}

func (n *Notifier) HasTransport() bool {
	if n == nil {
		return false
	}
	return n.telegramReady() || n.cfg.WebhookURL != "" || n.emailReady() || n.pushoverReady()
}

func (n *Notifier) telegramReady() bool {
	return n.cfg.TelegramToken != "" && n.cfg.TelegramChatID != ""
}

func (n *Notifier) wants() bool {
	return n.cfg.Enabled
}

func (n *Notifier) Notify(ev Event) {
	// сюда приходят из воркера трекера и предохранителя, их держать нельзя. Каждый канал
	// шлет в своей горутине, ошибки только в лог
	if !n.HasTransport() || !n.wants() {
		return
	}
	ev = n.withLink(ev)
	if !n.cfg.TelegramDisabled && n.telegramReady() && wants(n.cfg.TelegramEvents, ev.Kind) {
		n.deliver(n.sendTelegram, ev, "telegram")
	}
	if !n.cfg.WebhookDisabled && n.cfg.WebhookURL != "" && wants(n.cfg.WebhookEvents, ev.Kind) {
		n.deliver(n.sendWebhook, ev, "webhook")
	}
	if !n.cfg.EmailDisabled && n.emailReady() && wants(n.cfg.EmailEvents, ev.Kind) {
		n.deliver(n.sendEmail, ev, "email")
	}
	if !n.cfg.PushoverDisabled && n.pushoverReady() && wants(n.cfg.PushoverEvents, ev.Kind) {
		n.deliver(n.sendPushover, ev, "pushover")
	}
}

func (n *Notifier) deliver(send func(context.Context, Event) error, ev Event, transport string) {
	go func() {
		// бюджет покрывает обе попытки плюс паузу между ними, чтобы медленная
		// первая попытка не съедала окно повтора
		ctx, cancel := context.WithTimeout(context.Background(), 2*sendTimeout+notifyRetryDelay)
		defer cancel()
		if err := sendWithRetry(ctx, send, ev, transport); err != nil {
			logger.Warn("notify: " + err.Error())
			metrics.Notifications.Inc(transport, "error")
			return
		}
		metrics.Notifications.Inc(transport, "ok")
	}()
}

func sendWithRetry(ctx context.Context, send func(context.Context, Event) error, ev Event, transport string) error {
	// повторяем каждый транспорт отдельно: 4xx от telegram не должен мешать повтору вебхука
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
	if errors.As(err, new(permanentError)) {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.status >= http.StatusInternalServerError
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code < 500
	}
	// нет статуса, значит HTTP-ответа не было вовсе (дозвон, таймаут, сброс).
	// Стоит повторить
	return true
}

func (n *Notifier) Send(ctx context.Context, ev Event) error {
	if !n.HasTransport() {
		return errors.New("no notification transport configured")
	}
	ev = n.withLink(ev)
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
	if n.emailReady() {
		if err := n.sendEmail(ctx, ev); err != nil {
			errs = append(errs, fmt.Errorf("email: %w", err))
		}
	}
	if n.pushoverReady() {
		if err := n.sendPushover(ctx, ev); err != nil {
			errs = append(errs, fmt.Errorf("pushover: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (n *Notifier) sendTelegram(ctx context.Context, ev Event) error {
	if ev.ImageURL != "" {
		// картинку Telegram качает сам и без наших кук. Постер за хотлинк-защитой или с приватного
		// трекера даст ошибку, тогда шлем обычное сообщение, чтобы уведомление не пропало
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

func withLinkLine(text, line string, limit int) string {
	// ссылка идет только целиком: текст ради нее не режем, а обрезанная ссылка не работает
	text = helper.Substr(text, 0, limit)
	if line == "" || len([]rune(text))+1+len([]rune(line)) > limit {
		return text
	}
	return text + "\n" + line
}

func telegramText(ev Event, limit int) (text, parseMode string) {
	linkLine := ""
	if ev.AppLink != "" {
		linkLine = appLinkLabel + ": " + ev.AppLink
	}
	// разметка только когда есть и имя, и ссылка. В HTML экранировать надо лишь & < >
	if ev.Subject == "" || ev.URL == "" {
		text = ev.plain()
		if ev.URL != "" {
			text += "\n" + ev.URL
		}
		if linkLine == "" {
			return helper.Substr(text, 0, limit), ""
		}
		return withLinkLine(text, linkLine, limit), ""
	}
	// режем имя до сборки разметки, иначе обрезка порвет тег ссылки.
	// Telegram считает длину по видимому тексту
	subject := ev.Subject
	room := limit - len([]rune(ev.Text)) - 1
	if ev.AppLink != "" {
		room -= len([]rune(appLinkLabel)) + 1
	}
	if room < len([]rune(subject)) {
		subject = helper.Substr(subject, 0, max(room, 0))
	}
	text = html.EscapeString(ev.Text) + "\n" +
		`<a href="` + html.EscapeString(ev.URL) + `">` + html.EscapeString(subject) + `</a>`
	if ev.AppLink != "" {
		text += "\n" + `<a href="` + html.EscapeString(ev.AppLink) + `">` + appLinkLabel + `</a>`
	}
	return text, "HTML"
}

func (n *Notifier) sendTelegramPhoto(ctx context.Context, ev Event) error {
	caption, parseMode := telegramText(ev, maxCaption)
	// до части хостингов у Telegram нет доступа (fastpic ему отвечает ошибкой), поэтому
	// сначала качаем сами и шлем файлом. Ссылку отдаем, только если не скачалось: бывает
	// и наоброт, домен закрыт для нас, но открыт для Telegram
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

func ResolveChatID(ctx context.Context, token, proxyURL string) (_ string, err error) {
	defer func() { err = redactToken(helper.RedactURLError(err), token) }()
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

	// кроме message бывают edited_message, channel_post, callback_query и прочие.
	// Нужный чат только в обычных сообщениях, берем самое свежее
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
	payload := map[string]string{
		"event": ev.Kind,
		"text":  ev.plain(),
		"url":   ev.URL,
		"time":  time.Now().Format(time.RFC3339),
	}
	if ev.AppLink != "" {
		payload["app_url"] = ev.AppLink
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return n.post(ctx, n.cfg.WebhookURL, body)
}

func (n *Notifier) post(ctx context.Context, url string, body []byte) error {
	return n.postBody(ctx, url, "application/json", body)
}

func (n *Notifier) postBody(ctx context.Context, url, contentType string, body []byte) (err error) {
	defer func() { err = redactToken(helper.RedactURLError(err), n.cfg.TelegramToken) }()
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

func redactToken(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	// токен лежит в пути telegram-адреса, а *url.Error печатает URL целиком.
	// Без замены он утечет в лог и в ответ POST /api/integrations/test
	msg := err.Error()
	if !strings.Contains(msg, token) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, token, "***"))
}
