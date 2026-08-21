package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/helper/magnetmeta"
	"trackerr/internal/helper/torrentmeta"
	"trackerr/internal/model"
	"trackerr/internal/model/proxy"
	runnerModel "trackerr/internal/model/runner"
	storageModel "trackerr/internal/model/storage"
	trackerModel "trackerr/internal/model/tracker"
	"trackerr/internal/notify"
	storagePkg "trackerr/internal/storage"
	"trackerr/internal/system/logger"
	"trackerr/internal/tracker/client"
)

const (
	durationMonth = time.Hour * 24 * 30
	durationYear  = time.Hour * 24 * 365
)

var (
	errSkipped = errors.New("skipped")
)

type trackerService interface {
	BuildCache(ctx context.Context) (err error)
	CachedList() (res map[int]client.ITracker)
	GetID(url string) (id int)
	GetHourLimit(ttid int) (limit int)
	GetCachedClient(ctx context.Context, ttid int) (tc client.IClient, err error)
	Response(ctx context.Context, ttid int, link string, download bool) (buf *bytes.Buffer, err error)
}

type manager interface {
	GetEntries(ctx context.Context, opts model.FilterOptions) (entries []*model.Entry, err error)
	Submit(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
	// SubmitMagnet больше не зовётся из runner (см. submit), метод остаётся в
	// интерфейсе и у клиентов только ради подметальщика blackhole для старых
	// magnet-пар, записанных прежней версией
	SubmitMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error)
}

type storage interface {
	SaveItem(ctx context.Context, item *runnerModel.HistoryItem) (err error)
	AddCheckLog(ctx context.Context, entry *storageModel.CheckLogEntry) (int, error)
	AddIgnore(ctx context.Context, hash, name, url string) error
	AllItems(ctx context.Context) (map[string]*runnerModel.HistoryItem, error)
	GetIgnoreSet(ctx context.Context) (map[string]struct{}, error)
	DeleteItem(ctx context.Context, urlHash string) error
	PurgeDuplicateNames(ctx context.Context, winners map[string]string) error
	SetForceCheck(ctx context.Context, urlHash string, force bool) (bool, error)
}

type handler struct {
	mx             helper.KeyedMutex
	pushMu         sync.Mutex
	config         model.HandlerConfig
	idx            map[int]chan *task
	pri            map[int]chan *task
	manager        manager
	storage        storage
	trackerService trackerService
	state          *State
	notifier       *notify.Notifier
	gen            uint32
	// magnetFetch добывает метаданные по магниту сам, без участия клиента
	// загрузок; поле, а не прямой вызов magnetmeta.Fetch, чтобы тесты могли
	// подменить его, тем же приёмом, что уже используют manager и trackerService
	magnetFetch func(ctx context.Context, magnet string) (data []byte, err error)
}

// task несёт запись воркеру трекера. Ненулевой report значит, что запись
// относится к холостому прогону и не должна ничего записывать.
type task struct {
	entry  *model.Entry
	report *dryReport
}

func (h *handler) Run(ctx context.Context) {
	var err error

	cherr := make(chan error)
	var delay time.Duration
	if h.config.Immediate {
		delay = 0
	} else {
		delay = time.Until(h.nextRun(time.Now()))
	}
	pt := time.NewTimer(delay)

	if h.config.ThresholdMonth == 0 || h.config.ThresholdHalfYear == 0 || h.config.ThresholdYear == 0 || h.config.ThresholdOld == 0 {
		logger.Error("min thresholds are not set")
		return
	}
	h.gen = h.state.Reset()
	h.initTrackerChannels()
	for ttid := range h.idx {
		go h.trackerWorker(ctx, ttid, cherr)
	}
	if h.state.ConsumeRestart() {
		go h.runPush(ctx, pt, cherr, false)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err = <-cherr:
			if !errors.Is(err, context.Canceled) {
				logger.Error(err.Error())
			}
		case dry := <-h.state.Trigger():
			if h.state.Running() {
				logger.Info("run already in progress")
				continue
			}
			go h.runPush(ctx, pt, cherr, dry)
		case hash := <-h.state.Force():
			go h.pushForced(ctx, append([]string{hash}, h.state.DrainForce()...), cherr)
		case <-pt.C:
			if h.state.Running() {
				pt.Reset(time.Until(h.nextRun(time.Now())))
				continue
			}
			go h.runPush(ctx, pt, cherr, false)
		}
	}
}

