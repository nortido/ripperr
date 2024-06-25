package runner

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"trackerr/internal/model"
	"trackerr/internal/model/proxy"
	"trackerr/internal/model/runner"
	"trackerr/internal/tracker"
	"trackerr/internal/tracker/client"
)

var (
	ErrSkipped = fmt.Errorf("skipped")
)

type trackerClient interface {
	InitCache(ctx context.Context) (err error)
	CachedList() (res map[int]client.ITracker)
	GetID(url string) (id int)
	GetCached(ctx context.Context, ttid int) (tc tracker.IHttpClient, err error)
	Response(ctx context.Context, link string, tc tracker.IHttpClient, download bool) (buf *bytes.Buffer, err error)
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
	m       sync.RWMutex
	i       map[int]chan model.Entry
	offset  int
	config  model.HandlerConfig
	entries []*model.Entry
	tracker trackerClient
	manager manager
	storage storage
}

func (h *handler) Run(ctx context.Context) {
	var (
		ch    = make(chan *model.Entry)
		cherr = make(chan error)
		ge    = make(chan struct{})
		wt    = time.NewTimer(0)
		pt    = time.NewTimer(0)
		err   error
	)

	wt.Stop()

	defer close(ch)
	defer close(cherr)
	defer close(ge)

	h.initTrackerChannels()
	for {
		select {
		case <-ctx.Done():
			return
		case err = <-cherr:
			fmt.Println(err)
		case <-pt.C:
			go func() {
				if err = h.push(ctx); err != nil {
					cherr <- err
				}
				pt.Reset(time.Hour * 24)
				wt.Reset(0)
			}()
		case <-wt.C:
			for ttid := range h.i {
				go func(ttid int) {
					for j := 0; j < h.config.WorkerChunkLimit; j++ {
						go func() {
							if err = h.worker(ctx, ttid, <-h.i[ttid]); err != nil {
								cherr <- err
							}
						}()
					}
				}(ttid)
			}
			wt.Reset(h.config.WorkerDelay)
		}
	}
}

func (h *handler) initTrackerChannels() {
	var (
		ttid int
	)
	h.i = make(map[int]chan model.Entry, len(h.tracker.CachedList()))
	for ttid = range h.tracker.CachedList() {
		h.i[ttid] = make(chan model.Entry)
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
	//TODO: add separate channel for each tracker
	for i = range entries {
		if err = h.buildEntry(ctx, entries[i]); err == ErrSkipped {
			err = nil
			continue
		} else if err != nil {
			return
		}
		go func(i int) {
			t = h.tracker.GetID(entries[i].URL)
			h.i[t] <- *entries[i]
		}(i)
	}
	return
}

func (h *handler) buildEntry(ctx context.Context, e *model.Entry) (err error) {
	var hi *runner.HistoryItem

	if hi, err = h.checkHistory(ctx, e); err != nil {
		return
	}
	e.URLHash = hi.URLHash
	e.Update = hi.Exists

	if hi.UpdatedAt != nil {
		e.Timestamp = hi.UpdatedAt.Unix()
	}
	return
}

func (h *handler) checkHistory(ctx context.Context, e *model.Entry) (hi *runner.HistoryItem, err error) {
	// TODO: cache history
	if hi, err = h.storage.GetItem(ctx, e.URL); err == pgx.ErrNoRows {
		err = nil
	} else if err != nil {
		return
	}
	// TODO: save expire date instead of updated_at
	// TODO: check if created date is below half year, then check only every month
	if hi.Exists && time.Now().UTC().Unix() < (hi.UpdatedAt.Unix()+h.config.MinDateDiff) {
		// TODO: add log instead of print
		err = ErrSkipped
	}
	return
}

func (h *handler) worker(ctx context.Context, ttid int, e model.Entry) (err error) {
	var tc tracker.IHttpClient

	if h.config.MinDateDiff == 0 {
		err = fmt.Errorf("min threshold not set")
		return
	}
	fmt.Println("processing entry: ", e.Name)

	if tc, err = h.tracker.GetCached(ctx, ttid); err != nil {
		return
	}
	// TODO: remove param pointers OR do multithreading

	if err = h.checkPage(ctx, ttid, tc, e); err != nil {
		return
	}
	if err = h.submit(ctx, ttid, tc, e); err == nil {
		fmt.Println("saved: ", e.Name)
	}
	return
}

func (h *handler) checkPage(ctx context.Context, ttid int, tc tracker.IHttpClient, e model.Entry) (err error) {
	var cur *time.Time

	if cur, err = h.getPageDate(ctx, tc, e); err != nil {
		return
	}
	// TODO: add check count of updates in DB
	// TODO: save page date (if already saved) and expire date
	if err = h.saveHistory(ctx, ttid, e); err != nil {
		return
	}
	if (e.Timestamp + h.config.MinDateDiff) > cur.Unix() {
		err = fmt.Errorf("is not updated: %s", e.Name)
		return
	}
	return
}

func (h *handler) getPageDate(ctx context.Context, tc tracker.IHttpClient, e model.Entry) (cur *time.Time, err error) {
	var b *bytes.Buffer

	// TODO: add check url for tracker domain
	b, err = h.tracker.Response(ctx, e.URL, tc, false)
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

func (h *handler) submit(ctx context.Context, ttid int, tc tracker.IHttpClient, e model.Entry) (err error) {
	var b *bytes.Buffer

	b, err = h.tracker.Response(ctx, tc.DownloadLink(e.TopicID), tc, true)
	if err != nil {
		return
	} else if b == nil || b.Len() < 50 {
		err = fmt.Errorf("no file data")
		return
	}
	// TODO: add more attributes
	if err = h.manager.Submit(ctx, b, model.ManagerAddFileOptions{
		Name:     e.Name,
		Hash:     e.Hash,
		Category: e.Category,
		//Tag:            e.Tag,
		AutoManaged:    e.AutoManaged,
		MaxRatio:       e.MaxRatio,
		MaxSeedingTime: e.MaxSeedingTime,
	}); err != nil {
		err = fmt.Errorf("submit error: %w", err)
	}
	return
}

func (h *handler) saveHistory(ctx context.Context, ttid int, e model.Entry) (err error) {
	if err = h.storage.SaveItem(ctx, &runner.HistoryItem{
		URLHash:       e.URLHash,
		TopicID:       e.TopicID,
		Category:      e.Category,
		Tag:           e.Tag,
		Name:          e.Name,
		URL:           e.URL,
		TrackerTypeID: ttid,
		Exists:        e.Update,
	}); err != nil {
		return
	}
	return
}

func NewRunner(tracker trackerClient, manager manager, storage storage, config model.HandlerConfig) IHandler {
	return &handler{
		tracker: tracker,
		manager: manager,
		storage: storage,
		config:  config,
	}
}
