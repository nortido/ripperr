package client

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"trackerr/internal/model/tracker"
)

func TestReadLimitedWithinLimit(t *testing.T) {
	var buf bytes.Buffer
	if err := ReadLimited(&buf, strings.NewReader("hello"), 10); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello" {
		t.Fatalf("buf = %q", buf.String())
	}
}

func TestReadLimitedExceedsLimit(t *testing.T) {
	var buf bytes.Buffer
	err := ReadLimited(&buf, strings.NewReader(strings.Repeat("x", maxErrorResponse+1)), maxErrorResponse)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("err = %q", err.Error())
	}
}

func TestCheckResponseOmitsErrorBody(t *testing.T) {
	const marker = "SECRET-MARKER"
	c := &client{ITracker: &tracker.Tracker{Name: "test"}}
	req, err := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	res := &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader("<html>" + marker + "</html>")),
		Request:    req,
	}
	err = c.checkResponse(req, res, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("error leaks response body: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %q, want status 403", err.Error())
	}
}

func TestCheckResponseRejectsOversizedErrorBody(t *testing.T) {
	c := &client{ITracker: &tracker.Tracker{Name: "test"}}
	req, err := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	res := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", maxErrorResponse+1))),
		Request:    req,
	}
	err = c.checkResponse(req, res, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("err = %q", err.Error())
	}
}
