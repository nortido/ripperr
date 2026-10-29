package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strings"

	"ripperr/internal/system/logger"
)

var tgEmoji = map[string]string{
	KindUpdated:        "🔄",
	KindBroken:         "⚠️",
	KindCheckFailed:    "❌",
	KindNoSpace:        "💾",
	KindTrackerDown:    "🔴",
	KindTrackerUp:      "🟢",
	KindMirrorSwitched: "🔀",
	KindTest:           "✅",
}

type tgButton struct {
	Text         string `json:"text"`
	URL          string `json:"url,omitempty"`
	CallbackData string `json:"callback_data,omitempty"`
}

type tgKeyboard struct {
	InlineKeyboard [][]tgButton `json:"inline_keyboard"`
}

type tgMessage struct {
	Text  string
	Links []tgButton
}

func tgKeyboardOf(row []tgButton) *tgKeyboard {
	if len(row) == 0 {
		return nil
	}
	return &tgKeyboard{InlineKeyboard: [][]tgButton{row}}
}

func tgLinks(ev Event) []tgButton {
	var links []tgButton
	if ev.URL != "" {
		links = append(links, tgButton{Text: messages[NormalizeLanguage(ev.lang)][msgTgTopic], URL: ev.URL})
	}
	if ev.AppLink != "" {
		links = append(links, tgButton{Text: "Ripperr", URL: ev.AppLink})
	}
	return links
}

func tgParts(ev Event) (title, name, ctx string) {
	m := messages[NormalizeLanguage(ev.lang)]
	arg := func(i int) string {
		if i < len(ev.args) {
			return ev.args[i]
		}
		return ""
	}
	switch ev.msg {
	case msgUpdated:
		return m[msgPushUpdated], pushName(ev.Subject), strings.Join(nonEmpty(ev.tracker, ev.category), " · ")
	case msgBroken:
		return m[msgTgBroken], pushName(ev.Subject), fmt.Sprintf(m[msgTgBrokenNote], arg(0))
	case msgCheckFailed:
		return m[msgPushCheckFailed], pushName(ev.Subject), shortReason(arg(0))
	case msgNoSpace:
		return m[msgPushNoSpace], pushName(ev.Subject), fmt.Sprintf(m[msgNoSpaceNote], arg(0), arg(1))
	case msgTrackerDown:
		return m[msgTgTrackerDown], arg(0) + ": " + shortReason(arg(1)), m[msgTgTrackerDownNote]
	case msgTrackerUp:
		return m[msgTgTrackerUp], arg(0), m[msgTgTrackerUpNote]
	case msgMirrorSwitched:
		return m[msgPushSwitched], arg(0) + ": " + arg(2), ""
	case msgMirrorPaused:
		return m[msgPushPaused], arg(0), fmt.Sprintf(m[msgPushConfirm], arg(1))
	case msgMirrorDigest:
		// подробности в приложении: в уведомлении только какие трекеры ждут решения
		return m[msgPushMirrors], digestTrackers(ev.digest), ""
	}
	title, name, _ = strings.Cut(ev.plain(), "\n")
	return title, name, ""
}

func nonEmpty(parts ...string) []string {
	kept := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return kept
}

func buildTelegram(ev Event, limit int) tgMessage {
	title, name, ctx := tgParts(ev)
	links := tgLinks(ev)
	icon := tgEmoji[ev.Kind]
	if icon != "" {
		icon += " "
	}
	// Telegram считает видимый текст без тегов. Место под строки-ссылки резервируем всегда:
	// при ответе 400 те же строки встают под текст, и тот же обрезанный текст должен влезть
	room := limit - utf16Len(icon)
	for _, l := range links {
		room -= 1 + utf16Len(l.Text)
	}
	title = cutUTF16(title, max(room, 0))
	room -= utf16Len(title)
	ctxCost := 0
	if ctx != "" {
		ctxCost = 1 + utf16Len(ctx)
	}
	if name != "" && 1+utf16Len(name)+ctxCost > room {
		name = cutUTF16(name, max(room-1-ctxCost, 0))
	}
	nameCost := 0
	if name != "" {
		nameCost = 1 + utf16Len(name)
	}
	if ctx != "" && ctxCost+nameCost > room {
		ctx = cutUTF16(ctx, max(room-nameCost-1, 0))
	}

	var b strings.Builder
	b.WriteString("<b>" + html.EscapeString(icon+title) + "</b>")
	if name != "" {
		b.WriteString("\n" + html.EscapeString(name))
	}
	if ctx != "" {
		b.WriteString("\n<i>" + html.EscapeString(ctx) + "</i>")
	}
	return tgMessage{Text: b.String(), Links: links}
}

func (m tgMessage) withLinks() string {
	var b strings.Builder
	b.WriteString(m.Text)
	for _, l := range m.Links {
		b.WriteString("\n<a href=\"" + html.EscapeString(l.URL) + "\">" + html.EscapeString(l.Text) + "</a>")
	}
	return b.String()
}

func (n *Notifier) telegramSend(ctx context.Context, ev Event, limit int, send func(text string, kb *tgKeyboard) error) error {
	m := buildTelegram(ev, limit)
	mute := n.tgMuteButtons(ev)
	err := send(m.Text, tgKeyboardOf(append(slices.Clone(m.Links), mute...)))
	var se *statusError
	if len(m.Links) == 0 || !errors.As(err, &se) || se.status != http.StatusBadRequest {
		return err
	}
	logger.Warn("notify: telegram rejected url buttons, retrying with links in text: " + err.Error())
	return send(m.withLinks(), tgKeyboardOf(mute))
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		// Telegram меряет длину в единицах UTF-16: эмодзи вне BMP занимают две
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

func cutUTF16(s string, units int) string {
	n := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if n+w > units {
			return s[:i]
		}
		n += w
	}
	return s
}

func (n *Notifier) tgMuteButtons(ev Event) []tgButton {
	if !n.muteApplies(ev) || n.tgConflict.Load() {
		return nil
	}
	data := tgMuteVerb + ":" + ev.urlHash
	// лимит Telegram на callback_data; с fnv32 не достигается, страховка
	if len(data) > 64 {
		return nil
	}
	return []tgButton{{Text: tgMuteIcon, CallbackData: data}}
}
