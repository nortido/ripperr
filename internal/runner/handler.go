package runner

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/model"
	"trackerr/internal/model/proxy"
	"trackerr/internal/model/runner"
	"trackerr/internal/system/logger"
	"trackerr/internal/tracker"
	"trackerr/internal/tracker/client"
)

const (
	durationMonth = time.Hour * 24 * 30
	durationYear  = time.Hour * 24 * 365
)

var (
	errSkipped = fmt.Errorf("skipped")
)

type trackerService interface {
	BuildCache(ctx context.Context) (err error)
	CachedList() (res map[int]client.ITracker)
	GetID(url string) (id int)
	GetHourLimit(ttid int) (limit int)
	GetCachedClient(ctx context.Context, ttid int) (tc tracker.IHttpClient, err error)
	Response(ctx context.Context, ttid int, link string, download bool) (buf *bytes.Buffer, err error)
}

type manager interface {
	GetEntries(ctx context.Context, opts model.FilterOptions) (entries []*model.Entry, err error)
	Submit(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
	SubmitMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error)
}

type storage interface {
	GetItem(ctx context.Context, link string) (item *runner.HistoryItem, err error)
	SaveItem(ctx context.Context, item *runner.HistoryItem) (err error)
}

type IHandler interface {
	Run(ctx context.Context)
}

type handler struct {
	offset         int
	mx             *sync.RWMutex
	config         model.HandlerConfig
	entries        []*model.Entry
	idx            map[int]chan *model.Entry
	manager        manager
	storage        storage
	trackerService trackerService
}

func (h *handler) Run(ctx context.Context) {
	var err error

	cherr := make(chan error)
	pt := time.NewTimer(0)

	defer close(cherr)

	if h.config.ThresholdMonth == 0 || h.config.ThresholdHalfYear == 0 || h.config.ThresholdYear == 0 || h.config.ThresholdOld == 0 {
		logger.Error("min thresholds are not set")
		return
	}
	h.mx = &sync.RWMutex{}
	h.initTrackerChannels()
	for ttid := range h.idx {
		go h.trackerWorker(ctx, ttid, cherr)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err = <-cherr:
			logger.Error(err.Error())
		case <-pt.C:
			go func() {
				if err = h.push(ctx); err != nil {
					cherr <- err
				}
				pt.Reset(time.Duration(24/h.config.ListRefreshPerDay) * time.Hour)
			}()
		}
	}
}

