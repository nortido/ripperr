package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/metrics"
	"ripperr/internal/model/storage"
	"ripperr/internal/system/logger"
	"ripperr/internal/webpush"

	"golang.org/x/net/proxy"
)

const (
	KindUpdated        = "updated"
	KindBroken         = "broken"
	KindTrackerDown    = "tracker_down"
	KindTrackerUp      = "tracker_up"
	KindCheckFailed    = "check_failed"
	KindMirrorSwitched = "mirror_switched"
	KindTest           = "test"

	// старое имя события: принимаем из PUT и сохраненных списков, везде дальше это mirror_switched
	legacyMirrorSuggested = "mirror_suggested"
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
	EmailHost        string
	EmailPort        int
	EmailSecurity    string
	EmailUsername    string
	EmailPassword    string
	EmailFrom        string
	EmailTo          string
	PushoverToken    string
	PushoverUser     string
	BrowserDisabled  bool
	Language         string

	Events []string
}

type Event struct {
	Kind     string
	Text     string
	Subject  string
	URL      string
	ImageURL string
	// AppPath: хеш-маршрут в интерфейсе; AppLink: полная ссылка, ее собирает Notifier
	AppPath   string
	AppLink   string
	msg       string
	args      []string
	digest    []MirrorDigestItem
	linkLabel string
	lang      string
	urlHash   string
	tracker   string
	category  string
}

const (
	pathTrackers = "#trackers"
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
		msg:      msgUpdated,
		Subject:  name,
		URL:      url,
		ImageURL: imageURL,
		AppPath:  dashboardPath("", name),
	}.withDefaultText()
}

func BrokenEvent(name, url string, streak int) Event {
	return Event{
		Kind:    KindBroken,
		msg:     msgBroken,
		args:    []string{strconv.Itoa(streak)},
		Subject: name,
		URL:     url,
		AppPath: dashboardPath("ignored", name),
	}.withDefaultText()
}

func TrackerDownEvent(tracker, errMsg string) Event {
	return Event{
		Kind:    KindTrackerDown,
		msg:     msgTrackerDown,
		args:    []string{tracker, errMsg},
		AppPath: pathTrackers,
	}.withDefaultText()
}

func TrackerUpEvent(tracker string) Event {
	return Event{
		Kind:    KindTrackerUp,
		msg:     msgTrackerUp,
		args:    []string{tracker},
		AppPath: pathTrackers,
	}.withDefaultText()
}

func MirrorSwitchedEvent(tracker, from, to string) Event {
	return Event{
		Kind:    KindMirrorSwitched,
		msg:     msgMirrorSwitched,
		args:    []string{tracker, from, to},
		AppPath: pathTrackers,
	}.withDefaultText()
}

func MirrorDigestEvent(items []MirrorDigestItem) Event {
	return Event{
		Kind:    KindMirrorSwitched,
		msg:     msgMirrorDigest,
		digest:  items,
		AppPath: pathTrackers,
	}.withDefaultText()
}

func MirrorPausedEvent(tracker, host string) Event {
	return Event{
		Kind:    KindMirrorSwitched,
		msg:     msgMirrorPaused,
		args:    []string{tracker, host},
		AppPath: pathTrackers,
	}.withDefaultText()
}

func CheckFailedEvent(name, url, errMsg string) Event {
	return Event{
		Kind:    KindCheckFailed,
		msg:     msgCheckFailed,
		args:    []string{errMsg},
		Subject: name,
		URL:     url,
		AppPath: dashboardPath("", name),
	}.withDefaultText()
}

func TestEvent(appLink string) Event {
	return Event{Kind: KindTest, msg: msgTest, AppLink: appLink}.withDefaultText()
}

var eventOrder = []string{KindUpdated, KindBroken, KindTrackerDown, KindTrackerUp, KindMirrorSwitched, KindCheckFailed}

func DefaultEvents() []string {
	// check_failed тут нет: шум от каждой ошибки включают руками
	return []string{KindUpdated, KindBroken, KindTrackerDown, KindTrackerUp, KindMirrorSwitched}
}

func KnownEvent(kind string) bool {
	return slices.Contains(eventOrder, kind)
}

func CanonicalEvent(kind string) (string, bool) {
	if kind == legacyMirrorSuggested {
		return KindMirrorSwitched, true
	}
	return kind, KnownEvent(kind)
}

