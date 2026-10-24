package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/system/logger"
	"ripperr/internal/webpush"
)

const (
	maxPushBody = 1000
	// токены FCM, Mozilla и Apple лежат в сегментах пути, у WNS в значении query
	minSecretToken = 16
)

type browserMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

// проверка идет на каждое событие, а битая строка сама не исправится: одна запись до первого удачного чтения
var browserLoadWarned atomic.Bool

func (n *Notifier) browserReady() bool {
	if n.push == nil {
		return false
	}
	_, devices, err := n.push.Load(context.Background())
	if err != nil {
		if !browserLoadWarned.Swap(true) {
			logger.Warn("notify: browser: " + err.Error())
		}
		return false
	}
	browserLoadWarned.Store(false)
	return len(devices) > 0
}

func browserPayload(ev Event) []byte {
	msg := browserMessage{Title: subjectLine(ev), Body: helper.Substr(ev.plain(), 0, maxPushBody), URL: "/"}
	if ev.AppPath != "" {
		msg.URL = "/" + ev.AppPath
	}
	for {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		// строки и ничего больше: Encode не вернет ошибку
		_ = enc.Encode(msg)
		out := bytes.TrimRight(buf.Bytes(), "\n")
		if len(out) <= webpush.MaxPayload || msg.Body == "" {
			return out
		}
		msg.Body = helper.Substr(msg.Body, 0, len([]rune(msg.Body))/2)
	}
}

func (n *Notifier) browserSender() func(context.Context, Event) error {
	// устройства, которым уже доставлено или которых уже удалили: повтор их пропускает
	done := map[string]bool{}
	return func(ctx context.Context, ev Event) error {
		keys, devices, err := n.push.Load(ctx)
		if err != nil {
			return err
		}
		payload := browserPayload(ev)
		var errs []error
		retryable := false
		for _, d := range devices {
			if done[d.ID] {
				continue
			}
			err := n.sendBrowserDevice(ctx, keys, d, payload)
			if err == nil || isGone(err) {
				done[d.ID] = true
			}
			if err == nil {
				continue
			}
			retryable = retryable || isRetryable(err)
			errs = append(errs, fmt.Errorf("%s: %w", deviceName(d), err))
		}
		if len(errs) == 0 {
			return nil
		}
		joined := errors.Join(errs...)
		// isRetryable на сыром Join остановился бы на первом постоянном или 4xx члене,
		// поэтому решение принято здесь, а цепочка скрыта
		if !retryable {
			return permanentError{joined}
		}
		return errors.New(joined.Error())
	}
}

var errGone = errors.New("subscription expired, device removed")

func isGone(err error) bool {
	return errors.Is(err, errGone)
}

func deviceName(d webpush.Device) string {
	if d.Label != "" {
		return d.Label
	}
	return "device " + d.ID
}

func (n *Notifier) sendBrowserDevice(ctx context.Context, keys webpush.Keys, d webpush.Device, payload []byte) error {
	req, err := webpush.NewRequest(ctx, keys, d, payload, time.Now())
	if err != nil {
		// ошибки разбора ключей и адреса от повтора не пройдут
		return permanentError{err}
	}
	res, err := n.hc.Do(req)
	if err != nil {
		return helper.RedactURLError(err)
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode < http.StatusBadRequest:
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		if _, err := n.push.Remove(ctx, d.ID); err != nil {
			logger.Warn("notify: browser: remove expired device " + d.ID + ": " + err.Error())
		}
		return permanentError{errGone}
	}
	snippet, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	return &statusError{status: res.StatusCode, body: redactEndpoint(string(bytes.TrimSpace(snippet)), d.Endpoint)}
}

func redactEndpoint(s, endpoint string) string {
	s = strings.ReplaceAll(s, endpoint, "***")
	u, err := url.Parse(endpoint)
	if err != nil {
		return s
	}
	var tokens []string
	add := func(t string) {
		if len(t) >= minSecretToken {
			tokens = append(tokens, t)
		}
	}
	for _, p := range [...]string{u.Path, u.EscapedPath()} {
		for seg := range strings.SplitSeq(p, "/") {
			add(seg)
		}
	}
	for _, pair := range strings.FieldsFunc(u.RawQuery, func(r rune) bool { return r == '&' || r == ';' }) {
		_, v, _ := strings.Cut(pair, "=")
		add(v)
		if dec, err := url.QueryUnescape(v); err == nil {
			add(dec)
		}
	}
	// длинный токен заменяем раньше короткого, который может в нем лежать
	slices.SortFunc(tokens, func(a, b string) int { return len(b) - len(a) })
	for _, t := range tokens {
		s = strings.ReplaceAll(s, t, "***")
	}
	return s
}
