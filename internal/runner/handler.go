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
	"sync/atomic"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/helper/magnetmeta"
	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/metrics"
	"ripperr/internal/model"
	"ripperr/internal/model/proxy"
	runnerModel "ripperr/internal/model/runner"
	storageModel "ripperr/internal/model/storage"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/notify"
	storagePkg "ripperr/internal/storage"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker/client"
	"ripperr/internal/tracker/registry"
)

const (
	durationMonth = time.Hour * 24 * 30
	durationYear  = time.Hour * 24 * 365
)

var (
	errSkipped     = errors.New("skipped")
	errTorrentBusy = errors.New("torrent is checking, moving or has an error, update postponed")
)

type trackerService interface {
	BuildCache(ctx context.Context) (err error)
	CachedList() (res map[int]client.ITracker)
	GetID(url string) (id int)
	GetHourLimit(ttid int) (limit int)
	GetCachedClient(ctx context.Context, ttid int) (tc client.IClient, err error)
	Response(ctx context.Context, ttid int, link string, download bool) (buf *bytes.Buffer, err error)
	Adopt(ctx context.Context, name, hash string) (url string, err error)
}

type manager interface {
	GetEntries(ctx context.Context, opts model.FilterOptions) (entries []*model.Entry, err error)
	Submit(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
	// runner его больше не зовет (см. submit). Метод живет только ради чистки blackhole
	// от magnet-пар, записанных прежней версией
	SubmitMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error)
	AttachComment(ctx context.Context, hash, comment string, fetch func(context.Context) ([]byte, error)) error
}

type storage interface {
	SaveItem(ctx context.Context, item *runnerModel.HistoryItem) (err error)
	AddCheckLog(ctx context.Context, entry *storageModel.CheckLogEntry) (int, error)
	AddIgnore(ctx context.Context, hash, name, url string) error
	AllItems(ctx context.Context) (map[string]*runnerModel.HistoryItem, error)
	GetIgnoreSet(ctx context.Context) (map[string]struct{}, error)
	DeleteItem(ctx context.Context, urlHash string) error
	PurgeDuplicateNames(ctx context.Context, live map[string][]string) error
	SetForceCheck(ctx context.Context, urlHash string, force bool) (bool, error)
	ErrorMarks(ctx context.Context) (map[string]storageModel.ErrorMark, error)
	AddReleaseTime(ctx context.Context, urlHash string, at time.Time, keep int) error
	RecentReleaseTimes(ctx context.Context, urlHash string, since time.Time, limit int) ([]time.Time, error)
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
	// метаданные по магниту качаем сами, без клиента загрузок. Поле, чтобы тесты могли
	// его подменить, как уже делают с manager и trackerService
	magnetFetch func(ctx context.Context, magnet string) (data []byte, err error)
	// хеши, которым ссылку в комментарий уже пробовали записать в этом процессе
	attached sync.Map
	// хеши торрентов клиента по адресу релиза (URLHash) из последнего прогона
	siblings atomic.Pointer[map[string][]string]
	// хеш торрента -> retryState: его обновление не удалось, а строку истории он делит с соседями
	retry sync.Map
	// задачи одного релиза в очередях: после старта новее не перезапускаем устаревшую
	taskSeq   atomic.Uint64
	startedMu sync.Mutex
	// urlHash -> номер, под которым последний раз стартовала задача этого релиза
	started map[string]uint64
}

type retryState struct {
	urlHash string
	fails   int
}

type task struct {
	entry *model.Entry
	// не nil: холостой прогон, ничего не пишем
	report *dryReport
	seq    uint64
	// задача планового прохода или "запустить сейчас": ее учитывает счетчик normalActive
	normal bool
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
	return nextSlot(now, h.config.StartHour, h.config.ListRefreshPerDay, h.config.Location)
}

