package settings

import (
	"slices"

	"ripperr/internal/config"
	"ripperr/internal/notify"
)

func applyNotifyEvents(cfg *config.App, o integrationsOverlay) {
	n := &o.Notifications
	if n.Events != nil {
		cfg.Notify.Events = notify.NormalizeEvents(slices.Clone(*n.Events))
		return
	}
	// общего набора в БД еще нет: берем объединение старых списков каналов, сами они остаются в документе нетронутыми
	own := func(l *[]string) []string {
		if l == nil {
			return notify.DefaultEvents()
		}
		return *l
	}
	cfg.Notify.Events = notify.UnionEvents(
		own(n.Telegram.Events),
		own(n.Webhook.Events),
		own(n.Email.Events),
		own(n.Pushover.Events),
		own(n.Browser.Events),
	)
}
