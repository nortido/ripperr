package tracker

import "testing"

func TestDetectMirrorDomain(t *testing.T) {
	tr := Tracker{
		Domain:  "rutracker.org",
		Mirrors: []string{"rutracker.cc", ""},
	}
	if !tr.Detect("https://rutracker.cc/forum/viewtopic.php?t=1") {
		t.Fatal("expected mirror match")
	}
	if !tr.Detect("https://rutracker.org/forum/viewtopic.php?t=1") {
		t.Fatal("expected primary match")
	}
	if tr.Detect("https://example.com/t") {
		t.Fatal("unexpected match")
	}
}

func TestReplaceURLHost(t *testing.T) {
	got := ReplaceURLHost("https://rutracker.org/forum/dl.php?t=", "mirror.example")
	want := "https://mirror.example/forum/dl.php?t="
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
