package settings

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"ripperr/internal/config"
)

const SecretMask = "********"

type secretKind int

const (
	secretPlain secretKind = iota
	secretHost
	secretWebhook
)

type secretField struct {
	path    string
	kind    secretKind
	app     func(*config.App) *string
	overlay func(*integrationsOverlay) **string
}

var secretFields = []secretField{
	{"qbit.host", secretHost,
		func(c *config.App) *string { return &c.Manager.QbitTorrent.Host },
		func(o *integrationsOverlay) **string { return &o.Qbit.Host }},
	{"qbit.password", secretPlain,
		func(c *config.App) *string { return &c.Manager.QbitTorrent.Password },
		func(o *integrationsOverlay) **string { return &o.Qbit.Password }},
	{"transmission.host", secretHost,
		func(c *config.App) *string { return &c.Manager.Transmission.Host },
		func(o *integrationsOverlay) **string { return &o.Transmission.Host }},
	{"transmission.password", secretPlain,
		func(c *config.App) *string { return &c.Manager.Transmission.Password },
		func(o *integrationsOverlay) **string { return &o.Transmission.Password }},
	{"deluge.host", secretHost,
		func(c *config.App) *string { return &c.Manager.Deluge.Host },
		func(o *integrationsOverlay) **string { return &o.Deluge.Host }},
	{"deluge.password", secretPlain,
		func(c *config.App) *string { return &c.Manager.Deluge.Password },
		func(o *integrationsOverlay) **string { return &o.Deluge.Password }},
	{"downloadstation.host", secretHost,
		func(c *config.App) *string { return &c.Manager.DownloadStation.Host },
		func(o *integrationsOverlay) **string { return &o.DownloadStation.Host }},
	{"downloadstation.password", secretPlain,
		func(c *config.App) *string { return &c.Manager.DownloadStation.Password },
		func(o *integrationsOverlay) **string { return &o.DownloadStation.Password }},
	{"flaresolverr.url", secretHost,
		func(c *config.App) *string { return &c.Flaresolverr.Host },
		func(o *integrationsOverlay) **string { return &o.Flaresolverr.URL }},
	{"proxy.download_proxy", secretHost,
		func(c *config.App) *string { return &c.Runner.DownloadProxy },
		func(o *integrationsOverlay) **string { return &o.Proxy.DownloadProxy }},
	{"notifications.telegram.bot_token", secretPlain,
		func(c *config.App) *string { return &c.Notify.TelegramToken },
		func(o *integrationsOverlay) **string { return &o.Notifications.Telegram.BotToken }},
	{"notifications.webhook.url", secretWebhook,
		func(c *config.App) *string { return &c.Notify.WebhookURL },
		func(o *integrationsOverlay) **string { return &o.Notifications.Webhook.URL }},
	{"notifications.email.password", secretPlain,
		func(c *config.App) *string { return &c.Notify.EmailPassword },
		func(o *integrationsOverlay) **string { return &o.Notifications.Email.Password }},
	{"notifications.pushover.app_token", secretPlain,
		func(c *config.App) *string { return &c.Notify.PushoverToken },
		func(o *integrationsOverlay) **string { return &o.Notifications.Pushover.AppToken }},
	{"notifications.pushover.user_key", secretPlain,
		func(c *config.App) *string { return &c.Notify.PushoverUser },
		func(o *integrationsOverlay) **string { return &o.Notifications.Pushover.UserKey }},
	{"metrics.token", secretPlain,
		func(c *config.App) *string { return &c.Metrics.Token },
		func(o *integrationsOverlay) **string { return &o.Metrics.Token }},
}

func RevealIntegrationSecret(cfg *config.App, path string) (string, bool) {
	for _, f := range secretFields {
		if f.kind == secretPlain && f.path == path {
			return *f.app(cfg), true
		}
	}
	return "", false
}

func MaskSecret(v string) string {
	if v == "" {
		return ""
	}
	return SecretMask
}

func maskValue(kind secretKind, v string) string {
	if kind == secretPlain || v == "" {
		return MaskSecret(v)
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return SecretMask
	}
	if kind == secretHost {
		if u.User == nil {
			return v
		}
		// собираем руками: url.User(..).String() и u.String() превращают звездочки
		// в %2A, и SecretMask в маске уже не найти
		out := u.Scheme + "://" + SecretMask + "@" + u.Host + u.EscapedPath()
		if u.ForceQuery || u.RawQuery != "" {
			out += "?" + u.RawQuery
		}
		if u.Fragment != "" {
			out += "#" + u.EscapedFragment()
		}
		return out
	}
	if u.User == nil && (u.Path == "" || u.Path == "/") && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" {
		return v
	}
	return u.Scheme + "://" + u.Host + "/" + SecretMask
}

func maskedCopy(cfg *config.App) *config.App {
	out := *cfg
	for _, f := range secretFields {
		p := f.app(&out)
		*p = maskValue(f.kind, *p)
	}
	return &out
}

func unmaskValue(f secretField, v, stored string) (keep bool, err error) {
	if v == "" {
		return false, nil
	}
	if f.kind == secretPlain {
		if v == SecretMask {
			return true, nil
		}
	} else if m := maskValue(f.kind, stored); v == m && strings.Contains(m, SecretMask) {
		return true, nil
	}
	if strings.Contains(v, SecretMask) {
		return false, fmt.Errorf("%s: the hidden value cannot be edited in part, enter a new value or clear the field", f.path)
	}
	return false, nil
}

func UnmaskIntegrationsPatch(raw json.RawMessage, cur *config.App) (json.RawMessage, error) {
	var o integrationsOverlay
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	for _, f := range secretFields {
		p := f.overlay(&o)
		if *p == nil {
			continue
		}
		keep, err := unmaskValue(f, **p, *f.app(cur))
		if err != nil {
			return nil, err
		}
		if keep {
			*p = nil
		}
	}
	return json.Marshal(o)
}

func RestoreMaskedSecrets(dst, cur *config.App) error {
	for _, f := range secretFields {
		d, c := f.app(dst), f.app(cur)
		// сохраненный секрет может сам содержать маркер: такое значение не трогаем
		if *d == *c {
			continue
		}
		keep, err := unmaskValue(f, *d, *c)
		if err != nil {
			return err
		}
		if keep {
			*d = *c
		}
	}
	return nil
}
