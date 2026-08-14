package storage

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"trackerr/internal/helper"
	"trackerr/internal/model/runner"
	"trackerr/internal/model/storage"
	"trackerr/internal/model/tracker"
	"trackerr/internal/storage/client/pg"
)

type Service struct {
	*pg.Client
}

// GetTracker shadows the embedded pg.Client method: it returns *tracker.Tracker, not *storage.Tracker.
func (s *Service) GetTracker(ctx context.Context, trackerID int) (res *tracker.Tracker, err error) {
	var sttr *storage.Tracker

	if sttr, err = s.Client.GetTracker(ctx, trackerID); err != nil {
		return
	}
	res = &tracker.Tracker{
		TypeID:      sttr.TypeID,
		Name:        sttr.Name,
		Domain:      sttr.Domain,
		Cookies:     helper.ParseCookies(sttr.Cookies),
		Credentials: sttr.Credentials,
		Publicity:   sttr.Publicity,
		Exists:      sttr.Exists,
	}
	return
}

func (s *Service) SaveTracker(ctx context.Context, source *tracker.Tracker) (err error) {
	var target *storage.Tracker

	target = &storage.Tracker{
		TypeID:      source.TypeID,
		Name:        source.Name,
		Domain:      source.Domain,
		LoginURL:    source.LoginURL,
		DownloadURL: source.DownloadURL,
		Cookies:     helper.CookiesString(source.Cookies),
		Credentials: source.Credentials,
		Publicity:   source.Publicity,
	}
	if !source.Exists {
		return s.Client.AddTracker(ctx, target)
	}
	return s.Client.UpdateTracker(ctx, target)
}

// GetItem shadows the embedded pg.Client method: it returns *runner.HistoryItem, not *storage.Item.
func (s *Service) GetItem(ctx context.Context, link string) (item *runner.HistoryItem, err error) {
	var dbItem *storage.Item

	urlHash := URLHash(link)

	if dbItem, err = s.Client.GetItem(ctx, urlHash); err != nil {
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
		CreatedAt:     dbItem.CreatedAt,
		RecheckAt:     dbItem.RecheckAt,
		Exists:        dbItem.Exists,
		ForceCheck:    dbItem.ForceCheck,
	}
	return
}

func (s *Service) SaveItem(ctx context.Context, item *runner.HistoryItem) (err error) {
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
		return s.Client.UpdateItem(ctx, dbItem)
	}
	return s.Client.AddItem(ctx, dbItem)
}

func New(db *pgxpool.Pool) *Service {
	return &Service{pg.New(db)}
}
