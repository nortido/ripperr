package handler

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"torr/internal/helper"
	"torr/internal/model"
)

type tracker interface {
	GetData(ctx context.Context, e *model.Entry) (buf *bytes.Buffer, err error)
}

type storage interface {
	SaveLocalFile(ctx context.Context, buf []byte) (path string, err error)
	DeleteLocalFile(ctx context.Context, pathname string) (err error)
}

type manager interface {
	GetEntries(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error)
	Submit(ctx context.Context, e *model.Entry, path string, opts model.ManagerAddFileOptions) (err error)
}

type Handler interface {
	Handle(ctx context.Context)
}

type handler struct {
	tracker tracker
	manager manager
	storage storage
	entries []*model.Entry
	m       sync.RWMutex
	offset  int
	limit   int
}

func (h *handler) Handle(ctx context.Context) {
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
		// TODO: set time frea env
		case <-time.After(time.Second * 15):
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
	// TODO: set max const
	h.limit = len(h.entries) / 24
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
		fmt.Println("pushed entry: ", e.Name)
		ch <- e
	}
	h.m.RLock()
	h.offset += h.limit
	h.m.RUnlock()

	return
}

func (h *handler) processEntry(ctx context.Context, e *model.Entry) (err error) {
	var (
		b    *bytes.Buffer
		path string
		ts   int
	)
	fmt.Println("processing entry: ", e.Name)

	//TODO: parse webpage date instead of downloading file
	b, err = h.tracker.GetData(ctx, e)
	if err != nil {
		return
	} else if b == nil || b.Len() < 50 {
		err = fmt.Errorf("no data")
		return
	}
	if ts, err = helper.GetTimestamp(b); err != nil {
		return
	}
	if ts == e.Timestamp {
		//TODO: use buffer instead of files
		if path, err = h.storage.SaveLocalFile(ctx, b.Bytes()); err != nil {
			return
		}
		//TODO: add global options
		if err = h.manager.Submit(ctx, e, path, model.ManagerAddFileOptions{Category: e.Category, Tag: "watch"}); err != nil {
			return
		}
		if err = h.storage.DeleteLocalFile(ctx, path); err != nil {
			return
		}
		fmt.Println(e.Name, e.Timestamp, ts)
	}
	return
}

func NewHandler(tracker tracker, manager manager, storage storage) Handler {
	return &handler{
		tracker: tracker,
		manager: manager,
		storage: storage,
		limit:   2,
	}
}