func (h *handler) trackerWorker(ctx context.Context, ttid int, cherr chan error) {
	interval := h.requestInterval(ttid)
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-h.idx[ttid]:
			if err := h.worker(ctx, ttid, e); err != nil {
				select {
				case <-ctx.Done():
					return
				case cherr <- err:
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
}

func (h *handler) requestInterval(ttid int) time.Duration {
	limit := h.trackerService.GetHourLimit(ttid)
	if limit <= 0 {
		limit = 1
	}
	return time.Hour / time.Duration(limit)
}

func (h *handler) initTrackerChannels() {
	h.idx = make(map[int]chan *model.Entry, len(h.trackerService.CachedList()))
	for ttid := range h.trackerService.CachedList() {
		h.idx[ttid] = make(chan *model.Entry, 4096)
	}
}

func (h *handler) push(ctx context.Context) (err error) {
	var (
		entries []*model.Entry
		t       int
	)
	if entries, err = h.manager.GetEntries(ctx, model.FilterOptions{Tag: h.config.WatchTag}); err != nil {
		err = fmt.Errorf("get entries error: %w", err)
		return
	}
	for i := range entries {
		if entries[i].URL == "" {
			continue
		}
		if len(h.config.Categories) > 0 && !helper.InArray(entries[i].Category, h.config.Categories) {
			continue
		}
		if err = h.buildEntry(ctx, entries[i]); err == errSkipped {
			err = nil
			continue
		} else if err != nil {
			return
		}
		if t = h.trackerService.GetID(entries[i].URL); t == 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case h.idx[t] <- entries[i]:
		}
	}
	return
}

func (h *handler) buildEntry(ctx context.Context, e *model.Entry) (err error) {
	var hi *runner.HistoryItem

	if hi, err = h.getHistory(ctx, e.URL); err != nil {
		return
	}
	e.URLHash = hi.URLHash

	if hi.Exists {
		e.Update = true
		e.TopicID = hi.TopicID
		e.Timestamp = hi.CreatedAt
	}
	return
}

func (h *handler) getHistory(ctx context.Context, link string) (hi *runner.HistoryItem, err error) {
	// TODO: cache history
	if hi, err = h.storage.GetItem(ctx, link); err != nil {
		return
	}
	if !hi.Exists {
		return
	}
	if hi.RecheckAt != nil && time.Now().Before(*hi.RecheckAt) {
		err = errSkipped
	}
	if hi.TrackerTypeID == 0 {
		err = errSkipped
	}
	return
}

func (h *handler) worker(ctx context.Context, ttid int, e *model.Entry) (err error) {
	var (
		updated bool
		tc      tracker.IHttpClient
	)
	//logger.Info(fmt.Sprint("processing: ", e.Name))
	h.mx.Lock()
	defer h.mx.Unlock()

	if tc, err = h.trackerService.GetCachedClient(ctx, ttid); err != nil {
		return
	}
	if updated, err = h.checkPage(ctx, ttid, tc, e); err != nil {
		return
	}
	if !updated {
		logger.Info(fmt.Sprint("skipped: ", e.Name))
		return
	}
	if err = h.submit(ctx, ttid, tc, e); err != nil {
		err = fmt.Errorf("%s %w", entryTopicURL(tc, e), err)
		return
	}
	logger.Info(fmt.Sprint("updated: ", e.Name))

	return
}

func (h *handler) checkPage(ctx context.Context, ttid int, tc tracker.IHttpClient, e *model.Entry) (res bool, err error) {
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
	} else if cur, err = h.getDate(ctx, b, tc, e.Name); err != nil {
		return
	} else if cur == nil {
		return
	}
	// TODO: add check count of updates in DB
	if err = h.saveHistory(ctx, ttid, e, cur); err != nil {
		return
	}
	res = e.Timestamp.Before(*cur)
	if res {
		e.Magnet = tc.ParseMagnet(b, encoded)
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

func (h *handler) getDate(ctx context.Context, b *bytes.Buffer, tc tracker.IHttpClient, name string) (cur *time.Time, err error) {
	if cur = tc.ParseDate(b, tc.GetProxyType() != proxy.TypeNone); cur == nil {
		logger.Warn(fmt.Sprintf("current time not found for %s", name))
	}
	return
}

func (h *handler) submit(ctx context.Context, ttid int, tc tracker.IHttpClient, e *model.Entry) (err error) {
	opts := model.ManagerAddFileOptions{
		Name:           e.Name,
		Hash:           e.Hash,
		Category:       e.Category,
		Tag:            e.Tag,
		AutoManaged:    e.AutoManaged,
		MaxRatio:       e.MaxRatio,
		MaxSeedingTime: e.MaxSeedingTime,
		Comment:        entryTopicURL(tc, e),
	}
	if e.Magnet != "" {
		if err = h.manager.SubmitMagnet(ctx, e.Magnet, opts); err != nil {
			err = fmt.Errorf("submit magnet error: %w", err)
		}
		return
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

func entryTopicURL(tc tracker.IHttpClient, e *model.Entry) string {
	if e.URL != "" {
		return e.URL
	}
	return tc.TopicLink(e.TopicID)
}

func (h *handler) saveHistory(ctx context.Context, ttid int, e *model.Entry, cur *time.Time) (err error) {
	if err = h.storage.SaveItem(ctx, &runner.HistoryItem{
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
	var (
		tt = time.Now()
		d  = time.Since(*tm)
	)
	switch {
	case tm == nil, tm.Year() == 1, d < durationMonth:
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

func NewRunner(tracker trackerService, manager manager, storage storage, config model.HandlerConfig) IHandler {
	return &handler{
		trackerService: tracker,
		manager:        manager,
		storage:        storage,
		config:         config,
	}
}
