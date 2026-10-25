package notify

import (
	"context"
	"testing"
	"time"

	"ripperr/internal/model/storage"
)

type fakeRecorder struct {
	got chan storage.EventEntry
}

func (r *fakeRecorder) AddEvent(_ context.Context, e storage.EventEntry) error {
	r.got <- e
	return nil
}

func TestNotifyRecordsEvent(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"disabled", Config{Enabled: false, Language: LangRU}},
		{"enabled without channels", Config{Enabled: true, Language: LangRU}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &fakeRecorder{got: make(chan storage.EventEntry, 1)}
			n := New(tc.cfg, "").WithEvents(rec)
			n.Notify(UpdatedEvent("Some.Release", "https://example.com/t1", ""))
			select {
			case e := <-rec.got:
				if e.Source != "server" || e.Kind != KindUpdated {
					t.Fatalf("source/kind = %q/%q", e.Source, e.Kind)
				}
				if e.Text != "Раздача обновлена в torrent-клиенте:" {
					t.Fatalf("text = %q", e.Text)
				}
				if e.Subject != "Some.Release" || e.AppPath != "#dashboard?name=Some.Release" {
					t.Fatalf("subject/path = %q/%q", e.Subject, e.AppPath)
				}
				if e.At.IsZero() {
					t.Fatal("At is zero")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("event not recorded")
			}
		})
	}
}

func TestNotifyNilRecorder(t *testing.T) {
	New(Config{Enabled: false}, "").Notify(UpdatedEvent("x", "", ""))
}
