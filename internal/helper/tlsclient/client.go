package tlsclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/http2"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"

	"ripperr/internal/helper"
	"ripperr/internal/helper/tlscapture"
)

type Config struct {
	Timeout     time.Duration
	ProxyURL    string
	Profile     string
	Direct      bool
	NoRedirects bool
	HeaderOrder []string
	Fingerprint *tlscapture.Fingerprint
}

type Client struct {
	inner       tls_client.HttpClient
	headerOrder []string
}

func New(cfg Config) (*Client, error) {
	return newClient(cfg)
}

func Probe(ctx context.Context, cfg Config, target string) error {
	c, err := newClient(cfg, tls_client.WithInsecureSkipVerify())
	if err != nil {
		return err
	}
	defer c.inner.CloseIdleConnections()
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("probe status %d", resp.StatusCode)
	}
	return nil
}

func newClient(cfg Config, extra ...tls_client.HttpClientOption) (*Client, error) {
	profile, ok := profiles.MappedTLSClients[strings.ToLower(cfg.Profile)]
	if !ok {
		return nil, fmt.Errorf("unknown TLS profile: %s", cfg.Profile)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	var opts []tls_client.HttpClientOption
	if cfg.Fingerprint != nil {
		var err error
		if profile, err = capturedProfile(profile, cfg.Fingerprint); err != nil {
			return nil, err
		}
		// tls-client игнорирует ClientHelloID.RandomExtensionOrder, порядок расширений мешает только эта опция
		opts = append(opts, tls_client.WithRandomTLSExtensionOrder())
	}
	opts = append(opts,
		tls_client.WithTimeoutSeconds(int(timeout.Seconds())),
		tls_client.WithClientProfile(profile),
	)
	if cfg.NoRedirects {
		opts = append(opts, tls_client.WithNotFollowRedirects())
	} else {
		opts = append(opts, tls_client.WithCustomRedirectFunc(checkDownloadRedirect))
	}
	// Direct: только явный прокси, без env. Прокси страниц трекера решает вызывающий
	proxyURL := strings.TrimSpace(cfg.ProxyURL)
	if !cfg.Direct {
		proxyURL = resolveProxyURL(cfg.ProxyURL)
	}
	if proxyURL != "" {
		opts = append(opts, tls_client.WithProxyUrl(proxyURL))
	}

	opts = append(opts, extra...)
	inner, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), opts...)
	if err != nil {
		err = helper.RedactURLError(err)
		if proxyURL != "" {
			// tls-client печатает адрес прокси целиком, с логином и паролем
			err = errors.New(strings.ReplaceAll(err.Error(), proxyURL, helper.RedactURL(proxyURL)))
		}
		return nil, err
	}
	return &Client{inner: inner, headerOrder: cfg.HeaderOrder}, nil
}

func (c *Client) Do(req *nethttp.Request) (*nethttp.Response, error) {
	fReq, err := toFHTTPRequest(req)
	if err != nil {
		return nil, err
	}
	if len(c.headerOrder) > 0 {
		if _, has := fReq.Header[fhttp.HeaderOrderKey]; !has {
			fReq.Header[fhttp.HeaderOrderKey] = append([]string(nil), c.headerOrder...)
		}
	}
	fResp, err := c.inner.Do(fReq)
	if err != nil {
		return nil, err
	}
	return toNetHTTPResponse(fResp, req), nil
}

func ChromeProfile(ua string) string {
	var majors []int
	byMajor := map[int]string{}
	for key := range profiles.MappedTLSClients {
		rest, ok := strings.CutPrefix(key, "chrome_")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(rest)
		if err != nil {
			continue
		}
		majors = append(majors, n)
		byMajor[n] = key
	}
	if len(majors) == 0 {
		return ""
	}
	slices.Sort(majors)
	want := majors[len(majors)-1]
	if uaMajor, ok := ChromeMajor(ua); ok {
		want = majors[0]
		for _, n := range majors {
			if n <= uaMajor {
				want = n
			}
		}
	}
	return byMajor[want]
}

var chromeMajor = regexp.MustCompile(`Chrome/(\d+)`)

func ChromeMajor(ua string) (int, bool) {
	m := chromeMajor.FindStringSubmatch(ua)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}

