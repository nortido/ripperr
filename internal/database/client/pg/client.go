package pg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"trackerr/internal/model/database"
)

type IClient interface {
	GetTracker(ctx context.Context, trackerID int) (res *database.Tracker, err error)
	AddTracker(ctx context.Context, tracker *database.Tracker) (err error)
	UpdateTracker(ctx context.Context, tracker *database.Tracker) (err error)
}

type client struct {
	db *pgxpool.Pool
}

func (c *client) GetTracker(ctx context.Context, trackerID int) (res *database.Tracker, err error) {
	res = &database.Tracker{}
	err = c.db.QueryRow(ctx,
		"SELECT tracker_type_id, tracker_name, cookies FROM trackerr.trackerr.trackers WHERE tracker_type_id = $1::int",
		trackerID).Scan(&res.TypeID, &res.Name, &res.Cookies)
	if err != nil {
		return
	}
	return
}

func (c *client) AddTracker(ctx context.Context, tracker *database.Tracker) (err error) {
	_, err = c.db.Exec(ctx,
		"INSERT INTO trackerr.trackerr.trackers(tracker_type_id, tracker_name, cookies, credentials, updated_at) VALUES($1, $2, $3, $4, NOW())",
		tracker.TypeID, tracker.Name, tracker.Cookies, tracker.Credentials)
	return
}

func (c *client) UpdateTracker(ctx context.Context, tracker *database.Tracker) (err error) {
	_, err = c.db.Exec(ctx,
		"UPDATE trackerr.trackerr.trackers SET cookies = $1, credentials = $2,updated_at = NOW() WHERE tracker_type_id = $3",
		tracker.Cookies, tracker.Credentials, tracker.TypeID)
	return
}

func New(db *pgxpool.Pool) IClient {
	return &client{
		db: db,
	}
}
