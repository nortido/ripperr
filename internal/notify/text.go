package notify

import (
	"fmt"
	"strings"
)

const (
	LangEN = "en"
	LangRU = "ru"
)

const (
	DigestMain    = "main"
	DigestRemove  = "remove"
	DigestConfirm = "confirm"

	DigestReasonDead    = "dead"
	DigestReasonJackett = "jackett"
)

const (
	msgUpdated        = "updated"
	msgBroken         = "broken"
	msgTrackerDown    = "tracker_down"
	msgTrackerUp      = "tracker_up"
	msgMirrorSwitched = "mirror_switched"
	msgMirrorDigest   = "mirror_digest"
	msgMirrorPaused   = "mirror_paused"
	msgCheckFailed    = "check_failed"
	msgTest           = "test"
	msgAppLink        = "app_link"
	msgDigestMain     = "digest_main"
	msgDigestRemove   = "digest_remove"
	msgDigestConfirm  = "digest_confirm"
	msgReasonDead     = "reason_dead"
	msgReasonJackett  = "reason_jackett"
)

type MirrorDigestItem struct {
	Tracker, Kind, Host, From, Reason string
}

var messages = map[string]map[string]string{
	LangEN: {
		msgUpdated:        "Release updated in the torrent client:",
		msgBroken:         "Errors in a row: %s. The release was moved to ignored and is no longer checked:",
		msgTrackerDown:    "Tracker %s is unavailable: %s\nChecks of this tracker are paused.",
		msgTrackerUp:      "Tracker %s is available again, checks resumed.",
		msgMirrorSwitched: "Tracker %s: %s is not responding, checks switched to %s.",
		msgMirrorDigest:   "Tracker mirrors: %s",
		msgMirrorPaused:   "Tracker %s is paused: only the new mirror %s responds. Confirm it in the tracker settings.",
		msgCheckFailed:    "Release check failed: %s",
		msgTest:           "Ripperr: connectivity test",
		msgAppLink:        "Open in Ripperr",
		msgDigestMain:     "%s: main address %s instead of %s",
		msgDigestRemove:   "%s: remove %s%s",
		msgDigestConfirm:  "%s: new mirror %s awaits confirmation",
		msgReasonDead:     " (not responding)",
		msgReasonJackett:  " (not in the Jackett list)",
	},
	LangRU: {
		msgUpdated:        "Раздача обновлена в torrent-клиенте:",
		msgBroken:         "Ошибок подряд: %s. Раздача отправлена в игнор и больше не проверяется:",
		msgTrackerDown:    "Трекер %s недоступен: %s\nПроверки этого трекера приостановлены.",
		msgTrackerUp:      "Трекер %s снова доступен, проверки возобновлены.",
		msgMirrorSwitched: "Трекер %s: %s не отвечает, проверки переключены на %s.",
		msgMirrorDigest:   "Зеркала трекеров: %s",
		msgMirrorPaused:   "Трекер %s на паузе: отвечает только новое зеркало %s. Подтвердите его в настройках трекера.",
		msgCheckFailed:    "Ошибка проверки раздачи: %s",
		msgTest:           "Ripperr: проверка связи",
		msgAppLink:        "Открыть в Ripperr",
		msgDigestMain:     "%s: основной адрес %s вместо %s",
		msgDigestRemove:   "%s: убрать %s%s",
		msgDigestConfirm:  "%s: новое зеркало %s ждет подтверждения",
		msgReasonDead:     " (не отвечает)",
		msgReasonJackett:  " (нет в списке Jackett)",
	},
}

func NormalizeLanguage(s string) string {
	if s == LangRU {
		return LangRU
	}
	return LangEN
}

func (e Event) render(lang string) string {
	m := messages[lang]
	switch e.msg {
	case "":
		return e.Text
	case msgMirrorDigest:
		parts := make([]string, 0, len(e.digest))
		for _, it := range e.digest {
			parts = append(parts, digestPart(m, it))
		}
		return fmt.Sprintf(m[msgMirrorDigest], strings.Join(parts, "; "))
	}
	args := make([]any, len(e.args))
	for i, a := range e.args {
		args[i] = a
	}
	return fmt.Sprintf(m[e.msg], args...)
}

func digestPart(m map[string]string, it MirrorDigestItem) string {
	switch it.Kind {
	case DigestMain:
		return fmt.Sprintf(m[msgDigestMain], it.Tracker, it.Host, it.From)
	case DigestConfirm:
		return fmt.Sprintf(m[msgDigestConfirm], it.Tracker, it.Host)
	}
	reason := ""
	switch it.Reason {
	case DigestReasonDead:
		reason = m[msgReasonDead]
	case DigestReasonJackett:
		reason = m[msgReasonJackett]
	}
	return fmt.Sprintf(m[msgDigestRemove], it.Tracker, it.Host, reason)
}

func (e Event) label() string {
	if e.linkLabel != "" {
		return e.linkLabel
	}
	// отправитель мог получить событие в обход localize: пустая подпись ссылки не нужна
	return messages[LangEN][msgAppLink]
}

func (e Event) withDefaultText() Event {
	e.Text = e.render(LangEN)
	return e
}

func (n *Notifier) localize(ev Event) Event {
	lang := NormalizeLanguage(n.cfg.Language)
	if ev.msg != "" {
		ev.Text = ev.render(lang)
	}
	ev.linkLabel = messages[lang][msgAppLink]
	return ev
}
