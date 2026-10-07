package tapochek

import "net/http"

func (t *Tracker) LoginAuthIncomplete(cookies []*http.Cookie) bool {
	for _, c := range cookies {
		if c != nil && c.Name == "bb_data" && c.Value != "" {
			return false
		}
	}
	return true
}

func (t *Tracker) LoginAuthHint() string {
	return "check credentials format: expected login_username=...&login_password=...&autologin=1&redirect=index.php&login=1"
}
