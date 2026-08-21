package settings

import (
	"fmt"
	"net/url"
	"strings"

	"trackerr/internal/config"
)

func EnsureScheme(raw, defaultScheme string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = defaultScheme + "://" + raw
	}
	u, err := url.Parse(raw)
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
	u, err := url.Parse(raw)
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
	u, err := url.Parse(raw)
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
	return nil
}

func NormalizeAppIntegrations(cfg *config.App) error {
	if cfg == nil {
		return nil
	}
	// один раз здесь, в конце ApplyOverrides: дальше по коду (IntegrationsJSON,
	// managerConfig, factory) читают уже приведённое значение, а не сырое
	// MANAGER_CLIENT из env в любом регистре
	cfg.Manager.Client = config.NormalizeClient(cfg.Manager.Client)
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
