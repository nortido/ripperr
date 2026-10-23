package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"ripperr/internal/model/storage"
	"ripperr/internal/secrets"
)

const (
	SettingSchedule      = "schedule"
	SettingTrackers      = "trackers"
	SettingTrackersOrder = "trackers_order"
	SettingIntegrations  = "integrations"
	SettingAuth          = "auth"
)

type Client struct {
	db  *sql.DB
	box *secrets.Box
}

func (c *Client) GetTracker(ctx context.Context, trackerID int) (res *storage.Tracker, err error) {
	res = &storage.Tracker{}
	err = c.db.QueryRowContext(ctx,
		"SELECT tracker_type_id, tracker_name, domain_name, login_url, download_url, credentials, cookies, publicity FROM trackers WHERE tracker_type_id = ?",
		trackerID).Scan(&res.TypeID, &res.Name, &res.Domain, &res.LoginURL, &res.DownloadURL, &res.Credentials, &res.Cookies, &res.Publicity)
	if err == sql.ErrNoRows {
		err = nil
		return
	}
	res.Exists = true
	if err != nil {
		return
	}
	if res.Credentials, err = c.openTracker(res.TypeID, trackerCredentials, res.Credentials); err != nil {
		return
	}
	res.Cookies, err = c.openTracker(res.TypeID, trackerCookies, res.Cookies)

	return
}

func (c *Client) AddTracker(ctx context.Context, tracker *storage.Tracker) (err error) {
	cookies, credentials, err := c.sealTracker(tracker)
	if err != nil {
		return
	}
	_, err = c.db.ExecContext(ctx,
		"INSERT INTO trackers(tracker_type_id, tracker_name, domain_name, login_url, download_url, cookies, credentials, publicity, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, unixepoch())",
		tracker.TypeID, tracker.Name, tracker.Domain, tracker.LoginURL, tracker.DownloadURL, cookies, credentials, tracker.Publicity)

	return
}

func (c *Client) UpdateTracker(ctx context.Context, tracker *storage.Tracker) (err error) {
	cookies, credentials, err := c.sealTracker(tracker)
	if err != nil {
		return
	}
	_, err = c.db.ExecContext(ctx,
		"UPDATE trackers SET cookies = ?, credentials = ?, updated_at = unixepoch() WHERE tracker_type_id = ?",
		cookies, credentials, tracker.TypeID)

	return
}

func (c *Client) DeleteTracker(ctx context.Context, trackerID int) (err error) {
	_, err = c.db.ExecContext(ctx, "DELETE FROM trackers WHERE tracker_type_id = ?", trackerID)

	return
}

func (c *Client) AddItem(ctx context.Context, item *storage.Item) (err error) {
	_, err = c.db.ExecContext(ctx,
		"INSERT INTO history(url_hash, tracker_type_id, topic_id, category_name, item_tag, item_name, item_url, created_at, recheck_at, force_check) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 0)",
		item.URLHash, item.TrackerTypeID, item.TopicID, item.Category, item.Tag, item.Name, item.URL, timeArg(item.CreatedAt), timeArg(item.RecheckAt))

	return
}

func (c *Client) ListItems(ctx context.Context) ([]storage.Item, error) {
	rows, err := c.db.QueryContext(ctx,
		"SELECT url_hash, tracker_type_id, topic_id, category_name, COALESCE(item_tag, ''), COALESCE(item_name, ''), item_url, created_at, recheck_at, force_check FROM history")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var items []storage.Item
	for rows.Next() {
		var item storage.Item
		var createdAt, recheckAt sql.NullInt64
		if err = rows.Scan(&item.URLHash, &item.TrackerTypeID, &item.TopicID, &item.Category, &item.Tag, &item.Name, &item.URL, &createdAt, &recheckAt, &item.ForceCheck); err != nil {
			return nil, err
		}
		item.CreatedAt = timeFromNull(createdAt)
		item.RecheckAt = timeFromNull(recheckAt)
		item.Exists = true
		items = append(items, item)
	}
	return items, rows.Err()
}

