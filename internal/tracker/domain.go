package tracker

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	trackerModel "trackerr/internal/model/tracker"
	"trackerr/internal/tracker/client"
)

const domainProbeTimeout = 5 * time.Second

type domainAliveFunc func(ctx context.Context, domain string) bool

// проба зеркал идёт тем же клиентом, что и страницы трекера: своим она пошла бы
// мимо прокси и признала мёртвым домен, закрытый только для прямого доступа
func domainAliveWith(hc *http.Client) domainAliveFunc {
	// копия без банки кук: проба ходит по зеркалам, и Set-Cookie чужого домена не
	// должен оседать там же, где живёт залогиненная сессия трекера
	probe := *hc
	probe.Jar = nil
	return func(ctx context.Context, domain string) bool {
		ctx, cancel := context.WithTimeout(ctx, domainProbeTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain, nil)
		if err != nil {
			return false
		}
		resp, err := probe.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return true
	}
}

func selectActiveDomain(ctx context.Context, primary string, mirrors []string, alive domainAliveFunc) string {
	if len(mirrors) == 0 {
		return primary
	}
	if alive(ctx, primary) {
		return primary
	}
	for _, m := range mirrors {
		if m == "" {
			continue
		}
		if alive(ctx, m) {
			return m
		}
	}
	return primary
}

type mirrorProvider interface {
	GetMirrors() []string
}

type activeDomainProvider interface {
	GetActiveDomain() string
}

func trackerMirrors(tr client.ITracker) []string {
	if p, ok := tr.(mirrorProvider); ok {
		return p.GetMirrors()
	}
	return nil
}

func trackerActiveDomain(tr client.ITracker) string {
	if p, ok := tr.(activeDomainProvider); ok {
		return strings.TrimSpace(p.GetActiveDomain())
	}
	return ""
}

type domainOverride struct {
	client.ITracker
	domain      string
	host        string
	loginURL    string
	downloadURL string
}

func (d *domainOverride) GetDomain() string { return d.domain }
func (d *domainOverride) GetHost() string   { return d.host }
func (d *domainOverride) LoginLink() string { return d.loginURL }
func (d *domainOverride) DownloadLink(id string) string {
	return d.downloadURL + id
}

func (d *domainOverride) PrimaryDomain() string { return d.ITracker.GetDomain() }

func (d *domainOverride) LoginAuthIncomplete(cookies []*http.Cookie) bool {
	if v, ok := d.ITracker.(interface {
		LoginAuthIncomplete([]*http.Cookie) bool
	}); ok {
		return v.LoginAuthIncomplete(cookies)
	}
	return false
}

func (d *domainOverride) LoginAuthHint() string {
	if v, ok := d.ITracker.(interface {
		LoginAuthHint() string
	}); ok {
		return v.LoginAuthHint()
	}
	return ""
}

func withActiveDomain(tr client.ITracker, domain string) client.ITracker {
	if domain == "" || tr.GetDomain() == domain {
		return tr
	}
	return &domainOverride{
		ITracker:    tr,
		domain:      domain,
		host:        "https://" + domain,
		loginURL:    trackerModel.ReplaceURLHost(tr.LoginLink(), domain),
		downloadURL: trackerModel.ReplaceURLHost(tr.DownloadLink(""), domain),
	}
}

// адрес постера трекеры вида kinozal собирают из своего домена, а он у клиента
// подменён на живое зеркало: без подмены ссылка ведёт на домен, который и был
// признан недоступным
func (d *domainOverride) ParsePoster(b *bytes.Buffer, encoded bool) string {
	poster := d.ITracker.ParsePoster(b, encoded)
	u, err := url.Parse(poster)
	if err != nil || u.Host != d.ITracker.GetDomain() {
		return poster
	}
	return trackerModel.ReplaceURLHost(poster, d.domain)
}