func capturedProfile(base profiles.ClientProfile, fp *tlscapture.Fingerprint) (profiles.ClientProfile, error) {
	raw := slices.Clone(fp.ClientHello)
	if _, err := specFromHello(raw); err != nil {
		return profiles.ClientProfile{}, fmt.Errorf("parse captured ClientHello: %w", err)
	}
	id := tls.ClientHelloID{
		Client:  "Chrome",
		Version: "captured",
		// Спек собираем заново на каждое соединение: ApplyPreset меняет расширения, общие указатели дали бы гонку
		SpecFactory: func() (tls.ClientHelloSpec, error) { return specFromHello(raw) },
	}

	settings := base.GetSettings()
	settingsOrder := base.GetSettingsOrder()
	if len(fp.Settings) > 0 {
		settings = make(map[http2.SettingID]uint32, len(fp.Settings))
		settingsOrder = make([]http2.SettingID, 0, len(fp.Settings))
		for _, s := range fp.Settings {
			settings[http2.SettingID(s.ID)] = s.Val
			settingsOrder = append(settingsOrder, http2.SettingID(s.ID))
		}
	}
	pseudo := base.GetPseudoHeaderOrder()
	if len(fp.PseudoHeaderOrder) > 0 {
		pseudo = slices.Clone(fp.PseudoHeaderOrder)
	}
	flow := base.GetConnectionFlow()
	if fp.ConnectionFlow != 0 {
		flow = fp.ConnectionFlow
	}
	priorities := base.GetPriorities()
	if len(fp.Priorities) > 0 {
		priorities = make([]http2.Priority, 0, len(fp.Priorities))
		for _, p := range fp.Priorities {
			priorities = append(priorities, http2.Priority{
				StreamID:      p.StreamID,
				PriorityParam: http2.PriorityParam{StreamDep: p.StreamDep, Exclusive: p.Exclusive, Weight: p.Weight},
			})
		}
	}
	headerPriority := base.GetHeaderPriority()
	if hp := fp.HeaderPriority; hp != nil {
		headerPriority = &http2.PriorityParam{StreamDep: hp.StreamDep, Exclusive: hp.Exclusive, Weight: hp.Weight}
	}
	streamID := base.GetStreamID()
	if fp.StreamID != 0 {
		streamID = fp.StreamID
	}

	return profiles.NewClientProfile(id, settings, settingsOrder, pseudo, flow, priorities, headerPriority, streamID, false,
		base.GetHttp3Settings(), base.GetHttp3SettingsOrder(), base.GetHttp3PriorityParam(), base.GetHttp3PseudoHeaderOrder(), base.GetHttp3SendGreaseFrames()), nil
}

func specFromHello(raw []byte) (tls.ClientHelloSpec, error) {
	f := tls.Fingerprinter{AllowBluntMimicry: true}
	spec, err := f.FingerprintClientHello(raw)
	if err != nil {
		return tls.ClientHelloSpec{}, err
	}
	var hasSNI bool
	for _, ext := range spec.Extensions {
		switch e := ext.(type) {
		case *tls.SNIExtension:
			hasSNI = true
		case *tls.UtlsPaddingExtension:
			e.GetPaddingLen = tls.BoringPaddingStyle
		}
	}
	if !hasSNI {
		// Захват по IP идет без SNI, а трекерам он нужен; пустое расширение ApplyPreset заполнит именем хоста
		at := slices.IndexFunc(spec.Extensions, func(e tls.TLSExtension) bool {
			_, grease := e.(*tls.UtlsGREASEExtension)
			return !grease
		})
		if at < 0 {
			at = len(spec.Extensions)
		}
		spec.Extensions = slices.Insert(spec.Extensions, at, tls.TLSExtension(&tls.SNIExtension{}))
	}
	return *spec, nil
}

var ErrRedirectRefused = errors.New("redirect refused")

type redirectRefusal string

func (r redirectRefusal) Error() string { return string(r) }

func (redirectRefusal) Is(target error) bool { return target == ErrRedirectRefused }

func checkDownloadRedirect(req *fhttp.Request, via []*fhttp.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.URL.Scheme != "https" {
		return redirectRefusal("redirect to " + req.URL.Scheme + " refused")
	}
	// BOX.LOCAL. ведет туда же, а IsLocalHost сравнивает буквально
	host := strings.TrimSuffix(strings.ToLower(req.URL.Hostname()), ".")
	if helper.IsLocalHost(host) {
		return redirectRefusal("redirect to " + host + " refused")
	}
	return nil
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
