package sqlite

import (
	"testing"
	"time"

	"ripperr/internal/model/storage"
)

func TestErrorMarksOnlyErrorRows(t *testing.T) {
	c := newTestClient(t)
	ctx := t.Context()

	failing := &storage.CheckLogEntry{URLHash: "h1", ItemName: "A", Status: "error"}
	for range 2 {
		if _, err := c.AddCheckLog(ctx, failing); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h2", ItemName: "B", Status: "error", KeepStreak: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddCheckLog(ctx, &storage.CheckLogEntry{URLHash: "h3", ItemName: "C", Status: "unchanged"}); err != nil {
		t.Fatal(err)
	}

	marks, err := c.ErrorMarks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 1 {
		t.Fatalf("marks = %v, want only h1", marks)
	}
	m, ok := marks["h1"]
	if !ok || m.Streak != 2 {
		t.Fatalf("h1 mark = %+v ok=%v, want streak 2", m, ok)
	}
	if d := time.Since(m.CheckedAt); d < -5*time.Second || d > 5*time.Second {
		t.Fatalf("checked_at off by %v", d)
	}
}
