package pg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"trackerr/internal/model/storage"
)

type IClient interface {
	GetTracker(ctx context.Context, trackerID int) (res *storage.Tracker, err error)
	AddTracker(ctx context.Context, tracker *storage.Tracker) (err error)
	UpdateTracker(ctx context.Context, tracker *storage.Tracker) (err error)
	GetItem(ctx context.Context, urlHash string) (item *storage.Item, err error)
	AddItem(ctx context.Context, item *storage.Item) (err error)
	UpdateItem(ctx context.Context, item *storage.Item) (err error)
}

type client struct {
	db *pgxpool.Pool
}

func (c *client) GetTracker(ctx context.Context, trackerID int) (res *storage.Tracker, err error) {
	res = &storage.Tracker{}
	err = c.db.QueryRow(ctx,
		"SELECT tracker_type_id, tracker_name, host, login_url, download_url, credentials, cookies, publicity FROM trackerr.trackers WHERE tracker_type_id = $1::int",
		trackerID).Scan(&res.TypeID, &res.Name, &res.Host, &res.LoginURL, &res.DownloadURL, &res.Credentials, &res.Cookies, &res.Publicity)
	if err == pgx.ErrNoRows {
		err = nil
		return
	}
	res.Exists = true

	return
}

func (c *client) AddTracker(ctx context.Context, tracker *storage.Tracker) (err error) {
	_, err = c.db.Exec(ctx,
		"INSERT INTO trackerr.trackers(tracker_type_id, tracker_name, host, login_url, download_url, cookies, credentials, publicity, updated_at) VALUES($1, $2, $3, $4, $5, $6, $7, $8, NOW())",
		tracker.TypeID, tracker.Name, tracker.Host, tracker.LoginURL, tracker.DownloadURL, tracker.Cookies, tracker.Credentials, tracker.Publicity)

	return
}

func (c *client) UpdateTracker(ctx context.Context, tracker *storage.Tracker) (err error) {
	_, err = c.db.Exec(ctx,
		"UPDATE trackerr.trackers SET cookies = $2, credentials = $3, updated_at = NOW() WHERE tracker_type_id = $1",
		tracker.TypeID, tracker.Cookies, tracker.Credentials)

	return
}

func (c *client) GetItem(ctx context.Context, urlHash string) (item *storage.Item, err error) {
	item = &storage.Item{URLHash: urlHash}
	err = c.db.QueryRow(ctx, "SELECT tracker_type_id, topic_id, category_name, item_url, recheck_at FROM trackerr.history WHERE url_hash = $1", urlHash).Scan(
		&item.TrackerTypeID, &item.TopicID, &item.Category, &item.URL, &item.RecheckAt)
	if err == pgx.ErrNoRows {
		err = nil
		return
	}
	item.Exists = true

	return
}

func (c *client) AddItem(ctx context.Context, item *storage.Item) (err error) {
	_, err = c.db.Exec(ctx,
		"INSERT INTO trackerr.history(url_hash, tracker_type_id, topic_id, category_name, item_tag, item_name, item_url, created_at, recheck_at) VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9)",
		item.URLHash, item.TrackerTypeID, item.TopicID, item.Category, item.Tag, item.Name, item.URL, item.CreatedAt, item.RecheckAt)

	return
}

func (c *client) UpdateItem(ctx context.Context, item *storage.Item) (err error) {
	_, err = c.db.Exec(ctx,
		"UPDATE trackerr.history SET category_name = $2, item_tag = $3, item_name = $4, created_at = $5, recheck_at = $6 WHERE url_hash = $1",
		item.URLHash, item.Category, item.Tag, item.Name, item.CreatedAt, item.RecheckAt)
	return
}

func New(db *pgxpool.Pool) IClient {
	return &client{
		db: db,
	}
}
