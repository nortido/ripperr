package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"trackerr/internal/model/storage"
)

const (
	SettingSchedule     = "schedule"
	SettingTrackers     = "trackers"
	SettingIntegrations = "integrations"
	SettingAuth         = "auth"
)

type Client struct {
	db *pgxpool.Pool
}

func (c *Client) Bootstrap(ctx context.Context) error {
	_, err := c.db.Exec(ctx, `
CREATE SCHEMA IF NOT EXISTS trackerr;

CREATE TABLE IF NOT EXISTS trackerr.check_log (
    id bigserial PRIMARY KEY,
    checked_at timestamptz NOT NULL DEFAULT now(),
    url_hash text NOT NULL,
    item_name text NOT NULL DEFAULT '',
    item_url text NOT NULL DEFAULT '',
    tracker_type_id int NOT NULL DEFAULT 0,
    status text NOT NULL,
    error text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS check_log_checked_at_idx ON trackerr.check_log (checked_at DESC);

ALTER TABLE trackerr.check_log ADD COLUMN IF NOT EXISTS error_streak int NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS trackerr.ignore (
    url_hash text PRIMARY KEY,
    item_name text NOT NULL DEFAULT '',
    item_url text NOT NULL DEFAULT '',
    added_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS trackerr.settings (
    key text PRIMARY KEY,
    value jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS trackerr.trackers (
    tracker_type_id int PRIMARY KEY,
    tracker_name text,
    domain_name text,
    login_url text,
    download_url text,
    credentials text,
    cookies text,
    publicity text,
    updated_at timestamptz
);

CREATE TABLE IF NOT EXISTS trackerr.history (
    url_hash text PRIMARY KEY,
    tracker_type_id int,
    topic_id text,
    category_name text,
    item_tag text,
    item_name text,
    item_url text,
    created_at timestamptz,
    recheck_at timestamptz
);

ALTER TABLE trackerr.history ADD COLUMN IF NOT EXISTS force_check boolean NOT NULL DEFAULT false;

DELETE FROM trackerr.check_log
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (PARTITION BY url_hash ORDER BY checked_at DESC, id DESC) AS rn
        FROM trackerr.check_log
    ) dedup WHERE rn > 1
);
CREATE UNIQUE INDEX IF NOT EXISTS check_log_url_hash_uidx ON trackerr.check_log (url_hash);

DELETE FROM trackerr.check_log
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (PARTITION BY LOWER(TRIM(item_name)) ORDER BY checked_at DESC, id DESC) AS rn
        FROM trackerr.check_log
    ) dedup_name WHERE rn > 1
);
CREATE UNIQUE INDEX IF NOT EXISTS check_log_item_name_uidx ON trackerr.check_log (LOWER(TRIM(item_name))) WHERE TRIM(item_name) <> '';
`)
	return err
}

func (c *Client) GetTracker(ctx context.Context, trackerID int) (res *storage.Tracker, err error) {
	res = &storage.Tracker{}
	err = c.db.QueryRow(ctx,
		"SELECT tracker_type_id, tracker_name, domain_name, login_url, download_url, credentials, cookies, publicity FROM trackerr.trackers WHERE tracker_type_id = $1::int",
		trackerID).Scan(&res.TypeID, &res.Name, &res.Domain, &res.LoginURL, &res.DownloadURL, &res.Credentials, &res.Cookies, &res.Publicity)
	if err == pgx.ErrNoRows {
		err = nil
		return
	}
	res.Exists = true

	return
}

func (c *Client) AddTracker(ctx context.Context, tracker *storage.Tracker) (err error) {
	_, err = c.db.Exec(ctx,
		"INSERT INTO trackerr.trackers(tracker_type_id, tracker_name, domain_name, login_url, download_url, cookies, credentials, publicity, updated_at) VALUES($1, $2, $3, $4, $5, $6, $7, $8, NOW())",
		tracker.TypeID, tracker.Name, tracker.Domain, tracker.LoginURL, tracker.DownloadURL, tracker.Cookies, tracker.Credentials, tracker.Publicity)

	return
}

