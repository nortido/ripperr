package proxy

import (
	"fmt"
	"net/http"
	"torr/internal/model/proxy"

	fclient "github.com/nortido/go-flaresolverr"

	fmapper "torr/internal/proxy/mapper/flaresolverr"
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
	index struct {
		fl fclient.IClient
	}
}

func (f *client) Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (res *http.Response, err error) {
	var (
		params *fclient.Payload
		fres   *fclient.Response
	)
	switch proxyType {
	case proxy.ProxyNone:
		res, err = f.hc.Do(r)
	case proxy.ProxyFlaresolverr:
		fmt.Println("sending request to flaresolverr")
		params = fmapper.PrepareParams(r, opts)
		if err != nil {
			return
		}
		fres, err = f.index.fl.Send(r, params)
		res, err = fmapper.MakeResponse(fres, r)
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
	px.index.fl = fclient.New(hc, &fclient.Config{
		Host:       conf.Flaresolverr.URL,
		MaxTimeout: conf.Flaresolverr.MaxTimeout,
	})
	sw = px
	return
}
