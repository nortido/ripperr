package mirrorsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/model"
	"ripperr/internal/notify"
	"ripperr/internal/settings"
	"ripperr/internal/system/logger"
)

const (
	defaultStartDelay = 2 * time.Minute
	defaultInterval   = 24 * time.Hour
	idlePoll          = 30 * time.Second
)

type Store interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}

type Deps struct {
	Store      Store
	Items      config.Tracker
	HTTP       *http.Client
	Sources    []string
	Check      func(ctx context.Context, host string, proxyType int) (alive bool, location string)
	ConfirmOn  bool
	Reload     func()
	Idle       func() bool
	Entries    func(ctx context.Context) ([]string, error)
	StoredURLs func(ctx context.Context) ([]string, error)
	Now        func() time.Time
	After      func(d time.Duration) <-chan time.Time
	StartDelay time.Duration
	Interval   time.Duration
	Notify     func(notify.Event)

	// только для RunOnceIfDue: при сбое сети last_run не пишем, иначе повторный старт ждал бы сутки
	keepDueOnOutage bool
}

func withDefaults(d Deps) Deps {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.After == nil {
		d.After = time.After
	}
	if d.StartDelay <= 0 {
		d.StartDelay = defaultStartDelay
	}
	if d.Interval <= 0 {
		d.Interval = defaultInterval
	}
	if len(d.Sources) == 0 {
		d.Sources = []string{githubTemplate}
	}
	return d
}

func Run(ctx context.Context, d Deps) {
	d = withDefaults(d)
	// lastAttempt держит интервал, даже если last_run не сохранился
	var lastAttempt time.Time
	for {
		last := lastAttempt
		if doc, err := settings.GetMirrorSync(ctx, d.Store); err == nil && doc.LastRun.After(last) {
			last = doc.LastRun
		}
		wait := d.StartDelay
		if !last.IsZero() {
			wait = max(wait, d.Interval-d.Now().Sub(last))
		}
		select {
		case <-ctx.Done():
			return
		case <-d.After(wait):
		}
		lastAttempt = d.Now()
		runOnce(ctx, d)
	}
}

