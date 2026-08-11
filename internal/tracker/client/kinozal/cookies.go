package kinozal

import "net/http"

var requiredAuthCookieNames = []string{"uid", "pass"}

// LoginAuthIncomplete reports missing kinozal auth cookies after Flare login.
func (t *Tracker) LoginAuthIncomplete(cookies []*http.Cookie) bool {
	present := make(map[string]struct{}, len(cookies))
	for _, c := range cookies {
		if c != nil && c.Name != "" && c.Value != "" {
			present[c.Name] = struct{}{}
		}
	}
	for _, name := range requiredAuthCookieNames {
		if _, ok := present[name]; !ok {
			return true
		}
	}
	return false
}

// LoginAuthHint explains expected kinozal login credentials format.
func (t *Tracker) LoginAuthHint() string {
	return "check credentials format: expected username=...&password=...&to=%2F"
}

// EnhanceCookies applies kinozal-specific cookie attributes for FlareSolverr.
func (t *Tracker) EnhanceCookies(cookies []*http.Cookie) {
	for _, c := range cookies {
		if c == nil {
			continue
		}
		switch c.Name {
		case "uid", "pass":
			c.Path = "/"
		}
	}
}
