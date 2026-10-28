package mirrorsync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/helper"
)

const (
	githubTemplate   = "https://raw.githubusercontent.com/Jackett/Jackett/master/{path}"
	jsdelivrTemplate = "https://%s.jsdelivr.net/gh/Jackett/Jackett@master/{path}"
	jackettRoot      = "src/Jackett.Common/"
)

var jsdelivrHosts = []string{"cdn", "fastly", "gcore", "testingcf"}

func SourceURLs(ms config.MirrorSource) []string {
	switch ms.Type {
	case "jsdelivr":
		out := make([]string, len(jsdelivrHosts))
		for i, h := range jsdelivrHosts {
			out[i] = fmt.Sprintf(jsdelivrTemplate, h)
		}
		return out
	case "custom":
		return []string{ms.URL}
	}
	return []string{githubTemplate}
}

func expand(tmpl, path string) string {
	return strings.Replace(tmpl, "{path}", jackettRoot+path, 1)
}

func Client(ms config.MirrorSource, downloadProxy string, timeout time.Duration) *http.Client {
	if ms.Direct {
		// nil Transport: DefaultTransport, как у приложения с пустым прокси загрузок
		return &http.Client{Timeout: timeout, CheckRedirect: checkRedirect}
	}
	return &http.Client{Timeout: timeout, Transport: helper.ProxyTransport(downloadProxy), CheckRedirect: checkRedirect}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	// свой шаблон проверен при сохранении, но его https-хост может увести 302 на http или в LAN
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("redirect to %s refused", req.URL.Scheme)
	}
	// IsLocalHost сравнивает буквально, а BOX.LOCAL. ведет туда же
	host := strings.TrimSuffix(strings.ToLower(req.URL.Hostname()), ".")
	if helper.IsLocalHost(host) {
		return fmt.Errorf("redirect to %s refused", host)
	}
	return nil
}

func fetchAny(ctx context.Context, hc *http.Client, tmpls []string, start *int, path string) (source, error) {
	n := len(tmpls)
	if n == 0 {
		return source{}, errors.New("no source URL")
	}
	var last error
	for i := range n {
		if err := ctx.Err(); err != nil {
			return source{}, err
		}
		idx := (*start + i) % n
		full := expand(tmpls[idx], path)
		src, err := fetchSource(ctx, hc, full, path)
		if err == nil {
			*start = idx
			return src, nil
		}
		last = err
		if n > 1 {
			if u, perr := url.Parse(full); perr == nil && u.Host != "" {
				last = fmt.Errorf("%s: %w", u.Host, err)
			}
		}
	}
	return source{}, last
}

func TestSource(ctx context.Context, hc *http.Client, tmpls []string) (int, error) {
	start := 0
	src, err := fetchAny(ctx, hc, tmpls, &start, jackettSources["kinozal"])
	if err != nil {
		return 0, err
	}
	return len(src.Links), nil
}
