package flaresolverr

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ripperr/internal/helper"
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

func buildNoPanic(t *testing.T, resp *Response) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("BuildResponse panicked: %v", r)
		}
	}()
	req, _ := http.NewRequest(http.MethodGet, "https://rutracker.org/", nil)
	_, err = BuildResponse(resp, req)
	return err
}

func TestBuildResponseWithoutSolution(t *testing.T) {
	err := buildNoPanic(t, &Response{Status: "ok"})
	if err == nil || err.Error() != "flaresolverr error: response without solution" {
		t.Fatalf("err = %v", err)
	}
	err = buildNoPanic(t, nil)
	if err == nil || err.Error() != "flaresolverr error: empty response" {
		t.Fatalf("nil response err = %v", err)
	}
}

func TestBuildResponseErrorKeepsMessage(t *testing.T) {
	msg := "Error solving the challenge. Timeout after 60.0 seconds."
	err := buildNoPanic(t, &Response{Status: "error", Message: msg})
	if err == nil || err.Error() != "flaresolverr error: "+msg {
		t.Fatalf("err = %v", err)
	}
	if !helper.IsFlareTimeout(err) {
		t.Fatal("IsFlareTimeout must match")
	}
}
