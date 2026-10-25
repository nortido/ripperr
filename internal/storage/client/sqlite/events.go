package sqlite

import (
	"context"
	"time"

	"ripperr/internal/model/storage"
)

const (
	eventsKeep            = 500
	eventsKeepCheckFailed = 100
	eventsMaxAge          = 30 * 24 * time.Hour

	// совпадает с notify.KindCheckFailed, storage не импортирует notify
	kindCheckFailed = "check_failed"
)

func (c *Client) AddEvent(ctx context.Context, e storage.EventEntry) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx,
		`INSERT INTO events (created_at, source, kind, text, subject, app_path) VALUES (?, ?, ?, ?, ?, ?)`,
		e.At.Unix(), e.Source, e.Kind, e.Text, e.Subject, e.AppPath); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx,
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
