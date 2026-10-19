package mirrorsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const maxSourceBytes = 1 << 20

var jackettSources = map[string]string{
	"rutracker":   "Indexers/Definitions/RuTracker.cs",
	"kinozal":     "Definitions/kinozal.yml",
	"rutor":       "Definitions/rutor.yml",
	"teamhd":      "Definitions/teamhd.yml",
	"nnm":         "Definitions/noname-club.yml",
	"toloka":      "Indexers/Definitions/Toloka.cs",
	"tapochek":    "Definitions/tapochek.yml",
	"riperam":     "Definitions/riperam.yml",
	"booktracker": "Definitions/booktracker.yml",
	"anidub":      "Indexers/Definitions/AniDUB.cs",
	"animelayer":  "Definitions/animelayer.yml",
	"selezen":     "Definitions/selezen.yml",
	"rustorka":    "Definitions/rustorka.yml",
	"aniliberty":  "Indexers/Definitions/Anilibria.cs",
	"newstudio":   "Definitions/newstudio.yml",
}

var errNoLinks = errors.New("no links in source")

type source struct {
	Links  []string
	Legacy []string
}

var (
	yamlItemRe    = regexp.MustCompile(`^\s+-\s+(.*)$`)
	csSiteLinkRe  = regexp.MustCompile(`SiteLink\s*\{[^}]*\}\s*=\s*"([^"]+)"`)
	csAlternateRe = regexp.MustCompile(`AlternativeSiteLinks\b[^{;]*\{([^}]*)\}`)
	csLegacyRe    = regexp.MustCompile(`LegacySiteLinks\b[^{;]*\{([^}]*)\}`)
	csQuotedRe    = regexp.MustCompile(`"([^"]+)"`)
)

func normalizeHost(raw string) string {
	// источник не доверенный: берем только http/https с непустым хостом
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Host)
}

func appendHost(list []string, raw string) []string {
	h := normalizeHost(raw)
	if h == "" {
		return list
	}
	for _, e := range list {
		if e == h {
			return list
		}
	}
	return append(list, h)
}

func parseYAML(data []byte) (source, error) {
	var src source
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			section = ""
			if line[0] == '#' {
				continue
			}
			name, _, _ := strings.Cut(line, ":")
			switch strings.TrimSpace(name) {
			case "links":
				section = "links"
			case "legacylinks":
				section = "legacylinks"
			}
			continue
		}
		if section == "" {
			continue
		}
		m := yamlItemRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		item, _, _ := strings.Cut(m[1], " #")
		item = strings.TrimSpace(item)
		if n := len(item); n >= 2 && (item[0] == '"' || item[0] == '\'') && item[n-1] == item[0] {
			item = item[1 : n-1]
		}
		if section == "links" {
			src.Links = appendHost(src.Links, item)
		} else {
			src.Legacy = appendHost(src.Legacy, item)
		}
	}
	if len(src.Links) == 0 {
		return source{}, errNoLinks
	}
	return src, nil
}

func parseCS(data []byte) (source, error) {
	text := string(data)
	var src source
	m := csSiteLinkRe.FindStringSubmatch(text)
	if m == nil {
		return source{}, errNoLinks
	}
	src.Links = appendHost(src.Links, m[1])
	for _, q := range csQuotedRe.FindAllStringSubmatch(firstGroup(csAlternateRe, text), -1) {
		src.Links = appendHost(src.Links, q[1])
	}
	for _, q := range csQuotedRe.FindAllStringSubmatch(firstGroup(csLegacyRe, text), -1) {
		src.Legacy = appendHost(src.Legacy, q[1])
	}
	if len(src.Links) == 0 {
		return source{}, errNoLinks
	}
	return src, nil
}

func firstGroup(re *regexp.Regexp, text string) string {
	if m := re.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

func fetchSource(ctx context.Context, hc *http.Client, rawURL, path string) (source, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return source{}, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return source{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return source{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceBytes+1))
	if err != nil {
		return source{}, err
	}
	if len(data) > maxSourceBytes {
		return source{}, fmt.Errorf("body over %d bytes", maxSourceBytes)
	}
	if strings.HasSuffix(path, ".cs") {
		return parseCS(data)
	}
	return parseYAML(data)
}