func (c *Client) UpdateItem(ctx context.Context, item *storage.Item) (err error) {
	_, err = c.db.ExecContext(ctx,
		"UPDATE history SET topic_id = ?, category_name = ?, item_tag = ?, item_name = ?, created_at = ?, recheck_at = ?, force_check = 0 WHERE url_hash = ?",
		item.TopicID, item.Category, item.Tag, item.Name, timeArg(item.CreatedAt), timeArg(item.RecheckAt), item.URLHash)
	return
}

func (c *Client) AddCheckLog(ctx context.Context, entry *storage.CheckLogEntry) (int, error) {
	// арбитр upsert это url_hash, поэтому строка с тем же именем под другим хешем (релиз
	// переехал в новую тему) споткнется о check_log_item_name_uidx. Сносим ее заранее
	if strings.TrimSpace(entry.ItemName) != "" {
		if _, err := c.db.ExecContext(ctx,
			`DELETE FROM check_log WHERE LOWER(TRIM(item_name)) = LOWER(TRIM(?)) AND url_hash <> ?`,
			entry.ItemName, entry.URLHash); err != nil {
			return 0, err
		}
	}
	var errorStreak int
	err := c.db.QueryRowContext(ctx,
		`INSERT INTO check_log (url_hash, item_name, item_url, tracker_type_id, status, error, error_streak)
		 VALUES (?, ?, ?, ?, ?, ?, CASE WHEN ? = 'error' AND NOT ? THEN 1 ELSE 0 END)
		 ON CONFLICT(url_hash) DO UPDATE SET
		     checked_at = unixepoch(),
		     item_name = excluded.item_name,
		     item_url = excluded.item_url,
		     tracker_type_id = excluded.tracker_type_id,
		     status = excluded.status,
		     error = excluded.error,
		     error_streak = CASE WHEN excluded.status = 'error' THEN check_log.error_streak + CASE WHEN ? THEN 0 ELSE 1 END ELSE 0 END
		 RETURNING error_streak`,
		entry.URLHash, entry.ItemName, entry.ItemURL, entry.TrackerTypeID, entry.Status, entry.Error, entry.Status, entry.KeepStreak, entry.KeepStreak).Scan(&errorStreak)
	return errorStreak, err
}

func checkLogFilterClause(f storage.CheckLogFilter) (join, clause string, args []any) {
	join = ` LEFT JOIN history h ON h.url_hash = c.url_hash`
	if f.IgnoredOnly {
		join += ` INNER JOIN "ignore" i ON i.url_hash = c.url_hash`
	}
	var where []string
	if f.Status == "broken" {
		brokenStreak := f.BrokenStreak
		if brokenStreak < 1 {
			brokenStreak = 1
		}
		where = append(where, "c.error_streak >= ?")
		args = append(args, brokenStreak)
	} else if f.Status != "" {
		where = append(where, "c.status = ?")
		args = append(args, f.Status)
	}
	if f.Name != "" {
		where = append(where, "INSTR(LOWER(c.item_name), LOWER(?)) > 0")
		args = append(args, f.Name)
	}
	if f.TrackerID > 0 {
		where = append(where, "c.tracker_type_id = ?")
		args = append(args, f.TrackerID)
	}
	if f.Category != "" {
		where = append(where, "h.category_name = ?")
		args = append(args, f.Category)
	}
	if f.ExcludeIgnored && !f.IgnoredOnly {
		// IgnoredOnly уже делает inner join с "ignore", это исключение ему противоречит.
		// Побеждает IgnoredOnly
		where = append(where, `NOT EXISTS (SELECT 1 FROM "ignore" ig WHERE ig.url_hash = c.url_hash)`)
	}
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	return
}

