package database

import (
	"context"
	"fmt"
	"hash"
	"hash/fnv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"trackerr/internal/database/client/pg"
	"trackerr/internal/helper"
	"trackerr/internal/model/database"
	"trackerr/internal/model/runner"
	"trackerr/internal/model/tracker"
)

type postgres interface {
	GetTracker(ctx context.Context, trackerID int) (res *database.Tracker, err error)
	AddTracker(ctx context.Context, tracker *database.Tracker) (err error)
	UpdateTracker(ctx context.Context, tracker *database.Tracker) (err error)
	GetItem(ctx context.Context, urlHash string) (item *database.Item, err error)
	AddItem(ctx context.Context, item *database.Item) (err error)
	UpdateItem(ctx context.Context, item *database.Item) (err error)
}

type IClient interface {
	GetTracker(ctx context.Context, trackerID int) (res *tracker.Tracker, err error)
	SaveTracker(ctx context.Context, tracker *tracker.Tracker) (err error)
	GetItem(ctx context.Context, link string) (item *runner.HistoryItem, err error)
	SaveItem(ctx context.Context, item *runner.HistoryItem) (err error)
}

type service struct {
	pg postgres
}

func (s *service) GetTracker(ctx context.Context, trackerID int) (res *tracker.Tracker, err error) {
	var sttr *database.Tracker

	if sttr, err = s.pg.GetTracker(ctx, trackerID); err != nil {
		return
	}

	res = &tracker.Tracker{
		TypeID:      sttr.TypeID,
		Name:        sttr.Name,
		Cookies:     helper.ParseCookies(sttr.Cookies, ""),
		Credentials: sttr.Credentials,
		Exists:      true,
	}

	return
}

func (s *service) SaveTracker(ctx context.Context, source *tracker.Tracker) (err error) {
	var target *database.Tracker

	target = &database.Tracker{
		TypeID:      source.TypeID,
		Name:        source.Name,
		Cookies:     helper.CookiesString(source.Cookies),
		Credentials: source.Credentials,
	}
	if !source.Exists {
		return s.pg.AddTracker(ctx, target)
	}
	return s.pg.UpdateTracker(ctx, target)
}

func (s *service) GetItem(ctx context.Context, link string) (item *runner.HistoryItem, err error) {
	var (
		dbItem     *database.Item
		hashBuffer hash.Hash32
		urlHash    string
	)
	hashBuffer = fnv.New32()
	if _, err = hashBuffer.Write([]byte(link)); err != nil {
		return
	}
	urlHash = fmt.Sprint(hashBuffer.Sum32())

	if dbItem, err = s.pg.GetItem(ctx, urlHash); err == pgx.ErrNoRows {
		item = &runner.HistoryItem{URLHash: urlHash}
		return
	} else if err != nil {
		return
	}
	item = &runner.HistoryItem{
		URLHash:       dbItem.URLHash,
		TrackerTypeID: dbItem.TrackerTypeID,
		TopicID:       dbItem.TopicID,
		Category:      dbItem.Category,
		Tag:           dbItem.Tag,
		Name:          dbItem.Name,
		URL:           dbItem.URL,
		UpdatedAt:     dbItem.UpdatedAt,
		Exists:        true,
	}
	return
}

func (s *service) SaveItem(ctx context.Context, item *runner.HistoryItem) (err error) {
	var dbItem *database.Item

	dbItem = &database.Item{
		URLHash:       item.URLHash,
		TrackerTypeID: item.TrackerTypeID,
		TopicID:       item.TopicID,
		Category:      item.Category,
		Tag:           item.Tag,
		Name:          item.Name,
		URL:           item.URL,
	}
	if item.Exists {
		return s.pg.UpdateItem(ctx, dbItem)
	}
	return s.pg.AddItem(ctx, dbItem)
}
func New(db *pgxpool.Pool) IClient {
	return &service{
		pg: pg.New(db),
	}
}
