package rutracker

import (
	"net/http"
	"strings"
)

// cf_clearance хранится в сессии FlareSolverr; в БД нужна только сессия форума.
var requiredCookieNames = []string{"bb_session"}

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