func NormalizeEvents(list []string) []string {
	if list == nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, kind := range list {
		if k, ok := CanonicalEvent(kind); ok && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

func UnionEvents(lists ...[]string) []string {
	out := make([]string, 0, len(eventOrder))
	for _, kind := range eventOrder {
		if slices.ContainsFunc(lists, func(l []string) bool {
			return slices.ContainsFunc(l, func(k string) bool { c, _ := CanonicalEvent(k); return c == kind })
		}) {
			out = append(out, kind)
		}
	}
	return out
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
	cfg      Config
	hc       *http.Client
	posterHC *http.Client
	appURL   string
	push     *webpush.Store
	events   EventRecorder
	mutes    *TelegramMutes
	proxyURL string
	// последний getUpdates ответил 409: чужой поллер или webhook, кнопка не сработает
	tgConflict atomic.Bool
}

func New(cfg Config, proxyURL string) *Notifier {
	return &Notifier{cfg: cfg, hc: newHTTPClient(proxyURL), posterHC: newPosterClient(proxyURL), proxyURL: proxyURL}
}

func (n *Notifier) WithTelegramMutes(m *TelegramMutes) *Notifier {
	n.mutes = m
	return n
}

func (n *Notifier) WithAppURL(appURL string) *Notifier {
	n.appURL = appURL
	return n
}

type EventRecorder interface {
	RecordEvent(e storage.EventEntry) bool
}

func (n *Notifier) WithEvents(r EventRecorder) *Notifier {
	n.events = r
	return n
}

func (n *Notifier) WithPush(p *webpush.Store) *Notifier {
	n.push = p
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

var posterTransports sync.Map

// не func: notify_test.go подменяет разрешение имен
var lookupPosterHost = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func newPosterClient(proxyURL string) *http.Client {
	proxyURL = strings.TrimSpace(proxyURL)
	base := helper.ProxyTransport(proxyURL)
	// ключ по базовому транспорту: helper уже кеширует его вместе с NO_PROXY, а notify.New
	// зовут на каждом переходе предохранителя, и пулы соединений плодить нельзя
	var key any = ""
	if base != nil {
		key = base
	}
	if tr, ok := posterTransports.Load(key); ok {
		return &http.Client{Timeout: sendTimeout, Transport: tr.(http.RoundTripper)}
	}
	var tr *http.Transport
	if bt, ok := base.(*http.Transport); ok {
		tr = bt.Clone()
	} else {
		tr = http.DefaultTransport.(*http.Transport).Clone()
	}
	// постеры не должны идти через HTTP(S)_PROXY из окружения: это обход проверок
	if proxyURL == "" {
		tr.Proxy = nil
	}
	proxyAddr := proxyDialAddr(proxyURL)
	plain := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	guarded := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: publicOnly}
	var rt http.RoundTripper = tr
	if socks := socksPosterDialer(proxyURL, plain); socks != nil {
		// имя резолвим и проверяем тут, а прокси получает готовый IP: иначе он мог бы
		// заново резолвить имя в локальный адрес (DNS rebinding)
		tr.Proxy = nil
		tr.DialContext = socks
	} else {
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			// сам прокси может стоять в локальной сети: к нему идем без проверки, а он уже сам
			// резолвит адрес постера
			if proxyAddr != "" && addr == proxyAddr {
				return plain.DialContext(ctx, network, addr)
			}
			return guarded.DialContext(ctx, network, addr)
		}
		if proxyAddr != "" {
			rt = posterProxyGuard{next: tr, proxyAddr: proxyAddr}
		}
	}
	actual, _ := posterTransports.LoadOrStore(key, rt)
	return &http.Client{Timeout: sendTimeout, Transport: actual.(http.RoundTripper)}
}

func socksPosterDialer(raw string, forward proxy.Dialer) func(context.Context, string, string) (net.Conn, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "socks5" && u.Scheme != "socks5h") || u.Hostname() == "" {
		return nil
	}
	var auth *proxy.Auth
	if u.User != nil {
		pass, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: pass}
	}
	proxyHost := u.Hostname()
	d, err := proxy.SOCKS5("tcp", proxyDialAddr(raw), auth, forward)
	if err != nil {
		return nil
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := resolvePosterHost(ctx, host)
		if err != nil {
			return nil, err
		}
		// прокси сам может ходить на свой IP (hairpin): адрес постера не должен совпадать с адресом прокси
		proxyIPs, err := resolveProxyHost(ctx, proxyHost)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if slices.Contains(proxyIPs, ip.Unmap()) {
				return nil, errors.New("poster is on the proxy address")
			}
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := cd.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, lastErr
	}
}