func (c *Client) ListCheckLog(ctx context.Context, f storage.CheckLogFilter) ([]storage.CheckLogEntry, int, error) {
	var total int
	from := `check_log c`
	join, clause, args := checkLogFilterClause(f)
	countQ := "SELECT COUNT(*) FROM " + from + join
	listQ := `SELECT c.id, c.checked_at, c.url_hash, c.item_name, c.item_url, c.tracker_type_id, c.status, c.error, c.error_streak,
	          COALESCE(h.category_name, ''), h.recheck_at, COALESCE(h.force_check, 0)
	          FROM ` + from + join
	countQ += clause
	listQ += clause
	if err := c.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	orderBy := " ORDER BY c.checked_at DESC"
	if f.SortBy == "recheck" {
		orderBy = " ORDER BY h.recheck_at IS NULL, h.recheck_at ASC"
	}
	listQ += orderBy + " LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)
	rows, err := c.db.QueryContext(ctx, listQ, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var items []storage.CheckLogEntry
	for rows.Next() {
		var e storage.CheckLogEntry
		var checkedAt int64
		var recheckAt sql.NullInt64
		if err = rows.Scan(&e.ID, &checkedAt, &e.URLHash, &e.ItemName, &e.ItemURL, &e.TrackerTypeID, &e.Status, &e.Error, &e.ErrorStreak, &e.Category, &recheckAt, &e.ForceCheck); err != nil {
			return nil, 0, err
		}
		e.CheckedAt = time.Unix(checkedAt, 0)
		e.RecheckAt = timeFromNull(recheckAt)
		items = append(items, e)
	}
	return items, total, rows.Err()
}

func (c *Client) CheckLogSummary(ctx context.Context) (storage.CheckLogSummary, error) {
	var s storage.CheckLogSummary
	var updated, errs sql.NullInt64
	err := c.db.QueryRowContext(ctx, `
SELECT
    SUM(CASE WHEN status = 'updated' THEN 1 ELSE 0 END),
    SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END)
FROM check_log`).Scan(&updated, &errs)
	if err != nil {
		return s, err
	}
	s.Updated = int(updated.Int64)
	s.Errors = int(errs.Int64)
	return s, nil
}

func (c *Client) ListIgnore(ctx context.Context) ([]storage.IgnoreEntry, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT url_hash, item_name, item_url, added_at FROM "ignore" ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var items []storage.IgnoreEntry
	for rows.Next() {
		var e storage.IgnoreEntry
		var addedAt int64
		if err = rows.Scan(&e.URLHash, &e.ItemName, &e.ItemURL, &addedAt); err != nil {
			return nil, err
		}
		e.AddedAt = time.Unix(addedAt, 0)
		items = append(items, e)
	}
	return items, rows.Err()
}

func (c *Client) AddIgnore(ctx context.Context, hash, name, url string) error {
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO "ignore" (url_hash, item_name, item_url) VALUES (?, ?, ?)
		 ON CONFLICT(url_hash) DO UPDATE SET item_name = excluded.item_name, item_url = excluded.item_url`,
		hash, name, url)
	return err
}

func (c *Client) DeleteIgnore(ctx context.Context, hash string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM "ignore" WHERE url_hash = ?`, hash)
	return err
}

func (c *Client) ResetErrorStreak(ctx context.Context, urlHash string) error {
	_, err := c.db.ExecContext(ctx, `UPDATE check_log SET error_streak = 0 WHERE url_hash = ?`, urlHash)
	return err
}

func (c *Client) GetIgnoreSet(ctx context.Context) (map[string]struct{}, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT url_hash FROM "ignore"`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
	var raw []byte
	err := c.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	plain, err := c.openSetting(key, string(raw))
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage(plain), true, nil
}

func (c *Client) SaveSetting(ctx context.Context, key string, value json.RawMessage) error {
	sealed, err := c.seal(value, settingAAD(key))
	if err != nil {
		return err
	}
	_, err = c.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, unixepoch())
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = unixepoch()`,
		key, sealed)
	return err
}

