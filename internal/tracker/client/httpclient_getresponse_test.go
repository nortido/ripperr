package client

import (
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/model/tracker"
)

func TestGetResponseNoHeaderReturnsError(t *testing.T) {
	c := &client{ITracker: &tracker.Tracker{}}
	_, err := c.GetResponse(t.Context(), "http://example.com/", false)
	if err == nil || !strings.Contains(err.Error(), "no header") {
		t.Fatalf("err = %v, want no header error", err)
	}
}

func TestGetResponseRewritesHostForTrackerLinkOnly(t *testing.T) {
	tr := &tracker.Tracker{Domain: "kinozal.guru", Mirrors: []string{"kinozal.tv"}}
	var lastURL string
	hc := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		lastURL = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       http.NoBody,
			Request:    req,
		}, nil
	})}
	c := &client{
		ITracker: tr,
		hc:       hc,
		cb:       helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{}),
		header:   &http.Header{},
	}

	cases := []struct {
		name string
		link string
		want string
	}{
		{
			name: "tracker mirror rewritten to active domain",
			link: "https://kinozal.tv/details.php?id=1",
			want: "https://kinozal.guru/details.php?id=1",
		},
		{
			name: "foreign link left untouched",
			link: "https://rutracker.org/forum/viewtopic.php?t=1",
			want: "https://rutracker.org/forum/viewtopic.php?t=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.GetResponse(t.Context(), tc.link, false); err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if lastURL != tc.want {
				t.Fatalf("requested url = %q, want %q", lastURL, tc.want)
			}
		})
	}
}

func TestCheckResponseURLMismatch(t *testing.T) {
	c := &client{ITracker: &tracker.Tracker{Name: "test"}}
	req, err := http.NewRequest(http.MethodGet, "http://example.com/a", nil)
	if err != nil {
		t.Fatal(err)
	}
	otherURL, err := url.Parse("http://example.com/b")
	if err != nil {
		t.Fatal(err)
	}
	res := &http.Response{
		StatusCode: http.StatusOK,
		Request:    &http.Request{URL: otherURL},
	}
	err = c.checkResponse(req, res, false)
	if err == nil || !strings.Contains(err.Error(), "unexpected response url") {
		t.Fatalf("err = %v, want unexpected response url error", err)
	}
}

func TestCheckResponseDownloadRedirectRejected(t *testing.T) {
	c := &client{ITracker: &tracker.Tracker{Name: "test"}}
	req, err := http.NewRequest(http.MethodGet, "http://example.com/dl", nil)
	if err != nil {
		t.Fatal(err)
	}
	res := &http.Response{StatusCode: http.StatusFound, Request: req}
	err = c.checkResponse(req, res, true)
	if err == nil || !strings.Contains(err.Error(), "download redirect not followed") {
		t.Fatalf("err = %v, want download redirect error", err)
	}
}

type closeTrackingBody struct {
	io.Reader
	closed *bool
}

func (b closeTrackingBody) Close() error {
	*b.closed = true
	return nil
}

func TestCheckResponseErrorStatusClosesBody(t *testing.T) {
	c := &client{ITracker: &tracker.Tracker{Name: "test"}}
	req, err := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	res := &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       closeTrackingBody{Reader: strings.NewReader("err body"), closed: &closed},
		Request:    req,
	}
	err = c.checkResponse(req, res, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "application/json") {
		t.Fatalf("err = %q, want status and content type", err.Error())
	}
	if !closed {
		t.Fatal("response body was not closed")
	}
}

type topicLinkTracker struct {
	tracker.Tracker
}

func (t *topicLinkTracker) TopicLink(topicID string) string {
	return "https://example.com/topic/" + topicID
}

func TestApplyDownloadHeaders(t *testing.T) {
	c := &client{ITracker: &topicLinkTracker{}}
	c.header = &http.Header{}
	c.header.Set("User-Agent", "UA-test")
	c.header.Set("Accept-Language", "ru-RU")
	c.header.Set("sec-ch-ua", "chrome-hints")
	c.header.Set("sec-ch-ua-mobile", "?0")
	c.header.Set("sec-ch-ua-platform", `"macOS"`)
	c.cookies = []*http.Cookie{{Name: "sid", Value: "abc"}}

	req, err := http.NewRequest(http.MethodGet, "http://example.com/download.php?t=42", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.applyDownloadHeaders(req)

	want := map[string]string{
		"User-Agent":         "UA-test",
		"Accept-Language":    "ru-RU",
		"sec-ch-ua":          "chrome-hints",
		"sec-ch-ua-mobile":   "?0",
		"sec-ch-ua-platform": `"macOS"`,
		"Cookie":             "sid=abc",
		"Accept":             "application/x-bittorrent,application/octet-stream,*/*;q=0.9",
		"Referer":            "https://example.com/topic/42",
	}
	for key, wantVal := range want {
		if got := req.Header.Get(key); got != wantVal {
			t.Fatalf("header %q = %q, want %q", key, got, wantVal)
		}
	}
}

func TestBuildLoginRequest(t *testing.T) {
	tr := &tracker.Tracker{Domain: "example.com", LoginURL: "https://example.com/login"}

	t.Run("empty credentials produce GET", func(t *testing.T) {
		c := &client{ITracker: tr, header: &http.Header{}}
		req, err := c.buildLoginRequest(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		if req.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", req.Method)
		}
		if req.Header.Get("Content-Length") != "0" {
			t.Fatalf("content-length = %q", req.Header.Get("Content-Length"))
		}
	})

	t.Run("non-empty credentials produce POST", func(t *testing.T) {
		c := &client{ITracker: tr, header: &http.Header{}}
		body := "login=a&password=b"
		req, err := c.buildLoginRequest(t.Context(), body)
		if err != nil {
			t.Fatal(err)
		}
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", req.Method)
		}
		got, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != body {
			t.Fatalf("body = %q, want %q", got, body)
		}
		if req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatalf("content-type = %q", req.Header.Get("Content-Type"))
		}
	})
}

