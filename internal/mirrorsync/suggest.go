package mirrorsync

import (
	"slices"
	"strings"

	"ripperr/internal/config"
	"ripperr/internal/settings"
)

const (
	deadRuns = 3
	maxAdds  = 3
)

type input struct {
	keys      []string
	items     map[string]*config.TrackerItem
	sources   map[string]*source
	check     func(host string, proxyType int) (bool, string)
	prevFails map[string]map[string]int
	confirmOn bool
	dismissed func(key, kind, host string) bool
	pruned    map[string][]string
}

type retire struct {
	Host   string
	Reason string
}

type result struct {
	adds        map[string][]string
	retires     map[string][]retire
	suggestions map[string][]settings.MirrorSuggestion
	fails       map[string]map[string]int
	outage      bool
}

type trackerRun struct {
	key     string
	item    *config.TrackerItem
	src     *source
	hosts   []string
	up      map[string]bool
	fails   map[string]int
	outage  bool
	legacy  map[string]bool
	inLinks map[string]bool
}

func suggest(in input) result {
	res := result{
		adds:        map[string][]string{},
		retires:     map[string][]retire{},
		suggestions: map[string][]settings.MirrorSuggestion{},
		fails:       map[string]map[string]int{},
	}
	type probeKey struct {
		host      string
		proxyType int
	}
	type probeVal struct {
		alive bool
		loc   string
	}
	memo := map[probeKey]probeVal{}
	probe := func(host string, proxyType int) (bool, string) {
		k := probeKey{settings.MirrorHostKey(host), proxyType}
		if v, ok := memo[k]; ok {
			return v.alive, v.loc
		}
		a, loc := in.check(host, proxyType)
		memo[k] = probeVal{a, loc}
		return a, loc
	}
	dismissed := func(key, kind, host string) bool {
		return in.dismissed != nil && in.dismissed(key, kind, host)
	}

	var runs []*trackerRun
	probed, answered := 0, 0
	for _, key := range in.keys {
		item := in.items[key]
		if item == nil {
			continue
		}
		t := &trackerRun{key: key, item: item, src: in.sources[key], up: map[string]bool{}, legacy: map[string]bool{}, inLinks: map[string]bool{}}
		for _, h := range append([]string{item.Domain}, item.Mirrors...) {
			hk := settings.MirrorHostKey(h)
			if hk == "" || slices.ContainsFunc(t.hosts, func(e string) bool { return settings.MirrorHostKey(e) == hk }) {
				continue
			}
			t.hosts = append(t.hosts, strings.TrimSpace(h))
			a, _ := probe(h, item.ProxyType)
			t.up[hk] = a
			probed++
			if a {
				answered++
			}
		}
		if t.src != nil {
			for _, l := range t.src.Links {
				t.inLinks[settings.MirrorHostKey(l)] = true
			}
			for _, l := range t.src.Legacy {
				if lk := settings.MirrorHostKey(l); !t.inLinks[lk] {
					t.legacy[lk] = true
				}
			}
		}
		runs = append(runs, t)
	}
	// ни один хост не ответил: скорее лежит наша сеть, а не зеркала
	res.outage = probed > 0 && answered == 0

	for _, t := range runs {
		t.outage = res.outage
		prev := in.prevFails[t.key]
		t.fails = map[string]int{}
		for _, h := range t.hosts {
			hk := settings.MirrorHostKey(h)
			switch {
			case res.outage:
				if n, ok := prev[hk]; ok {
					t.fails[hk] = n
				}
			case !t.up[hk]:
				t.fails[hk] = prev[hk] + 1
			}
		}
		if len(t.fails) > 0 {
			res.fails[t.key] = t.fails
		}

		usable := func(h string) bool { return usableHost(in.items, t.key, h) }
		var mains, removes []settings.MirrorSuggestion
		for _, s := range mainSuggestions(t, probe, usable) {
			if dismissed(t.key, s.Kind, s.Host) {
				continue
			}
			if s.Kind == settings.MirrorKindMain {
				mains = append(mains, s)
			} else {
				removes = append(removes, s)
			}
		}

		var retireCand []retire
		for _, h := range t.hosts {
			hk := settings.MirrorHostKey(h)
			// active_domain выбран пользователем явно, его не убираем ни сами, ни советом
			if hk == settings.MirrorHostKey(t.item.Domain) || hk == settings.MirrorHostKey(t.item.ActiveDomain) {
				continue
			}
			reason := ""
			switch {
			case t.legacy[hk]:
				reason = settings.MirrorReasonJackett
			case !t.outage && t.fails[hk] >= deadRuns:
				reason = settings.MirrorReasonDead
			default:
				continue
			}
			if dismissed(t.key, settings.MirrorKindRemove, h) {
				continue
			}
			if in.confirmOn {
				removes = append(removes, settings.MirrorSuggestion{Kind: settings.MirrorKindRemove, Host: h, Reason: reason})
			} else {
				retireCand = append(retireCand, retire{Host: h, Reason: reason})
			}
		}
		if r := pickRetires(t, retireCand); len(r) > 0 {
			res.retires[t.key] = r
		}

		sortByHost(removes)
		list := dedupeSuggestions(append(mains, removes...))
		if len(list) > 0 {
			res.suggestions[t.key] = list
		}
		if adds := pickAdds(t, in, probe, usable); len(adds) > 0 {
			res.adds[t.key] = adds
		}
	}
	return res
}

