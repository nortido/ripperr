package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"torr/internal/helper"
	"torr/internal/model"
)

type tracker interface {
	Header() (header *http.Header)
	Host() (host string)
	DownloadURL(topicID string) (url string)
	TopicURL(topicID string) (url string)
	Detect(url string) bool
	Name() (name string)
}

type proxy interface {
	Get(r *http.Request) (resp *http.Response, err error)
}

type Client interface {
	Prepare(ctx context.Context) (err error)
	GetResponse(ctx context.Context, e *model.Entry) (res *http.Response, err error)
}

type client struct {
	cookie string
	t      tracker
	hc     *http.Client
	header *http.Header
	px     proxy
	cb     *helper.CircuitBreaker
}

func (c *client) Prepare(ctx context.Context) (err error) {
	if c.cookie == "" {
		var (
			host *url.URL
			res  *http.Response
			req  *http.Request
		)
		//TODO: add flaresolverr login and grab cookies
		host, _ = url.Parse(c.t.Host())
		req = &http.Request{Method: http.MethodGet, URL: host}

		req.Header = *c.t.Header()
		res, err = c.response(ctx, req)
		if err != nil || res.StatusCode != http.StatusOK {
			err = fmt.Errorf("%s login error: %w", c.t.Name(), err)
			return
		}
		//c.header = &res.Header
		c.header = c.t.Header()
	}
	return
}

func (c *client) GetResponse(ctx context.Context, e *model.Entry) (res *http.Response, err error) {
	var req *http.Request
	if req, err = http.NewRequestWithContext(ctx, http.MethodGet, c.t.DownloadURL(e.ID), nil); err != nil {
		return
	}
	if c.header == nil {
		err = fmt.Errorf("no header")
		return
	}
	req.Header = *c.header

	//TODO: add flaresolverr
	err = c.cb.Call(func() (err error) {
		res, err = c.response(ctx, req)
		if err != nil {
			return
		}
		if res.StatusCode != http.StatusOK {
			err = fmt.Errorf("unexpected status code: %d", res.StatusCode)
		}
		return
	})
	return
}

func (c *client) response(ctx context.Context, req *http.Request) (res *http.Response, err error) {
	if _, ok := c.px.(proxy); ok {
		return c.px.Get(req)
	}
	res, err = c.hc.Do(req)
	return
}

func New(hc *http.Client, t tracker, px proxy) Client {
	return &client{
		hc: hc,
		t:  t,
		px: px,
		cb: helper.NewCircuitBreaker(2, 10*time.Minute),
	}
}
