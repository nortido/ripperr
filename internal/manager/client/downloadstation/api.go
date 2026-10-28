package downloadstation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"ripperr/internal/helper"
)

const (
	apiInfo        = "SYNO.API.Info"
	apiAuth        = "SYNO.API.Auth"
	apiTask        = "SYNO.DownloadStation.Task"
	apiTask2       = "SYNO.DownloadStation2.Task"
	apiTaskList    = "SYNO.DownloadStation2.Task.List"
	apiTaskPolling = "SYNO.DownloadStation2.Task.List.Polling"
	apiTaskSource  = "SYNO.DownloadStation2.Task.Source"
	apiFileList    = "SYNO.FileStation.List"

	defaultTimeout = 60 * time.Second
	maxBody        = 64 << 20

	codeSessionTimeout   = 106
	codeSessionDuplicate = 107
	codeSessionMissing   = 119
)

var errCrossHostRedirect = errors.New("redirect to another host refused")

var requiredAPIs = []string{apiAuth, apiTask, apiTask2, apiTaskList, apiTaskPolling, apiTaskSource}

var optionalAPIs = []string{apiFileList}

var commonTexts = map[int]string{
	100: "unknown error",
	101: "invalid parameter",
	102: "API does not exist",
	103: "method does not exist",
	104: "API version not supported",
	105: "permission denied",
	106: "session timeout",
	107: "session interrupted by duplicate login",
	119: "session id not found",
}

var authTexts = map[int]string{
	400: "wrong DSM account or password",
	401: "DSM account disabled",
	402: "DSM account has no permission",
	403: "2-step verification is on for this DSM account, use an account without it",
	404: "2-step verification is on for this DSM account, use an account without it",
	407: "IP is blocked by DSM",
}

type apiInfoEntry struct {
	Path       string `json:"path"`
	MinVersion int    `json:"minVersion"`
	MaxVersion int    `json:"maxVersion"`
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code int `json:"code"`
	} `json:"error"`
}

type apiError struct {
	API    string
	Method string
	Code   int
}

func (e *apiError) Error() string {
	text, ok := commonTexts[e.Code]
	if e.API == apiAuth {
		if t, found := authTexts[e.Code]; found {
			text, ok = t, true
		}
	}
	if !ok {
		text = "error"
	}
	return fmt.Sprintf("download station %s %s: %s (code %d)", e.API, e.Method, text, e.Code)
}

func sessionLost(code int) bool {
	return code == codeSessionTimeout || code == codeSessionDuplicate || code == codeSessionMissing
}

type apiClient struct {
	httpClient *http.Client
	base       string
	account    string
	passwd     string
	apis       map[string]apiInfoEntry

	loginMu sync.Mutex
	mu      sync.Mutex
	sid     string
}

func newAPIClient(host, account, passwd string) (*apiClient, error) {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	if _, err := url.Parse(host); err != nil {
		return nil, fmt.Errorf("download station: bad host: %w", err)
	}
	return &apiClient{
		httpClient: &http.Client{Timeout: defaultTimeout, Transport: helper.DirectTransport(), CheckRedirect: sameHostRedirect},
		base:       host + "/webapi/",
		account:    account,
		passwd:     passwd,
	}, nil
}

func sameHostRedirect(req *http.Request, via []*http.Request) error {
	// 307 повторил бы тело входа с паролем на чужом хосте
	if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
		return errCrossHostRedirect
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

func (c *apiClient) discover(ctx context.Context) error {
	q := url.Values{"api": {apiInfo}, "version": {"1"}, "method": {"query"}}
	q.Set("query", strings.Join(append(append([]string{}, requiredAPIs...), optionalAPIs...), ","))
	body, err := c.send(ctx, http.MethodGet, "query.cgi?"+q.Encode(), nil, apiInfo, "query")
	if err != nil {
		return err
	}
	var apis map[string]apiInfoEntry
	if err = decode(apiInfo, "query", body, &apis); err != nil {
		return err
	}
	for _, name := range requiredAPIs {
		if a, ok := apis[name]; !ok || a.Path == "" || a.MaxVersion < 1 {
			return fmt.Errorf("download station: %s not available, DSM 7 with Download Station required", name)
		}
	}
	if apis[apiTask].MinVersion > 1 {
		return fmt.Errorf("download station: %s version 1 not supported", apiTask)
	}
	c.apis = apis
	return nil
}

func (c *apiClient) version(api string) int {
	v := c.apis[api].MaxVersion
	switch api {
	case apiTask:
		// как Sonarr: v1 задач описан в PDF, поведение старших версий не проверено
		return 1
	case apiAuth:
		if v >= 7 {
			return 6
		}
		return 2
	}
	return v
}

func (c *apiClient) session() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sid
}