type pageLinkTracker struct {
	tracker.Tracker
}

func (pageLinkTracker) PageLink(link string) string {
	return strings.Replace(link, "/release/", "/api/", 1)
}

func TestGetResponsePageLinkOnlyForPages(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	var seen []string
	hc := &http.Client{Jar: jar, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seen = append(seen, req.URL.Path)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: req}, nil
	})}
	c := &client{
		ITracker:   &pageLinkTracker{Tracker: tracker.Tracker{Domain: "example.org"}},
		hc:         hc,
		downloadHC: hc,
		cb:         helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{}),
		header:     &http.Header{},
	}
	for _, download := range []bool{false, true} {
		if _, err := c.GetResponse(t.Context(), "https://example.org/release/x", download); err != nil {
			t.Fatalf("download=%v: %v", download, err)
		}
	}
	if len(seen) != 2 || seen[0] != "/api/x" || seen[1] != "/release/x" {
		t.Fatalf("paths = %v, want [/api/x /release/x]", seen)
	}
}

func TestNotFoundAnswerDoesNotTripBreaker(t *testing.T) {
	hits := 0
	status := http.StatusNotFound
	hc := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hits++
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("{}")),
			Request:    req,
		}, nil
	})}
	newClient := func() *client {
		return &client{
			ITracker: &tracker.Tracker{Name: "test", Domain: "example.com"},
			hc:       hc,
			cb:       helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{MaxFailures: 2, Timeout: time.Hour}),
			header:   &http.Header{},
		}
	}

	c := newClient()
	ctx := WithNotFoundAnswer(t.Context())
	for i := 0; i < 5; i++ {
		_, err := c.GetResponse(ctx, "http://example.com/a", false)
		var se *StatusError
		if !errors.As(err, &se) || se.Code != http.StatusNotFound {
			t.Fatalf("lookup #%d err = %v, want a 404 StatusError", i, err)
		}
	}
	status = http.StatusOK
	if _, err := c.GetResponse(ctx, "http://example.com/a", false); err != nil {
		t.Fatalf("lookup after 404s err = %v, want nil (breaker closed)", err)
	}
	if hits != 6 {
		t.Fatalf("server hits = %d, want 6", hits)
	}

	status = http.StatusNotFound
	c = newClient()
	for i := 0; i < 2; i++ {
		_, _ = c.GetResponse(t.Context(), "http://example.com/a", false)
	}
	if _, err := c.GetResponse(t.Context(), "http://example.com/a", false); !errors.Is(err, helper.ErrCircuitOpen) {
		t.Fatalf("plain 404s err = %v, want circuit open", err)
	}

	status = http.StatusBadGateway
	c = newClient()
	for i := 0; i < 2; i++ {
		_, _ = c.GetResponse(ctx, "http://example.com/a", false)
	}
	if _, err := c.GetResponse(ctx, "http://example.com/a", false); !errors.Is(err, helper.ErrCircuitOpen) {
		t.Fatalf("5xx err = %v, want circuit open", err)
	}
}

func TestCheckResponseClosesBodyOnError(t *testing.T) {
	otherURL, err := url.Parse("http://example.com/b")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		status   int
		download bool
		resURL   *url.URL
		want     string
	}{
		{"unexpected url", http.StatusOK, false, otherURL, "unexpected response url"},
		{"download redirect", http.StatusFound, true, nil, "download redirect not followed"},
		{"status 404", http.StatusNotFound, false, nil, "404"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &client{ITracker: &tracker.Tracker{Name: "test"}}
			req, err := http.NewRequest(http.MethodGet, "http://example.com/a", nil)
			if err != nil {
				t.Fatal(err)
			}
			closed := false
			res := &http.Response{
				StatusCode: tc.status,
				Header:     http.Header{},
				Body:       closeTrackingBody{Reader: strings.NewReader("body"), closed: &closed},
				Request:    req,
			}
			if tc.resURL != nil {
				res.Request = &http.Request{URL: tc.resURL}
			}
			err = c.checkResponse(req, res, tc.download)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if !closed {
				t.Fatal("response body was not closed")
			}
		})
	}
}