func (c *Client) UpdateTracker(ctx context.Context, tracker *storage.Tracker) (err error) {
	_, err = c.db.Exec(ctx,
		"UPDATE trackerr.trackers SET cookies = $2, credentials = $3, updated_at = NOW() WHERE tracker_type_id = $1",
		tracker.TypeID, tracker.Cookies, tracker.Credentials)

	return
}

func (c *Client) AddItem(ctx context.Context, item *storage.Item) (err error) {
	_, err = c.db.Exec(ctx,
		"INSERT INTO trackerr.history(url_hash, tracker_type_id, topic_id, category_name, item_tag, item_name, item_url, created_at, recheck_at, force_check) VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, false)",
		item.URLHash, item.TrackerTypeID, item.TopicID, item.Category, item.Tag, item.Name, item.URL, item.CreatedAt, item.RecheckAt)

	return
}

func (c *Client) ListItems(ctx context.Context) ([]storage.Item, error) {
	rows, err := c.db.Query(ctx,
		"SELECT url_hash, tracker_type_id, topic_id, category_name, COALESCE(item_tag, ''), COALESCE(item_name, ''), item_url, created_at, recheck_at, force_check FROM trackerr.history")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []storage.Item
	for rows.Next() {
		var item storage.Item
		if err = rows.Scan(&item.URLHash, &item.TrackerTypeID, &item.TopicID, &item.Category, &item.Tag, &item.Name, &item.URL, &item.CreatedAt, &item.RecheckAt, &item.ForceCheck); err != nil {
			return nil, err
		}
		item.Exists = true
		items = append(items, item)
	}
	return items, rows.Err()
}

func (c *Client) UpdateItem(ctx context.Context, item *storage.Item) (err error) {
	_, err = c.db.Exec(ctx,
		"UPDATE trackerr.history SET topic_id = $2, category_name = $3, item_tag = $4, item_name = $5, created_at = $6, recheck_at = $7, force_check = false WHERE url_hash = $1",
		item.URLHash, item.TopicID, item.Category, item.Tag, item.Name, item.CreatedAt, item.RecheckAt)
	return
}

func (c *Client) AddCheckLog(ctx context.Context, entry *storage.CheckLogEntry) (int, error) {
	// the upsert arbiter is url_hash, so a same-name row under a different hash
	// (release moved to a new topic) would trip check_log_item_name_uidx: drop it first
	if strings.TrimSpace(entry.ItemName) != "" {
		if _, err := c.db.Exec(ctx,
			`DELETE FROM trackerr.check_log WHERE LOWER(TRIM(item_name)) = LOWER(TRIM($1)) AND url_hash <> $2`,
			entry.ItemName, entry.URLHash); err != nil {
			return 0, err
		}
	}
	var errorStreak int
	err := c.db.QueryRow(ctx,
		`INSERT INTO trackerr.check_log (url_hash, item_name, item_url, tracker_type_id, status, error, error_streak)
		 VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $5 = 'error' THEN 1 ELSE 0 END)
		 ON CONFLICT (url_hash) DO UPDATE SET
		     checked_at = NOW(),
		     item_name = EXCLUDED.item_name,
		     item_url = EXCLUDED.item_url,
		     tracker_type_id = EXCLUDED.tracker_type_id,
		     status = EXCLUDED.status,
		     error = EXCLUDED.error,
		     error_streak = CASE WHEN EXCLUDED.status = 'error' THEN trackerr.check_log.error_streak + 1 ELSE 0 END
		 RETURNING error_streak`,
		entry.URLHash, entry.ItemName, entry.ItemURL, entry.TrackerTypeID, entry.Status, entry.Error).Scan(&errorStreak)
	return errorStreak, err
}

