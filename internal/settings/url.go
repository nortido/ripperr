package settings

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"ripperr/internal/config"
	"ripperr/internal/helper"
)

func parseURL(raw string) (*url.URL, error) {
	// текст *url.Error содержит весь URL с паролем и путем, а он уходит в ответы и логи
	u, err := url.Parse(raw)
	var ue *url.Error
	if errors.As(err, &ue) {
		// текст причины тоже цитирует кусок адреса (порт, символ, escape), а там бывает пароль
		msg := ue.Err.Error()
		switch {
		case strings.HasPrefix(msg, "invalid port"):
			msg = "invalid port"
		case strings.Contains(msg, `"`):
			msg = "invalid character or escape"
		}
		return nil, errors.New("invalid url: " + msg)
	}
	return u, err
}

func EnsureScheme(raw, defaultScheme string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = defaultScheme + "://" + raw
	}
	u, err := parseURL(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid url: missing host")
	}
	return raw, nil
}

func ValidateDownloadProxy(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := parseURL(raw)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "http", "https", "socks5":
		if u.Host == "" {
			return fmt.Errorf("invalid proxy url: missing host")
		}
		return nil
	default:
		return fmt.Errorf("download_proxy scheme must be http, https, or socks5")
	}
}

func ValidateWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := parseURL(raw)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "http", "https":
		if u.Host == "" {
			return fmt.Errorf("invalid webhook url: missing host")
		}
		return nil
	default:
		return fmt.Errorf("webhook.url scheme must be http or https")
	}
}

func ValidateMirrorSourceURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("mirror source url is empty")
	}
	if strings.Count(raw, "{path}") != 1 {
		return fmt.Errorf("mirror source url must contain {path} exactly once")
	}
	// две разные подстановки: если {path} попал в хост, хосты разойдутся
	var host, frag string
	for _, sub := range []string{"p", "src/x.yml"} {
		u, err := parseURL(strings.Replace(raw, "{path}", sub, 1))
		if err != nil {
			return err
		}
		if u.Scheme != "https" {
			return fmt.Errorf("mirror source url scheme must be https")
		}
		if u.User != nil {
			return fmt.Errorf("mirror source url must not contain credentials")
		}
		if u.Host == "" || (host != "" && u.Host != host) {
			return fmt.Errorf("mirror source url must have a host before {path}")
		}
		// фрагмент не уходит в запрос: все файлы качались бы по одному адресу
		if host != "" && u.Fragment != frag {
			return fmt.Errorf("mirror source url must not have {path} after #")
		}
		host = u.Host
		frag = u.Fragment
		// isLocalTarget сравнивает буквально, а BOX.LOCAL. и localhost. ведут туда же
		if helper.IsLocalHost(strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")) {
			return fmt.Errorf("mirror source url must not point to a local host")
		}
	}
	return nil
}

func ValidateMirrorSource(ms config.MirrorSource) error {
	switch ms.Type {
	case "", "github", "jsdelivr":
		return nil
	case "custom":
		return ValidateMirrorSourceURL(ms.URL)
	default:
		return fmt.Errorf("mirror source must be github, jsdelivr or custom")
	}
}

