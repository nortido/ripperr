package notify

import (
	"context"
	"net/url"

	"ripperr/internal/helper"
)

const (
	maxPushoverMessage = 1024
	maxPushoverURL     = 512
)

// не const: pushover_test.go подменяет адрес на сервер httptest
var pushoverAPIURL = "https://api.pushover.net/1/messages.json"

func (n *Notifier) pushoverReady() bool {
	return n.cfg.PushoverToken != "" && n.cfg.PushoverUser != ""
}

func (n *Notifier) sendPushover(ctx context.Context, ev Event) error {
	title, body := pushText(ev)
	if body == "" {
		// Pushover не примет пустое сообщение
		body = title
	}
	message := helper.Substr(body, 0, maxPushoverMessage)
	form := url.Values{
		"token": {n.cfg.PushoverToken},
		"user":  {n.cfg.PushoverUser},
		"title": {title},
	}
	// длинную ссылку Pushover отклонит вместе со всем сообщением, а без ссылки уведомление тоже полезно
	if ev.URL != "" && len([]rune(ev.URL)) <= maxPushoverURL {
		form.Set("url", ev.URL)
	}
	if ev.AppLink != "" {
		if form.Get("url") == "" && len([]rune(ev.AppLink)) <= maxPushoverURL {
			form.Set("url", ev.AppLink)
			form.Set("url_title", ev.label())
		} else {
			// поле url одно и занято ссылкой на раздачу: ссылка приложения идет в текст
			message = withLinkLine(body, ev.label()+": "+ev.AppLink, maxPushoverMessage)
		}
	}
	form.Set("message", message)
	return n.postBody(ctx, pushoverAPIURL, "application/x-www-form-urlencoded", []byte(form.Encode()))
}
