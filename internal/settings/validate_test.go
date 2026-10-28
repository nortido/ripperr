package settings

import (
	"encoding/json"
	"slices"
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

func TestSchedulePatchMagnetTimeoutBounds(t *testing.T) {
	base := `{"start_hour":7,"list_refresh_per_day":2,"threshold_month":1,"threshold_half_year":1,"threshold_year":1`
	ok := []string{base + `,"magnet_timeout":5}`, base + `,"magnet_timeout":600}`}
	for _, patch := range ok {
		if _, err := SchedulePatchFromJSON(json.RawMessage(patch)); err != nil {
			t.Errorf("%s: unexpected error: %v", patch, err)
		}
	}
	bad := []string{base + `,"magnet_timeout":4}`, base + `,"magnet_timeout":601}`}
	for _, patch := range bad {
		if _, err := SchedulePatchFromJSON(json.RawMessage(patch)); err == nil {
			t.Errorf("%s: expected error", patch)
		}
	}
}

func TestValidateScheduleMagnetTimeoutBounds(t *testing.T) {
	// границы как в ValidateSchedulePatch (5..600). Если считать 0 за "не задано",
	// разойдемся с реальным RUNNER_MAGNET_TIMEOUT=0
	base := Schedule{StartHour: 7, ListRefreshPerDay: 2, ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1}
	for _, v := range []int64{5, 60, 600} {
		s := base
		s.MagnetTimeout = v
		if err := ValidateSchedule(&s); err != nil {
			t.Errorf("magnet_timeout=%d: unexpected error: %v", v, err)
		}
	}
	for _, v := range []int64{0, 4, 601} {
		s := base
		s.MagnetTimeout = v
		if err := ValidateSchedule(&s); err == nil {
			t.Errorf("magnet_timeout=%d: expected error", v)
		}
	}
}

func TestSchedulePatchMagnetTimeoutAbsentAllowed(t *testing.T) {
	o, err := SchedulePatchFromJSON(json.RawMessage(`{
		"start_hour":7,"list_refresh_per_day":2,
		"threshold_month":1,"threshold_half_year":1,"threshold_year":1
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if o.MagnetTimeout != nil {
		t.Fatalf("MagnetTimeout = %v, want nil (key absent means keep current)", o.MagnetTimeout)
	}
}

func TestIntegrationsPatchFromJSONClient(t *testing.T) {
	// UI и MANAGER_CLIENT из env шлют имя клиента в любом регистре и с пробелами
	for _, v := range []string{"qbit", "transmission", "", "Transmission", " transmission ", "QBIT", "deluge", " Deluge ", "downloadstation", " DownloadStation "} {
		if _, err := IntegrationsPatchFromJSON(json.RawMessage(`{"client":"` + v + `"}`)); err != nil {
			t.Errorf("client=%q: unexpected error: %v", v, err)
		}
	}
	if _, err := IntegrationsPatchFromJSON(json.RawMessage(`{"client":"utorrent"}`)); err == nil {
		t.Fatal("expected error for unknown client")
	}
	if _, err := IntegrationsPatchFromJSON(json.RawMessage(`{"qbit":{"host":"x"}}`)); err != nil {
		t.Fatalf("missing client key should not error: %v", err)
	}
}

func TestIntegrationsPatchRejectsUnknownEvent(t *testing.T) {
	for _, body := range []string{
		`{"notifications":{"telegram":{"events":["updated","nope"]}}}`,
		`{"notifications":{"webhook":{"events":["test"]}}}`,
	} {
		if _, err := IntegrationsPatchFromJSON(json.RawMessage(body)); err == nil {
			t.Errorf("expected error for %s", body)
		}
	}
	for _, body := range []string{
		`{"notifications":{"telegram":{"events":[]}}}`,
		`{"notifications":{"webhook":{"events":["check_failed","mirror_switched"]}}}`,
	} {
		if _, err := IntegrationsPatchFromJSON(json.RawMessage(body)); err != nil {
			t.Errorf("%s: unexpected error %v", body, err)
		}
	}
}

func TestIntegrationsPatchValidatesEmail(t *testing.T) {
	for _, body := range []string{
		`{"notifications":{"email":{"port":70000}}}`,
		`{"notifications":{"email":{"port":-1}}}`,
		`{"notifications":{"email":{"security":"ssl"}}}`,
		`{"notifications":{"email":{"from":"not an address"}}}`,
		`{"notifications":{"email":{"to":"a@b.example, nope"}}}`,
		`{"notifications":{"email":{"events":["nope"]}}}`,
		`{"notifications":{"pushover":{"events":["test"]}}}`,
	} {
		if _, err := IntegrationsPatchFromJSON(json.RawMessage(body)); err == nil {
			t.Errorf("expected error for %s", body)
		}
	}
	for _, body := range []string{
		`{"notifications":{"email":{"host":"smtp.example.com","port":0,"security":"","from":"","to":""}}}`,
		`{"notifications":{"email":{"port":65535,"security":"tls","from":"Ripperr <bot@example.com>","to":"a@b.example, c@d.example"}}}`,
		`{"notifications":{"email":{"security":"none","events":[]},"pushover":{"app_token":"t","user_key":"u","events":["check_failed"]}}}`,
	} {
		if _, err := IntegrationsPatchFromJSON(json.RawMessage(body)); err != nil {
			t.Errorf("%s: unexpected error %v", body, err)
		}
	}
	o, err := IntegrationsPatchFromJSON(json.RawMessage(`{"notifications":{"email":{"events":["updated","updated"]},"pushover":{"events":["broken","broken"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := *o.Notifications.Email.Events; len(got) != 1 {
		t.Errorf("email events not deduped: %v", got)
	}
	if got := *o.Notifications.Pushover.Events; len(got) != 1 {
		t.Errorf("pushover events not deduped: %v", got)
	}
}

func TestWebPatchFromJSON(t *testing.T) {
	for _, p := range []string{`{"port":0}`, `{"port":-1}`, `{"port":65536}`, `{"port":"80"}`} {
		if _, err := WebPatchFromJSON(json.RawMessage(p)); err == nil {
			t.Errorf("%s: expected error", p)
		}
	}
	for _, p := range []string{`{"port":1}`, `{"port":65535}`} {
		if _, err := WebPatchFromJSON(json.RawMessage(p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, h := range []string{"https://x", "x:443", "a b", "*.x", "u@x"} {
		raw, _ := json.Marshal(map[string][]string{"allowed_hosts": {"ok.lan", h}})
		_, err := WebPatchFromJSON(raw)
		if err == nil || !strings.Contains(err.Error(), h) {
			t.Errorf("host %q: err = %v, want error naming the entry", h, err)
		}
	}
	o, err := WebPatchFromJSON(json.RawMessage(`{"allowed_hosts":["MY.Keenetic.PRO."," x.lan ","","::1","x.lan","my.keenetic.pro"]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"my.keenetic.pro", "x.lan", "::1"}
	if o.AllowedHosts == nil || !slices.Equal(*o.AllowedHosts, want) {
		t.Errorf("hosts = %v, want %v", o.AllowedHosts, want)
	}
}

func TestIntegrationsPatchValidatesLanguage(t *testing.T) {
	for _, lang := range []string{"de", "RU", "en "} {
		body := `{"notifications":{"language":"` + lang + `"}}`
		_, err := IntegrationsPatchFromJSON(json.RawMessage(body))
		if err == nil || !strings.Contains(err.Error(), "notifications.language must be en or ru") {
			t.Errorf("%s: err = %v, want notifications.language must be en or ru", body, err)
		}
	}
	for _, lang := range []string{"", "en", "ru"} {
		body := `{"notifications":{"language":"` + lang + `"}}`
		if _, err := IntegrationsPatchFromJSON(json.RawMessage(body)); err != nil {
			t.Errorf("%s: unexpected error %v", body, err)
		}
	}
}

func TestIntegrationsPatchRejectsUnknownBrowserEvent(t *testing.T) {
	o, err := IntegrationsPatchFromJSON(json.RawMessage(`{"notifications":{"browser":{"events":["nope"]}}}`))
	if err == nil {
		err = ValidateIntegrationsPatch(o)
	}
	if err == nil || !strings.Contains(err.Error(), "notifications.browser.events") {
		t.Fatalf("err = %v, want notifications.browser.events", err)
	}
}

func TestSchedulePatchPartialBlockStillRequiresFields(t *testing.T) {
	if _, err := SchedulePatchFromJSON(json.RawMessage(`{"mirror_confirm":true}`)); err != nil {
		t.Errorf("patch without schedule block: %v", err)
	}
	if _, err := SchedulePatchFromJSON(json.RawMessage(`{"start_hour":7}`)); err == nil {
		t.Error("partial schedule block must stay an error")
	}
}
