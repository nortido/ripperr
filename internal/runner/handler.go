package runner

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
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

	ch := make(chan *model.Entry)
	cherr := make(chan error)
	delay := time.Duration(h.config.WorkerDelay) * time.Minute
	pt := time.NewTimer(0)
	wt := time.NewTimer(0)

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
				pt.Reset(time.Duration(24/h.config.ListRefreshPerDay) * time.Hour)
				wt.Reset(0)
			}()
		case <-wt.C:
			for ttid := range h.idx {
				go func(ttid int) {
					for j := 0; j < h.chunkSize(ttid); j++ {
						if err = h.worker(ctx, ttid, <-h.idx[ttid]); err != nil {
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
	h.idx = make(map[int]chan *model.Entry, len(h.trackerService.CachedList()))
	for ttid := range h.trackerService.CachedList() {
		h.idx[ttid] = make(chan *model.Entry)
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
		go func(i int) {
			if t = h.trackerService.GetID(entries[i].URL); t == 0 {
				return
			}
			h.idx[t] <- entries[i]
		}(i)
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
		err = fmt.Errorf("%s %w", tc.DownloadLink(e.TopicID), err)
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
	)
	if b, err = h.getPage(ctx, ttid, e.URL); err != nil {
		return
	}
	downloadID = tc.ParseDownloadID(b, tc.GetProxyType() != proxy.TypeNone)

	if e.Update == true && downloadID != "" && e.TopicID != downloadID {
		e.TopicID = downloadID
		curPtr := time.Now()
		cur = &curPtr
	} else if cur, err = h.getDate(ctx, b, tc, e.Name); err != nil {
		return
	}
	// TODO: add check count of updates in DB
	if err = h.saveHistory(ctx, ttid, e, cur); err != nil {
		return
	}
	res = e.Timestamp.Before(*cur)

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
		err = fmt.Errorf("current time not found for %s", name)
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
