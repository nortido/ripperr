package proxy

import (
	"fmt"
	"net/http"

	fclient "github.com/nortido/go-flaresolverr"

	"trackerr/internal/model/proxy"
	fmapper "trackerr/internal/proxy/mapper/flaresolverr"
)

type Config struct {
	Host       string
	MaxTimeout int
}

type IClient interface {
	Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (resp *http.Response, err error)
}

type client struct {
	hc    *http.Client
	conf  *proxy.Config
	cache struct {
		fl fclient.IClient
	}
}

func (f *client) Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (res *http.Response, err error) {
	var (
		params *fclient.Payload
		fres   *fclient.Response
	)
	switch proxyType {
	case proxy.TypeFlare:
		//logger.Info(fmt.Sprintf("getting \"%s\" by flaresolverr", r.URL.String()))
		params = fmapper.PrepareParams(r, opts)
		if err != nil {
			return
		}
		if fres, err = f.cache.fl.Send(r, params); err != nil {
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
	px.cache.fl = fclient.New(hc, &fclient.Config{
		Host:       conf.Flaresolverr.URL,
		MaxTimeout: conf.Flaresolverr.MaxTimeout,
	})
	sw = px
	return
}