func (h *handler) runPush(ctx context.Context, pt *time.Timer, cherr chan error, dry bool) {
	if !h.pushMu.TryLock() {
		pt.Reset(time.Until(h.nextRun(time.Now())))
		return
	}
	defer h.pushMu.Unlock()

	if pushErr := h.push(ctx, dry, nil); pushErr != nil {
		select {
		case <-ctx.Done():
		case cherr <- pushErr:
		}
	}
	pt.Reset(time.Until(h.nextRun(time.Now())))
}

func (h *handler) pushForced(ctx context.Context, hashes []string, cherr chan error) {
	forced := make(map[string]struct{}, len(hashes))
	for _, hash := range hashes {
		forced[hash] = struct{}{}
	}
	if err := h.push(ctx, false, forced); err != nil {
		select {
		case <-ctx.Done():
		case cherr <- err:
		}
	}
}

func (h *handler) nextRun(now time.Time) time.Time {
	loc := scheduleLocation(h.config.Location)
	now = now.In(loc)
	interval := time.Duration(24/h.config.ListRefreshPerDay) * time.Hour
	anchor := time.Date(now.Year(), now.Month(), now.Day(), h.config.StartHour, 0, 0, 0, loc)
	t := anchor
	if !t.After(now) {
		k := int64(now.Sub(anchor))/int64(interval) + 1
		t = anchor.Add(time.Duration(k) * interval)
	}
	return t
}

