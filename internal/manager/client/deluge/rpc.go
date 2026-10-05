package deluge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ripperr/internal/helper"
)

const (
	jsonPath       = "/json"
	defaultTimeout = 60 * time.Second

	codeNotAuthenticated = 1
	codeUnknownMethod    = 2
)

var errLoginFailed = errors.New("deluge: login failed (check Web UI password)")

type rpcRequest struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
	ID     uint64 `json:"id"`
}

type rpcErrorBody struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcErrorBody   `json:"error"`
}

type rpcError struct {
	Method  string
	Code    int
	Message string
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("deluge %s: %s (code %d)", e.Method, e.Message, e.Code)
}

func errorCode(err error) int {
	var re *rpcError
	if errors.As(err, &re) {
		return re.Code
	}
	return 0
}

type rpcClient struct {
	httpClient *http.Client
	url        string
	password   string
	nextID     atomic.Uint64
	// один вход и одно переподключение за раз, параллельные вызовы ждут;
	// замки разные: переподключение само может перелогиниться
	loginMu   sync.Mutex
	connectMu sync.Mutex
}

func newRPCClient(host, password string) *rpcClient {
	jar, _ := cookiejar.New(nil)
	return &rpcClient{
		httpClient: &http.Client{Timeout: defaultTimeout, Transport: helper.DirectTransport(), Jar: jar},
		url:        jsonURL(host),
		password:   password,
	}
}

func jsonURL(host string) string {
	host = strings.TrimRight(host, "/")
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host + jsonPath
	}
	return "http://" + host + jsonPath
}

func (c *rpcClient) call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	res, err := c.callAuthed(ctx, method, params)
	if err == nil || errors.Is(err, errLoginFailed) || (!strings.HasPrefix(method, "core.") && !strings.HasPrefix(method, "label.")) {
		return res, err
	}
	// Web UI без связи с демоном отвечает на core. и label. кодом 2,
	// переподключаемся только если он сам говорит, что связи нет
	if connected, cerr := c.connected(ctx); cerr != nil || connected {
		return nil, err
	}
	if cerr := c.connect(ctx); cerr != nil {
		return nil, cerr
	}
	return c.callAuthed(ctx, method, params)
}

func (c *rpcClient) callAuthed(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	res, err := c.do(ctx, method, params)
	if errorCode(err) != codeNotAuthenticated {
		return res, err
	}
	if err = c.login(ctx); err != nil {
		return nil, err
	}
	return c.do(ctx, method, params)
}

func (c *rpcClient) login(ctx context.Context) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	res, err := c.do(ctx, "auth.login", []any{c.password})
	if err != nil {
		return err
	}
	if s := string(bytes.TrimSpace(res)); s == "false" || s == "null" || s == "" {
		return errLoginFailed
	}
	return nil
}

func (c *rpcClient) connected(ctx context.Context) (bool, error) {
	res, err := c.callAuthed(ctx, "web.connected", nil)
	if err != nil {
		return false, err
	}
	var ok bool
	if err = json.Unmarshal(res, &ok); err != nil {
		return false, fmt.Errorf("deluge: decode web.connected: %w", err)
	}
	return ok, nil
}

func (c *rpcClient) connect(ctx context.Context) error {
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	if ok, err := c.connected(ctx); err != nil || ok {
		return err
	}
	res, err := c.callAuthed(ctx, "web.get_hosts", nil)
	if err != nil {
		return err
	}
	var hosts [][]json.RawMessage
	if err = json.Unmarshal(res, &hosts); err != nil {
		return fmt.Errorf("deluge: decode web.get_hosts: %w", err)
	}
	if len(hosts) == 0 || len(hosts[0]) == 0 {
		return errors.New("deluge: Web UI has no daemon hosts configured")
	}
	var id string
	if err = json.Unmarshal(hosts[0][0], &id); err != nil {
		return fmt.Errorf("deluge: decode host id: %w", err)
	}
	_, err = c.callAuthed(ctx, "web.connect", []any{id})
	return err
}

func (c *rpcClient) ensureConnected(ctx context.Context) error {
	ok, err := c.connected(ctx)
	if err != nil || ok {
		return err
	}
	if err = c.connect(ctx); err != nil {
		return err
	}
	if ok, err = c.connected(ctx); err != nil {
		return err
	}
	if !ok {
		return errors.New("deluge: Web UI is not connected to a daemon")
	}
	return nil
}

func (c *rpcClient) do(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(rpcRequest{Method: method, Params: params, ID: c.nextID.Add(1)})
	if err != nil {
		return nil, fmt.Errorf("deluge: marshal %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("deluge: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("deluge %s: unexpected status %d: %s", method, resp.StatusCode, string(data))
	}
	var rr rpcResponse
	if err = json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return nil, fmt.Errorf("deluge %s: decode response: %w", method, err)
	}
	if rr.Error != nil {
		return nil, &rpcError{Method: method, Code: rr.Error.Code, Message: rr.Error.Message}
	}
	return rr.Result, nil
}
