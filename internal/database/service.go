package database

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"trackerr/internal/database/client/pg"
	"trackerr/internal/helper"
	"trackerr/internal/model/database"
	"trackerr/internal/model/tracker"
)

type postgres interface {
	GetTracker(ctx context.Context, trackerID int) (res *database.Tracker, err error)
	AddTracker(ctx context.Context, tracker *database.Tracker) (err error)
	UpdateTracker(ctx context.Context, tracker *database.Tracker) (err error)
}

type IClient interface {
	GetTracker(ctx context.Context, trackerID int) (res *tracker.Tracker, err error)
	SaveTracker(ctx context.Context, tracker *tracker.Tracker) (err error)
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

func New(db *pgxpool.Pool) IClient {
	return &service{
		pg: pg.New(db),
	}
}
