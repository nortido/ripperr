package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFixture(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPreloadOrderAndFormat(t *testing.T) {
	resetLogState(t)
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "ripperr.log.1"),
		"2026-03-05 09:00:00 INFO: old1",
		"2026-03-05 09:00:01 WARN: old2",
	)
	writeFixture(t, filepath.Join(dir, "ripperr.log"),
		"stray continuation",
		"2026-03-05 10:00:00 ERROR: x",
		"  line2",
		"2026-13-99 bad",
		"2026-03-05 10:00:01 DEBUG: dbg",
		"dbg continuation",
		"2026-03-05 10:00:02 INFO: ",
	)

	InitFile(dir)
	Info("new")

	got := Snapshot(0)
	if len(got) != 5 {
		t.Fatalf("%d entries, want 5: %v", len(got), got)
	}
	want := []struct{ level, msg string }{
		{"INFO", "old1"},
		{"WARN", "old2"},
		{"ERROR", "x\n  line2\n2026-13-99 bad"},
		{"INFO", ""},
		{"INFO", "new"},
	}
	for i, w := range want {
		if got[i].Level != w.level || got[i].Message != w.msg {
			t.Fatalf("entry %d = %q %q, want %q %q", i, got[i].Level, got[i].Message, w.level, w.msg)
		}
	}
	wantTime, err := time.ParseInLocation(time.DateTime, "2026-03-05 10:00:00", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if !got[2].Time.Equal(wantTime) {
		t.Fatalf("time %v, want %v", got[2].Time, wantTime)
	}
}

func TestPreloadCapAndSkipsOld(t *testing.T) {
	resetLogState(t)
	dir := t.TempDir()
	lines := make([]string, 0, 1200)
	for i := 1; i <= 1200; i++ {
		lines = append(lines, fmt.Sprintf("2026-03-05 10:00:00 INFO: line %d", i))
	}
	writeFixture(t, filepath.Join(dir, "ripperr.log"), lines...)
	writeFixture(t, filepath.Join(dir, "ripperr.log.1"), "2026-03-05 09:00:00 INFO: marker")

	InitFile(dir)

	got := Snapshot(0)
	if len(got) != 1000 {
		t.Fatalf("%d entries, want 1000", len(got))
	}
	if got[0].Message != "line 201" || got[999].Message != "line 1200" {
		t.Fatalf("window is %q .. %q", got[0].Message, got[999].Message)
	}
}

func TestPreloadUsesOlderWhenShort(t *testing.T) {
	resetLogState(t)
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "ripperr.log"), "2026-03-05 10:00:00 INFO: cur")
	writeFixture(t, filepath.Join(dir, "ripperr.log.1"),
		"2026-03-05 09:00:00 INFO: o1",
		"2026-03-05 09:00:01 INFO: o2",
		"2026-03-05 09:00:02 INFO: o3",
	)

	InitFile(dir)

	got := Snapshot(0)
	if len(got) != 4 {
		t.Fatalf("%d entries, want 4", len(got))
	}
	for i, m := range []string{"o1", "o2", "o3", "cur"} {
		if got[i].Message != m {
			t.Fatalf("entry %d = %q, want %q", i, got[i].Message, m)
		}
	}
}

func TestPreloadEmptyDir(t *testing.T) {
	resetLogState(t)
	dir := t.TempDir()

	InitFile(dir)
	if n := len(Snapshot(0)); n != 0 {
		t.Fatalf("%d entries after init of an empty dir", n)
	}
	Info("first")

	if n := len(Snapshot(0)); n != 1 {
		t.Fatalf("%d entries, want only the new one", n)
	}
	if got := readLog(t, filepath.Join(dir, "ripperr.log")); !strings.HasSuffix(got, "INFO: first\n") {
		t.Fatalf("file content %q", got)
	}
}

func TestPreloadReadsOnlyWindow(t *testing.T) {
	resetLogState(t)
	maxFileSize = 500
	dir := t.TempDir()
	lines := make([]string, 0, 100)
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("2026-03-05 10:00:00 INFO: line %03d", i))
	}
	writeFixture(t, filepath.Join(dir, "ripperr.log"), lines...)

	InitFile(dir)

	got := Snapshot(0)
	if len(got) == 0 || len(got) >= 100 {
		t.Fatalf("%d entries, want a partial window", len(got))
	}
	if got[0].Message == "line 001" || got[len(got)-1].Message != "line 100" {
		t.Fatalf("window is %q .. %q", got[0].Message, got[len(got)-1].Message)
	}
	if !strings.HasPrefix(got[0].Message, "line ") {
		t.Fatalf("first entry is a partial line: %q", got[0].Message)
	}
}
