package runner

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"trackerr/internal/model"
	"trackerr/internal/model/proxy"
	"trackerr/internal/model/runner"
	"trackerr/internal/system/logger"
	"trackerr/internal/tracker"
	"trackerr/internal/tracker/client"
)

const (
	durationDay   = time.Hour * 24
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
}

type storage interface {
	GetItem(ctx context.Context, link string) (item *runner.HistoryItem, err error)
	SaveItem(ctx context.Context, item *runner.HistoryItem) (err error)
}

type IHandler interface {
	Run(ctx context.Context)
}

type handler struct {
	mx             *sync.RWMutex
	i              map[int]chan *model.Entry
	offset         int
	config         model.HandlerConfig
	entries        []*model.Entry
	manager        manager
	storage        storage
	trackerService trackerService
}

func (h *handler) Run(ctx context.Context) {
	var (
		ch    = make(chan *model.Entry)
		cherr = make(chan error)
		wt    = time.NewTimer(0)
		pt    = time.NewTimer(0)
		delay = time.Duration(h.config.WorkerDelay) * time.Minute
		err   error
	)

	wt.Stop()

	defer close(ch)
	defer close(cherr)

	if h.config.ThresholdMonth == 0 || h.config.ThresholdHalfYear == 0 || h.config.ThresholdYear == 0 || h.config.ThresholdOld == 0 {
		logger.Error("min thresholds are not set")
		return
	}
	h.mx = &sync.RWMutex{}
	h.initTrackerChannels()
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
				pt.Reset(durationDay)
				wt.Reset(0)
			}()
		case <-wt.C:
			for ttid := range h.i {
				go func(ttid int) {
					for j := 0; j < h.chunkSize(ttid); j++ {
						if err = h.worker(ctx, ttid, <-h.i[ttid]); err != nil {
							cherr <- err
						}
					}
				}(ttid)
			}
			wt.Reset(delay)
		}
	}
}

func (h *handler) initTrackerChannels() {
	h.i = make(map[int]chan *model.Entry, len(h.trackerService.CachedList()))
	for ttid := range h.trackerService.CachedList() {
		h.i[ttid] = make(chan *model.Entry)
	}
}

func (h *handler) push(ctx context.Context) (err error) {
	var (
		i       int
		t       int
		entries []*model.Entry
	)
	if entries, err = h.manager.GetEntries(ctx, model.FilterOptions{Tag: h.config.WatchTag}); err != nil {
		err = fmt.Errorf("get entries error: %w", err)
		return
	}
	for i = range entries {
		if err = h.buildEntry(ctx, entries[i]); err == errSkipped {
			err = nil
			continue
		} else if err != nil {
			return
		}
		go func(i int) {
			if t = h.trackerService.GetID(entries[i].URL); t == 0 {
				return
			}
			h.i[t] <- entries[i]
		}(i)
	}
	return
}

func (h *handler) buildEntry(ctx context.Context, e *model.Entry) (err error) {
	var hi *runner.HistoryItem

	if hi, err = h.checkHistory(ctx, e.URL); err != nil {
		return
	}
	e.URLHash = hi.URLHash

	if hi.Exists {
		e.Update = true
		e.Timestamp = hi.RecheckAt
	}
	return
}

func (h *handler) checkHistory(ctx context.Context, link string) (hi *runner.HistoryItem, err error) {
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

func (h *handler) chunkSize(ttid int) (cs int) {
	hourLimit := h.trackerService.GetHourLimit(ttid)
	freq := h.config.WorkerDelay
	cs = int(math.Floor(float64(hourLimit*freq) / 60))

	if cs == 0 {
		cs = 1
	}
	return
}

func (h *handler) worker(ctx context.Context, ttid int, e *model.Entry) (err error) {
	var (
		updated bool
		tc      tracker.IHttpClient
	)
	logger.Info(fmt.Sprint("processing: ", e.Name))
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
		return
	}
	logger.Info(fmt.Sprint("updated: ", e.Name))

	return
}

func (h *handler) checkPage(ctx context.Context, ttid int, tc tracker.IHttpClient, e *model.Entry) (res bool, err error) {
	var cur *time.Time

	if cur, err = h.getPageDate(ctx, ttid, tc, e); err != nil {
		return
	}
	// TODO: add check count of updates in DB
	if err = h.saveHistory(ctx, ttid, e, cur); err != nil {
		return
	}
	res = e.Timestamp.Before(*cur)

	return
}

func (h *handler) getPageDate(ctx context.Context, ttid int, tc tracker.IHttpClient, e *model.Entry) (cur *time.Time, err error) {
	var b *bytes.Buffer

	// TODO: add check url for tracker domain
	b, err = h.trackerService.Response(ctx, ttid, e.URL, false)
	if err != nil {
		return
	} else if b == nil || b.Len() < 50 {
		err = fmt.Errorf("no page data")
		return
	}
	if cur = tc.ParseDate(b, tc.ProxyType() != proxy.TypeNone); cur == nil {
		err = fmt.Errorf("current time not found")
	}
	return
}

func (h *handler) submit(ctx context.Context, ttid int, tc tracker.IHttpClient, e *model.Entry) (err error) {
	var b *bytes.Buffer

	b, err = h.trackerService.Response(ctx, ttid, tc.DownloadLink(e.TopicID), true)
	if err != nil {
		return
	} else if b == nil || b.Len() < 50 {
		err = fmt.Errorf("no file data")
		return
	}
	if strings.Contains(b.String(), "<html") {
		err = fmt.Errorf("unexpected html response on download")
		return
	}
	// TODO: add more attributes
	if err = h.manager.Submit(ctx, b, model.ManagerAddFileOptions{
		Name:           e.Name,
		Hash:           e.Hash,
		Category:       e.Category,
		Tag:            e.Tag,
		AutoManaged:    e.AutoManaged,
		MaxRatio:       e.MaxRatio,
		MaxSeedingTime: e.MaxSeedingTime,
	}); err != nil {
		err = fmt.Errorf("submit error: %w", err)
	}
	return
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
