package runner

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"torr/internal/model"
	"torr/internal/tracker"
)

type trackerClient interface {
	CacheAll(ctx context.Context) (err error)
	ClientByLink(ctx context.Context, link string) (tc tracker.IHttpClient, err error)
	Response(ctx context.Context, link string, tc tracker.IHttpClient) (buf *bytes.Buffer, err error)
}

type manager interface {
	GetEntries(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error)
	Submit(ctx context.Context, e *model.Entry, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
}

type IHandler interface {
	Run(ctx context.Context)
}

type handler struct {
	m       sync.RWMutex
	offset  int
	limit   int
	config  model.HandlerConfig
	entries []*model.Entry
	tracker trackerClient
	manager manager
}

func (h *handler) Run(ctx context.Context) {
	var (
		e     *model.Entry
		ch    = make(chan *model.Entry)
		cherr = make(chan error)
		ge    = make(chan struct{})
		err   error
	)
	defer close(ch)
	defer close(cherr)
	defer close(ge)
	go func() {
		if err = h.getEntries(ctx); err != nil {
			cherr <- err
		}
	}()

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
			}()
		case <-time.After(h.config.ProcessTimeout):
			go func() {
				var reset bool
				if reset, err = h.pushEntries(ctx, ch); err != nil {
					cherr <- err
				} else if reset {
					ge <- struct{}{}
				}
			}()
		case e = <-ch:
			go func(e *model.Entry) {
				if err = h.processEntry(ctx, e); err != nil {
					cherr <- err
				}
			}(e)
		}
	}
}

func (h *handler) getEntries(ctx context.Context) (err error) {
	var entries []*model.Entry
	if entries, err = h.manager.GetEntries(ctx, model.TorrentFilterOptions{Tag: "watch"}); err != nil {
		err = fmt.Errorf("get torrents error: %w", err)
		return
	}
	h.m.Lock()
	defer h.m.Unlock()

	h.entries = entries
	h.limit = len(h.entries) / h.config.RunsPerDay
	if h.limit == 0 {
		h.limit = 1
	}
	h.offset = 0

	return
}

func (h *handler) pushEntries(ctx context.Context, ch chan *model.Entry) (reset bool, err error) {
	var limit = h.offset + h.limit

	if h.offset >= len(h.entries) {
		reset = true
		return
	}
	if len(h.entries) < h.offset+h.limit {
		limit = len(h.entries)
	}
	for _, e := range h.entries[h.offset:limit] {
		ch <- e
	}
	h.m.RLock()
	h.offset += h.limit
	h.m.RUnlock()

	return
}

func (h *handler) processEntry(ctx context.Context, e *model.Entry) (err error) {
	var tc tracker.IHttpClient

	if h.config.MinDateDiff == 0 {
		err = fmt.Errorf("min threshold not set")
		return
	}
	fmt.Println("processing entry: ", e.Name)

	if tc, err = h.tracker.ClientByLink(ctx, e.URL); err != nil {
		return
	}
	if err = h.forUpdate(ctx, tc, e); err != nil {
		return
	}
	err = h.save(ctx, tc, e)
	fmt.Println("saved entry: ", e.Name)

	return
}

func (h *handler) forUpdate(ctx context.Context, tc tracker.IHttpClient, e *model.Entry) (err error) {
	var (
		b    *bytes.Buffer
		cur  *time.Time
		diff int64
	)
	b, err = h.tracker.Response(ctx, tc.TopicLink(e.ID), tc)
	if err != nil {
		return
	} else if b == nil || b.Len() < 50 {
		err = fmt.Errorf("no page data")
		return
	}
	if cur = tc.Datetime(b, false); cur == nil {
		err = fmt.Errorf("current time not found")
		return
	}
	diff = cur.Unix() - e.Timestamp
	// TODO: add check count of updates in DB
	if diff < h.config.MinDateDiff {
		err = fmt.Errorf("file is not updated")
		return
	}
	return
}

func (h *handler) save(ctx context.Context, tc tracker.IHttpClient, e *model.Entry) (err error) {
	var b *bytes.Buffer

	b, err = h.tracker.Response(ctx, tc.DownloadLink(e.ID), tc)
	if err != nil {
		return
	} else if b == nil || b.Len() < 50 {
		err = fmt.Errorf("no file data")
		return
	}
	err = h.manager.Submit(ctx, e, b, model.ManagerAddFileOptions{
		Category:       e.Category,
		Tag:            e.Tag,
		AutoManaged:    e.AutoManaged,
		MaxRatio:       e.MaxRatio,
		MaxSeedingTime: e.MaxSeedingTime,
	})
	return
}

func NewRunner(tracker trackerClient, manager manager, config model.HandlerConfig) IHandler {
	return &handler{
		tracker: tracker,
		manager: manager,
		config:  config,
	}
}
