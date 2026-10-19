package tracker

import (
	"slices"
	"testing"
)

func TestDetectMatchesRetiredHost(t *testing.T) {
	tr := Tracker{Domain: "kinozal.me", Mirrors: []string{"kinozal.guru"}, Retired: []string{"kinozal.tv"}}
	if !tr.Detect("https://kinozal.tv/details.php?id=1") {
		t.Fatal("retired host not detected")
	}
	if slices.Contains(tr.GetMirrors(), "kinozal.tv") {
		t.Fatalf("GetMirrors = %v, must not contain the retired host", tr.GetMirrors())
	}
	if tr.Detect("https://example.com/details.php?id=1") {
		t.Fatal("unexpected match")
	}
}
