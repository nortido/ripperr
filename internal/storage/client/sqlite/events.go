package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"ripperr/internal/model/storage"
	"ripperr/internal/system/logger"
)

const (
	eventsKeep            = 500
	eventsKeepCheckFailed = 100
	eventsMaxAge          = 30 * 24 * time.Hour
	eventsWriteTimeout    = 30 * time.Second

	// совпадает с notify.KindCheckFailed, storage не импортирует notify
	kindCheckFailed = "check_failed"
)

var eventsQueueMax = 1000

type eventQueue struct {
	mu      sync.Mutex
	buf     []storage.EventEntry
	running bool
	dropped int
	done    chan struct{}
}

func (c *Client) RecordEvent(e storage.EventEntry) bool {
	q := &c.events
	q.mu.Lock()
	if len(q.buf) >= eventsQueueMax {
		q.dropped++
		q.mu.Unlock()
		return false
	}
	q.buf = append(q.buf, e)
	if !q.running {
		q.running = true
		q.done = make(chan struct{})
		go c.flushEvents()
	}
	q.mu.Unlock()
	return true
}

func (c *Client) FlushEvents(ctx context.Context) error {
	q := &c.events
	for {
		q.mu.Lock()
		if !q.running {
			q.mu.Unlock()
			return nil
		}
		done := q.done
		q.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) flushEvents() {
	q := &c.events
	for {
		ctx, cancel := context.WithTimeout(context.Background(), eventsWriteTimeout)
		// буфер берем только после BeginTx: пока соединение занято, он остается под лимитом
		tx, err := c.db.BeginTx(ctx, nil)
		batch, dropped := q.take()
		if err != nil {
			logger.Warn(fmt.Sprintf("events: begin batch of %d failed, dropped: %v", len(batch), err))
		} else if err = insertEvents(ctx, tx, batch); err != nil {
			logger.Warn(fmt.Sprintf("events: write batch of %d failed: %v", len(batch), err))
		}
		cancel()
		if dropped > 0 {
			logger.Warn(fmt.Sprintf("events: queue full, dropped %d events", dropped))
		}
		if q.finishIfEmpty() {
			return
		}
	}
}

func (q *eventQueue) take() ([]storage.EventEntry, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	batch, dropped := q.buf, q.dropped
	q.buf, q.dropped = nil, 0
	return batch, dropped
}

func (q *eventQueue) finishIfEmpty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.buf) > 0 {
		return false
	}
	q.running = false
	close(q.done)
	return true
}

func (c *Client) AddEvent(ctx context.Context, e storage.EventEntry) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	return insertEvents(ctx, tx, []storage.EventEntry{e})
}

func insertEvents(ctx context.Context, tx *sql.Tx, batch []storage.EventEntry) error {
	defer func() { _ = tx.Rollback() }()

	for _, e := range batch {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events (created_at, source, kind, text, subject, app_path) VALUES (?, ?, ?, ?, ?, ?)`,
			e.At.Unix(), e.Source, e.Kind, e.Text, e.Subject, e.AppPath); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM events WHERE created_at < ?
		 OR (kind = ? AND id NOT IN (SELECT id FROM events WHERE kind = ? ORDER BY created_at DESC, id DESC LIMIT ?))
		 OR (kind <> ? AND id NOT IN (SELECT id FROM events WHERE kind <> ? ORDER BY created_at DESC, id DESC LIMIT ?))`,
		time.Now().Add(-eventsMaxAge).Unix(),
		kindCheckFailed, kindCheckFailed, eventsKeepCheckFailed,
		kindCheckFailed, kindCheckFailed, eventsKeep); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Client) ListEvents(ctx context.Context, limit int) ([]storage.EventEntry, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT id, created_at, source, kind, text, subject, app_path FROM events WHERE created_at >= ? ORDER BY created_at DESC, id DESC LIMIT ?`,
		time.Now().Add(-eventsMaxAge).Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []storage.EventEntry
	for rows.Next() {
		var e storage.EventEntry
		var at int64
		if err = rows.Scan(&e.ID, &at, &e.Source, &e.Kind, &e.Text, &e.Subject, &e.AppPath); err != nil {
			return nil, err
		}
		e.At = time.Unix(at, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}