func (h *handler) trackerWorker(ctx context.Context, ttid int, cherr chan error) {
	interval := h.requestInterval(ttid)
	for {
		tk := h.nextTask(ctx, ttid)
		if tk == nil {
			return
		}
		e := tk.entry
		var err error
		func() {
			defer h.state.End(h.gen)
			defer tk.report.done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error(fmt.Sprintf("panic processing %q: %v\n%s", e.Name, r, debug.Stack()))
					h.logCheck(ctx, e, ttid, "error", fmt.Sprintf("panic: %v", r), tk.report)
				}
			}()
			err = h.worker(ctx, ttid, e, tk.report)
		}()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				logger.Info("processing interrupted")
			} else {
				select {
				case <-ctx.Done():
					return
				case cherr <- err:
				}
			}
		}
		if interval <= 0 {
			continue
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (h *handler) nextTask(ctx context.Context, ttid int) *task {
	select {
	case tk := <-h.pri[ttid]:
		return tk
	default:
	}
	select {
	case <-ctx.Done():
		return nil
	case tk := <-h.pri[ttid]:
		return tk
	case tk := <-h.idx[ttid]:
		return tk
	}
}

func (h *handler) requestInterval(ttid int) time.Duration {
	limit := h.trackerService.GetHourLimit(ttid)
	if limit <= 0 {
		limit = 1
	}
	return time.Hour / time.Duration(limit)
}

func (h *handler) initTrackerChannels() {
	list := h.trackerService.CachedList()
	h.idx = make(map[int]chan *task, len(list))
	h.pri = make(map[int]chan *task, len(list))
	for ttid := range list {
		h.idx[ttid] = make(chan *task, normalQueueSize)
		h.pri[ttid] = make(chan *task, forceQueueSize)
	}
}

type pushPass struct {
	dry    bool
	forced map[string]struct{}
	rep    *dryReport
	items  map[string]*runnerModel.HistoryItem
	ignore map[string]struct{}
}

func (h *handler) push(ctx context.Context, dry bool, forced map[string]struct{}) (err error) {
	h.state.Begin(h.gen)
	defer h.state.End(h.gen)

	var rep *dryReport
	if dry {
		rep = newDryReport(h.state)
		h.state.setDry(true)
		logger.Info(fmt.Sprintf("dry-run started: up to %d entries, nothing will be written", dryRunLimit))
		defer rep.release()
	}

	rawEntries, entries, partial, err := h.collectEntries(ctx)
	if err != nil {
		return err
	}

	if !dry && forced == nil {
		h.purgeDuplicateNames(ctx, entries)
	}

	items, err := h.storage.AllItems(ctx)
	if err != nil {
		return err
	}

	var ignoreSet map[string]struct{}
	if forced == nil {
		// форсированный проход это явное действие пользователя над записью (кнопка "проверить сейчас"),
		// поэтому он должен доставать и записи из списка игнора, включая автоигнорированные
		if ignoreSet, err = h.storage.GetIgnoreSet(ctx); err != nil {
			return err
		}
	}

	if err = h.enqueueEntries(ctx, entries, &pushPass{
		dry: dry, forced: forced, rep: rep, items: items, ignore: ignoreSet,
	}); err != nil {
		return err
	}

	// форс-прогон тоже чистит: список торрентов он берёт целиком, тем же
	// collectEntries, а пользователь жмёт кнопку как раз после удаления раздачи
	if !dry {
		if partial {
			logger.Warn("history purge skipped: partial torrent list")
		} else {
			h.purgeStaleHistory(ctx, rawEntries, items)
		}
	}
	return nil
}

func (h *handler) collectEntries(ctx context.Context) (rawEntries, entries []*model.Entry, partial bool, err error) {
	rawEntries, err = h.manager.GetEntries(ctx, model.FilterOptions{})
	if err != nil {
		if !errors.Is(err, model.ErrPartialList) {
			err = fmt.Errorf("get entries error: %w", err)
			return
		}
		logger.Warn(fmt.Sprintf("partial torrent list: %v", err))
		partial = true
		err = nil
	}
	entries = helper.DedupeEntriesByName(rawEntries)
	return
}

func (h *handler) purgeDuplicateNames(ctx context.Context, entries []*model.Entry) {
	winners := make(map[string]string)
	for _, e := range entries {
		if e.URL == "" {
			continue
		}
		winners[strings.TrimSpace(e.Name)] = storagePkg.URLHash(e.URL)
	}
	if err := h.storage.PurgeDuplicateNames(ctx, winners); err != nil {
		logger.Warn(fmt.Sprintf("purge duplicate names: %v", err))
	}
}

func (h *handler) enqueueEntries(ctx context.Context, entries []*model.Entry, p *pushPass) (err error) {
	queue := h.idx
	if p.forced != nil {
		queue = h.pri
	}
	for i := range entries {
		if entries[i].URL == "" {
			continue
		}
		if p.forced != nil {
			if _, ok := p.forced[storagePkg.URLHash(entries[i].URL)]; !ok {
				continue
			}
		}
		if len(h.config.Categories) > 0 && !slices.Contains(h.config.Categories, entries[i].Category) {
			continue
		}
		if len(h.config.WatchTags) > 0 && !entryMatchesWatchTags(entries[i].Tag, h.config.WatchTags) {
			continue
		}
		if err = h.buildEntry(ctx, entries[i], p.dry, p.items); errors.Is(err, errSkipped) {
			continue
		} else if err != nil {
			return err
		}
		t := h.trackerService.GetID(entries[i].URL)
		if t == 0 {
			continue
		}
		if _, ignored := p.ignore[entries[i].URLHash]; ignored {
			continue
		}
		if p.dry && p.rep.queued.Load() >= dryRunLimit {
			break
		}
		h.state.Begin(h.gen)
		p.rep.add()
		select {
		case <-ctx.Done():
			h.state.End(h.gen)
			p.rep.done()
			return nil
		case queue[t] <- &task{entry: entries[i], report: p.rep}:
		}
	}
	return nil
}

func entryMatchesWatchTags(entryTags string, watchTags []string) bool {
	for _, t := range strings.Split(entryTags, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if slices.Contains(watchTags, t) {
			return true
		}
	}
	return false
}

func (h *handler) purgeStaleHistory(ctx context.Context, allEntries []*model.Entry, items map[string]*runnerModel.HistoryItem) {
	present := make(map[string]struct{})
	for _, e := range allEntries {
		if e.URL == "" {
			continue
		}
		present[storagePkg.URLHash(e.URL)] = struct{}{}
	}
	if len(allEntries) == 0 {
		logger.Warn("history purge skipped: empty torrent list")
		return
	}
	if len(present) == 0 {
		logger.Warn("history purge skipped: no torrent URLs found")
		return
	}
	// чистится вся история, а не только записи с наступившим recheck_at: торрент
	// удалили из qBittorrent, значит следить не за чем, и ждать своей очереди
	// такая запись может месяцами
	for hash, item := range items {
		if _, ok := present[hash]; ok {
			continue
		}
		if err := h.storage.DeleteItem(ctx, hash); err != nil {
			logger.Error(fmt.Sprintf("history purge delete %s: %v", hash, err))
			continue
		}
		logger.Info(fmt.Sprintf("purged from history: %s", item.Name))
	}
}

func (h *handler) buildEntry(ctx context.Context, e *model.Entry, dry bool, items map[string]*runnerModel.HistoryItem) (err error) {
	var hi *runnerModel.HistoryItem

	if hi, err = h.getHistory(ctx, e.URL, dry, items); err != nil {
		return
	}
	e.URLHash = hi.URLHash

	if hi.Exists {
		e.Update = true
		e.TopicID = hi.TopicID
		if !hi.ForceCheck {
			e.Timestamp = hi.CreatedAt
		}
	}
	return
}

func (h *handler) getHistory(ctx context.Context, link string, dry bool, items map[string]*runnerModel.HistoryItem) (hi *runnerModel.HistoryItem, err error) {
	var ok bool
	if hi, ok = items[storagePkg.URLHash(link)]; !ok {
		hi = &runnerModel.HistoryItem{URLHash: storagePkg.URLHash(link), Exists: false}
	}
	if !hi.Exists {
		return
	}
	if dry {
		return
	}
	if hi.RecheckAt != nil && time.Now().Before(*hi.RecheckAt) && !hi.ForceCheck {
		err = errSkipped
	}
	if hi.TrackerTypeID == 0 {
		err = errSkipped
	}
	return
}

func (h *handler) worker(ctx context.Context, ttid int, e *model.Entry, rep *dryReport) (err error) {
	var tc client.IClient
	// блокировка на каждый трекер: записи разных трекеров обрабатываются параллельно,
	// записи одного трекера строго друг за другом.
	mu := h.mx.Mutex(ttid)
	mu.Lock()
	defer mu.Unlock()

	if tc, err = h.trackerService.GetCachedClient(ctx, ttid); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("processing interrupted")
			return nil
		}
		h.logCheck(ctx, e, ttid, "error", err.Error(), rep)
		return
	}
	pc, err := h.checkPage(ctx, ttid, tc, e, rep)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("processing interrupted")
			return nil
		}
		h.logCheck(ctx, e, ttid, "error", err.Error(), rep)
		return
	}
	if pc.dateMissing {
		if rep == nil {
			if _, clearErr := h.storage.SetForceCheck(ctx, e.URLHash, false); clearErr != nil {
				logger.Error(fmt.Sprintf("force_check clear failed: %v", clearErr))
			}
		}
		h.logCheck(ctx, e, ttid, "unknown", pc.sample, rep)
		return
	}
	if !pc.changed {
		if rep == nil {
			logger.Info(fmt.Sprint("skipped: ", e.Name))
		}
		h.logCheck(ctx, e, ttid, "unchanged", "", rep)
		return
	}
	if err = h.submit(ctx, ttid, tc, e, rep); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("processing interrupted")
			return nil
		}
		if errors.Is(err, model.ErrAlreadyAdded) {
			logger.Info(fmt.Sprint("already in qbittorrent, skipped: ", e.Name))
			// история пишется как при обычном unchanged: торрент с этим
			// инфохешем уже стоит, значит релиз актуален. Иначе force_check
			// остаётся взведённым, а дата в истории старой, и каждый прогон
			// снова считает релиз обновлённым и снова упирается в already added
			if rep == nil {
				if saveErr := h.saveHistory(ctx, ttid, e, pc.date); saveErr != nil {
					h.logCheck(ctx, e, ttid, "error", saveErr.Error(), rep)
					return saveErr
				}
			}
			h.logCheck(ctx, e, ttid, "unchanged", "", rep)
			return nil
		}
		err = fmt.Errorf("%s %w", entryTopicURL(tc, e), err)
		h.logCheck(ctx, e, ttid, "error", err.Error(), rep)
		return
	}
	if rep == nil {
		if err = h.saveHistory(ctx, ttid, e, pc.date); err != nil {
			h.logCheck(ctx, e, ttid, "error", err.Error(), rep)
			return
		}
	}
	if rep == nil {
		logger.Info(fmt.Sprint("updated: ", e.Name))
	}
	h.logCheck(ctx, e, ttid, "updated", "", rep)

	return
}