func (h *handler) trackerWorker(ctx context.Context, ttid int, cherr chan error) {
	interval := h.requestInterval(ttid)
	for {
		tk := h.nextTask(ctx, ttid)
		if tk == nil {
			return
		}
		if h.staleTask(tk) {
			h.endTask(tk.normal)
			tk.report.done()
			continue
		}
		e := tk.entry
		h.state.StartItem(h.gen, ttid, e.URLHash)
		var err error
		func() {
			defer h.endTask(tk.normal)
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

func (h *handler) endTask(normal bool) {
	if normal {
		h.state.EndNormal(h.gen)
	}
	h.state.End(h.gen)
}

func (h *handler) staleTask(tk *task) bool {
	// холостой прогон ничего не пишет: его не пропускаем и в учет старта не берем
	if tk.report != nil {
		return false
	}
	h.startedMu.Lock()
	defer h.startedMu.Unlock()
	// соседи одной темы (тот же URL, другой торрент) это разные задачи
	key := tk.entry.URLHash + "\x00" + tk.entry.Hash
	if h.started[key] > tk.seq {
		return true
	}
	if h.started == nil {
		h.started = make(map[string]uint64)
	}
	h.started[key] = h.taskSeq.Add(1)
	return false
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
	dry     bool
	forced  map[string]struct{}
	rep     *dryReport
	items   map[string]*runnerModel.HistoryItem
	ignore  map[string]struct{}
	adopted map[*model.Entry]struct{}
	marks   map[string]storageModel.ErrorMark
	slot    time.Time
}

func (h *handler) push(ctx context.Context, dry bool, forced map[string]struct{}) (err error) {
	// прогон из-за hour_limit длится часами, все его записи должны вернуться в тот же слот
	slot := h.passSlot(time.Now())
	h.state.Begin(h.gen)
	defer h.state.End(h.gen)
	if forced == nil && !dry {
		h.state.BeginNormal(h.gen)
		defer h.state.EndNormal(h.gen)
	}

	var rep *dryReport
	if dry {
		rep = newDryReport(h.state)
		h.state.setDry(true)
		logger.Info(fmt.Sprintf("dry-run started: up to %d entries, nothing will be written", dryRunLimit))
		defer rep.release()
	}

	rawEntries, entries, adopted, partial, err := h.collectEntries(ctx)
	if err != nil {
		return err
	}
	h.storeSiblings(rawEntries, partial)

	if !dry && forced == nil {
		if partial {
			logger.Warn("duplicate names purge skipped: partial torrent list")
		} else {
			h.purgeDuplicateNames(ctx, entries)
		}
	}

	items, err := h.storage.AllItems(ctx)
	if err != nil {
		return err
	}

	var ignoreSet map[string]struct{}
	if forced == nil {
		// форс это явная кнопка "проверить сейчас", поэтому берем и записи из игнора,
		// автоигнор тоже
		if ignoreSet, err = h.storage.GetIgnoreSet(ctx); err != nil {
			return err
		}
	}

	var marks map[string]storageModel.ErrorMark
	if !dry && forced == nil {
		if marks, err = h.storage.ErrorMarks(ctx); err != nil {
			logger.Warn(fmt.Sprintf("error backoff skipped: %v", err))
			marks = nil
		}
	}

	if err = h.enqueueEntries(ctx, entries, &pushPass{
		dry: dry, forced: forced, rep: rep, items: items, ignore: ignoreSet, adopted: adopted, marks: marks, slot: slot,
	}); err != nil {
		return err
	}

	// форс-прогон тоже чистит: список торрентов он берет целиком тем же collectEntries,
	// а пользователь жмет кнопку как раз после удаления раздачи
	if !dry {
		if partial {
			logger.Warn("history purge skipped: partial torrent list")
		} else {
			h.purgeStaleHistory(ctx, rawEntries, items)
		}
	}
	return nil
}

func (h *handler) collectEntries(ctx context.Context) (rawEntries, entries []*model.Entry, adopted map[*model.Entry]struct{}, partial bool, err error) {
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
	// те же указатели идут дальше в дедупликацию, очередь и чистку истории.
	// Усыновляем все: чистке нужен адрес каждого
	adopted = make(map[*model.Entry]struct{})
	for _, e := range rawEntries {
		if e.URL != "" {
			continue
		}
		url, adoptErr := h.trackerService.Adopt(ctx, e.Name, e.Hash)
		if adoptErr != nil {
			logger.Warn(fmt.Sprintf("adopt %s: %v", e.Name, adoptErr))
			partial = true
			continue
		}
		e.URL = url
		if url != "" {
			adopted[e] = struct{}{}
		}
	}
	entries = helper.DedupeEntriesByName(rawEntries)
	return
}

func (h *handler) storeSiblings(rawEntries []*model.Entry, partial bool) {
	byURL := make(map[string][]string)
	present := make(map[string]struct{}, len(rawEntries))
	for _, e := range rawEntries {
		present[e.Hash] = struct{}{}
		if e.URL != "" {
			key := storagePkg.URLHash(e.URL)
			byURL[key] = append(byURL[key], e.Hash)
		}
	}
	h.siblings.Store(&byURL)
	// в неполном списке торрента может не быть временно, его повтор не теряем
	if partial {
		return
	}
	h.retry.Range(func(hash, _ any) bool {
		if _, ok := present[hash.(string)]; !ok {
			h.retry.Delete(hash)
		}
		return true
	})
}

func (h *handler) markRetry(ctx context.Context, e *model.Entry, cause error) {
	if len(h.siblingHashes(e)) == 0 {
		return
	}
	st := retryState{urlHash: e.URLHash, fails: 1}
	if v, ok := h.retry.Load(e.Hash); ok {
		st.fails = v.(retryState).fails + 1
	}
	// автоигнор сюда не дойдет: удачные проверки соседа сбрасывают общий
	// error_streak строки, поэтому повторы считаем сами
	limit := h.config.ErrorStreakThreshold
	if limit < 1 {
		limit = 3
	}
	if st.fails >= limit {
		h.retry.Delete(e.Hash)
		logger.Warn(fmt.Sprintf("update of %s (%s) failed %d times in a row, not retried until the topic changes again: %v",
			e.Name, e.URL, st.fails, cause))
		// флаг мог вернуть сосед при сохранении, без снятия повтор пойдет по кругу
		if !h.retryPending(e.URLHash) {
			if _, err := h.storage.SetForceCheck(ctx, e.URLHash, false); err != nil {
				logger.Error(fmt.Sprintf("force_check clear failed: %v", err))
			}
		}
		return
	}
	// сосед по строке истории сохранит новую дату и срок проверки и спрячет
	// неудачное обновление этого торрента; force_check вернет его в проверку
	h.retry.Store(e.Hash, st)
	h.forceRow(ctx, e.URLHash)
}

func (h *handler) deferRetry(ctx context.Context, e *model.Entry) {
	if len(h.siblingHashes(e)) == 0 {
		return
	}
	// счетчик попыток не растет: занятый торрент ждет сколько нужно, а сосед иначе спрячет его обновление
	st := retryState{urlHash: e.URLHash}
	if v, ok := h.retry.Load(e.Hash); ok {
		st = v.(retryState)
	}
	h.retry.Store(e.Hash, st)
	h.forceRow(ctx, e.URLHash)
}

func (h *handler) forceRow(ctx context.Context, urlHash string) {
	if _, err := h.storage.SetForceCheck(ctx, urlHash, true); err != nil {
		logger.Error(fmt.Sprintf("force_check set failed: %v", err))
	}
}

func (h *handler) retryPending(urlHash string) (pending bool) {
	h.retry.Range(func(_, v any) bool {
		pending = v.(retryState).urlHash == urlHash
		return !pending
	})
	return
}

func (h *handler) siblingHashes(e *model.Entry) (out []string) {
	byURL := h.siblings.Load()
	if byURL == nil || e.URL == "" {
		return nil
	}
	for _, hash := range (*byURL)[storagePkg.URLHash(e.URL)] {
		if hash != e.Hash {
			out = append(out, hash)
		}
	}
	return
}

func (h *handler) attachComment(ctx context.Context, e *model.Entry) {
	// занято заменой этого торрента: не помечаем, следующий проход повторит, горутина push не ждет
	unlock, ok := h.state.TryLockTorrent(e.Hash)
	if !ok {
		return
	}
	defer unlock()
	if _, done := h.attached.LoadOrStore(e.Hash, struct{}{}); done {
		return
	}
	hash, link := e.Hash, e.URL
	fetch := func(ctx context.Context) ([]byte, error) {
		id := h.trackerService.GetID(link)
		tc, err := h.trackerService.GetCachedClient(ctx, id)
		if err != nil {
			return nil, err
		}
		b, err := h.trackerService.Response(ctx, id, tc.DownloadLink(hash), true)
		if err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	if err := h.manager.AttachComment(ctx, hash, link, fetch); err != nil {
		logger.Warn(fmt.Sprintf("attach link to %s: %v", e.Name, err))
		return
	}
	logger.Info(fmt.Sprintf("attached link to %s: %s", e.Name, link))
}

func (h *handler) purgeDuplicateNames(ctx context.Context, entries []*model.Entry) {
	live := make(map[string][]string)
	for _, e := range entries {
		if e.URL == "" {
			continue
		}
		k := storageModel.FoldName(e.Name)
		if k == "" {
			continue
		}
		hash := storagePkg.URLHash(e.URL)
		if !slices.Contains(live[k], hash) {
			live[k] = append(live[k], hash)
		}
	}
	if err := h.storage.PurgeDuplicateNames(ctx, live); err != nil {
		logger.Warn(fmt.Sprintf("purge duplicate names: %v", err))
	}
}

func (h *handler) enqueueEntries(ctx context.Context, entries []*model.Entry, p *pushPass) (err error) {
	queue := h.idx
	if p.forced != nil {
		queue = h.pri
	}
	backedOff := 0
	defer func() {
		if backedOff > 0 {
			logger.Info(fmt.Sprintf("%d entries in error backoff", backedOff))
		}
	}()
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
		// запись комментария трогает клиент (Transmission перезаливает торрент), поэтому
		// только для тех, за кем следим. До проверки срока, как и раньше
		if _, ok := p.adopted[entries[i]]; ok && !p.dry {
			if _, ignored := p.ignore[storagePkg.URLHash(entries[i].URL)]; !ignored {
				h.attachComment(ctx, entries[i])
			}
		}
		if err = h.buildEntry(ctx, entries[i], p.dry, p.items); errors.Is(err, errSkipped) {
			continue
		} else if err != nil {
			return err
		}
		if h.backedOff(p, entries[i]) {
			backedOff++
			continue
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
		entries[i].PassSlot = p.slot
		normal := p.forced == nil && !p.dry
		h.state.Begin(h.gen)
		if normal {
			h.state.BeginNormal(h.gen)
		}
		h.state.Enqueue(h.gen, entries[i].URLHash)
		p.rep.add()
		select {
		case <-ctx.Done():
			h.state.Unqueue(h.gen, entries[i].URLHash)
			h.endTask(normal)
			p.rep.done()
			return nil
		case queue[t] <- &task{entry: entries[i], report: p.rep, seq: h.taskSeq.Add(1), normal: normal}:
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
	// чистим всю историю, не только с наступившим recheck_at. Торрент удалили из qBittorrent,
	// следить не за чем, а своей очереди такая запись ждала бы месяцами
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
	// лок на трекер: разные трекеры идут параллельно, записи одного трекера по очереди
	mu := h.mx.Mutex(ttid)
	mu.Lock()
	defer mu.Unlock()

	if tc, err = h.trackerService.GetCachedClient(ctx, ttid); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("processing interrupted")
			return nil
		}
		h.logCheckStreak(ctx, e, ttid, "error", err.Error(), true, rep)
		return
	}
	pc, err := h.checkPage(ctx, ttid, tc, e, rep)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("processing interrupted")
			return nil
		}
		if rep == nil && serverError(err) {
			h.state.PageServerError(ttid, e.URLHash, time.Now())
		}
		h.logCheckStreak(ctx, e, ttid, "error", err.Error(), isTrackerWide(err), rep)
		return
	}
	if rep == nil {
		h.state.PageOK(ttid, e.URLHash)
	}
	if pc.dateMissing {
		if rep == nil {
			// гостевая страница без ссылки или даты: возможно, сессия мертва, пусть следующий запрос войдет заново
			if f, ok := h.trackerService.(interface{ ForceLoginNext(ttid int) }); ok {
				f.ForceLoginNext(ttid)
			}
		}
		// флаг держит повтор неудачного обновления торрента этой строки, его не снимаем
		if rep == nil && !h.retryPending(e.URLHash) {
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
	if rep == nil && e.Unhealthy {
		// клиент проверяет, переносит или ругается на старый торрент: замену не трогаем, следующий проход повторит
		logger.Info(fmt.Sprintf("%s: %s", errTorrentBusy, e.Name))
		h.deferRetry(ctx, e)
		h.logCheckStreak(ctx, e, ttid, "error", errTorrentBusy.Error(), isTrackerWide(errTorrentBusy), rep)
		return nil
	}
	if err = h.submitLocked(ctx, ttid, tc, e, rep); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("processing interrupted")
			return nil
		}
		if errors.Is(err, model.ErrAlreadyAdded) {
			logger.Info(fmt.Sprint("already in qbittorrent, skipped: ", e.Name))
			// историю пишем как при обычном unchanged: торрент с этим инфохешем уже стоит,
			// релиз актуален. Иначе force_check останется взведенным со старой датой,
			// и каждый прогон будет упираться в already added
			if rep == nil {
				if saveErr := h.saveHistory(ctx, ttid, e, pc.date, false); saveErr != nil {
					h.logCheck(ctx, e, ttid, "error", saveErr.Error(), rep)
					return saveErr
				}
			}
			h.logCheck(ctx, e, ttid, "unchanged", "", rep)
			return nil
		}
		if errors.Is(err, model.ErrHashUnchanged) {
			// инфохеш не поменялся, торрент уже раздается, перезаливать не надо. Но дату в истории
			// фиксируем иначе следующий прогон снова увидит новую дату темы и пойдет по кругу
			logger.Info(fmt.Sprint("hash unchanged, torrent kept: ", e.Name))
			if rep == nil {
				if saveErr := h.saveHistory(ctx, ttid, e, pc.date, false); saveErr != nil {
					h.logCheck(ctx, e, ttid, "error", saveErr.Error(), rep)
					return saveErr
				}
			}
			// статус как у обычного unchanged: перезаливки не было, в списке обновленных
			// этой записи делать нечего
			h.logCheck(ctx, e, ttid, "unchanged", "", rep)
			return nil
		}
		err = fmt.Errorf("%s %w", entryTopicURL(tc, e), err)
		if rep == nil {
			h.markRetry(ctx, e, err)
		}
		h.logCheckStreak(ctx, e, ttid, "error", err.Error(), isTrackerWide(err), rep)
		return
	}
	if rep == nil {
		if pc.pageDated {
			if relErr := h.storage.AddReleaseTime(ctx, e.URLHash, *pc.date, forecastSamples); relErr != nil {
				logger.Warn(fmt.Sprintf("release time of %s not saved: %v", e.Name, relErr))
			}
		}
		if err = h.saveHistory(ctx, ttid, e, pc.date, true); err != nil {
			h.logCheck(ctx, e, ttid, "error", err.Error(), rep)
			return
		}
	}
	if rep == nil {
		logger.Info(fmt.Sprint("updated: ", e.Name))
		h.notifier.Notify(notify.UpdatedEvent(e.Name, e.URL, e.Poster))
	}
	h.logCheck(ctx, e, ttid, "updated", "", rep)

	return
}

func (h *handler) logCheck(ctx context.Context, e *model.Entry, ttid int, status, errMsg string, rep *dryReport) {
	h.logCheckStreak(ctx, e, ttid, status, errMsg, false, rep)
}

func isTrackerWide(err error) bool {
	// отказ всего трекера не вина записи: статус error пишем, серию не двигаем
	return errors.Is(err, client.ErrTrackerUnavailable) || errors.Is(err, helper.ErrCircuitOpen) || errors.Is(err, model.ErrReplacePending) || errors.Is(err, errTorrentBusy) || serverError(err)
}

func serverError(err error) bool {
	var se *client.StatusError
	return errors.As(err, &se) && se.Code >= 500
}

func (h *handler) logCheckStreak(ctx context.Context, e *model.Entry, ttid int, status, errMsg string, keepStreak bool, rep *dryReport) {
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
	metrics.Checks.Inc(trackerLabel(ttid), status)
	if status == "error" && !keepStreak {
		h.notifier.Notify(notify.CheckFailedEvent(e.Name, e.URL, errMsg))
	}
	streak, logErr := h.storage.AddCheckLog(ctx, &storageModel.CheckLogEntry{
		URLHash:       e.URLHash,
		ItemName:      e.Name,
		ItemURL:       e.URL,
		TrackerTypeID: ttid,
		Status:        status,
		Error:         errMsg,
		KeepStreak:    keepStreak,
	})
	if logErr != nil {
		logger.Error(fmt.Sprintf("check_log write failed: %v", logErr))
		return
	}
	if status == "error" && !keepStreak && h.config.ErrorStreakThreshold >= 1 && streak >= h.config.ErrorStreakThreshold {
		if ignoreErr := h.storage.AddIgnore(ctx, e.URLHash, e.Name, e.URL); ignoreErr != nil {
			logger.Error(fmt.Sprintf("auto-ignore failed for %s: %v", e.Name, ignoreErr))
		} else {
			logger.Warn(fmt.Sprintf("%s: %d errors in a row, sent to ignore", e.Name, streak))
			h.notifier.Notify(notify.BrokenEvent(e.Name, e.URL, streak))
		}
	}
}

type pageCheck struct {
	changed     bool
	dateMissing bool
	date        *time.Time
	sample      string
	pageDated   bool
}

func (h *handler) checkPage(ctx context.Context, ttid int, tc client.IClient, e *model.Entry, rep *dryReport) (pc pageCheck, err error) {
	var (
		cur        *time.Time
		b          *bytes.Buffer
		downloadID string
		encoded    = tc.GetProxyType() != proxy.TypeNone
	)
	start := time.Now()
	if b, err = h.getPage(ctx, ttid, e.URL); err != nil {
		return
	}
	pick := tc.PickTorrent(b, encoded, trackerModel.Held{
		URL: e.URL, TopicID: e.TopicID, Stored: e.Update, Hash: e.Hash, Name: e.Name, Timestamp: e.Timestamp, Siblings: h.siblingHashes(e),
	})
	if pick.Miss != "" {
		// как при ненайденной дате: релиз не определен, значит unknown, а не тихое "без изменений"
		logger.Warn(fmt.Sprintf("torrent not picked for %s: %s", e.Name, pick.Miss))
		pc.dateMissing = true
		pc.sample = pick.Miss
		return
	}
	if downloadID = pick.DownloadID; downloadID == "" {
		downloadID = tc.ParseDownloadID(b, encoded)
	}

	// у части трекеров id вложения не равен id темы из комментария торрента. Дата
	// от выбора трекера главнее смены id: id в общей строке истории мог записать
	// соседний торрент того же релиза
	if downloadID != "" && (!e.Update || pick.Date != nil) {
		e.TopicID = downloadID
	}
	if e.Update && downloadID != "" && e.TopicID != downloadID {
		e.TopicID = downloadID
		curPtr := time.Now()
		cur = &curPtr
	} else if cur = pick.Date; cur == nil {
		if cur = tc.ParseDate(b, encoded); cur == nil {
			logger.Warn(fmt.Sprintf("current time not found for %s", e.Name))
			pc.dateMissing = true
			pc.sample = pageSample(b, encoded)
			return
		}
	}
	pc.changed = e.Timestamp.Before(*cur)
	pc.date = cur
	// даты из time.Now() (смена id темы, трекеры без даты на странице) это время проверки,
	// а не выхода релиза, в прогноз они не идут
	pc.pageDated = cur.Before(start)
	if pc.changed {
		e.Magnet = tc.ParseMagnet(b, encoded)
		e.Poster = tc.ParsePoster(b, encoded)
		return
	}
	if rep == nil {
		err = h.saveHistory(ctx, ttid, e, cur, false)
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

func (h *handler) submitLocked(ctx context.Context, ttid int, tc client.IClient, e *model.Entry, rep *dryReport) error {
	// замена и дописывание комментария одного торрента не пересекаются, это же держит web-перекачка
	if rep == nil {
		defer h.state.LockTorrent(e.Hash)()
	}
	return h.submit(ctx, ttid, tc, e, rep)
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

	// магнит пробуем первым и клиент загрузок не трогаем. Решить "магнит не вышел, идем
	// на трекер" надо до того, как manager.Submit удалит старый торрент и добавит новый
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
	// свое содержимое трекера не трогаем: ссылку пишем только в файл без
	// комментария (AniLiberty), инфохеш от этого не меняется
	if opts.Comment != "" {
		if cur, cerr := torrentmeta.Comment(b.Bytes()); cerr == nil && cur == "" {
			if withLink, serr := torrentmeta.SetComment(b.Bytes(), opts.Comment); serr == nil {
				b = bytes.NewBuffer(withLink)
			}
		}
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
		// нулевой или отрицательный таймаут в конфиге не должен вешать раздачу навсегда,
		// берем минуту
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

func (h *handler) saveHistory(ctx context.Context, ttid int, e *model.Entry, cur *time.Time, released bool) (err error) {
	now := time.Now()
	slot := e.PassSlot
	if slot.IsZero() {
		slot = h.passSlot(now)
	}
	samples, readErr := h.storage.RecentReleaseTimes(ctx, e.URLHash, now.Add(-forecastWindow), forecastSamples)
	if readErr != nil {
		logger.Warn(fmt.Sprintf("release times of %s not read: %v", e.Name, readErr))
		samples = nil
	}
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
		RecheckAt:     h.getRecheckAt(cur, slot, now, released, samples),
	}); err != nil {
		return
	}
	// сохранение сбрасывает force_check, а неудачное обновление соседа еще ждет повтора
	h.retry.Delete(e.Hash)
	if h.retryPending(e.URLHash) {
		h.forceRow(ctx, e.URLHash)
	}
	return
}

func (h *handler) passSlot(now time.Time) time.Time {
	return slotFloor(now, h.config.StartHour, h.config.ListRefreshPerDay, h.config.Location)
}

func (h *handler) getRecheckAt(tm *time.Time, slot, now time.Time, released bool, samples []time.Time) (result *time.Time) {
	days := h.recheckDays(tm, now)
	tt := slot.AddDate(0, 0, days)
	if h.config.ListRefreshPerDay > 0 {
		if mean, late, ok := forecast(samples, scheduleLocation(h.config.Location)); ok {
			tt = h.placeForecast(slot, days, mean, late, released)
		}
	}
	return &tt
}

func (h *handler) recheckDays(tm *time.Time, now time.Time) int {
	if tm == nil {
		return h.config.ThresholdMonth
	}
	d := now.Sub(*tm)
	switch {
	case tm.Year() == 1, d < durationMonth:
		return h.config.ThresholdMonth
	case d < durationYear/2:
		return h.config.ThresholdHalfYear
	case d < durationYear:
		return h.config.ThresholdYear
	}
	return h.config.ThresholdOld
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

func trackerLabel(ttid int) string {
	if key := registry.Key(ttid); key != "" {
		return key
	}
	return "unknown"
}
