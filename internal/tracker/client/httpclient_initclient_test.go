package client

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"

	"ripperr/internal/model/tracker"
)

func TestInitClientPublicSkipsLogin(t *testing.T) {
	cases := []struct {
		name    string
		tracker *tracker.Tracker
		payload *tracker.Tracker
	}{
		{
			name:    "tracker publicity public",
			tracker: &tracker.Tracker{Publicity: tracker.PublicityPublic},
			payload: &tracker.Tracker{Publicity: "private"},
		},
		{
			name:    "payload publicity public",
			tracker: &tracker.Tracker{Publicity: "private"},
			payload: &tracker.Tracker{Publicity: tracker.PublicityPublic},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			hc := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				called = true
				return nil, errors.New("transport should not be called")
			})}
			c := &client{ITracker: tc.tracker, hc: hc}
			if err := c.InitClient(t.Context(), tc.payload, false); err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if called {
				t.Fatal("login request issued for a public tracker")
			}
		})
	}
}

func TestInitClientSavedCookiesNoForceLoginSkipsLogin(t *testing.T) {
	cj, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	hc := &http.Client{Jar: cj, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("transport should not be called")
	})}
	tr := &tracker.Tracker{Domain: "example.com", LoginURL: "https://example.com/login", Publicity: "private"}
	c := &client{ITracker: tr, hc: hc}
	payload := &tracker.Tracker{
		Exists:    true,
		Cookies:   []*http.Cookie{{Name: "sid", Value: "abc"}},
		Publicity: "private",
	}
	if err = c.InitClient(t.Context(), payload, false); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if called {
		t.Fatal("login request issued despite saved cookies")
	}
	if len(c.cookies) != 1 || c.cookies[0].Name != "sid" || c.cookies[0].Value != "abc" {
		t.Fatalf("client cookies = %+v", c.cookies)
	}
	loginURL, _ := url.Parse(tr.LoginURL)
	jarCookies := cj.Cookies(loginURL)
	if len(jarCookies) != 1 || jarCookies[0].Name != "sid" {
		t.Fatalf("jar cookies = %+v", jarCookies)
	}
}

type incompleteCookieTracker struct {
	tracker.Tracker
}

func (i *incompleteCookieTracker) CookiesIncomplete([]*http.Cookie) bool { return true }

func TestInitClientLoginFailsFallsBackToSavedCookies(t *testing.T) {
	cj, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	hc := &http.Client{Jar: cj, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("login unreachable")
	})}
	tr := &incompleteCookieTracker{Tracker: tracker.Tracker{
		Domain:    "example.com",
		LoginURL:  "https://example.com/login",
		Publicity: "private",
	}}
	c := &client{ITracker: tr, hc: hc}
	payload := &tracker.Tracker{
		Exists:    true,
		Cookies:   []*http.Cookie{{Name: "sid", Value: "stale"}},
		Publicity: "private",
	}
	if err = c.InitClient(t.Context(), payload, false); err != nil {
		t.Fatalf("expected fallback success, got err: %v", err)
	}
	if calls != 1 {
		t.Fatalf("login attempts = %d, want 1", calls)
	}
	if len(c.cookies) != 1 || c.cookies[0].Name != "sid" || c.cookies[0].Value != "stale" {
		t.Fatalf("client cookies = %+v, want saved db cookies", c.cookies)
	}
	loginURL, _ := url.Parse(tr.LoginURL)
	if jarCookies := cj.Cookies(loginURL); len(jarCookies) != 1 {
		t.Fatalf("jar cookies = %+v", jarCookies)
	}
}

func TestInitClientForceLoginDropsSavedCookies(t *testing.T) {
	cj, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	hc := &http.Client{Jar: cj, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		res := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       http.NoBody,
			Request:    req,
		}
		res.Header.Add("Set-Cookie", "sid=fresh")
		return res, nil
	})}
	tr := &tracker.Tracker{Domain: "example.com", LoginURL: "https://example.com/login", Publicity: "private"}
	c := &client{ITracker: tr, hc: hc}
	payload := &tracker.Tracker{
		Exists:    true,
		Cookies:   []*http.Cookie{{Name: "sid", Value: "stale"}},
		Publicity: "private",
	}
	if err = c.InitClient(t.Context(), payload, true); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !called {
		t.Fatal("forceLogin should have issued a login request")
	}
	if len(c.cookies) != 1 || c.cookies[0].Value != "fresh" {
		t.Fatalf("client cookies = %+v, want fresh login cookies", c.cookies)
	}
	for _, ck := range c.cookies {
		if ck.Value == "stale" {
			t.Fatalf("saved cookie survived forceLogin: %+v", c.cookies)
		}
	}
}
