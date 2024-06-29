package storage

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5/pgxpool"

	"trackerr/internal/helper"
	"trackerr/internal/model/runner"
	"trackerr/internal/model/storage"
	"trackerr/internal/model/tracker"
	"trackerr/internal/storage/client/pg"
)

type postgres interface {
	GetTracker(ctx context.Context, trackerID int) (res *storage.Tracker, err error)
	AddTracker(ctx context.Context, tracker *storage.Tracker) (err error)
	UpdateTracker(ctx context.Context, tracker *storage.Tracker) (err error)
	GetItem(ctx context.Context, urlHash string) (item *storage.Item, err error)
	AddItem(ctx context.Context, item *storage.Item) (err error)
	UpdateItem(ctx context.Context, item *storage.Item) (err error)
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
	var sttr = &storage.Tracker{}

	res = &tracker.Tracker{}

	if sttr, err = s.pg.GetTracker(ctx, trackerID); err != nil {
		return
	}
	res = &tracker.Tracker{
		TypeID:      sttr.TypeID,
		Name:        sttr.Name,
		Cookies:     helper.ParseCookies(sttr.Cookies, ""),
		Credentials: sttr.Credentials,
		Publicity:   sttr.Publicity,
		Exists:      sttr.Exists,
	}
	return
}

func (s *service) SaveTracker(ctx context.Context, source *tracker.Tracker) (err error) {
	var target *storage.Tracker

	target = &storage.Tracker{
		TypeID:      source.TypeID,
		Name:        source.Name,
		Host:        source.Host,
		LoginURL:    source.LoginURL,
		DownloadURL: source.DownloadURL,
		Cookies:     helper.CookiesString(source.Cookies),
		Credentials: source.Credentials,
		Publicity:   source.Publicity,
	}
	if !source.Exists {
		return s.pg.AddTracker(ctx, target)
	}
	return s.pg.UpdateTracker(ctx, target)
}

func (s *service) GetItem(ctx context.Context, link string) (item *runner.HistoryItem, err error) {
	var (
		urlHash    string
		dbItem     *storage.Item
		hashBuffer = fnv.New32()
	)
	if _, err = hashBuffer.Write([]byte(link)); err != nil {
		return
	}
	urlHash = fmt.Sprint(hashBuffer.Sum32())
	item = &runner.HistoryItem{URLHash: urlHash}

	if dbItem, err = s.pg.GetItem(ctx, urlHash); err != nil {
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
		RecheckAt:     dbItem.RecheckAt,
		Exists:        dbItem.Exists,
	}
	return
}

func (s *service) SaveItem(ctx context.Context, item *runner.HistoryItem) (err error) {
	var dbItem *storage.Item

	dbItem = &storage.Item{
		URLHash:       item.URLHash,
		TrackerTypeID: item.TrackerTypeID,
		TopicID:       item.TopicID,
		Category:      item.Category,
		Tag:           item.Tag,
		Name:          item.Name,
		URL:           item.URL,
		CreatedAt:     item.CreatedAt,
		RecheckAt:     item.RecheckAt,
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