func resolveProxyHost(ctx context.Context, host string) ([]netip.Addr, error) {
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		var lerr error
		if ips, lerr = lookupPosterHost(ctx, host); lerr != nil {
			return nil, fmt.Errorf("socks proxy host %s: %w", host, lerr)
		}
	}
	out := make([]netip.Addr, len(ips))
	for i, ip := range ips {
		out[i] = ip.Unmap()
	}
	return out, nil
}

func resolvePosterHost(ctx context.Context, host string) ([]netip.Addr, error) {
	var ips []netip.Addr
	var err error
	if ip, perr := netip.ParseAddr(host); perr == nil {
		ips = []netip.Addr{ip}
	} else if ips, err = lookupPosterHost(ctx, host); err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("poster host %s did not resolve", host)
	}
	for _, ip := range ips {
		if err = publicAddr(ip); err != nil {
			return nil, err
		}
	}
	return ips, nil
}

func proxyDialAddr(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

type posterProxyGuard struct {
	next      http.RoundTripper
	proxyAddr string
}

func (g posterProxyGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := g.check(req); err != nil {
		return nil, err
	}
	return g.next.RoundTrip(req)
}

// прокси сам резолвит имя заново, так что подмена DNS между проверкой и запросом остается
func (g posterProxyGuard) check(req *http.Request) error {
	host := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		port = "80"
		if req.URL.Scheme == "https" {
			port = "443"
		}
	}
	if strings.EqualFold(net.JoinHostPort(host, port), g.proxyAddr) {
		return errors.New("poster is on the proxy address")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return publicAddr(ip)
	}
	ips, err := lookupPosterHost(req.Context(), host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if err = publicAddr(ip); err != nil {
			return err
		}
	}
	return nil
}

var (
	cgnat          = netip.MustParsePrefix("100.64.0.0/10")
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local     = netip.MustParsePrefix("64:ff9b:1::/48")
)

func publicAddr(ip netip.Addr) error {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || cgnat.Contains(ip) || nat64Local.Contains(ip) {
		return fmt.Errorf("poster address %s is not public", ip)
	}
	if nat64WellKnown.Contains(ip) {
		// в 64:ff9b::/96 IPv4 лежит в последних четырех байтах: за ним может стоять частный адрес
		b := ip.As16()
		return publicAddr(netip.AddrFrom4([4]byte(b[12:])))
	}
	return nil
}

func publicOnly(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	return publicAddr(ap.Addr())
}

func (n *Notifier) HasTransport() bool {
	if n == nil {
		return false
	}
	return n.otherReady() || n.browserReady()
}

func (n *Notifier) otherReady() bool {
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
	if n == nil {
		return
	}
	// пишем до проверки wants: выключенные уведомления и отсутствие каналов не должны терять историю
	n.record(ev)
	if !n.wants() {
		return
	}
	// устройства читаются из БД один раз за вызов: Notify не должен надолго держать воркер
	browser := !n.cfg.BrowserDisabled && wants(n.cfg.Events, ev.Kind) && n.browserReady()
	if !browser && !n.otherReady() {
		return
	}
	ev = n.withLink(n.localize(ev))
	if !n.cfg.TelegramDisabled && n.telegramReady() && wants(n.cfg.Events, ev.Kind) {
		n.deliver(n.sendTelegram, ev, "telegram")
	}
	if !n.cfg.WebhookDisabled && n.cfg.WebhookURL != "" && wants(n.cfg.Events, ev.Kind) {
		n.deliver(n.sendWebhook, ev, "webhook")
	}
	if !n.cfg.EmailDisabled && n.emailReady() && wants(n.cfg.Events, ev.Kind) {
		n.deliver(n.sendEmail, ev, "email")
	}
	if !n.cfg.PushoverDisabled && n.pushoverReady() && wants(n.cfg.Events, ev.Kind) {
		n.deliver(n.sendPushover, ev, "pushover")
	}
	if browser {
		n.deliver(n.browserSender(), ev, "browser")
	}
}