func runOnce(ctx context.Context, d Deps) {
	prev, err := settings.GetMirrorSync(ctx, d.Store)
	if err != nil {
		logger.Warn(fmt.Sprintf("mirror sync: load failed: %v", err))
		return
	}
	keys, err := addedKeys(ctx, d)
	if err != nil {
		logger.Warn(fmt.Sprintf("mirror sync: load failed: %v", err))
		return
	}
	now := d.Now()
	sources, fetched := loadSources(ctx, d, keys, prev.Sources, now)
	if ctx.Err() != nil {
		return
	}
	prunedHosts := map[string][]string{}
	for key, list := range prev.Pruned {
		for _, p := range list {
			prunedHosts[key] = append(prunedHosts[key], p.Host)
		}
	}
	res := suggest(input{
		keys:      keys,
		items:     d.Items,
		sources:   sources,
		check:     func(host string, proxyType int) (bool, string) { return d.Check(ctx, host, proxyType) },
		prevFails: prev.Fails,
		confirmOn: d.ConfirmOn,
		dismissed: prev.IsDismissed,
		pruned:    prunedHosts,
	})
	if ctx.Err() != nil {
		return
	}
	if res.outage {
		logger.Warn("mirror sync: no host answered, probe results ignored")
	}
	prune := pruneHosts(ctx, d, keys)
	if ctx.Err() != nil {
		return
	}

	// дальше пишем до конца без отмены: правка trackers без записи в auto потеряла бы откат
	wctx := context.WithoutCancel(ctx)
	autoLog := map[string][]settings.MirrorAuto{}
	prunedLog := map[string][]settings.MirrorPruned{}
	added, removed, changed := 0, 0, false
	for _, key := range keys {
		adds, rets, prs := res.adds[key], res.retires[key], prune[key]
		if len(adds)+len(rets)+len(prs) == 0 {
			continue
		}
		reasons := map[string]string{}
		retHosts := make([]string, 0, len(rets))
		for _, r := range rets {
			retHosts = append(retHosts, r.Host)
			reasons[settings.MirrorHostKey(r.Host)] = r.Reason
		}
		a, r, p, err := func() ([]string, []string, []string, error) {
			unlock := settings.LockTrackers()
			defer unlock()
			return settings.MaintainTrackerMirrors(wctx, d.Store, key, *d.Items[key], adds, retHosts, prs)
		}()
		if err != nil {
			logger.Warn(fmt.Sprintf("mirror sync: save failed: %v", err))
			continue
		}
		for _, h := range a {
			logger.Info(fmt.Sprintf("mirror sync: %s: added %s", key, h))
			autoLog[key] = append(autoLog[key], settings.MirrorAuto{Kind: settings.MirrorAutoAdded, Host: h, Reason: settings.MirrorReasonJackett, At: now})
		}
		for _, h := range r {
			reason := reasons[settings.MirrorHostKey(h)]
			logger.Info(fmt.Sprintf("mirror sync: %s: removed %s (%s)", key, h, reason))
			autoLog[key] = append(autoLog[key], settings.MirrorAuto{Kind: settings.MirrorAutoRemoved, Host: h, Reason: reason, At: now})
		}
		for _, h := range p {
			logger.Info(fmt.Sprintf("mirror sync: %s: pruned retired %s (no torrent references it)", key, h))
			prunedLog[key] = append(prunedLog[key], settings.MirrorPruned{Host: h, At: now})
		}
		added += len(a)
		removed += len(r)
		changed = changed || len(a)+len(r)+len(p) > 0
	}

	var total int
	var fresh []string
	var digest []notify.MirrorDigestItem
	err = settings.UpdateMirrorSync(wctx, d.Store, func(m *settings.MirrorSync) {
		total, fresh, digest = 0, nil, nil
		if !d.keepDueOnOutage || !res.outage {
			m.LastRun = now
		}
		for path, s := range fetched {
			if m.Sources == nil {
				m.Sources = map[string]settings.MirrorSource{}
			}
			m.Sources[path] = s
		}
		for key := range m.Fails {
			if !slices.Contains(keys, key) {
				delete(m.Fails, key)
			}
		}
		for key := range m.Suggestions {
			if !slices.Contains(keys, key) {
				delete(m.Suggestions, key)
			}
		}
		for _, key := range keys {
			if f := res.fails[key]; len(f) > 0 {
				if m.Fails == nil {
					m.Fails = map[string]map[string]int{}
				}
				m.Fails[key] = f
			} else {
				delete(m.Fails, key)
			}
			// зеркала выключены: список меняется молча, решать пользователь будет, когда их включит
			mirrorsOn := d.Items[key].ActiveDomain == ""
			old := m.Suggestions[key]
			m.SetSuggestions(key, res.suggestions[key])
			for _, s := range m.Suggestions[key] {
				total++
				if !slices.ContainsFunc(old, func(o settings.MirrorSuggestion) bool {
					return o.Kind == s.Kind && settings.MirrorHostKey(o.Host) == settings.MirrorHostKey(s.Host)
				}) {
					fresh = append(fresh, suggestLine(key, s))
					// remove основного адреса Pending не показывает, в сводке он был бы без кнопки
					ofDomain := settings.MirrorHostKey(s.Host) == settings.MirrorHostKey(d.Items[key].Domain)
					if mirrorsOn && (s.Kind == settings.MirrorKindMain || (s.Kind == settings.MirrorKindRemove && d.ConfirmOn && !ofDomain)) {
						digest = append(digest, digestItem(d, key, s))
					}
				}
			}
			// в режиме с подтверждением новое зеркало ждет решения, без него добавление автоматическое и тихое
			if d.ConfirmOn && mirrorsOn {
				for _, a := range autoLog[key] {
					if a.Kind == settings.MirrorAutoAdded {
						digest = append(digest, notify.MirrorDigestItem{Tracker: trackerLabel(d, key), Kind: notify.DigestConfirm, Host: a.Host})
					}
				}
			}
			if list := autoLog[key]; len(list) > 0 {
				if m.Auto == nil {
					m.Auto = map[string][]settings.MirrorAuto{}
				}
				m.Auto[key] = append(m.Auto[key], list...)
			}
			if list := prunedLog[key]; len(list) > 0 {
				if m.Pruned == nil {
					m.Pruned = map[string][]settings.MirrorPruned{}
				}
				m.Pruned[key] = append(m.Pruned[key], list...)
			}
		}
	})
	if err != nil {
		logger.Warn(fmt.Sprintf("mirror sync: save failed: %v", err))
	}
	for _, line := range fresh {
		logger.Info(line)
	}
	// не записали документ: следующий прогон увидит это новым и пошлет сам, без дубля
	if err == nil && len(digest) > 0 && d.Notify != nil {
		d.Notify(notify.MirrorDigestEvent(digest))
	}
	logger.Info(fmt.Sprintf("mirror sync: %d trackers checked, %d added, %d removed, %d suggestions (%d new)", len(keys), added, removed, total, len(fresh)))

	if !changed || d.Reload == nil {
		return
	}
	for d.Idle != nil && !d.Idle() {
		select {
		case <-ctx.Done():
			return
		case <-d.After(idlePoll):
		}
	}
	// при отмене перезагрузка и так идет, лишний сигнал дал бы вторую
	if ctx.Err() != nil {
		return
	}
	logger.Info("mirror sync: reload after mirror changes")
	d.Reload()
}

