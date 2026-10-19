package mirrorsync

import (
	"reflect"
	"sort"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/settings"
	"ripperr/internal/tracker/registry"
	_ "ripperr/internal/tracker/registry/all"
)

const kinozalYAML = `id: kinozal
links:
  - https://kinozal.me/
  - https://kinozal.guru/
legacylinks:
  - https://kinozal-tv.appspot.com/
  - http://kinozal.tv/
  - https://kinozal-guru.appspot.com/
  - https://kinozal.tv/
`

const rutorYAML = `id: rutor
links:
  - https://rutor.info/
  - https://rutor.is/
  - http://6tor.org/ # IPv6-only
legacylinks:
  - http://rutor-unblock.example/
  - http://rutor.info/
  - http://rutor.is/
`

type probe struct {
	alive bool
	loc   string
}

type fixture struct {
	items   map[string]*config.TrackerItem
	sources map[string]*source
	probes  map[string]probe
	prev    map[string]map[string]int
	pruned  map[string][]string
	dismiss map[string]bool
	confirm bool
}

func (f fixture) run() result {
	keys := make([]string, 0, len(f.items))
	for k := range f.items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return suggest(input{
		keys:    keys,
		items:   f.items,
		sources: f.sources,
		check: func(host string, _ int) (bool, string) {
			p := f.probes[host]
			return p.alive, p.loc
		},
		prevFails: f.prev,
		confirmOn: f.confirm,
		dismissed: func(key, kind, host string) bool {
			return f.dismiss[key+"|"+kind+"|"+settings.MirrorHostKey(host)]
		},
		pruned: f.pruned,
	})
}

