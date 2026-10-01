package flaresolverr

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ripperr/internal/model/proxy"
)

func TestPrepareParamsOmitsEmptyCookies(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://kinozal.guru/details.php?id=1", nil)
	params := PrepareParams(req, &proxy.Options{})
	if params.Cookies != nil {
		t.Fatalf("expected nil cookies in payload, got %+v", params.Cookies)
	}
}

func TestPrepareParamsPassesCookiesAndSession(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://kinozal.guru/details.php?id=1", nil)
	cookies := []*http.Cookie{{Name: "uid", Value: "1", Domain: ".kinozal.guru", Path: "/"}, {Name: "pass", Value: "secret", Domain: "kinozal.guru"}}
	params := PrepareParams(req, &proxy.Options{Session: "kinozal", Cookies: cookies})
	if params.Session != "kinozal" {
		t.Fatalf("session = %q", params.Session)
	}
	if len(params.Cookies) != 2 {
		t.Fatalf("cookies = %+v", params.Cookies)
	}
	for _, c := range params.Cookies {
		if c.Domain != "" || c.Path != "" {
			t.Fatalf("expected name/value only, got %+v", c)
		}
	}
}

func TestCookiesForFlareJSON(t *testing.T) {
	cookies := cookiesForFlare([]*http.Cookie{{Name: "uid", Value: "1", Domain: ".kinozal.guru"}})
	raw, err := json.Marshal(cookies)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, "domain") {
		t.Fatalf("domain must be omitted: %s", body)
	}
	if !strings.Contains(body, `"uid"`) || !strings.Contains(body, `"1"`) {
		t.Fatalf("unexpected json: %s", body)
	}
}

func TestBuildResponseSolutionCookies(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://kinozal.guru/browse.php", nil)
	resp := &Response{
		Status: "ok",
		Solution: &ResponseSolution{
			Status:   200,
			Response: "<html>ok</html>",
			Cookies: Cookies{
				{Name: "uid", Value: "42"},
				{Name: "pass", Value: "hash"},
			},
			UserAgent: "test-agent",
		},
	}
	got, err := BuildResponse(resp, req)
	if err != nil {
		t.Fatal(err)
	}
	parsed := got.Cookies()
	if len(parsed) != 2 || parsed[0].Name != "uid" || parsed[1].Value != "hash" {
		t.Fatalf("cookies = %+v", parsed)
	}
	if got.Header.Get("User-Agent") != "test-agent" {
		t.Fatalf("ua = %q", got.Header.Get("User-Agent"))
	}
}

func TestSolutionCookiesSkipsEmptyNames(t *testing.T) {
	resp := &Response{
		Solution: &ResponseSolution{
			Cookies: Cookies{{Name: "", Value: "x"}, {Name: "uid", Value: "1"}},
		},
	}
	got := SolutionCookies(resp)
	if len(got) != 1 || got[0].Name != "uid" {
		t.Fatalf("got = %+v", got)
	}
}
