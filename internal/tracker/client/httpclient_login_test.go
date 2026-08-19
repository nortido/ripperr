package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"trackerr/internal/model/tracker"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestLoginFromPage4xxWrapsErrorWithTrackerName(t *testing.T) {
	c := &client{
		ITracker: &tracker.Tracker{Name: "kinozal"},
		hc: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Body:       io.NopCloser(strings.NewReader("blocked")),
					Request:    req,
				}, nil
			}),
		},
	}
	req, err := http.NewRequest(http.MethodGet, "http://example.com/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.loginFromPage(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "kinozal") {
		t.Fatalf("err = %q, want tracker name", err.Error())
	}
	if strings.Contains(err.Error(), "%!w") {
		t.Fatalf("err leaks nil-wrap artifact: %q", err.Error())
	}
}
