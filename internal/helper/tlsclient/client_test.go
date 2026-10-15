package tlsclient

import (
	"strings"
	"testing"
)

func TestNewHidesProxySecrets(t *testing.T) {
	for _, raw := range []string{"http://u:SEC-pw@/SEC-path", "http://u:SEC-pw@host:bad"} {
		_, err := New(Config{Profile: "chrome_131", ProxyURL: raw})
		if err == nil {
			t.Fatalf("%s: want error", raw)
		}
		if strings.Contains(err.Error(), "SEC-") {
			t.Fatalf("%s: secret leaked: %s", raw, err)
		}
	}
}