func normalizeIntegrationsPatch(o *integrationsOverlay) error {
	if o.Qbit.Host != nil && strings.TrimSpace(*o.Qbit.Host) != "" {
		norm, err := EnsureScheme(*o.Qbit.Host, "http")
		if err != nil {
			return fmt.Errorf("qbit.host: %w", err)
		}
		o.Qbit.Host = &norm
	}
	if o.Transmission.Host != nil && strings.TrimSpace(*o.Transmission.Host) != "" {
		norm, err := EnsureScheme(*o.Transmission.Host, "http")
		if err != nil {
			return fmt.Errorf("transmission.host: %w", err)
		}
		o.Transmission.Host = &norm
	}
	if o.Deluge.Host != nil && strings.TrimSpace(*o.Deluge.Host) != "" {
		norm, err := EnsureScheme(*o.Deluge.Host, "http")
		if err != nil {
			return fmt.Errorf("deluge.host: %w", err)
		}
		o.Deluge.Host = &norm
	}
	if o.Flaresolverr.URL != nil && strings.TrimSpace(*o.Flaresolverr.URL) != "" {
		norm, err := EnsureScheme(*o.Flaresolverr.URL, "http")
		if err != nil {
			return fmt.Errorf("flaresolverr.url: %w", err)
		}
		o.Flaresolverr.URL = &norm
	}
	if o.Proxy.DownloadProxy != nil {
		if err := ValidateDownloadProxy(*o.Proxy.DownloadProxy); err != nil {
			return fmt.Errorf("proxy.download_proxy: %w", err)
		}
	}
	if o.Notifications.Webhook.URL != nil {
		if err := ValidateWebhookURL(*o.Notifications.Webhook.URL); err != nil {
			return fmt.Errorf("notifications.webhook.url: %w", err)
		}
	}
	return normalizeMirrorSourcePatch(o)
}

func normalizeMirrorSourcePatch(o *integrationsOverlay) error {
	ms := &o.MirrorSource
	if ms.Type != nil && *ms.Type != "custom" {
		// при github и jsdelivr поле адреса скрыто: опечатка в нем не должна мешать сохранению,
		// а сохраненный свой адрес вернется при обратном переключении
		ms.URL = nil
		return nil
	}
	if ms.URL == nil {
		if ms.Type != nil {
			return fmt.Errorf("mirror_source.url: mirror source url is empty")
		}
		return nil
	}
	if err := ValidateMirrorSourceURL(*ms.URL); err != nil {
		return fmt.Errorf("mirror_source.url: %w", err)
	}
	v := strings.TrimSpace(*ms.URL)
	ms.URL = &v
	return nil
}

func NormalizeAppIntegrations(cfg *config.App) error {
	if cfg == nil {
		return nil
	}
	// нормализуем один раз в конце ApplyOverrides, дальше IntegrationsJSON, managerConfig
	// и factory читают готовое значение. Сырой MANAGER_CLIENT из env бывает в любом регистре
	cfg.Manager.Client = config.NormalizeClient(cfg.Manager.Client)
	// ValidateMirrorSource на старте проверяет обрезанный MIRROR_SOURCE_URL, а из env он приходит как есть
	cfg.MirrorSource.URL = strings.TrimSpace(cfg.MirrorSource.URL)
	if strings.TrimSpace(cfg.Manager.QbitTorrent.Host) != "" {
		norm, err := EnsureScheme(cfg.Manager.QbitTorrent.Host, "http")
		if err != nil {
			return fmt.Errorf("qbit.host: %w", err)
		}
		cfg.Manager.QbitTorrent.Host = norm
	}
	if strings.TrimSpace(cfg.Manager.Transmission.Host) != "" {
		norm, err := EnsureScheme(cfg.Manager.Transmission.Host, "http")
		if err != nil {
			return fmt.Errorf("transmission.host: %w", err)
		}
		cfg.Manager.Transmission.Host = norm
	}
	if strings.TrimSpace(cfg.Manager.Deluge.Host) != "" {
		norm, err := EnsureScheme(cfg.Manager.Deluge.Host, "http")
		if err != nil {
			return fmt.Errorf("deluge.host: %w", err)
		}
		cfg.Manager.Deluge.Host = norm
	}
	if strings.TrimSpace(cfg.Flaresolverr.Host) != "" {
		norm, err := EnsureScheme(cfg.Flaresolverr.Host, "http")
		if err != nil {
			return fmt.Errorf("flaresolverr.url: %w", err)
		}
		cfg.Flaresolverr.Host = norm
	}
	if err := ValidateDownloadProxy(cfg.Runner.DownloadProxy); err != nil {
		return fmt.Errorf("proxy.download_proxy: %w", err)
	}
	if err := ValidateWebhookURL(cfg.Notify.WebhookURL); err != nil {
		return fmt.Errorf("notifications.webhook.url: %w", err)
	}
	return nil
}