func (h *handler) logCheck(ctx context.Context, e *model.Entry, ttid int, status, errMsg string, rep *dryReport) {
	if rep != nil {
		rep.count(status)
		verb := status
		if status == "updated" {
			verb = "would update"
		}
		msg := fmt.Sprintf("dry-run: %s: %s", verb, e.Name)
		if errMsg != "" {
			msg += ": " + errMsg
		}
		logger.Info(msg)
		return
	}
	streak, logErr := h.storage.AddCheckLog(ctx, &storageModel.CheckLogEntry{
		URLHash:       e.URLHash,
		ItemName:      e.Name,
		ItemURL:       e.URL,
		TrackerTypeID: ttid,
		Status:        status,
		Error:         errMsg,
	})
	if logErr != nil {
		logger.Error(fmt.Sprintf("check_log write failed: %v", logErr))
		return
	}
	switch status {
	case "error":
		if h.config.ErrorStreakThreshold >= 1 && streak >= h.config.ErrorStreakThreshold {
			if ignoreErr := h.storage.AddIgnore(ctx, e.URLHash, e.Name, e.URL); ignoreErr != nil {
				logger.Error(fmt.Sprintf("auto-ignore failed for %s: %v", e.Name, ignoreErr))
			} else {
				logger.Warn(fmt.Sprintf("%s: %d errors in a row, sent to ignore", e.Name, streak))
				h.notifier.Notify(notify.BrokenEvent(e.Name, e.URL, streak))
			}
		}
	case "updated":
		h.notifier.Notify(notify.UpdatedEvent(e.Name, e.URL, e.Poster))
	}
}

