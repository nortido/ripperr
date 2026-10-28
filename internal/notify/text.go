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
	msgNoSpace        = "no_space"
	msgTest           = "test"
	msgAppLink        = "app_link"
	msgDigestMain     = "digest_main"
	msgDigestRemove   = "digest_remove"
	msgDigestConfirm  = "digest_confirm"
	msgReasonDead     = "reason_dead"
	msgReasonJackett  = "reason_jackett"

	msgPushUpdated     = "push_updated"
	msgPushBroken      = "push_broken"
	msgPushCheckFailed = "push_check_failed"
	msgPushStreak      = "push_streak"
	msgPushNoSpace     = "push_no_space"
	msgNoSpaceNote     = "no_space_note"

	msgTgBroken          = "tg_broken"
	msgTgBrokenNote      = "tg_broken_note"
	msgTgTrackerDown     = "tg_tracker_down"
	msgTgTrackerDownNote = "tg_tracker_down_note"
	msgTgTrackerUp       = "tg_tracker_up"
	msgTgTrackerUpNote   = "tg_tracker_up_note"
	msgTgTopic           = "tg_topic"
	msgTgMuted           = "tg_muted"
	msgTgUnmuted         = "tg_unmuted"
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
		msgNoSpace:        "Not enough disk space to update the release: need %s GB, free %s GB. The old version stays, the update will be retried:",
		msgTest:           "Ripperr: connectivity test",
		msgAppLink:        "Open in Ripperr",
		msgDigestMain:     "%s: main address %s instead of %s",
		msgDigestRemove:   "%s: remove %s%s",
		msgDigestConfirm:  "%s: new mirror %s awaits confirmation",
		msgReasonDead:     " (not responding)",
		msgReasonJackett:  " (not in the Jackett list)",

		msgPushUpdated:     "Release updated",
		msgPushBroken:      "Release ignored",
		msgPushCheckFailed: "Check failed",
		msgPushStreak:      "Errors in a row: %s",
		msgPushNoSpace:     "Not enough disk space",
		msgNoSpaceNote:     "Need %s GB, free %s GB",

		msgTgBroken:          "Release moved to ignored",
		msgTgBrokenNote:      "Errors in a row: %s, no longer checked",
		msgTgTrackerDown:     "Tracker unavailable",
		msgTgTrackerDownNote: "Tracker checks paused",
		msgTgTrackerUp:       "Tracker is back",
		msgTgTrackerUpNote:   "Checks resumed",
		msgTgTopic:           "Topic on tracker",
		msgTgMuted:           "Telegram notifications for this release are off",
		msgTgUnmuted:         "Telegram notifications for this release are on",
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
		msgNoSpace:        "Не хватает места на диске для обновления раздачи: нужно %s ГБ, свободно %s ГБ. Старая версия остается, обновление повторится:",
		msgTest:           "Ripperr: проверка связи",
		msgAppLink:        "Открыть в Ripperr",
		msgDigestMain:     "%s: основной адрес %s вместо %s",
		msgDigestRemove:   "%s: убрать %s%s",
		msgDigestConfirm:  "%s: новое зеркало %s ждет подтверждения",
		msgReasonDead:     " (не отвечает)",
		msgReasonJackett:  " (нет в списке Jackett)",

		msgPushUpdated:     "Раздача обновлена",
		msgPushBroken:      "Раздача в игноре",
		msgPushCheckFailed: "Ошибка проверки",
		msgPushStreak:      "Ошибок подряд: %s",
		msgPushNoSpace:     "Не хватает места на диске",
		msgNoSpaceNote:     "Нужно %s ГБ, свободно %s ГБ",

		msgTgBroken:          "Раздача ушла в игнор",
		msgTgBrokenNote:      "Ошибок подряд: %s, больше не проверяется",
		msgTgTrackerDown:     "Трекер недоступен",
		msgTgTrackerDownNote: "Проверки трекера на паузе",
		msgTgTrackerUp:       "Трекер снова доступен",
		msgTgTrackerUpNote:   "Проверки возобновлены",
		msgTgTopic:           "Тема на трекере",
		msgTgMuted:           "Уведомления об этой раздаче в Telegram выключены",
		msgTgUnmuted:         "Уведомления об этой раздаче в Telegram включены",
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
	ev.lang = lang
	ev.linkLabel = messages[lang][msgAppLink]
	return ev
}
