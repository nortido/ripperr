package pg

import (
	"context"

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
		"SELECT tracker_type_id, tracker_name, cookies FROM trackerr.trackers WHERE tracker_type_id = $1::int",
		trackerID).Scan(&res.TypeID, &res.Name, &res.Cookies)
	if err != nil {
		return
	}
	return
}

func (c *client) AddTracker(ctx context.Context, tracker *storage.Tracker) (err error) {
	_, err = c.db.Exec(ctx,
		"INSERT INTO trackerr.trackers(tracker_type_id, tracker_name, cookies, credentials, updated_at) VALUES($1, $2, $3, $4, NOW())",
		tracker.TypeID, tracker.Name, tracker.Cookies, tracker.Credentials)

	return
}

func (c *client) UpdateTracker(ctx context.Context, tracker *storage.Tracker) (err error) {
	_, err = c.db.Exec(ctx,
		"UPDATE trackerr.trackers SET cookies = $1, credentials = $2, updated_at = NOW() WHERE tracker_type_id = $3",
		tracker.Cookies, tracker.Credentials, tracker.TypeID)

	return
}

func (c *client) GetItem(ctx context.Context, urlHash string) (item *storage.Item, err error) {
	item = &storage.Item{}
	err = c.db.QueryRow(ctx, "SELECT topic_id, category_name, item_url, updated_at FROM trackerr.history WHERE url_hash = $1", urlHash).Scan(
		&item.TopicID, &item.Category, &item.URL, &item.UpdatedAt)
	item.URLHash = urlHash

	return
}

func (c *client) AddItem(ctx context.Context, item *storage.Item) (err error) {
	_, err = c.db.Exec(ctx,
		"INSERT INTO trackerr.history(url_hash, tracker_type_id, topic_id, category_name, item_tag, item_name, item_url, updated_at) VALUES($1, $2, $3, $4, $5, $6, $7, NOW())",
		item.URLHash, item.TrackerTypeID, item.TopicID, item.Category, item.Tag, item.Name, item.URL)

	return
}

func (c *client) UpdateItem(ctx context.Context, item *storage.Item) (err error) {
	_, err = c.db.Exec(ctx,
		"UPDATE trackerr.history SET topic_id = $1, category_name = $2, item_tag = $3, item_name = $4, item_url = $5, updated_at = NOW() WHERE url_hash = $6",
		item.TopicID, item.Category, item.Tag, item.Name, item.URL, item.URLHash)
	return
}

func New(db *pgxpool.Pool) IClient {
	return &client{
		db: db,
	}
}
