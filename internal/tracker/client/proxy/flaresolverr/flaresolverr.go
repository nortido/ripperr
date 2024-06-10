package flaresolverr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ServiceParams struct {
	Host       string
	MaxTimeout int
}

type Client interface {
	Get(r *http.Request, onlyCookies bool) (resp *http.Response, err error)
}

type request struct {
	Cmd         string          `json:"cmd"`
	URL         string          `json:"url"`
	Cookies     []requestCookie `json:"cookies,omitempty"`
	OnlyCookies bool            `json:"returnOnlyCookies,omitempty"`
	MaxTimeout  int             `json:"maxTimeout"`
	PostData    string          `json:"postData,omitempty"`
}

type requestCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type response struct {
	Status   string `json:"status"`
	Solution struct {
		Url       string           `json:"url"`
		Status    int              `json:"status"`
		Cookies   []responseCookie `json:"cookies"`
		UserAgent string           `json:"userAgent"`
		Response  string           `json:"response"`
	} `json:"solution"`
}

type responseCookie struct {
	Domain   string `json:"domain"`
	HttpOnly bool   `json:"httpOnly"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	SameSite string `json:"sameSite"`
	Secure   bool   `json:"secure"`
	Value    string `json:"value"`
	Expiry   int64  `json:"expiry,omitempty"`
}

type client struct {
	hc   *http.Client
	conf *ServiceParams
}

func (f *client) Get(r *http.Request, onlyCookies bool) (result *http.Response, err error) {
	var (
		data   []byte
		p      *request
		flresp *response
		resp   *http.Response
	)

	if p, err = f.getFlareParams(r, onlyCookies); err != nil {
		return
	}
	if data, err = json.Marshal(p); err != nil {
		return
	}
	if resp, err = f.hc.Post(f.conf.Host, "application/json", bytes.NewBuffer(data)); err != nil {
		return
	}
	if err = json.NewDecoder(resp.Body).Decode(&flresp); err != nil {
		return
	}
	result, err = getHttpResponse(flresp)

	return
}

func (f *client) getFlareParams(r *http.Request, onlyCookies bool) (params *request, err error) {
	var (
		b []byte
	)
	p := &request{
		URL:         r.URL.String(),
		MaxTimeout:  f.conf.MaxTimeout,
		OnlyCookies: onlyCookies,
	}
	switch r.Method {
	case http.MethodGet:
		p.Cmd = "request.get"
	case http.MethodPost:
		if b, err = io.ReadAll(r.Body); err != nil {
			err = fmt.Errorf("error reading proxy request body")
			return
		}
		p.Cmd = "request.post"
		p.PostData = string(b)
	default:
		err = fmt.Errorf("unknown http method: %s", r.Method)
		return
	}

	// TODO: add sessions instead of cookies
	p.Cookies = make([]requestCookie, 0, len(r.Cookies()))

	for _, c := range r.Cookies() {
		if c == nil {
			continue
		}
		p.Cookies = append(p.Cookies, requestCookie{Name: c.Name, Value: c.Value})
	}
	params = p

	return
}

func getHttpResponse(flresp *response) (result *http.Response, err error) {
	var (
		cv string
	)
	result = &http.Response{
		StatusCode: flresp.Solution.Status,
		Body:       io.NopCloser(bytes.NewBufferString(flresp.Solution.Response)),
		Header: http.Header{
			"Content-Type":   []string{"text/html; charset=Windows-1251"}, //TODO: add parser for content type
			"Location":       []string{flresp.Solution.Url},
			"Content-Length": []string{fmt.Sprintf("%d", len(flresp.Solution.Response))},
			"User-Agent":     []string{flresp.Solution.UserAgent},
		},
	}
	for _, cv = range getCookiesString(flresp.Solution.Cookies) {
		result.Header.Add("Set-Cookie", cv)
	}

	return
}

func getCookiesString(source []responseCookie) (result []string) {
	var (
		ck *http.Cookie
		c  responseCookie
		i  int
	)

	result = make([]string, len(source))

	for i, c = range source {
		ck = &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Domain:   c.Domain,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		}
		if c.Expiry != 0 {
			ck.Expires = time.Unix(c.Expiry, 0)
		}
		result[i] = ck.String()
	}
	return
}

func New(hc *http.Client, conf *ServiceParams) Client {
	return &client{
		hc:   hc,
		conf: conf,
	}
}
