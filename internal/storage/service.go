package storage

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"trackerr/internal/helper"
	"trackerr/internal/model/runner"
	"trackerr/internal/model/storage"
	"trackerr/internal/model/tracker"
	"trackerr/internal/storage/client/pg"
)

type postgres interface {
	Bootstrap(ctx context.Context) error
	GetTracker(ctx context.Context, trackerID int) (res *storage.Tracker, err error)
	AddTracker(ctx context.Context, tracker *storage.Tracker) (err error)
	UpdateTracker(ctx context.Context, tracker *storage.Tracker) (err error)
	GetItem(ctx context.Context, urlHash string) (item *storage.Item, err error)
	AddItem(ctx context.Context, item *storage.Item) (err error)
	UpdateItem(ctx context.Context, item *storage.Item) (err error)
	AddCheckLog(ctx context.Context, entry *storage.CheckLogEntry) error
	ListCheckLog(ctx context.Context, limit, offset int, statusFilter string, ignoredOnly bool, sortBy string) ([]storage.CheckLogEntry, int, error)
	ListIgnore(ctx context.Context) ([]storage.IgnoreEntry, error)
	AddIgnore(ctx context.Context, hash, name, url string) error
	DeleteIgnore(ctx context.Context, hash string) error
	GetIgnoreSet(ctx context.Context) (map[string]struct{}, error)
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
	ListDueItems(ctx context.Context) ([]storage.DueItem, error)
	DeleteItem(ctx context.Context, urlHash string) error
	SetForceCheck(ctx context.Context, urlHash string, force bool) (bool, error)
	ForceCheckAll(ctx context.Context, force bool) (int64, error)
}

type IClient interface {
	Bootstrap(ctx context.Context) error
	GetTracker(ctx context.Context, trackerID int) (res *tracker.Tracker, err error)
	SaveTracker(ctx context.Context, source *tracker.Tracker) (err error)
	GetItem(ctx context.Context, link string) (item *runner.HistoryItem, err error)
	SaveItem(ctx context.Context, item *runner.HistoryItem) (err error)
	AddCheckLog(ctx context.Context, entry *storage.CheckLogEntry) error
	ListCheckLog(ctx context.Context, limit, offset int, statusFilter string, ignoredOnly bool, sortBy string) ([]storage.CheckLogEntry, int, error)
	ListIgnore(ctx context.Context) ([]storage.IgnoreEntry, error)
	AddIgnore(ctx context.Context, hash, name, url string) error
	DeleteIgnore(ctx context.Context, hash string) error
	GetIgnoreSet(ctx context.Context) (map[string]struct{}, error)
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
	ListDueItems(ctx context.Context) ([]storage.DueItem, error)
	DeleteItem(ctx context.Context, urlHash string) error
	SetForceCheck(ctx context.Context, urlHash string, force bool) (bool, error)
	ForceCheckAll(ctx context.Context, force bool) (int64, error)
}

type service struct {
	pg postgres
}

func (s *service) Bootstrap(ctx context.Context) error {
	return s.pg.Bootstrap(ctx)
}

func (s *service) GetTracker(ctx context.Context, trackerID int) (res *tracker.Tracker, err error) {
	var sttr *storage.Tracker

	if sttr, err = s.pg.GetTracker(ctx, trackerID); err != nil {
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

func (s *service) SaveTracker(ctx context.Context, source *tracker.Tracker) (err error) {
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
		return s.pg.AddTracker(ctx, target)
	}
	return s.pg.UpdateTracker(ctx, target)
}

func (s *service) GetItem(ctx context.Context, link string) (item *runner.HistoryItem, err error) {
	var dbItem *storage.Item

	urlHash := URLHash(link)

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
		CreatedAt:     dbItem.CreatedAt,
		RecheckAt:     dbItem.RecheckAt,
		Exists:        dbItem.Exists,
		ForceCheck:    dbItem.ForceCheck,
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

func (s *service) AddCheckLog(ctx context.Context, entry *storage.CheckLogEntry) error {
	return s.pg.AddCheckLog(ctx, entry)
}

func (s *service) ListCheckLog(ctx context.Context, limit, offset int, statusFilter string, ignoredOnly bool, sortBy string) ([]storage.CheckLogEntry, int, error) {
	return s.pg.ListCheckLog(ctx, limit, offset, statusFilter, ignoredOnly, sortBy)
}

func (s *service) ListIgnore(ctx context.Context) ([]storage.IgnoreEntry, error) {
	return s.pg.ListIgnore(ctx)
}

func (s *service) AddIgnore(ctx context.Context, hash, name, url string) error {
	return s.pg.AddIgnore(ctx, hash, name, url)
}

func (s *service) DeleteIgnore(ctx context.Context, hash string) error {
	return s.pg.DeleteIgnore(ctx, hash)
}

func (s *service) GetIgnoreSet(ctx context.Context) (map[string]struct{}, error) {
	return s.pg.GetIgnoreSet(ctx)
}

func (s *service) GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error) {
	return s.pg.GetSetting(ctx, key)
}

func (s *service) SaveSetting(ctx context.Context, key string, value json.RawMessage) error {
	return s.pg.SaveSetting(ctx, key, value)
}

func (s *service) ListDueItems(ctx context.Context) ([]storage.DueItem, error) {
	return s.pg.ListDueItems(ctx)
}

func (s *service) DeleteItem(ctx context.Context, urlHash string) error {
	return s.pg.DeleteItem(ctx, urlHash)
}

func (s *service) SetForceCheck(ctx context.Context, urlHash string, force bool) (bool, error) {
	return s.pg.SetForceCheck(ctx, urlHash, force)
}

func (s *service) ForceCheckAll(ctx context.Context, force bool) (int64, error) {
	return s.pg.ForceCheckAll(ctx, force)
}

func New(db *pgxpool.Pool) IClient {
	return &service{
		pg: pg.New(db),
	}
}
