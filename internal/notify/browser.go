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
	"unicode"

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

var pushExts = func() map[string]bool {
	set := map[string]bool{}
	for _, e := range strings.Fields(`mkv mp4 m4v avi mov wmv flv webm mpg mpeg m2ts mts ts vob ogv 3gp rmvb divx flac mp3 m4a m4b aac ac3 dts wav ape alac ogg opus wma wv dsf dff aiff mka srt ass ssa sub idx sup vtt iso img bin cue mdf mds nrg vhd vmdk zip rar 7z tar gz tgz bz2 xz zst cab nsp nsz xci xcz nro cia 3ds wbfs wux rvz gcm chd cso pbp exe msi dmg pkg deb rpm appimage apk ipa pdf epub mobi azw3 fb2 djvu cbz cbr jpg png`) {
		set[e] = true
	}
	return set
}()

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func digitsRun(r []rune) bool {
	for _, c := range r {
		if !isWordRune(c) {
			return true
		}
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

func yearAfterNumber(r []rune, i int) bool {
	n := 0
	for n < len(r)-i-1 && isWordRune(r[i+1+n]) {
		n++
	}
	if n != 4 {
		return false
	}
	if p := string(r[i+1 : i+3]); p != "19" && p != "20" {
		return false
	}
	for j := i - 1; j >= 0 && isWordRune(r[j]); j-- {
		if !unicode.IsDigit(r[j]) {
			return false
		}
	}
	return true
}

func pushName(s string) string {
	s = strings.TrimSpace(s)
	// расширение режем только из списка и только строчное: ".MKV" остается как есть
	if i := strings.LastIndexByte(s, '.'); i > 0 && pushExts[s[i+1:]] {
		s = s[:i]
	}
	r := []rune(s)
	for i := 1; i < len(r)-1; i++ {
		if r[i] != '.' || !isWordRune(r[i-1]) || !isWordRune(r[i+1]) {
			continue
		}
		// "5.1", "v1.0.12" это версии: точку после цифры оставляем, если до следующего не-слова идут только цифры ("2024.1080p" режем)
		// число перед годом ("Iron.Man.3.2013", "1917.2019") не склеиваем с ним
		if unicode.IsDigit(r[i-1]) && digitsRun(r[i+1:]) && !yearAfterNumber(r, i) {
			continue
		}
		r[i] = ' '
	}
	return strings.TrimSpace(string(r))
}

func joinLines(parts ...string) string {
	kept := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n")
}

func pushText(ev Event) (title, body string) {
	m := messages[NormalizeLanguage(ev.lang)]
	name := pushName(ev.Subject)
	reason := ""
	switch ev.msg {
	case msgUpdated:
		return m[msgPushUpdated], name
	case msgBroken:
		if len(ev.args) > 0 {
			reason = fmt.Sprintf(m[msgPushStreak], ev.args[0])
		}
		return m[msgPushBroken], joinLines(name, reason)
	case msgCheckFailed:
		if len(ev.args) > 0 {
			reason = ev.args[0]
		}
		return m[msgPushCheckFailed], joinLines(name, reason)
	}
	text := strings.TrimPrefix(strings.TrimSpace(ev.Text), subjectHead)
	first, rest, _ := strings.Cut(text, "\n")
	first = strings.TrimSuffix(strings.TrimSpace(first), ":")
	title = helper.Substr(strings.ReplaceAll(first, "\r", " "), 0, maxSubject)
	if len([]rune(first)) > maxSubject {
		rest = text
	}
	return title, joinLines(strings.TrimSpace(rest), name)
}

func browserPayload(ev Event) []byte {
	title, body := pushText(ev)
	msg := browserMessage{Title: title, Body: helper.Substr(body, 0, maxPushBody), URL: "/"}
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