func (c *apiClient) login(ctx context.Context, stale string) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	// параллельный вызов уже вошел заново, второй вход выбил бы его сессию
	if stale != "" && c.session() != stale {
		return nil
	}
	form := url.Values{
		"api":     {apiAuth},
		"version": {strconv.Itoa(c.version(apiAuth))},
		"method":  {"login"},
		"account": {c.account},
		"passwd":  {c.passwd},
		"session": {"DownloadStation"},
		"format":  {"sid"},
	}
	body, err := c.send(ctx, http.MethodPost, c.apis[apiAuth].Path, strings.NewReader(form.Encode()), apiAuth, "login")
	if err != nil {
		return err
	}
	var data struct {
		SID string `json:"sid"`
	}
	if err = decode(apiAuth, "login", body, &data); err != nil {
		return err
	}
	if data.SID == "" {
		return errors.New("download station: login returned no session id")
	}
	c.mu.Lock()
	c.sid = data.SID
	c.mu.Unlock()
	return nil
}

func (c *apiClient) call(ctx context.Context, api, method string, params url.Values, out any) error {
	body, err := c.get(ctx, api, method, params)
	if err != nil {
		return err
	}
	return decode(api, method, body, out)
}

func (c *apiClient) get(ctx context.Context, api, method string, params url.Values) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		sid := c.session()
		q := url.Values{}
		for k, v := range params {
			q[k] = v
		}
		q.Set("api", api)
		q.Set("version", strconv.Itoa(c.version(api)))
		q.Set("method", method)
		q.Set("_sid", sid)
		body, err := c.send(ctx, http.MethodGet, c.apis[api].Path+"?"+q.Encode(), nil, api, method)
		if err != nil {
			return nil, err
		}
		code, failed := failure(body)
		if !failed {
			return body, nil
		}
		if !sessionLost(code) || attempt > 0 {
			return nil, &apiError{API: api, Method: method, Code: code}
		}
		if err = c.login(ctx, sid); err != nil {
			return nil, err
		}
	}
}

func (c *apiClient) send(ctx context.Context, httpMethod, ref string, body io.Reader, api, method string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, httpMethod, c.base+ref, body)
	if err != nil {
		// текст ошибки разбора содержит URL вместе с _sid
		return nil, fmt.Errorf("download station %s %s: cannot build request", api, method)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, transportError(api, method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("download station %s %s: unexpected status %d", api, method, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, transportError(api, method, err)
	}
	return data, nil
}

func transportError(api, method string, err error) error {
	// url.Error печатает весь URL запроса, а в нем _sid
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("download station %s %s: %w", api, method, err)
}

func failure(body []byte) (int, bool) {
	b := bytes.TrimSpace(body)
	if len(b) == 0 || b[0] != '{' {
		return 0, false
	}
	var env envelope
	if json.Unmarshal(b, &env) != nil || env.Success {
		return 0, false
	}
	if env.Error == nil {
		return 0, true
	}
	return env.Error.Code, true
}

func decode(api, method string, body []byte, out any) error {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("download station %s %s: decode response: %w", api, method, err)
	}
	if !env.Success {
		code := 0
		if env.Error != nil {
			code = env.Error.Code
		}
		return &apiError{API: api, Method: method, Code: code}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("download station %s %s: decode data: %w", api, method, err)
	}
	return nil
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
