package flaresolverr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"ripperr/internal/model/proxy"
)

const responseOK = "ok"

func PrepareParams(r *http.Request, opts *proxy.Options) (params *Payload) {
	params = &Payload{
		URL: r.URL.String(),
	}
	if opts != nil {
		params.Session = opts.Session
		params.OnlyCookies = opts.OnlyCookies
		if len(opts.Cookies) > 0 {
			params.Cookies = cookiesForFlare(opts.Cookies)
		}
	}
	return
}

func cookiesForFlare(src []*http.Cookie) Cookies {
	if len(src) == 0 {
		return nil
	}
	out := make(Cookies, 0, len(src))
	for _, c := range src {
		if c == nil || c.Name == "" {
			continue
		}
		// без домена и пути FlareSolverr сам привяжет куку к URL запроса
		out = append(out, &http.Cookie{Name: c.Name, Value: c.Value})
	}
	return out
}

func SolutionCookies(resp *Response) []*http.Cookie {
	if resp == nil || resp.Solution == nil || len(resp.Solution.Cookies) == 0 {
		return nil
	}
	out := make([]*http.Cookie, 0, len(resp.Solution.Cookies))
	for _, c := range resp.Solution.Cookies {
		if c == nil || c.Name == "" {
			continue
		}
		clone := *c
		out = append(out, &clone)
	}
	return out
}

func BuildResponse(resp *Response, req *http.Request) (result *http.Response, err error) {
	if resp == nil {
		err = errors.New("flaresolverr error: empty response")
		return
	}
	if resp.Status != responseOK {
		err = fmt.Errorf("flaresolverr error: %s", resp.Message)
		return
	}
	if resp.Solution == nil {
		err = errors.New("flaresolverr error: response without solution")
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
	for _, cv := range SolutionCookies(resp) {
		result.Header.Add("Set-Cookie", cv.String())
	}

	return
}
