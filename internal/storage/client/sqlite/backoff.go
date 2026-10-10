package sqlite

import (
	"context"
	"time"

	"ripperr/internal/model/storage"
)

func (c *Client) ErrorMarks(ctx context.Context) (map[string]storage.ErrorMark, error) {
	rows, err := c.db.QueryContext(ctx,
		"SELECT url_hash, error_streak, checked_at FROM check_log WHERE status = 'error' AND error_streak > 0")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]storage.ErrorMark)
	for rows.Next() {
		var hash string
		var streak int
		var at int64
		if err = rows.Scan(&hash, &streak, &at); err != nil {
			return nil, err
		}
		out[hash] = storage.ErrorMark{Streak: streak, CheckedAt: time.Unix(at, 0)}
	}
	return out, rows.Err()
}