type pageCheck struct {
	changed     bool
	dateMissing bool
	date        *time.Time
	sample      string
}

func (h *handler) checkPage(ctx context.Context, ttid int, tc client.IClient, e *model.Entry, rep *dryReport) (pc pageCheck, err error) {
	var (
		cur        *time.Time
		b          *bytes.Buffer
		downloadID string
		encoded    = tc.GetProxyType() != proxy.TypeNone
	)
	if b, err = h.getPage(ctx, ttid, e.URL); err != nil {
		return
	}
	downloadID = tc.ParseDownloadID(b, encoded)

	if e.Update && downloadID != "" && e.TopicID != downloadID {
		e.TopicID = downloadID
		curPtr := time.Now()
		cur = &curPtr
	} else if cur = tc.ParseDate(b, encoded); cur == nil {
		logger.Warn(fmt.Sprintf("current time not found for %s", e.Name))
		pc.dateMissing = true
		pc.sample = pageSample(b, encoded)
		return
	}
	pc.changed = e.Timestamp.Before(*cur)
	pc.date = cur
	if pc.changed {
		e.Magnet = tc.ParseMagnet(b, encoded)
		e.Poster = tc.ParsePoster(b, encoded)
		return
	}
	if rep == nil {
		err = h.saveHistory(ctx, ttid, e, cur)
	}
	return
}

func (h *handler) getPage(ctx context.Context, ttid int, link string) (b *bytes.Buffer, err error) {
	b, err = h.trackerService.Response(ctx, ttid, link, false)

	if err != nil {
		return
	} else if b == nil || b.Len() < 50 {
		err = fmt.Errorf("no page data")
	}
	return
}