func (c *Client) ListCheckLog(ctx context.Context, f storage.CheckLogFilter) ([]storage.CheckLogEntry, int, error) {
	var total int
	from := `trackerr.check_log c`
	join := " LEFT JOIN trackerr.history h ON h.url_hash = c.url_hash"
	if f.IgnoredOnly {
		join += " INNER JOIN trackerr.ignore i ON i.url_hash = c.url_hash"
	}
	countQ := "SELECT COUNT(*) FROM " + from + join
	listQ := `SELECT c.id, c.checked_at, c.url_hash, c.item_name, c.item_url, c.tracker_type_id, c.status, c.error, c.error_streak,
	          h.recheck_at, COALESCE(h.force_check, false)
	          FROM ` + from + join
	args := []any{}
	n := 1
	var where []string
	if f.Status == "broken" {
		brokenStreak := f.BrokenStreak
		if brokenStreak < 1 {
			brokenStreak = 1
		}
		where = append(where, "c.error_streak >= $"+itoa(n))
		n++
		args = append(args, brokenStreak)
	} else if f.Status != "" {
		where = append(where, "c.status = $"+itoa(n))
		n++
		args = append(args, f.Status)
	}
	if f.Name != "" {
		where = append(where, "POSITION(LOWER($"+itoa(n)+") IN LOWER(c.item_name)) > 0")
		n++
		args = append(args, f.Name)
	}
	if f.TrackerID > 0 {
		where = append(where, "c.tracker_type_id = $"+itoa(n))
		n++
		args = append(args, f.TrackerID)
	}
	if f.Category != "" {
		where = append(where, "h.category_name = $"+itoa(n))
		n++
		args = append(args, f.Category)
	}
	if len(where) > 0 {
		clause := " WHERE " + strings.Join(where, " AND ")
		countQ += clause
		listQ += clause
	}
	if err := c.db.QueryRow(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	orderBy := " ORDER BY c.checked_at DESC"
	if f.SortBy == "recheck" {
		orderBy = " ORDER BY h.recheck_at ASC NULLS LAST"
	}
	listQ += orderBy + " LIMIT $" + itoa(n) + " OFFSET $" + itoa(n+1)
	args = append(args, f.Limit, f.Offset)
	rows, err := c.db.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var items []storage.CheckLogEntry
	for rows.Next() {
		var e storage.CheckLogEntry
		if err = rows.Scan(&e.ID, &e.CheckedAt, &e.URLHash, &e.ItemName, &e.ItemURL, &e.TrackerTypeID, &e.Status, &e.Error, &e.ErrorStreak, &e.RecheckAt, &e.ForceCheck); err != nil {
			return nil, 0, err
		}
		items = append(items, e)
	}
	return items, total, rows.Err()
}

func (c *Client) CheckLogSummary(ctx context.Context) (storage.CheckLogSummary, error) {
	var s storage.CheckLogSummary
	err := c.db.QueryRow(ctx, `
SELECT
    COUNT(*) FILTER (WHERE status = 'updated'),
    COUNT(*) FILTER (WHERE status = 'error')
FROM trackerr.check_log`).Scan(&s.Updated, &s.Errors)
	return s, err
}

func (c *Client) ListIgnore(ctx context.Context) ([]storage.IgnoreEntry, error) {
	rows, err := c.db.Query(ctx,
		`SELECT url_hash, item_name, item_url, added_at FROM trackerr.ignore ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []storage.IgnoreEntry
	for rows.Next() {
		var e storage.IgnoreEntry
		if err = rows.Scan(&e.URLHash, &e.ItemName, &e.ItemURL, &e.AddedAt); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

func (c *Client) AddIgnore(ctx context.Context, hash, name, url string) error {
	_, err := c.db.Exec(ctx,
		`INSERT INTO trackerr.ignore (url_hash, item_name, item_url) VALUES ($1, $2, $3)
		 ON CONFLICT (url_hash) DO UPDATE SET item_name = EXCLUDED.item_name, item_url = EXCLUDED.item_url`,
		hash, name, url)
	return err
}

func (c *Client) DeleteIgnore(ctx context.Context, hash string) error {
	_, err := c.db.Exec(ctx, `DELETE FROM trackerr.ignore WHERE url_hash = $1`, hash)
	return err
}

func (c *Client) ResetErrorStreak(ctx context.Context, urlHash string) error {
	_, err := c.db.Exec(ctx, `UPDATE trackerr.check_log SET error_streak = 0 WHERE url_hash = $1`, urlHash)
	return err
}

func (c *Client) GetIgnoreSet(ctx context.Context) (map[string]struct{}, error) {
	rows, err := c.db.Query(ctx, `SELECT url_hash FROM trackerr.ignore`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := make(map[string]struct{})
	for rows.Next() {
		var h string
		if err = rows.Scan(&h); err != nil {
			return nil, err
		}
		set[h] = struct{}{}
	}
	return set, rows.Err()
}

func (c *Client) GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error) {
	var raw json.RawMessage
	err := c.db.QueryRow(ctx, `SELECT value FROM trackerr.settings WHERE key = $1`, key).Scan(&raw)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func (c *Client) SaveSetting(ctx context.Context, key string, value json.RawMessage) error {
	_, err := c.db.Exec(ctx,
		`INSERT INTO trackerr.settings (key, value, updated_at) VALUES ($1, $2, NOW())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key, value)
	return err
}

func (c *Client) ListDueItems(ctx context.Context) ([]storage.DueItem, error) {
	rows, err := c.db.Query(ctx,
		`SELECT url_hash, COALESCE(item_name, '') FROM trackerr.history WHERE recheck_at <= NOW()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []storage.DueItem
	for rows.Next() {
		var e storage.DueItem
		if err = rows.Scan(&e.URLHash, &e.ItemName); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

func (c *Client) DeleteItem(ctx context.Context, urlHash string) error {
	if _, err := c.db.Exec(ctx, `DELETE FROM trackerr.check_log WHERE url_hash = $1`, urlHash); err != nil {
		return err
	}
	_, err := c.db.Exec(ctx, `DELETE FROM trackerr.history WHERE url_hash = $1`, urlHash)
	return err
}

func (c *Client) PurgeDuplicateNames(ctx context.Context, winners map[string]string) error {
	if len(winners) == 0 {
		return nil
	}
	names := make([]string, 0, len(winners))
	hashes := make([]string, 0, len(winners))
	for name, hash := range winners {
		names = append(names, strings.ToLower(strings.TrimSpace(name)))
		hashes = append(hashes, hash)
	}
	const purgeQ = `
DELETE FROM %s x
WHERE EXISTS (
    SELECT 1 FROM unnest($1::text[], $2::text[]) AS w(norm_name, winner_hash)
    WHERE LOWER(TRIM(x.item_name)) = w.norm_name AND x.url_hash != w.winner_hash
)`
	if _, err := c.db.Exec(ctx, fmt.Sprintf(purgeQ, "trackerr.check_log"), names, hashes); err != nil {
		return err
	}
	_, err := c.db.Exec(ctx, fmt.Sprintf(purgeQ, "trackerr.history"), names, hashes)
	return err
}

func (c *Client) SetForceCheck(ctx context.Context, urlHash string, force bool) (bool, error) {
	tag, err := c.db.Exec(ctx, `UPDATE trackerr.history SET force_check = $2 WHERE url_hash = $1`, urlHash, force)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (c *Client) ForceCheckAll(ctx context.Context, force bool) (int64, error) {
	checkLogExists := `EXISTS (SELECT 1 FROM trackerr.check_log c WHERE c.url_hash = trackerr.history.url_hash)`
	if force {
		tag, err := c.db.Exec(ctx, `
UPDATE trackerr.history SET force_check = true
WHERE `+checkLogExists+`
AND NOT EXISTS (
	SELECT 1 FROM trackerr.ignore i WHERE i.url_hash = trackerr.history.url_hash
)`)
		if err != nil {
			return 0, err
		}
		return tag.RowsAffected(), nil
	}
	tag, err := c.db.Exec(ctx, `
UPDATE trackerr.history SET force_check = false
WHERE `+checkLogExists)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.db.Ping(ctx)
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func New(db *pgxpool.Pool) *Client {
	return &Client{
		db: db,
	}
}
