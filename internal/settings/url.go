package settings

import (
	"fmt"
	"net/url"
	"strings"

	"trackerr/internal/config"
)

// EnsureScheme adds defaultScheme if raw has no "://", then parses and requires a host.
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

// ValidateDownloadProxy accepts empty or a URL with scheme http, https, or socks5.
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

func normalizeIntegrationsPatch(o *integrationsOverlay) error {
	if o.Qbit.Host != nil && strings.TrimSpace(*o.Qbit.Host) != "" {
		norm, err := EnsureScheme(*o.Qbit.Host, "http")
		if err != nil {
			return fmt.Errorf("qbit.host: %w", err)
		}
		o.Qbit.Host = &norm
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
	return nil
}

// NormalizeAppIntegrations normalizes integration URLs on the effective config.
func NormalizeAppIntegrations(cfg *config.App) error {
	if cfg == nil {
		return nil
	}
	if cfg.Manager != nil && strings.TrimSpace(cfg.Manager.QbitTorrent.Host) != "" {
		norm, err := EnsureScheme(cfg.Manager.QbitTorrent.Host, "http")
		if err != nil {
			return fmt.Errorf("qbit.host: %w", err)
		}
		cfg.Manager.QbitTorrent.Host = norm
	}
	if cfg.Flaresolverr != nil && strings.TrimSpace(cfg.Flaresolverr.Host) != "" {
		norm, err := EnsureScheme(cfg.Flaresolverr.Host, "http")
		if err != nil {
			return fmt.Errorf("flaresolverr.url: %w", err)
		}
		cfg.Flaresolverr.Host = norm
	}
	if cfg.Runner != nil {
		if err := ValidateDownloadProxy(cfg.Runner.DownloadProxy); err != nil {
			return fmt.Errorf("proxy.download_proxy: %w", err)
		}
	}
	return nil
}
