package tlsclient

import (
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"os"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type Config struct {
	Timeout  time.Duration
	ProxyURL string
	Profile  string
}

type Client struct {
	inner tls_client.HttpClient
}

func New(cfg Config) (*Client, error) {
	profile, ok := profiles.MappedTLSClients[strings.ToLower(cfg.Profile)]
	if !ok {
		return nil, fmt.Errorf("unknown TLS profile: %s", cfg.Profile)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	opts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(int(timeout.Seconds())),
		tls_client.WithClientProfile(profile),
	}
	if proxyURL := resolveProxyURL(cfg.ProxyURL); proxyURL != "" {
		opts = append(opts, tls_client.WithProxyUrl(proxyURL))
	}

	inner, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		return nil, err
	}
	return &Client{inner: inner}, nil
}

func (c *Client) Do(req *nethttp.Request) (*nethttp.Response, error) {
	fReq, err := toFHTTPRequest(req)
	if err != nil {
		return nil, err
	}
	fResp, err := c.inner.Do(fReq)
	if err != nil {
		return nil, err
	}
	return toNetHTTPResponse(fResp, req), nil
}

func resolveProxyURL(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func toFHTTPRequest(req *nethttp.Request) (*fhttp.Request, error) {
	var body io.Reader
	if req.Body != nil {
		body = req.Body
	}
	fReq, err := fhttp.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), body)
	if err != nil {
		return nil, err
	}
	fReq.Header = make(fhttp.Header, len(req.Header))
	for k, vv := range req.Header {
		dst := make([]string, len(vv))
		copy(dst, vv)
		fReq.Header[k] = dst
	}
	fReq.ContentLength = req.ContentLength
	if req.URL != nil {
		parsed, parseErr := url.Parse(req.URL.String())
		if parseErr != nil {
			return nil, parseErr
		}
		fReq.URL = parsed
		if host := parsed.Host; host != "" && (req.Host == "" || strings.Contains(req.Host, "://")) {
			fReq.Host = host
		} else if req.Host != "" {
			fReq.Host = req.Host
		}
	}
	return fReq, nil
}

func toNetHTTPResponse(fResp *fhttp.Response, req *nethttp.Request) *nethttp.Response {
	return &nethttp.Response{
		Status:           fResp.Status,
		StatusCode:       fResp.StatusCode,
		Proto:            fResp.Proto,
		ProtoMajor:       fResp.ProtoMajor,
		ProtoMinor:       fResp.ProtoMinor,
		Header:           nethttp.Header(fResp.Header),
		Body:             fResp.Body,
		ContentLength:    fResp.ContentLength,
		TransferEncoding: fResp.TransferEncoding,
		Close:            fResp.Close,
		Uncompressed:     fResp.Uncompressed,
		Trailer:          nethttp.Header(fResp.Trailer),
		Request:          req,
	}
}
