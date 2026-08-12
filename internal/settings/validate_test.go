package settings

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateTrackerPatchDomain(t *testing.T) {
	ok := []string{
		"rutracker.org", "kinozal.tv", "kinozal.guru", "nnmclub.to",
		"rutor.is", "teamhd.org", "example.com:443",
	}
	for _, d := range ok {
		if err := ValidateTrackerPatch(trackerItemOverlay{Domain: &d}); err != nil {
			t.Errorf("domain %q: %v", d, err)
		}
	}
	bad := []struct {
		patch string
		sub   string
	}{
		{`{"domain":""}`, "empty"},
		{`{"domain":"https://rutracker.org"}`, "scheme"},
		{`{"domain":"rutracker.org/path"}`, "slash"},
		{`{"domain":"rutracker org"}`, "space"},
		{`{"domain":"rutracker_org"}`, "underscore"},
	}
	for _, c := range bad {
		_, err := TrackerPatchFromJSON(json.RawMessage(c.patch))
		if err == nil {
			t.Errorf("%s: expected error for %s", c.patch, c.sub)
		}
	}
}

func TestValidateTrackerPatchURLs(t *testing.T) {
	ok := []string{
		"https://rutracker.org/forum/login.php",
		"https://kinozal.tv/takelogin.php",
		"https://d.rutor.info/download/",
		"http://192.168.1.200:8068/api/v2/auth/login",
	}
	for _, u := range ok {
		login := u
		dl := u
		if err := ValidateTrackerPatch(trackerItemOverlay{LoginURL: &login, DownloadURL: &dl}); err != nil {
			t.Errorf("urls %q: %v", u, err)
		}
	}
	bad := []struct {
		patch string
		sub   string
	}{
		{`{"login_url":"forum/login.php"}`, "no scheme"},
		{`{"login_url":"javascript:alert(1)"}`, "javascript"},
		{`{"download_url":"ftp://host/path"}`, "ftp"},
		{`{"login_url":"https://host/path with space"}`, "space"},
	}
	for _, c := range bad {
		_, err := TrackerPatchFromJSON(json.RawMessage(c.patch))
		if err == nil {
			t.Errorf("%s: expected error for %s", c.patch, c.sub)
		}
	}
}

func TestValidateTrackerPatchEmptyURLsAllowed(t *testing.T) {
	empty := ""
	if err := ValidateTrackerPatch(trackerItemOverlay{LoginURL: &empty, DownloadURL: &empty}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTrackerPatchMirrorsAndActiveDomain(t *testing.T) {
	mirrors := []string{"kinozal.guru", "kinozal.me", "rutracker.net"}
	active := "kinozal.guru"
	if err := ValidateTrackerPatch(trackerItemOverlay{Mirrors: &mirrors, ActiveDomain: &active}); err != nil {
		t.Fatal(err)
	}
	_, err := TrackerPatchFromJSON(json.RawMessage(`{"mirrors":["bad domain"]}`))
	if err == nil {
		t.Fatal("expected mirror validation error")
	}
	_, err = TrackerPatchFromJSON(json.RawMessage(`{"active_domain":"http://evil"}`))
	if err == nil {
		t.Fatal("expected active_domain validation error")
	}
}

func TestValidateTrackerPatchRealDBValues(t *testing.T) {
	const raw = `{"nnm": {"domain": "nnmclub.to", "login_url": "https://nnmclub.to/forum/login.php", "download_url": "https://nnmclub.to/forum/download.php?id="}, "rutor": {"domain": "rutor.is", "login_url": "", "download_url": "https://d.rutor.info/download/"}, "teamhd": {"domain": "teamhd.org", "login_url": "", "download_url": "https://teamhd.org/download.php?id="}, "kinozal": {"domain": "kinozal.tv", "mirrors": ["kinozal.guru", "kinozal.me"], "login_url": "https://kinozal.tv/takelogin.php", "download_url": "https://kinozal.tv/download.php?id=", "active_domain": "kinozal.guru"}, "rutracker": {"domain": "rutracker.org", "mirrors": ["rutracker.net"], "login_url": "https://rutracker.org/forum/login.php", "download_url": "https://rutracker.org/forum/dl.php?t=", "active_domain": ""}}`
	var trackers map[string]trackerItemOverlay
	if err := json.Unmarshal([]byte(raw), &trackers); err != nil {
		t.Fatal(err)
	}
	for key, o := range trackers {
		if err := ValidateTrackerPatch(o); err != nil {
			t.Errorf("tracker %q: %v", key, err)
		}
		if o.Mirrors != nil {
			if _, err := normalizeMirrorList(*o.Mirrors); err != nil {
				t.Errorf("tracker %q mirrors: %v", key, err)
			}
		}
	}
}

func TestValidateTrackerHostnamePort(t *testing.T) {
	if err := validateTrackerHostname("tracker.local:8080", "domain"); err != nil {
		t.Fatal(err)
	}
	if err := validateTrackerHostname("tracker.local:abc", "domain"); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestValidateTrackerPatchErrorMessages(t *testing.T) {
	_, err := TrackerPatchFromJSON(json.RawMessage(`{"domain":"http://x"}`))
	if err == nil {
		t.Fatal("expected domain error")
	}
	_, err = TrackerPatchFromJSON(json.RawMessage(`{"login_url":"javascript:x"}`))
	if err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("want scheme error, got %v", err)
	}
}
