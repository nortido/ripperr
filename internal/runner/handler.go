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
)

var (
	ErrSkipped = fmt.Errorf("skipped")
)

type trackerClient interface {
	CacheAll(ctx context.Context) (err error)
	ClientByLink(ctx context.Context, link string) (tc tracker.IHttpClient, err error)
	Response(ctx context.Context, link string, tc tracker.IHttpClient, download bool) (buf *bytes.Buffer, err error)
}

type manager interface {
	GetEntries(ctx context.Context, opts model.FilterOptions) (entries []*model.Entry, err error)
	Submit(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
}

type database interface {
	GetItem(ctx context.Context, link string) (item *runner.HistoryItem, err error)
	SaveItem(ctx context.Context, item *runner.HistoryItem) (err error)
}

type IHandler interface {
	Run(ctx context.Context)
}

type handler struct {
	m        sync.RWMutex
	offset   int
	config   model.HandlerConfig
	entries  []*model.Entry
	tracker  trackerClient
	manager  manager
	database database
}

func (h *handler) Run(ctx context.Context) {
	var (
		e     *model.Entry
		ch    = make(chan *model.Entry)
		cherr = make(chan error)
		ge    = make(chan struct{})
		tmr   = time.NewTimer(0)
		err   error
	)

	defer close(ch)
	defer close(cherr)
	defer close(ge)

	for {
		select {
		case <-ctx.Done():
			return
		case err = <-cherr:
			fmt.Println(err)
		case <-ge:
			go func() {
				if err = h.getEntries(ctx); err != nil {
					cherr <- err
				}
				tmr.Reset(0)
			}()
		case <-tmr.C:
			go func() {
				var reset bool
				if reset, err = h.pushEntries(ctx, ch); err != nil {
					cherr <- err
				} else if reset {
					ge <- struct{}{}
				}
				tmr.Reset(h.config.ProcessDelay)
			}()
		case e = <-ch:
			go func(e *model.Entry) {
				if err = h.processEntry(ctx, *e); err != nil {
					cherr <- err
				}
			}(e)
		}
	}
}

func (h *handler) getEntries(ctx context.Context) (err error) {
	if h.entries, err = h.manager.GetEntries(ctx, model.FilterOptions{Tag: "watch"}); err != nil {
		err = fmt.Errorf("get torrents error: %w", err)
		return
	}
	h.m.Lock()
	defer h.m.Unlock()

	if h.config.ChunkSize == 0 {
		h.config.ChunkSize = 1
	}
	h.offset = 0

	return
}

func (h *handler) pushEntries(ctx context.Context, ch chan *model.Entry) (reset bool, err error) {
	var pushed int

	if h.offset >= len(h.entries) {
		reset = true
		return
	}
	for _, e := range h.entries[h.offset:] {
		h.offset++
		if err = h.setEntry(ctx, e); err == ErrSkipped {
			continue
		} else if err != nil {
			fmt.Println(err)
			return
		}
		pushed++

		if pushed > h.config.ChunkSize {
			pushed = 0
			break
		}
		ch <- e
	}
	err = nil

	return
}

func (h *handler) setEntry(ctx context.Context, e *model.Entry) (err error) {
	var hi *runner.HistoryItem

	if hi, err = h.checkHistoryDate(ctx, e); err != nil {
		return
	}
	e.URLHash = hi.URLHash
	e.Update = hi.Exists

	if hi.UpdatedAt != nil {
		e.Timestamp = hi.UpdatedAt.Unix()
	}
	return
}

func (h *handler) checkHistoryDate(ctx context.Context, e *model.Entry) (hi *runner.HistoryItem, err error) {
	// TODO: cache history
	if hi, err = h.database.GetItem(ctx, e.URL); err == pgx.ErrNoRows {
		err = nil
	} else if err != nil {
		return
	}
	// TODO: save expire date instead of updated_at
	if hi.Exists && time.Now().UTC().Unix() < (hi.UpdatedAt.Unix()+h.config.MinDateDiff) {
		// TODO: add log instead of print
		err = ErrSkipped
	}
	return
}

func (h *handler) processEntry(ctx context.Context, e model.Entry) (err error) {
	var tc tracker.IHttpClient

	if h.config.MinDateDiff == 0 {
		err = fmt.Errorf("min threshold not set")
		return
	}
	fmt.Println("processing entry: ", e.Name)

	if tc, err = h.tracker.ClientByLink(ctx, e.URL); err != nil {
		return
	}

	// TODO: remove param pointers OR do multithreading
	h.m.Lock()
	defer h.m.Unlock()
	if err = h.checkPageUpdate(ctx, tc, &e); err != nil {
		return
	}
	if err = h.submit(ctx, tc, &e); err == nil {
		fmt.Println("saved: ", e.Name)
	}
	return
}

func (h *handler) checkPageUpdate(ctx context.Context, tc tracker.IHttpClient, e *model.Entry) (err error) {
	var cur *time.Time

	if cur, err = h.getPageDate(ctx, tc, e); err != nil {
		return
	}
	// TODO: add check count of updates in DB
	// TODO: save page date and expire date
	if err = h.saveHistory(ctx, e); err != nil {
		return
	}
	if (e.Timestamp + h.config.MinDateDiff) > cur.Unix() {
		err = fmt.Errorf("page is not updated")
		return
	}
	return
}

func (h *handler) getPageDate(ctx context.Context, tc tracker.IHttpClient, e *model.Entry) (cur *time.Time, err error) {
	var b *bytes.Buffer

	b, err = h.tracker.Response(ctx, tc.TopicLink(e.TopicID), tc, false)
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

func (h *handler) submit(ctx context.Context, tc tracker.IHttpClient, e *model.Entry) (err error) {
	var b *bytes.Buffer

	b, err = h.tracker.Response(ctx, tc.DownloadLink(e.TopicID), tc, true)
	if err != nil {
		return
	} else if b == nil || b.Len() < 50 {
		err = fmt.Errorf("no file data")
		return
	}
	// TODO: add more attributes
	err = h.manager.Submit(ctx, b, model.ManagerAddFileOptions{
		Name:     e.Name,
		Hash:     e.Hash,
		Category: e.Category,
		//Tag:            e.Tag,
		AutoManaged:    e.AutoManaged,
		MaxRatio:       e.MaxRatio,
		MaxSeedingTime: e.MaxSeedingTime,
	})
	return
}

func (h *handler) saveHistory(ctx context.Context, e *model.Entry) (err error) {
	if err = h.database.SaveItem(ctx, &runner.HistoryItem{
		URLHash:  e.URLHash,
		TopicID:  e.TopicID,
		Category: e.Category,
		Tag:      e.Tag,
		Name:     e.Name,
		URL:      e.URL,
		Exists:   e.Update,
	}); err != nil {
		return
	}
	return
}

func NewRunner(tracker trackerClient, manager manager, database database, config model.HandlerConfig) IHandler {
	return &handler{
		tracker:  tracker,
		manager:  manager,
		config:   config,
		database: database,
	}
}