func mustParse(t *testing.T, yaml string) *source {
	t.Helper()
	src, err := parseYAML([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return &src
}

func alive(hosts ...string) map[string]probe {
	m := make(map[string]probe, len(hosts))
	for _, h := range hosts {
		m[h] = probe{alive: true}
	}
	return m
}

func single(domain string, mirrors ...string) map[string]*config.TrackerItem {
	return map[string]*config.TrackerItem{"site": {Domain: domain, Mirrors: mirrors}}
}

func sug(kind, host, from, reason string) settings.MirrorSuggestion {
	return settings.MirrorSuggestion{Kind: kind, Host: host, From: from, Reason: reason}
}

func checkSug(t *testing.T, got result, key string, want ...settings.MirrorSuggestion) {
	t.Helper()
	if !reflect.DeepEqual(got.suggestions[key], want) {
		t.Errorf("suggestions[%s] = %+v, want %+v", key, got.suggestions[key], want)
	}
}

func checkAdds(t *testing.T, got result, key string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got.adds[key], want) {
		t.Errorf("adds[%s] = %v, want %v", key, got.adds[key], want)
	}
}

func checkRetires(t *testing.T, got result, key string, want ...retire) {
	t.Helper()
	if !reflect.DeepEqual(got.retires[key], want) {
		t.Errorf("retires[%s] = %v, want %v", key, got.retires[key], want)
	}
}

func TestSuggestKinozalLegacyDomain(t *testing.T) {
	f := fixture{
		items:   map[string]*config.TrackerItem{"kinozal": {Domain: "kinozal.tv"}},
		sources: map[string]*source{"kinozal": mustParse(t, kinozalYAML)},
		probes:  alive("kinozal.tv", "kinozal.me", "kinozal.guru"),
	}
	got := f.run()
	checkSug(t, got, "kinozal",
		sug(settings.MirrorKindMain, "kinozal.me", "kinozal.tv", settings.MirrorReasonJackett),
		sug(settings.MirrorKindRemove, "kinozal.tv", "", settings.MirrorReasonJackett))
	checkAdds(t, got, "kinozal", "kinozal.me", "kinozal.guru")
	checkRetires(t, got, "kinozal")
	if got.outage {
		t.Error("outage = true")
	}
}

func TestSuggestRutorHttpVariantsAreNotLegacy(t *testing.T) {
	f := fixture{
		items:   map[string]*config.TrackerItem{"rutor": {Domain: "rutor.info", Mirrors: []string{"rutor.is"}}},
		sources: map[string]*source{"rutor": mustParse(t, rutorYAML)},
		probes:  alive("rutor.info", "rutor.is"),
	}
	got := f.run()
	if len(got.adds) != 0 || len(got.retires) != 0 || len(got.suggestions) != 0 {
		t.Errorf("want nothing, got adds=%v retires=%v suggestions=%v", got.adds, got.retires, got.suggestions)
	}
}

func TestSuggestAddsFilters(t *testing.T) {
	items := map[string]*config.TrackerItem{
		"site":  {Domain: "site.example", Mirrors: []string{"a.example", "b.example"}, Retired: []string{"gone.example"}},
		"other": {Domain: "other.example"},
	}
	src := &source{Links: []string{
		"site.example", "dead.example", "site-unblock.example", "proxy.site.example", "10.1.2.3", "x.appspot.com",
		"www.a.example", "gone.example", "my.other.example", "banned.example", "new.example",
	}}
	f := fixture{
		items:   items,
		sources: map[string]*source{"site": src},
		probes: alive("site.example", "a.example", "b.example", "site-unblock.example", "proxy.site.example", "10.1.2.3",
			"x.appspot.com", "www.a.example", "gone.example", "my.other.example", "banned.example", "new.example"),
		pruned: map[string][]string{"site": {"BANNED.example"}},
	}
	got := f.run()
	checkAdds(t, got, "site", "new.example")
	if _, ok := got.adds["other"]; ok {
		t.Errorf("adds[other] = %v, want none", got.adds["other"])
	}
}

func TestSuggestAddsCapThreeInOrder(t *testing.T) {
	src := &source{Links: []string{"l1.example", "l2.example", "dead.example", "l3.example", "l4.example", "l5.example"}}
	f := fixture{
		items:   single("site.example"),
		sources: map[string]*source{"site": src},
		probes:  alive("site.example", "l1.example", "l2.example", "l3.example", "l4.example", "l5.example"),
	}
	checkAdds(t, f.run(), "site", "l1.example", "l2.example", "l3.example")
}

func TestSuggestOwnLegacyMirror(t *testing.T) {
	src := &source{Links: []string{"site.example"}, Legacy: []string{"old.example"}}
	for _, on := range []bool{true, false} {
		f := fixture{
			items:   single("site.example", "old.example"),
			sources: map[string]*source{"site": src},
			probes:  alive("site.example", "old.example"),
			confirm: on,
		}
		got := f.run()
		if on {
			checkSug(t, got, "site", sug(settings.MirrorKindRemove, "old.example", "", settings.MirrorReasonJackett))
			checkRetires(t, got, "site")
		} else {
			checkSug(t, got, "site")
			checkRetires(t, got, "site", retire{"old.example", settings.MirrorReasonJackett})
		}
	}
}

func TestSuggestDeadMirrorCounting(t *testing.T) {
	cases := []struct {
		prev      int
		confirm   bool
		wantFails int
		wantSug   []settings.MirrorSuggestion
		wantRet   []retire
	}{
		{2, true, 3, []settings.MirrorSuggestion{sug(settings.MirrorKindRemove, "m.example", "", settings.MirrorReasonDead)}, nil},
		{2, false, 3, nil, []retire{{"m.example", settings.MirrorReasonDead}}},
		{1, true, 2, nil, nil},
		{1, false, 2, nil, nil},
	}
	for _, c := range cases {
		f := fixture{
			items:   single("site.example", "m.example"),
			probes:  alive("site.example"),
			prev:    map[string]map[string]int{"site": {"m.example": c.prev}},
			confirm: c.confirm,
		}
		got := f.run()
		if n := got.fails["site"]["m.example"]; n != c.wantFails {
			t.Errorf("prev %d on=%v: fails = %d, want %d", c.prev, c.confirm, n, c.wantFails)
		}
		if _, ok := got.fails["site"]["site.example"]; ok {
			t.Errorf("prev %d: alive domain must be dropped from fails", c.prev)
		}
		checkSug(t, got, "site", c.wantSug...)
		checkRetires(t, got, "site", c.wantRet...)
	}
}

func TestSuggestRetireGuards(t *testing.T) {
	deadPrev := map[string]map[string]int{"site": {"m.example": 2}}
	t.Run("domain is never retired", func(t *testing.T) {
		f := fixture{
			items:   single("old.example", "m.example"),
			sources: map[string]*source{"site": {Links: []string{"new.example"}, Legacy: []string{"old.example"}}},
			probes:  alive("old.example", "m.example", "new.example"),
		}
		got := f.run()
		checkRetires(t, got, "site")
		checkSug(t, got, "site",
			sug(settings.MirrorKindMain, "new.example", "old.example", settings.MirrorReasonJackett),
			sug(settings.MirrorKindRemove, "old.example", "", settings.MirrorReasonJackett))
	})
	t.Run("dismissed id", func(t *testing.T) {
		f := fixture{
			items:   single("site.example", "m.example"),
			probes:  alive("site.example"),
			prev:    deadPrev,
			dismiss: map[string]bool{"site|remove|m.example": true},
		}
		got := f.run()
		checkRetires(t, got, "site")
		checkSug(t, got, "site")
		f.confirm = true
		checkSug(t, f.run(), "site")
	})
	t.Run("no other host answered", func(t *testing.T) {
		items := single("site.example", "m.example")
		items["live"] = &config.TrackerItem{Domain: "live.example"}
		f := fixture{items: items, probes: alive("live.example"), prev: deadPrev}
		got := f.run()
		if got.outage {
			t.Fatal("outage = true, another tracker answered")
		}
		checkRetires(t, got, "site")
		f.confirm = true
		checkSug(t, f.run(), "site", sug(settings.MirrorKindRemove, "m.example", "", settings.MirrorReasonDead))
	})
	t.Run("answered host is a mirror removed too", func(t *testing.T) {
		src := &source{Links: []string{"site.example"}, Legacy: []string{"old.example", "m.example"}}
		f := fixture{
			items:   single("site.example", "old.example", "m.example"),
			sources: map[string]*source{"site": src},
			probes:  alive("old.example", "m.example"),
		}
		checkRetires(t, f.run(), "site", retire{"m.example", settings.MirrorReasonJackett})
	})
}

func TestSuggestDeadMainPicksReplacement(t *testing.T) {
	prev := map[string]map[string]int{"site": {"dead.example": 2}}
	wantRemove := sug(settings.MirrorKindRemove, "dead.example", "", settings.MirrorReasonDead)
	t.Run("first alive usable link", func(t *testing.T) {
		src := &source{Links: []string{"l1.example", "l2-unblock.example", "l3.example", "l4.example", "dead.example"}}
		f := fixture{
			items:   single("dead.example", "own.example"),
			sources: map[string]*source{"site": src},
			probes:  alive("l2-unblock.example", "l3.example", "l4.example", "own.example"),
			prev:    prev,
		}
		got := f.run()
		checkSug(t, got, "site", sug(settings.MirrorKindMain, "l3.example", "dead.example", settings.MirrorReasonDead), wantRemove)
		if n := got.fails["site"]["dead.example"]; n != 3 {
			t.Errorf("fails = %d, want 3", n)
		}
	})
	t.Run("confirmed own mirror", func(t *testing.T) {
		items := single("dead.example", "new.example", "own.example")
		items["site"].Unconfirmed = []string{"new.example"}
		f := fixture{
			items:   items,
			sources: map[string]*source{"site": {Links: []string{"l1.example"}}},
			probes:  alive("new.example", "own.example"),
			prev:    prev,
		}
		checkSug(t, f.run(), "site", sug(settings.MirrorKindMain, "own.example", "dead.example", settings.MirrorReasonDead), wantRemove)
	})
	t.Run("two runs is not enough", func(t *testing.T) {
		f := fixture{
			items:  single("dead.example", "own.example"),
			probes: alive("own.example"),
			prev:   map[string]map[string]int{"site": {"dead.example": 1}},
		}
		checkSug(t, f.run(), "site")
	})
	t.Run("dismissed main keeps remove", func(t *testing.T) {
		f := fixture{
			items:   single("dead.example", "own.example"),
			probes:  alive("own.example"),
			prev:    prev,
			dismiss: map[string]bool{"site|main|own.example": true},
		}
		checkSug(t, f.run(), "site", wantRemove)
	})
}

func TestSuggestRedirect(t *testing.T) {
	kz := func(links ...string) map[string]*source {
		return map[string]*source{"kinozal": {Links: links}}
	}
	items := map[string]*config.TrackerItem{"kinozal": {Domain: "kinozal.tv"}}
	t.Run("same name label not in links", func(t *testing.T) {
		f := fixture{
			items:   items,
			sources: kz("kinozal.guru"),
			probes:  map[string]probe{"kinozal.tv": {loc: "kinozal.me"}, "kinozal.me": {alive: true}},
		}
		checkSug(t, f.run(), "kinozal", sug(settings.MirrorKindMain, "kinozal.me", "kinozal.tv", settings.MirrorReasonRedirectUnlisted))
	})
	t.Run("unrelated host not in links", func(t *testing.T) {
		f := fixture{
			items:   items,
			sources: kz("kinozal.guru"),
			probes:  map[string]probe{"kinozal.tv": {loc: "parking.example"}, "parking.example": {alive: true}},
		}
		checkSug(t, f.run(), "kinozal")
	})
	t.Run("host in links", func(t *testing.T) {
		f := fixture{
			items:   items,
			sources: kz("kinozal.guru", "alt.example"),
			probes:  map[string]probe{"kinozal.tv": {loc: "ALT.example"}, "alt.example": {alive: true}},
		}
		checkSug(t, f.run(), "kinozal", sug(settings.MirrorKindMain, "alt.example", "kinozal.tv", settings.MirrorReasonRedirect))
	})
	t.Run("redirect target dead", func(t *testing.T) {
		f := fixture{
			items:   items,
			sources: kz("alt.example"),
			probes:  map[string]probe{"kinozal.tv": {loc: "alt.example"}},
		}
		checkSug(t, f.run(), "kinozal")
	})
	t.Run("redirect to itself with www", func(t *testing.T) {
		f := fixture{
			items:   items,
			sources: kz("alt.example"),
			probes:  map[string]probe{"kinozal.tv": {alive: true, loc: "www.kinozal.tv"}},
		}
		checkSug(t, f.run(), "kinozal")
	})
	t.Run("redirect wins over legacy", func(t *testing.T) {
		f := fixture{
			items:   items,
			sources: map[string]*source{"kinozal": {Links: []string{"kinozal.guru"}, Legacy: []string{"kinozal.tv"}}},
			probes:  map[string]probe{"kinozal.tv": {loc: "kinozal.me"}, "kinozal.me": {alive: true}, "kinozal.guru": {alive: true}},
		}
		checkSug(t, f.run(), "kinozal", sug(settings.MirrorKindMain, "kinozal.me", "kinozal.tv", settings.MirrorReasonRedirectUnlisted))
	})
}

func TestSuggestOutage(t *testing.T) {
	src := &source{Links: []string{"new.example"}, Legacy: []string{"old.example"}}
	prev := map[string]map[string]int{"site": {"site.example": 2, "old.example": 2, "m.example": 2}}
	for _, on := range []bool{true, false} {
		f := fixture{
			items:   single("site.example", "old.example", "m.example"),
			sources: map[string]*source{"site": src},
			prev:    prev,
			confirm: on,
		}
		got := f.run()
		if !got.outage {
			t.Fatal("outage = false")
		}
		if !reflect.DeepEqual(got.fails, prev) {
			t.Errorf("fails = %v, want %v", got.fails, prev)
		}
		checkRetires(t, got, "site")
		checkAdds(t, got, "site")
		if on {
			checkSug(t, got, "site", sug(settings.MirrorKindRemove, "old.example", "", settings.MirrorReasonJackett))
		} else {
			checkSug(t, got, "site")
		}
	}
}

func TestSuggestNilSource(t *testing.T) {
	f := fixture{
		items:   single("site.example", "m.example"),
		sources: map[string]*source{"site": nil},
		probes:  alive("site.example"),
		prev:    map[string]map[string]int{"site": {"m.example": 2}},
		confirm: true,
	}
	got := f.run()
	checkSug(t, got, "site", sug(settings.MirrorKindRemove, "m.example", "", settings.MirrorReasonDead))
	checkAdds(t, got, "site")
	f.sources = nil
	f.prev = nil
	f.probes = map[string]probe{"site.example": {loc: "site.org"}, "site.org": {alive: true}}
	checkSug(t, f.run(), "site", sug(settings.MirrorKindMain, "site.org", "site.example", settings.MirrorReasonRedirectUnlisted))
}

func TestSuggestOrderAndDuplicates(t *testing.T) {
	src := &source{Links: []string{"good.example"}, Legacy: []string{"old.example", "b.example", "a.example"}}
	f := fixture{
		items:   single("old.example", "B.example", "www.b.example", "a.example", "OLD.example"),
		sources: map[string]*source{"site": src},
		probes:  alive("old.example", "b.example", "a.example", "good.example"),
		confirm: true,
	}
	got := f.run()
	checkSug(t, got, "site",
		sug(settings.MirrorKindMain, "good.example", "old.example", settings.MirrorReasonJackett),
		sug(settings.MirrorKindRemove, "a.example", "", settings.MirrorReasonJackett),
		sug(settings.MirrorKindRemove, "B.example", "", settings.MirrorReasonJackett),
		sug(settings.MirrorKindRemove, "old.example", "", settings.MirrorReasonJackett))
	checkAdds(t, got, "site", "good.example")
}

func TestSuggestSkipsMissingItem(t *testing.T) {
	got := suggest(input{
		keys:      []string{"ghost"},
		items:     map[string]*config.TrackerItem{},
		check:     func(string, int) (bool, string) { return true, "" },
		dismissed: func(string, string, string) bool { return false },
	})
	if got.outage || len(got.adds) != 0 || len(got.suggestions) != 0 || len(got.fails) != 0 {
		t.Errorf("want empty result, got %+v", got)
	}
}

func TestUnreferenced(t *testing.T) {
	retired := map[string][]string{"kinozal": {"kinozal.tv"}}
	cases := []struct {
		name    string
		retired map[string][]string
		urls    []string
		want    map[string][]string
	}{
		{"kept", retired, []string{"https://kinozal.tv/details.php?id=1"}, nil},
		{"pruned", retired, []string{"https://kinozal.me/details.php?id=1"}, retired},
		{"uppercase URL", retired, []string{"HTTPS://KINOZAL.TV/details.php?id=1"}, nil},
		{"uppercase host", map[string][]string{"kinozal": {"Kinozal.TV"}}, []string{"https://kinozal.tv/x"}, nil},
		{"empty retired", nil, []string{"https://kinozal.tv/x"}, nil},
		{"partial", map[string][]string{"kinozal": {"kinozal.tv", "kinozal.guru"}, "rutor": {"6tor.org"}},
			[]string{"https://kinozal.guru/a", "https://rutor.info/b"},
			map[string][]string{"kinozal": {"kinozal.tv"}, "rutor": {"6tor.org"}}},
	}
	for _, c := range cases {
		got := unreferenced(c.retired, c.urls)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSourcesCoverRegistry(t *testing.T) {
	for _, d := range registry.All() {
		if jackettSources[d.Key] == "" {
			t.Errorf("registry key %q has no jackettSources entry", d.Key)
		}
	}
	for key := range jackettSources {
		if _, ok := registry.ByKey(key); !ok {
			t.Errorf("jackettSources key %q is not in the registry", key)
		}
	}
}

func TestSuggestDeadMainSkipsMirrorsRemovedInSameRun(t *testing.T) {
	src := &source{Links: []string{"l1.example"}, Legacy: []string{"old.example"}}
	prev := map[string]map[string]int{"site": {"dead.example": 2}}
	mainOK := sug(settings.MirrorKindMain, "ok.example", "dead.example", settings.MirrorReasonDead)
	removeDomain := sug(settings.MirrorKindRemove, "dead.example", "", settings.MirrorReasonDead)
	removeOld := sug(settings.MirrorKindRemove, "old.example", "", settings.MirrorReasonJackett)
	for _, on := range []bool{true, false} {
		f := fixture{
			items:   single("dead.example", "old.example", "ok.example"),
			sources: map[string]*source{"site": src},
			probes:  alive("old.example", "ok.example"),
			prev:    prev,
			confirm: on,
		}
		got := f.run()
		if on {
			checkSug(t, got, "site", mainOK, removeDomain, removeOld)
			checkRetires(t, got, "site")
		} else {
			checkSug(t, got, "site", mainOK, removeDomain)
			checkRetires(t, got, "site", retire{"old.example", settings.MirrorReasonJackett})
		}
	}

	f := fixture{
		items:   single("dead.example", "old.example"),
		sources: map[string]*source{"site": src},
		probes:  alive("old.example"),
		prev:    prev,
		confirm: true,
	}
	checkSug(t, f.run(), "site", removeOld)

	items := single("dead.example", "ret.example")
	items["site"].Retired = []string{"ret.example"}
	f = fixture{items: items, probes: alive("ret.example"), prev: prev}
	checkSug(t, f.run(), "site")
}

func TestSuggestKeepsActiveDomain(t *testing.T) {
	src := &source{Links: []string{"site.example"}, Legacy: []string{"old.example"}}
	for _, on := range []bool{true, false} {
		items := single("site.example", "old.example", "dead.example")
		items["site"].ActiveDomain = "WWW.old.example"
		f := fixture{
			items:   items,
			sources: map[string]*source{"site": src},
			probes:  alive("site.example", "old.example"),
			prev:    map[string]map[string]int{"site": {"dead.example": 2}},
			confirm: on,
		}
		got := f.run()
		if on {
			checkSug(t, got, "site", sug(settings.MirrorKindRemove, "dead.example", "", settings.MirrorReasonDead))
			checkRetires(t, got, "site")
		} else {
			checkSug(t, got, "site")
			checkRetires(t, got, "site", retire{"dead.example", settings.MirrorReasonDead})
		}
	}
}

func TestSuggestIgnoresLocalHosts(t *testing.T) {
	items := map[string]*config.TrackerItem{"kinozal": {Domain: "kinozal.tv"}}
	for _, tc := range []struct {
		host string
		ok   bool
	}{
		{"nas.lan", false},
		{"router.local", false},
		{"x.internal", false},
		{"a.home.arpa", false},
		{"1.168.192.in-addr.arpa", false},
		{"box.localhost", false},
		{"Box.LocalDomain", false},
		{"kinozal.me", true},
	} {
		t.Run(tc.host, func(t *testing.T) {
			host := settings.MirrorHostKey(tc.host)
			adds := fixture{
				items:   items,
				sources: map[string]*source{"kinozal": {Links: []string{host}}},
				probes:  alive("kinozal.tv", host),
			}.run()
			redirect := fixture{
				items:   items,
				sources: map[string]*source{"kinozal": {Links: []string{host}}},
				probes:  map[string]probe{"kinozal.tv": {loc: tc.host}, host: {alive: true}},
			}.run()
			if tc.ok {
				checkAdds(t, adds, "kinozal", host)
				checkSug(t, redirect, "kinozal", sug(settings.MirrorKindMain, host, "kinozal.tv", settings.MirrorReasonRedirect))
			} else {
				checkAdds(t, adds, "kinozal")
				checkSug(t, redirect, "kinozal")
			}
		})
	}
}
