package client

import (
	"testing"

	"ripperr/internal/model/tracker"
)

type sessionTracker struct {
	tracker.Tracker
	name string
}

func (s *sessionTracker) FlareSession() string { return s.name }

func TestWithFlareSessionOverridesOnlyExistingSession(t *testing.T) {
	cases := []struct {
		name     string
		tr       ITracker
		override string
		want     string
		wantOK   bool
	}{
		{"tracker with session", &sessionTracker{name: "rutracker"}, "rutracker-test-ab12cd34", "rutracker-test-ab12cd34", true},
		{"no override", &sessionTracker{name: "rutracker"}, "", "rutracker", true},
		{"empty tracker session stays none", &sessionTracker{}, "x-test-1", "", false},
		{"tracker without provider stays none", &tracker.Tracker{}, "x-test-1", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []Option
			if tc.override != "" {
				opts = append(opts, WithFlareSession(tc.override))
			}
			c := New(nil, nil, tc.tr, nil, nil, opts...).(*client)
			got, ok := c.flareSessionID()
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("flareSessionID = %q, %v; want %q, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
