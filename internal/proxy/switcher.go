package proxy

import (
	"fmt"
	"net/http"

	"trackerr/internal/model/proxy"
	fmapper "trackerr/internal/proxy/mapper/flaresolverr"
)

type IClient interface {
	Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (resp *http.Response, err error)
	DestroyFlareSession(sessionID string) error
}

type client struct {
	hc   *http.Client
	conf *proxy.Config
	fl   *fmapper.Client
}

func (f *client) Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (res *http.Response, err error) {
	var (
		params *fmapper.Payload
		fres   *fmapper.Response
	)
	switch proxyType {
	case proxy.TypeFlare:
		if opts != nil && opts.Session != "" {
			mu := sessionLocks.Mutex(opts.Session)
			mu.Lock()
			defer mu.Unlock()
		}
		params = fmapper.PrepareParams(r, opts)
		if fres, err = f.fl.Send(r, params); err != nil {
			return
		}
		res, err = fmapper.BuildResponse(fres, r)
	default:
		err = fmt.Errorf("unknown proxy type: %d", proxyType)
	}
	return
}

func New(hc *http.Client, conf *proxy.Config) (sw IClient) {
	px := &client{
		hc:   hc,
		conf: conf,
	}
	px.fl = fmapper.New(hc, &fmapper.Config{
		Host:       conf.Flaresolverr.URL,
		MaxTimeout: conf.Flaresolverr.MaxTimeout,
	})
	sw = px
	return
}
