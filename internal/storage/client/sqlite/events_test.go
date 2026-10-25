package sqlite

import (
	"context"
	"database/sql"
	"errors"
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

func TestCheckFailedDoesNotEvictOthers(t *testing.T) {
	c := newTestClient(t)
	base := time.Now().Add(-time.Hour)
	add := func(i int, kind string) {
		t.Helper()
		e := storage.EventEntry{At: base.Add(time.Duration(i) * time.Second), Source: "server", Kind: kind, Text: fmt.Sprintf("%s%d", kind, i)}
		if err := c.AddEvent(t.Context(), e); err != nil {
			t.Fatalf("add %s %d: %v", kind, i, err)
		}
	}
	for i := range 10 {
		add(i, "updated")
	}
	for i := 10; i < 610; i++ {
		add(i, "check_failed")
	}
	got, err := c.ListEvents(t.Context(), 1000)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var updated, failed int
	for _, e := range got {
		switch e.Kind {
		case "updated":
			updated++
		case "check_failed":
			failed++
		}
	}
	if updated != 10 || failed != 100 {
		t.Fatalf("updated %d check_failed %d, want 10 and 100", updated, failed)
	}
	if got[0].Text != "check_failed609" {
		t.Fatalf("first = %q, want check_failed609", got[0].Text)
	}
}

func TestCheckFailedDoesNotShrinkOtherCap(t *testing.T) {
	c := newTestClient(t)
	base := time.Now().Add(-time.Hour)
	for i := range 510 {
		kind := "updated"
		if i >= 505 {
			kind = "check_failed"
		}
		e := storage.EventEntry{At: base.Add(time.Duration(i) * time.Second), Source: "server", Kind: kind, Text: fmt.Sprintf("e%d", i)}
		if err := c.AddEvent(t.Context(), e); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	got, err := c.ListEvents(t.Context(), 1000)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var updated, failed int
	for _, e := range got {
		if e.Kind == "check_failed" {
			failed++
		} else {
			updated++
		}
	}
	if updated != 500 || failed != 5 {
		t.Fatalf("updated %d check_failed %d, want 500 and 5", updated, failed)
	}
}

func holdConn(t *testing.T, c *Client) *sql.Tx {
	t.Helper()
	tx, err := c.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestRecordEventFlushWaitsForPending(t *testing.T) {
	c := newTestClient(t)
	hold := holdConn(t, c)
	base := time.Now().Add(-time.Hour)
	for i := range 150 {
		c.RecordEvent(storage.EventEntry{At: base.Add(time.Duration(i) * time.Second), Source: "server", Kind: "check_failed", Text: fmt.Sprintf("f%d", i)})
		if i%50 == 0 {
			c.RecordEvent(storage.EventEntry{At: base.Add(time.Duration(i)*time.Second + 500*time.Millisecond), Source: "server", Kind: "updated", Text: fmt.Sprintf("u%d", i)})
		}
	}
	short, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := c.FlushEvents(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("flush while connection held = %v, want DeadlineExceeded", err)
	}
	if err := hold.Rollback(); err != nil {
		t.Fatal(err)
	}
	long, cancelLong := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelLong()
	if err := c.FlushEvents(long); err != nil {
		t.Fatalf("flush after release: %v", err)
	}
	got, err := c.ListEvents(t.Context(), 1000)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var failed, updated int
	for i, e := range got {
		if e.Kind == "check_failed" {
			failed++
		} else {
			updated++
		}
		if i > 0 && e.ID >= got[i-1].ID {
			t.Fatalf("ids not in record order at %d: %d after %d", i, e.ID, got[i-1].ID)
		}
	}
	if failed != 100 || updated != 3 {
		t.Fatalf("check_failed %d updated %d, want 100 and 3", failed, updated)
	}
	if got[0].Text != "f149" {
		t.Fatalf("newest = %q, want f149", got[0].Text)
	}
}

func TestRecordEventOverflowDropsBeyondCap(t *testing.T) {
	c := newTestClient(t)
	old := eventsQueueMax
	eventsQueueMax = 5
	t.Cleanup(func() { eventsQueueMax = old })
	hold := holdConn(t, c)
	base := time.Now().Add(-time.Hour)
	started := time.Now()
	var accepted []bool
	for i := range 8 {
		accepted = append(accepted, c.RecordEvent(storage.EventEntry{At: base.Add(time.Duration(i) * time.Second), Source: "server", Kind: "updated", Text: fmt.Sprintf("e%d", i)}))
	}
	if d := time.Since(started); d > 50*time.Millisecond {
		t.Fatalf("8 RecordEvent calls took %v, want under 50ms", d)
	}
	if want := []bool{true, true, true, true, true, false, false, false}; !slices.Equal(accepted, want) {
		t.Fatalf("accepted = %v, want %v", accepted, want)
	}
	if err := hold.Rollback(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := c.FlushEvents(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	got, err := c.ListEvents(t.Context(), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 5 || got[0].Text != "e4" || got[4].Text != "e0" {
		t.Fatalf("rows = %+v, want e4..e0", got)
	}
}

func TestFlushEventsIdleReturnsAtOnce(t *testing.T) {
	c := newTestClient(t)
	done := make(chan error, 1)
	go func() { done <- c.FlushEvents(t.Context()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("flush idle: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle FlushEvents blocked")
	}
}

func TestRecordEventSecondBatchAfterFlusherExit(t *testing.T) {
	c := newTestClient(t)
	for round, text := range []string{"first", "second"} {
		if !c.RecordEvent(storage.EventEntry{At: time.Now(), Source: "server", Kind: "updated", Text: text}) {
			t.Fatalf("round %d: record refused", round)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		err := c.FlushEvents(ctx)
		cancel()
		if err != nil {
			t.Fatalf("round %d flush: %v", round, err)
		}
		got, err := c.ListEvents(t.Context(), 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != round+1 || got[0].Text != text {
			t.Fatalf("round %d rows = %+v", round, got)
		}
	}
}