func mainSuggestions(t *trackerRun, probe func(string, int) (bool, string), usable func(string) bool) []settings.MirrorSuggestion {
	domain := t.item.Domain
	dk := settings.MirrorHostKey(domain)
	if dk == "" {
		return nil
	}
	pt := t.item.ProxyType
	mainOf := func(host, reason string) settings.MirrorSuggestion {
		return settings.MirrorSuggestion{Kind: settings.MirrorKindMain, Host: host, From: domain, Reason: reason}
	}
	removeDomain := func(reason string) settings.MirrorSuggestion {
		return settings.MirrorSuggestion{Kind: settings.MirrorKindRemove, Host: domain, Reason: reason}
	}
	firstLink := func() string {
		if t.src == nil {
			return ""
		}
		for _, l := range t.src.Links {
			if lk := settings.MirrorHostKey(l); lk != dk && usable(l) {
				if a, _ := probe(l, pt); a {
					return l
				}
			}
		}
		return ""
	}

	// первое сработавшее правило выигрывает, правило без кандидата уступает следующему
	_, loc := probe(domain, pt)
	if lk := settings.MirrorHostKey(loc); lk != "" && lk != dk && usable(lk) {
		listed := t.inLinks[lk]
		if listed || (nameLabel(lk) != "" && nameLabel(lk) == nameLabel(dk)) {
			if a, _ := probe(lk, pt); a {
				reason := settings.MirrorReasonRedirectUnlisted
				if listed {
					reason = settings.MirrorReasonRedirect
				}
				return []settings.MirrorSuggestion{mainOf(lk, reason)}
			}
		}
	}
	if t.legacy[dk] {
		if l := firstLink(); l != "" {
			return []settings.MirrorSuggestion{mainOf(l, settings.MirrorReasonJackett), removeDomain(settings.MirrorReasonJackett)}
		}
	}
	if !t.outage && t.fails[dk] >= deadRuns {
		host := firstLink()
		if host == "" {
			for _, m := range t.item.Mirrors {
				mk := settings.MirrorHostKey(m)
				// legacy-зеркало в этом же прогоне уходит в remove или retire
				if mk == dk || hasKey(t.item.Unconfirmed, mk) || t.legacy[mk] || hasKey(t.item.Retired, mk) || !usable(m) {
					continue
				}
				if t.up[mk] {
					host = m
					break
				}
			}
		}
		if host != "" {
			return []settings.MirrorSuggestion{mainOf(host, settings.MirrorReasonDead), removeDomain(settings.MirrorReasonDead)}
		}
	}
	return nil
}

