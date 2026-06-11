package rutracker

import (
	"net/http"
	"strings"
)

// cf_clearance is kept in the FlareSolverr session; only forum session is required in DB.
var requiredCookieNames = []string{"bb_session"}

// CookiesIncomplete reports missing rutracker session cookies.
func (t *Tracker) CookiesIncomplete(cookies []*http.Cookie) bool {
	present := make(map[string]struct{}, len(cookies))
	for _, c := range cookies {
		if c != nil && c.Name != "" {
			present[c.Name] = struct{}{}
		}
	}
	for _, name := range requiredCookieNames {
		if _, ok := present[name]; !ok {
			return true
		}
	}
	return false
}

// EnhanceCookies applies rutracker.org-specific cookie attributes (Cloudflare + phpBB).
func (t *Tracker) EnhanceCookies(cookies []*http.Cookie) {
	for _, c := range cookies {
		if c == nil {
			continue
		}
		switch c.Name {
		case "cf_clearance":
			c.Path = "/"
			c.Secure = true
			c.HttpOnly = true
			c.SameSite = http.SameSiteNoneMode
		default:
			if strings.HasPrefix(c.Name, "bb_") {
				c.Path = "/forum/"
				if c.Name == "bb_session" {
					c.Secure = true
					c.HttpOnly = true
				}
			}
		}
	}
}