func (c *Client) DeleteItem(ctx context.Context, urlHash string) error {
	if _, err := c.db.ExecContext(ctx, `DELETE FROM check_log WHERE url_hash = ?`, urlHash); err != nil {
		return err
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM release_times WHERE url_hash = ?`, urlHash); err != nil {
		return err
	}
	_, err := c.db.ExecContext(ctx, `DELETE FROM history WHERE url_hash = ?`, urlHash)
	return err
}

func (c *Client) PurgeDuplicateNames(ctx context.Context, winners map[string]string) error {
	if len(winners) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for name, hash := range winners {
		norm := strings.ToLower(strings.TrimSpace(name))
		if _, err := tx.ExecContext(ctx, `DELETE FROM check_log WHERE LOWER(TRIM(item_name)) = ? AND url_hash <> ?`, norm, hash); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM release_times WHERE url_hash IN (SELECT url_hash FROM history WHERE LOWER(TRIM(item_name)) = ? AND url_hash <> ?)`, norm, hash); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM history WHERE LOWER(TRIM(item_name)) = ? AND url_hash <> ?`, norm, hash); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Client) SetForceCheck(ctx context.Context, urlHash string, force bool) (bool, error) {
	res, err := c.db.ExecContext(ctx, `UPDATE history SET force_check = ? WHERE url_hash = ?`, force, urlHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (c *Client) ForceCheckAll(ctx context.Context, force bool, f storage.CheckLogFilter) ([]string, int64, error) {
	// хеши нужны раннеру, чтобы форс всем запустил проверку сразу, как одиночный
	join, clause, args := checkLogFilterClause(f)
	// условие то же, что у списка на дашборде, поэтому форс ложится на отфильтрованные
	// релизы, а не на всю историю
	sub := `SELECT c.url_hash FROM check_log c` + join + clause
	rows, err := c.db.QueryContext(ctx, sub, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var hashes []string
	for rows.Next() {
		var hash string
		if err = rows.Scan(&hash); err != nil {
			return nil, 0, err
		}
		hashes = append(hashes, hash)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	flag := 0
	if force {
		flag = 1
	}
	// force_check <> ? отсекает строки, которые уже в нужном состоянии, и RowsAffected
	// считает только реально измененные релизы
	res, err := c.db.ExecContext(ctx,
		`UPDATE history SET force_check = ? WHERE force_check <> ? AND url_hash IN (`+sub+`)`,
		append([]any{flag, flag}, args...)...)
	if err != nil {
		return nil, 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, 0, err
	}
	return hashes, n, nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.db.PingContext(ctx)
}

func timeArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func timeFromNull(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(n.Int64, 0)
	return &t
}

func New(db *sql.DB, box *secrets.Box) *Client {
	return &Client{
		db:  db,
		box: box,
	}
}

func (c *Client) PurgeCategoriesExcept(ctx context.Context, allowed []string) (int64, error) {
	// релизы выключенной категории больше не проверяются. Без чистки они висят в плане
	// как живые, и форс на них молча ничего не делает
	if len(allowed) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(allowed))
	for _, a := range allowed {
		args = append(args, a)
	}
	// пустую категорию не трогаем: она неизвестна, а не выключена
	cond := `category_name IS NOT NULL AND category_name <> '' AND category_name NOT IN (` +
		strings.TrimSuffix(strings.Repeat("?,", len(allowed)), ",") + `)`
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx, `DELETE FROM check_log WHERE url_hash IN (SELECT url_hash FROM history WHERE `+cond+`)`, args...); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM release_times WHERE url_hash IN (SELECT url_hash FROM history WHERE `+cond+`)`, args...); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM history WHERE `+cond, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

func (c *Client) AddReleaseTime(ctx context.Context, urlHash string, at time.Time, keep int) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO release_times (url_hash, released_at) VALUES (?, ?)`, urlHash, at.Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM release_times WHERE url_hash = ? AND released_at NOT IN (
		    SELECT released_at FROM release_times WHERE url_hash = ? ORDER BY released_at DESC LIMIT ?)`,
		urlHash, urlHash, keep); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Client) RecentReleaseTimes(ctx context.Context, urlHash string, since time.Time, limit int) ([]time.Time, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT released_at FROM release_times WHERE url_hash = ? AND released_at >= ? ORDER BY released_at DESC LIMIT ?`,
		urlHash, since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []time.Time
	for rows.Next() {
		var v int64
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, time.Unix(v, 0))
	}
	return out, rows.Err()
}