func (n *Notifier) record(ev Event) {
	if n.events == nil {
		return
	}
	entry := storage.EventEntry{
		At:      time.Now(),
		Source:  "server",
		Kind:    ev.Kind,
		Text:    n.localize(ev).Text,
		Subject: ev.Subject,
		AppPath: ev.AppPath,
	}
	n.events.RecordEvent(entry)
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
	ev = n.withLink(n.localize(ev))
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
	if n.push != nil {
		if err := n.browserSender()(ctx, ev); err != nil {
			errs = append(errs, fmt.Errorf("browser: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (n *Notifier) sendTelegram(ctx context.Context, ev Event) error {
	if n.telegramMuted(ctx, ev) {
		return nil
	}
	if ev.ImageURL != "" {
		// картинку Telegram качает сам и без наших кук. Постер за хотлинк-защитой или с приватного
		// трекера даст ошибку, тогда шлем обычное сообщение, чтобы уведомление не пропало
		err := n.sendTelegramPhoto(ctx, ev)
		if err == nil {
			return nil
		}
		logger.Warn("notify: telegram sendPhoto: " + err.Error())
	}
	return n.telegramSend(ctx, ev, maxMessage, func(text string, kb *tgKeyboard) error {
		return n.postTelegramJSON(ctx, telegramAPIURL, map[string]any{
			"chat_id":                  n.cfg.TelegramChatID,
			"text":                     text,
			"parse_mode":               "HTML",
			"disable_web_page_preview": true,
		}, kb)
	})
}

func (n *Notifier) postTelegramJSON(ctx context.Context, endpoint string, payload map[string]any, kb *tgKeyboard) error {
	if kb != nil {
		payload["reply_markup"] = kb
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return n.post(ctx, fmt.Sprintf(endpoint, n.cfg.TelegramToken), body)
}

func withLinkLine(text, line string, limit int) string {
	// ссылка идет только целиком: текст ради нее не режем, а обрезанная ссылка не работает
	text = helper.Substr(text, 0, limit)
	if line == "" || len([]rune(text))+1+len([]rune(line)) > limit {
		return text
	}
	return text + "\n" + line
}

func (n *Notifier) sendTelegramPhoto(ctx context.Context, ev Event) error {
	// до части хостингов у Telegram нет доступа (fastpic ему отвечает ошибкой), поэтому
	// сначала качаем сами и шлем файлом. Ссылку отдаем, только если не скачалось: бывает
	// и наоброт, домен закрыт для нас, но открыт для Telegram
	img, err := n.fetchImage(ctx, ev.ImageURL)
	if err == nil {
		return n.telegramSend(ctx, ev, maxCaption, func(caption string, kb *tgKeyboard) error {
			return n.postPhotoUpload(ctx, img, caption, kb)
		})
	}
	logger.Warn("notify: poster download: " + err.Error())

	return n.telegramSend(ctx, ev, maxCaption, func(caption string, kb *tgKeyboard) error {
		return n.postTelegramJSON(ctx, telegramPhotoURL, map[string]any{
			"chat_id":    n.cfg.TelegramChatID,
			"photo":      ev.ImageURL,
			"caption":    caption,
			"parse_mode": "HTML",
		}, kb)
	})
}

func (n *Notifier) fetchImage(ctx context.Context, imageURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", posterUserAgent)
	res, err := n.posterHC.Do(req)
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

func (n *Notifier) postPhotoUpload(ctx context.Context, img []byte, caption string, kb *tgKeyboard) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("chat_id", n.cfg.TelegramChatID); err != nil {
		return err
	}
	if err := mw.WriteField("caption", caption); err != nil {
		return err
	}
	if err := mw.WriteField("parse_mode", "HTML"); err != nil {
		return err
	}
	if kb != nil {
		markup, err := json.Marshal(kb)
		if err != nil {
			return err
		}
		if err := mw.WriteField("reply_markup", string(markup)); err != nil {
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

func (e Event) WithRelease(urlHash, tracker, category string) Event {
	e.urlHash, e.tracker, e.category = urlHash, tracker, category
	return e
}
