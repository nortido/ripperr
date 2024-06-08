package tracker

import (
	"bytes"
	"context"
	"net/http"
	"torr/internal/model"
	"torr/internal/tracker/router"
)

type routerService interface {
	GetTracker(ctx context.Context, e *model.Entry) (t router.Tracker, err error)
}

type Client struct {
	client http.Client
	router routerService
}

func (c *Client) GetData(ctx context.Context, e *model.Entry) (buf *bytes.Buffer, err error) {
	var (
		b   = bytes.Buffer{}
		res *http.Response
		tr  router.Tracker
	)
	if tr, err = c.router.GetTracker(ctx, e); err != nil {
		return
	}
	if res, err = tr.Request(ctx, e); err != nil {
		return
	}
	if _, err = b.ReadFrom(res.Body); err != nil {
		return
	}
	if err = res.Body.Close(); err != nil {
		return
	}
	buf = &b

	return
}

func New(r routerService) *Client {
	return &Client{
		client: http.Client{},
		router: r,
	}
}
