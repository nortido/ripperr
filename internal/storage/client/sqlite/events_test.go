package sqlite

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"ripperr/internal/model/storage"
)

func TestBootstrapCreatesEvents(t *testing.T) {
	db := newRawDB(t)
	c := New(db, testBox(t))
	if err := c.Bootstrap(t.Context()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('events') ORDER BY cid`)
	if err != nil {
		t.Fatalf("table info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"id", "created_at", "source", "kind", "text", "subject", "app_path"}; !slices.Equal(cols, want) {
		t.Fatalf("events columns = %v, want %v", cols, want)
	}
	if got := userVersion(t, db); got != len(migrations) {
		t.Fatalf("user_version = %d, want %d", got, len(migrations))
	}
}

func TestAddEventKeepsNewest500(t *testing.T) {
	c := newTestClient(t)
	base := time.Now().Add(-time.Hour)
	for i := range 505 {
		e := storage.EventEntry{At: base.Add(time.Duration(i) * time.Second), Source: "server", Kind: "updated", Text: fmt.Sprintf("e%d", i), Subject: "subj", AppPath: "#dashboard"}
		if err := c.AddEvent(t.Context(), e); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	got, err := c.ListEvents(t.Context(), 1000)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 500 {
		t.Fatalf("len = %d, want 500", len(got))
	}
	if got[0].Text != "e504" || got[499].Text != "e5" {
		t.Fatalf("order: first %q last %q, want e504 and e5", got[0].Text, got[499].Text)
	}
	if got[0].Source != "server" || got[0].Kind != "updated" || got[0].Subject != "subj" || got[0].AppPath != "#dashboard" || got[0].ID == 0 {
		t.Fatalf("round trip lost fields: %+v", got[0])
	}
	if want := base.Add(504 * time.Second).Unix(); got[0].At.Unix() != want {
		t.Fatalf("At = %d, want %d", got[0].At.Unix(), want)
	}
}

func TestEventsDropOlderThan30Days(t *testing.T) {
	c := newTestClient(t)
	if err := c.AddEvent(t.Context(), storage.EventEntry{At: time.Now().Add(-31 * 24 * time.Hour), Source: "server", Kind: "updated", Text: "old"}); err != nil {
		t.Fatalf("add old: %v", err)
	}
	if err := c.AddEvent(t.Context(), storage.EventEntry{At: time.Now(), Source: "ui", Kind: "ui_error", Text: "fresh"}); err != nil {
		t.Fatalf("add fresh: %v", err)
	}
	got, err := c.ListEvents(t.Context(), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Text != "fresh" {
		t.Fatalf("list = %+v, want one fresh item", got)
	}
	var n int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestListEventsHidesStaleRows(t *testing.T) {
	c := newTestClient(t)
	stale := time.Now().Add(-31 * 24 * time.Hour).Unix()
	fresh := time.Now().Unix()
	for text, at := range map[string]int64{"stale": stale, "fresh": fresh} {
		if _, err := c.db.Exec(`INSERT INTO events (created_at, source, kind, text) VALUES (?, 'server', 'updated', ?)`, at, text); err != nil {
			t.Fatal(err)
		}
	}
	got, err := c.ListEvents(t.Context(), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Text != "fresh" {
		t.Fatalf("list = %+v, want only fresh", got)
	}
}
