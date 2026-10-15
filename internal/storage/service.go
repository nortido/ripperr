package storage

import (
	"context"
	"database/sql"

	"ripperr/internal/helper"
	"ripperr/internal/model/runner"
	"ripperr/internal/model/storage"
	"ripperr/internal/model/tracker"
	"ripperr/internal/secrets"
	"ripperr/internal/storage/client/sqlite"
)

type Service struct {
	*sqlite.Client
}

func (s *Service) GetTracker(ctx context.Context, trackerID int) (res *tracker.Tracker, err error) {
	// перекрывает метод встроенного sqlite.Client, тот отдает *storage.Tracker
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
	target := &storage.Tracker{
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
		return s.AddTracker(ctx, target)
	}
	return s.UpdateTracker(ctx, target)
}

func (s *Service) AllItems(ctx context.Context) (map[string]*runner.HistoryItem, error) {
	dbItems, err := s.ListItems(ctx)
	if err != nil {
		return nil, err
	}
	items := make(map[string]*runner.HistoryItem, len(dbItems))
	for i := range dbItems {
		dbItem := &dbItems[i]
		items[dbItem.URLHash] = &runner.HistoryItem{
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
	}
	return items, nil
}

func (s *Service) SaveItem(ctx context.Context, item *runner.HistoryItem) (err error) {
	dbItem := &storage.Item{
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
		return s.UpdateItem(ctx, dbItem)
	}
	return s.AddItem(ctx, dbItem)
}

func New(db *sql.DB, box *secrets.Box) *Service {
	return &Service{sqlite.New(db, box)}
}
