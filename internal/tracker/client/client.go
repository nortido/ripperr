package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"torr/internal/helper"
	"torr/internal/model"
)

type tracker interface {
	Header() (header *http.Header)
	Host() (host string)
	LoginData() (link string, data *strings.Reader)
	DownloadURL(topicID string) (url string)
	TopicURL(topicID string) (url string)
	Detect(url string) bool
	Name() (name string)
}

type proxy interface {
	Get(r *http.Request, onlyCookies bool) (resp *http.Response, err error)
}

type Client interface {
	Login(ctx context.Context) (err error)
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

func (c *client) Login(ctx context.Context) (err error) {
	var (
		link string
		cv   string
		req  *http.Request
		body *strings.Reader
		res  *http.Response
	)
	link, body = c.t.LoginData()
	if req, err = http.NewRequestWithContext(ctx, http.MethodPost, link, body); err != nil {
		return
	}
	res, err = c.response(ctx, req, true)

	if err != nil || res.StatusCode != http.StatusOK {
		err = fmt.Errorf("%s login error: %w", c.t.Name(), err)
		return
	}
	c.header = c.t.Header()
	for _, cv = range res.Header.Values("Set-Cookie") {
		c.header.Add("Cookie", cv)
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

	// TODO: add flaresolverr
	err = c.cb.Call(func() (err error) {
		//res, err = c.response(ctx, req, false)
		res, err = c.hc.Do(req)
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

func (c *client) response(ctx context.Context, req *http.Request, onlyCookies bool) (res *http.Response, err error) {
	if _, ok := c.px.(proxy); ok {
		return c.px.Get(req, onlyCookies)
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
