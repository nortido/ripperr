package kinozal

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
	"torr/internal/helper"
	"torr/internal/model"
)

const (
	Host        = "https://kinozal.tv"
	DownloadURL = Host + "/details.php?id="
	TopicURL    = Host + "/download.php?id="
)

type TrackerHttp interface {
	Login(ctx context.Context) (err error)
	Request(ctx context.Context, e *model.Entry) (res *http.Response, err error)
}

type tracker struct {
	hc     *http.Client
	cookie string
	cb     *helper.CircuitBreaker
}

func (t *tracker) Login(ctx context.Context) (err error) {
	if t.cookie == "" {
		var (
			host *url.URL
			res  *http.Response
		)
		host, _ = url.Parse(Host)
		res, err = t.hc.Do(&http.Request{Method: http.MethodGet, URL: host})
		if err != nil || res.StatusCode != http.StatusOK {
			err = fmt.Errorf("kinozal login error: %w", err)
			return
		}
		//TODO: add full cookie fetching
		t.cookie = "uid=PLACEHOLDER,pass=PLACEHOLDER"
	}
	return
}

func (t *tracker) Request(ctx context.Context, e *model.Entry) (res *http.Response, err error) {
	var req *http.Request
	if req, err = http.NewRequestWithContext(ctx, http.MethodGet, DownloadURL+e.ID, nil); err != nil {
		return
	}
	if t.cookie == "" {
		err = fmt.Errorf("no login cookie")
		return
	}
	req.Header.Set("Cookie", t.cookie)

	err = t.cb.Call(func() (err error) {
		res, err = t.hc.Do(req)
		if res.StatusCode != http.StatusOK {
			err = fmt.Errorf("unexpected status code: %d", res.StatusCode)
		}
		return
	})
	return
}

func New(hc *http.Client) TrackerHttp {
	return &tracker{
		hc: hc,
		cb: helper.NewCircuitBreaker(2, 10*time.Minute),
	}
}