func trackerLabel(d Deps, key string) string {
	if item := d.Items[key]; item != nil && item.Name != "" {
		return item.Name
	}
	return key
}

func digestItem(d Deps, key string, s settings.MirrorSuggestion) notify.MirrorDigestItem {
	item := notify.MirrorDigestItem{Tracker: trackerLabel(d, key), Host: s.Host}
	if s.Kind == settings.MirrorKindMain {
		item.Kind = notify.DigestMain
		item.From = s.From
		return item
	}
	item.Kind = notify.DigestRemove
	switch s.Reason {
	case settings.MirrorReasonDead:
		item.Reason = notify.DigestReasonDead
	case settings.MirrorReasonJackett:
		item.Reason = notify.DigestReasonJackett
	}
	return item
}

func suggestLine(key string, s settings.MirrorSuggestion) string {
	from := ""
	if s.From != "" {
		from = " from " + s.From
	}
	return fmt.Sprintf("mirror sync: %s: suggest %s %s%s (%s)", key, s.Kind, s.Host, from, s.Reason)
}

func addedKeys(ctx context.Context, d Deps) ([]string, error) {
	list, ok, err := settings.TrackersAdded(ctx, d.Store)
	if err != nil {
		return nil, err
	}
	// без документа trackers_added видны все трекеры
	if !ok {
		list = slices.Sorted(maps.Keys(d.Items))
	}
	var keys []string
	for _, key := range list {
		if d.Items[key] != nil && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func loadSources(ctx context.Context, d Deps, keys []string, cached map[string]settings.MirrorSource, now time.Time) (map[string]*source, map[string]settings.MirrorSource) {
	sources := map[string]*source{}
	fetched := map[string]settings.MirrorSource{}
	total, failed, first := 0, 0, ""
	start := 0
	for _, key := range keys {
		path, ok := jackettSources[key]
		if !ok {
			continue
		}
		total++
		src, err := fetchAny(ctx, d.HTTP, d.Sources, &start, path)
		if err == nil {
			sources[key] = &src
			fetched[path] = settings.MirrorSource{Links: src.Links, Legacy: src.Legacy, Fetched: now}
			continue
		}
		failed++
		if first == "" {
			first = fmt.Sprintf("%s: %v", path, err)
		}
		if c, ok := cached[path]; ok && len(c.Links) > 0 {
			sources[key] = &source{Links: c.Links, Legacy: c.Legacy}
		}
	}
	if failed > 0 && ctx.Err() == nil {
		logger.Warn(fmt.Sprintf("mirror sync: jackett unavailable for %d of %d sources, cached lists used: %s", failed, total, first))
	}
	return sources, fetched
}

func pruneHosts(ctx context.Context, d Deps, keys []string) map[string][]string {
	retired := map[string][]string{}
	for _, key := range keys {
		if list := d.Items[key].Retired; len(list) > 0 {
			retired[key] = list
		}
	}
	if len(retired) == 0 || d.Entries == nil || d.StoredURLs == nil {
		return nil
	}
	urls, reason := referenceURLs(ctx, d)
	if reason != "" {
		if ctx.Err() == nil {
			logger.Warn("mirror sync: retired prune skipped: " + reason)
		}
		return nil
	}
	return unreferenced(retired, urls)
}

func referenceURLs(ctx context.Context, d Deps) ([]string, string) {
	// неполный список торрентов не доказывает, что на хост никто не ссылается
	live, err := d.Entries(ctx)
	switch {
	case errors.Is(err, model.ErrPartialList):
		return nil, "partial list"
	case err != nil:
		return nil, "list error"
	case len(live) == 0:
		return nil, "empty list"
	}
	urls := nonBlank(live)
	if len(urls) == 0 {
		return nil, "no URLs"
	}
	stored, err := d.StoredURLs(ctx)
	if err != nil {
		return nil, "history error"
	}
	return append(urls, nonBlank(stored)...), ""
}

func nonBlank(list []string) []string {
	var out []string
	for _, s := range list {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func RunOnceIfDue(ctx context.Context, d Deps) bool {
	d = withDefaults(d)
	// после вызова приложение перезапускается целиком, ждать простоя и звать Reload незачем
	d.Reload, d.Idle = nil, nil
	d.keepDueOnOutage = true
	if ctx.Err() != nil {
		return false
	}
	doc, err := settings.GetMirrorSync(ctx, d.Store)
	if err != nil || (!doc.LastRun.IsZero() && d.Now().Sub(doc.LastRun) < d.Interval) {
		return false
	}
	runOnce(ctx, d)
	return true
}
