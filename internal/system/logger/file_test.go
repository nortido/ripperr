package logger

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func dropFile() {
	fileMu.Lock()
	defer fileMu.Unlock()
	if logFile != nil {
		_ = logFile.Close()
	}
	logFile = nil
	logSize = 0
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	writeReported = false
	rotateReported = false
}

func resetLogState(t *testing.T) {
	t.Helper()
	oldMax, oldLevel := maxFileSize, minLevel
	reset := func() {
		dropFile()
		globalBuffer = newRingBuffer(defaultBufferCap)
	}
	reset()
	t.Cleanup(func() {
		reset()
		maxFileSize = oldMax
		minLevel = oldLevel
	})
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func countReports(entries []Entry) int {
	n := 0
	for _, e := range entries {
		if e.Level == "WARN" && strings.Contains(e.Message, "logger: file log") {
			n++
		}
	}
	return n
}

func TestFileLineFormat(t *testing.T) {
	resetLogState(t)
	dir := t.TempDir()
	InitFile(dir)
	Info("a")
	Warn("b")
	Error("c")

	re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} INFO: a\n` +
		`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} WARN: b\n` +
		`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} ERROR: c\n$`)
	got := readLog(t, filepath.Join(dir, "ripperr.log"))
	if !re.MatchString(got) {
		t.Fatalf("unexpected file content %q", got)
	}
	if n := len(Snapshot(0)); n != 3 {
		t.Fatalf("snapshot has %d entries, want 3", n)
	}
}

func TestFileDebugFollowsLevel(t *testing.T) {
	resetLogState(t)
	dir := t.TempDir()
	InitFile(dir)

	SetLevel("debug")
	Debug("d")
	SetLevel("info")
	Debug("e")

	got := readLog(t, filepath.Join(dir, "ripperr.log"))
	if !strings.Contains(got, " DEBUG: d\n") {
		t.Fatalf("debug line d missing in %q", got)
	}
	if strings.Contains(got, "DEBUG: e") {
		t.Fatalf("debug line e must be filtered, got %q", got)
	}
	if n := len(Snapshot(0)); n != 0 {
		t.Fatalf("debug must not reach the buffer, got %d entries", n)
	}
}

func lineNumber(t *testing.T, line string) int {
	t.Helper()
	i := strings.LastIndex(line, " n")
	if i < 0 {
		t.Fatalf("no number in %q", line)
	}
	n, err := strconv.Atoi(line[i+2:])
	if err != nil {
		t.Fatalf("bad number in %q: %v", line, err)
	}
	return n
}

func TestFileRotationKeepsThree(t *testing.T) {
	resetLogState(t)
	maxFileSize = 200
	dir := t.TempDir()
	InitFile(dir)
	for i := 0; i < 200; i++ {
		Info(fmt.Sprintf("n%03d", i))
	}

	base := filepath.Join(dir, "ripperr.log")
	for _, suffix := range []string{"", ".1", ".2", ".3"} {
		st, err := os.Stat(base + suffix)
		if err != nil {
			t.Fatalf("stat %s: %v", base+suffix, err)
		}
		if st.Size() > maxFileSize {
			t.Fatalf("%s is %d bytes, limit %d", base+suffix, st.Size(), maxFileSize)
		}
	}
	if _, err := os.Stat(base + ".4"); !os.IsNotExist(err) {
		t.Fatalf(".4 must not exist, err=%v", err)
	}

	cur := strings.Split(strings.TrimSuffix(readLog(t, base), "\n"), "\n")
	prev := strings.Split(strings.TrimSuffix(readLog(t, base+".1"), "\n"), "\n")
	if got, want := lineNumber(t, cur[0]), lineNumber(t, prev[len(prev)-1])+1; got != want {
		t.Fatalf("first line of log is %d, last of .1 + 1 is %d", got, want)
	}
	if last := lineNumber(t, cur[len(cur)-1]); last != 199 {
		t.Fatalf("last numbered line in log is %d, want 199", last)
	}
}

func TestFileAppendsAfterReopen(t *testing.T) {
	resetLogState(t)
	maxFileSize = 200
	dir := t.TempDir()
	InitFile(dir)
	for i := 0; i < 5; i++ {
		Info(fmt.Sprintf("a%d", i))
	}
	dropFile()

	InitFile(dir)
	Info("b0")
	Info("b1")

	base := filepath.Join(dir, "ripperr.log")
	old := readLog(t, base+".1")
	if !strings.Contains(old, "INFO: a0\n") || !strings.Contains(old, "INFO: b0\n") {
		t.Fatalf("rotated file lacks old and appended lines: %q", old)
	}
	cur := readLog(t, base)
	if strings.Count(cur, "\n") != 1 || !strings.HasSuffix(cur, "INFO: b1\n") {
		t.Fatalf("current file must hold only b1, got %q", cur)
	}
}

func TestFileInitFailure(t *testing.T) {
	resetLogState(t)
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	InitFile(notDir)
	Info("1")
	Info("2")
	Info("3")

	entries := Snapshot(0)
	infos := 0
	for _, e := range entries {
		if e.Level == "INFO" {
			infos++
		}
	}
	if infos != 3 {
		t.Fatalf("%d info entries, want 3", infos)
	}
	if n := countReports(entries); n != 1 {
		t.Fatalf("%d reports, want 1 (%v)", n, entries)
	}
}

func TestFileWriteErrorReportedOnce(t *testing.T) {
	resetLogState(t)
	InitFile(t.TempDir())
	fileMu.Lock()
	_ = logFile.Close()
	fileMu.Unlock()

	Info("1")
	Info("2")
	Info("3")

	entries := Snapshot(0)
	if len(entries) != 4 {
		t.Fatalf("%d entries, want 3 info + 1 report: %v", len(entries), entries)
	}
	if n := countReports(entries); n != 1 {
		t.Fatalf("%d reports, want 1", n)
	}
}

func TestFileRotateFailureKeepsWriting(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	resetLogState(t)
	maxFileSize = 200
	dir := t.TempDir()
	InitFile(dir)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	for i := 0; i < 50; i++ {
		Info(fmt.Sprintf("n%03d", i))
	}

	base := filepath.Join(dir, "ripperr.log")
	st, err := os.Stat(base)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() <= maxFileSize {
		t.Fatalf("log stopped growing at %d bytes", st.Size())
	}
	if _, err := os.Stat(base + ".1"); !os.IsNotExist(err) {
		t.Fatalf(".1 must not exist, err=%v", err)
	}
	if n := countReports(Snapshot(0)); n != 1 {
		t.Fatalf("%d reports, want 1", n)
	}
}

func TestFileConcurrent(t *testing.T) {
	resetLogState(t)
	maxFileSize = 1 << 30
	dir := t.TempDir()
	InitFile(dir)

	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				Info(fmt.Sprintf("g%d n%d", g, i))
			}
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(readLog(t, filepath.Join(dir, "ripperr.log")), "\n"), "\n")
	if len(lines) != 1000 {
		t.Fatalf("%d lines, want 1000", len(lines))
	}
	re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} INFO: g\d+ n\d+$`)
	for _, l := range lines {
		if !re.MatchString(l) {
			t.Fatalf("torn line %q", l)
		}
	}
}

func TestFileInitTwice(t *testing.T) {
	resetLogState(t)
	dir := t.TempDir()
	InitFile(dir)
	Info("x")
	InitFile(dir)

	if n := len(Snapshot(0)); n != 1 {
		t.Fatalf("%d entries, want 1", n)
	}
}

func TestFileCrashOutput(t *testing.T) {
	if dir := os.Getenv("LOGGER_CRASH_DIR"); dir != "" {
		maxFileSize = 200
		InitFile(dir)
		for i := 0; i < 10; i++ {
			Info(fmt.Sprintf("n%03d", i))
		}
		panic("boom-marker")
	}

	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestFileCrashOutput$")
	cmd.Env = append(os.Environ(), "LOGGER_CRASH_DIR="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("child must crash")
	}

	base := filepath.Join(dir, "ripperr.log")
	if got := readLog(t, base); !strings.Contains(got, "panic: boom-marker") {
		t.Fatalf("crash trace missing in current log: %q", got)
	}
	if got := readLog(t, base+".1"); strings.Contains(got, "boom-marker") {
		t.Fatalf("crash trace went to the rotated file: %q", got)
	}
}
