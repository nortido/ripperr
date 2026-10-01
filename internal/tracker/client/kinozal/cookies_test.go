package kinozal

import (
	"net/http"
	"testing"

	"ripperr/internal/model/tracker"
)

func TestLoginAuthIncomplete(t *testing.T) {
	tr := New(tracker.Tracker{})
	if !tr.LoginAuthIncomplete(nil) {
		t.Fatal("expected incomplete for nil cookies")
	}
	if !tr.LoginAuthIncomplete([]*http.Cookie{{Name: "uid", Value: "1"}}) {
		t.Fatal("expected incomplete without pass")
	}
	if tr.LoginAuthIncomplete([]*http.Cookie{{Name: "uid", Value: "1"}, {Name: "pass", Value: "x"}}) {
		t.Fatal("expected complete auth cookies")
	}
	if !tr.LoginAuthIncomplete([]*http.Cookie{{Name: "uid", Value: "1"}, {Name: "pass", Value: ""}}) {
		t.Fatal("expected incomplete when pass value empty")
	}
}

func TestLoginAuthHint(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.LoginAuthHint(); got == "" {
		t.Fatal("expected credentials format hint")
	}
}