func pickRetires(t *trackerRun, cands []retire) []retire {
	slices.SortFunc(cands, func(a, b retire) int {
		return strings.Compare(settings.MirrorHostKey(a.Host), settings.MirrorHostKey(b.Host))
	})
	gone := map[string]bool{}
	var out []retire
	for _, c := range cands {
		ck := settings.MirrorHostKey(c.Host)
		if gone[ck] {
			continue
		}
		// удаляем, только если после этого отвечает хотя бы один другой хост трекера
		left := false
		for _, h := range t.hosts {
			if hk := settings.MirrorHostKey(h); hk != ck && !gone[hk] && t.up[hk] {
				left = true
				break
			}
		}
		if left {
			gone[ck] = true
			out = append(out, c)
		}
	}
	return out
}

func pickAdds(t *trackerRun, in input, probe func(string, int) (bool, string), usable func(string) bool) []string {
	if t.src == nil {
		return nil
	}
	skip := map[string]bool{}
	for _, h := range t.hosts {
		skip[settings.MirrorHostKey(h)] = true
	}
	for _, h := range t.item.Retired {
		skip[settings.MirrorHostKey(h)] = true
	}
	for _, h := range in.pruned[t.key] {
		skip[settings.MirrorHostKey(h)] = true
	}
	var out []string
	for _, l := range t.src.Links {
		if len(out) >= maxAdds {
			break
		}
		lk := settings.MirrorHostKey(l)
		if skip[lk] || !usable(l) {
			continue
		}
		skip[lk] = true
		if a, _ := probe(l, t.item.ProxyType); a {
			out = append(out, l)
		}
	}
	return out
}

var localTLDs = map[string]bool{
	"localhost": true, "local": true, "lan": true, "internal": true, "intranet": true,
	"corp": true, "home": true, "private": true, "localdomain": true, "arpa": true,
}

func usableHost(items map[string]*config.TrackerItem, ownKey, h string) bool {
	if !settings.ValidMirrorHost(h) {
		return false
	}
	low := strings.ToLower(h)
	if strings.Contains(low, "unblock") || strings.Contains(low, "proxy") || strings.HasSuffix(low, ".appspot.com") {
		return false
	}
	// локальные имена из Jackett не годятся: это не адрес трекера, а чья-то домашняя сеть
	if localTLDs[low[strings.LastIndexByte(low, '.')+1:]] {
		return false
	}
	for key, it := range items {
		if key == ownKey || it == nil {
			continue
		}
		known := append([]string{it.Domain, it.ActiveDomain}, it.Mirrors...)
		for _, k := range append(known, it.Retired...) {
			k = strings.ToLower(strings.TrimSpace(k))
			if k != "" && (strings.Contains(low, k) || strings.Contains(k, low)) {
				return false
			}
		}
	}
	return true
}

func nameLabel(host string) string {
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return ""
	}
	return labels[len(labels)-2]
}

func hasKey(list []string, key string) bool {
	return slices.ContainsFunc(list, func(h string) bool { return settings.MirrorHostKey(h) == key })
}

func sortByHost(list []settings.MirrorSuggestion) {
	slices.SortStableFunc(list, func(a, b settings.MirrorSuggestion) int {
		return strings.Compare(settings.MirrorHostKey(a.Host), settings.MirrorHostKey(b.Host))
	})
}

func dedupeSuggestions(list []settings.MirrorSuggestion) []settings.MirrorSuggestion {
	seen := map[string]bool{}
	var out []settings.MirrorSuggestion
	for _, s := range list {
		id := s.Kind + "|" + settings.MirrorHostKey(s.Host)
		if !seen[id] {
			seen[id] = true
			out = append(out, s)
		}
	}
	return out
}

func unreferenced(retired map[string][]string, urls []string) map[string][]string {
	low := make([]string, len(urls))
	for i, u := range urls {
		low[i] = strings.ToLower(u)
	}
	var out map[string][]string
	for key, hosts := range retired {
		for _, h := range hosts {
			hl := strings.ToLower(strings.TrimSpace(h))
			if hl == "" || slices.ContainsFunc(low, func(u string) bool { return strings.Contains(u, hl) }) {
				continue
			}
			if out == nil {
				out = map[string][]string{}
			}
			out[key] = append(out[key], h)
		}
	}
	return out
}
