package flaresolverr

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/nortido/go-flaresolverr"

	"trackerr/internal/model/proxy"
)

const (
	responseOK    = "ok"
	responseError = "error"
)

func PrepareParams(r *http.Request, opts *proxy.Options) (params *flaresolverr.Payload) {
	params = &flaresolverr.Payload{
		URL: r.URL.String(),
	}
	if opts != nil {
		params.Session = opts.Session
		params.OnlyCookies = opts.OnlyCookies
		params.Cookies = opts.Cookies
	}
	return
}

func BuildResponse(resp *flaresolverr.Response, req *http.Request) (result *http.Response, err error) {
	var (
		cv *http.Cookie
	)

	if resp.Status != responseOK {
		err = fmt.Errorf("flaresolverr error: %s", resp.Message)
		return
	}
	result = &http.Response{
		StatusCode: resp.Solution.Status,
		Body:       io.NopCloser(bytes.NewBufferString(resp.Solution.Response)),
		Request:    req,
		Header: http.Header{
			"Content-Length": []string{fmt.Sprint(len(resp.Solution.Response))},
			"User-Agent":     []string{resp.Solution.UserAgent},
		},
	}
	for _, cv = range resp.Solution.Cookies {
		result.Header.Add("Set-Cookie", cv.String())
	}

	return
}