func (h *handler) submit(ctx context.Context, ttid int, tc client.IClient, e *model.Entry, rep *dryReport) (err error) {
	if rep != nil {
		return nil
	}
	opts := model.ManagerAddFileOptions{
		Name:           e.Name,
		Hash:           e.Hash,
		Category:       e.Category,
		SavePath:       e.SavePath,
		Tag:            e.Tag,
		AutoManaged:    e.AutoManaged,
		MaxRatio:       e.MaxRatio,
		MaxSeedingTime: e.MaxSeedingTime,
		Comment:        entryTopicURL(tc, e),
	}

	// добыча метаданных по магниту идёт первой и не трогает клиент загрузок:
	// решение "магнит не вышел, идём на трекер" обязано быть принято до того,
	// как manager.Submit внутри себя удалит старый торрент и добавит новый
	if e.Magnet != "" {
		if data, buildErr := h.buildTorrentFromMagnet(ctx, e.Magnet, opts.Comment); buildErr == nil {
			if err = h.manager.Submit(ctx, bytes.NewBuffer(data), opts); err != nil {
				err = fmt.Errorf("submit error: %w", err)
			}
			return
		} else {
			logger.Warn(fmt.Sprintf("magnet metadata fetch failed for %s: %v, falling back to tracker download", e.Name, buildErr))
		}
	}

	var b *bytes.Buffer
	b, err = h.trackerService.Response(ctx, ttid, tc.DownloadLink(e.TopicID), true)
	if err != nil {
		return
	}
	if err = h.manager.Submit(ctx, b, opts); err != nil {
		err = fmt.Errorf("submit error: %w", err)
	}
	return
}

func (h *handler) buildTorrentFromMagnet(ctx context.Context, magnet, comment string) (data []byte, err error) {
	if h.magnetFetch == nil {
		return nil, errors.New("magnet fetch is not configured")
	}
	timeout := time.Duration(h.config.MagnetTimeout) * time.Second
	if timeout <= 0 {
		// ponytail: нулевой/отрицательный таймаут в конфиге не должен вешать
		// раздачу навсегда, минута это разумный потолок по умолчанию
		timeout = time.Minute
	}
	fctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if data, err = h.magnetFetch(fctx, magnet); err != nil {
		return nil, err
	}
	return torrentmeta.SetComment(data, comment)
}

func entryTopicURL(tc client.IClient, e *model.Entry) string {
	primary := primaryDomain(tc)
	if e.URL != "" {
		if primary != "" {
			return trackerModel.ReplaceURLHost(e.URL, primary)
		}
		return e.URL
	}
	link := tc.TopicLink(e.TopicID)
	if link != "" && primary != "" {
		return trackerModel.ReplaceURLHost(link, primary)
	}
	return link
}

func primaryDomain(tc client.IClient) string {
	type primaryProvider interface {
		PrimaryDomain() string
	}
	if p, ok := tc.(primaryProvider); ok {
		return p.PrimaryDomain()
	}
	return tc.GetDomain()
}

func (h *handler) saveHistory(ctx context.Context, ttid int, e *model.Entry, cur *time.Time) (err error) {
	if err = h.storage.SaveItem(ctx, &runnerModel.HistoryItem{
		TrackerTypeID: ttid,
		URLHash:       e.URLHash,
		TopicID:       e.TopicID,
		URL:           e.URL,
		Category:      e.Category,
		Tag:           e.Tag,
		Name:          e.Name,
		Exists:        e.Update,
		CreatedAt:     cur,
		RecheckAt:     h.getRecheckAt(cur),
	}); err != nil {
		return
	}
	return
}

func (h *handler) getRecheckAt(tm *time.Time) (result *time.Time) {
	tt := time.Now()
	if tm == nil {
		tt = tt.AddDate(0, 0, h.config.ThresholdMonth)
		return &tt
	}
	d := time.Since(*tm)
	switch {
	case tm.Year() == 1, d < durationMonth:
		tt = tt.AddDate(0, 0, h.config.ThresholdMonth)
	case d < durationYear/2:
		tt = tt.AddDate(0, 0, h.config.ThresholdHalfYear)
	case d < durationYear:
		tt = tt.AddDate(0, 0, h.config.ThresholdYear)
	case d >= durationYear:
		tt = tt.AddDate(0, 0, h.config.ThresholdOld)
	}
	return &tt
}

func NewRunner(tracker trackerService, manager manager, storage storage, config model.HandlerConfig, state *State, notifier *notify.Notifier) *handler {
	return &handler{
		trackerService: tracker,
		manager:        manager,
		storage:        storage,
		config:         config,
		state:          state,
		notifier:       notifier,
		magnetFetch:    magnetmeta.Fetch,
	}
}
