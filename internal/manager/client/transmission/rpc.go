package transmission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"trackerr/internal/helper"
)

const (
	sessionHeader  = "X-Transmission-Session-Id"
	rpcPath        = "/transmission/rpc"
	defaultTimeout = 60 * time.Second
)

type rpcRequest struct {
	Method    string      `json:"method"`
	Arguments interface{} `json:"arguments,omitempty"`
}

type rpcResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
}

type rpcClient struct {
	httpClient *http.Client
	url        string
	username   string
	password   string

	mu        sync.Mutex
	sessionID string
}

func newRPCClient(host, username, password string) *rpcClient {
	return &rpcClient{
		httpClient: &http.Client{Timeout: defaultTimeout, Transport: helper.DirectTransport()},
		url:        rpcURL(host),
		username:   username,
		password:   password,
	}
}

func rpcURL(host string) string {
	host = strings.TrimRight(host, "/")
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host + rpcPath
	}
	return "http://" + host + rpcPath
}

func (c *rpcClient) call(ctx context.Context, method string, arguments interface{}) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{Method: method, Arguments: arguments})
	if err != nil {
		return nil, fmt.Errorf("marshal rpc request: %w", err)
	}

	resp, err := c.do(ctx, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusConflict {
		// Transmission на первый (или просроченный) запрос отвечает 409 и отдаёт
		// свежий session id в заголовке; тело того же запроса шлём повторно
		// ровно один раз, дальше уже реальная ошибка
		c.mu.Lock()
		c.sessionID = resp.Header.Get(sessionHeader)
		c.mu.Unlock()
		_ = resp.Body.Close()
		resp, err = c.do(ctx, body)
		if err != nil {
			return nil, err
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("transmission rpc: unexpected status %d: %s", resp.StatusCode, string(data))
	}

	var rr rpcResponse
	if err = json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return nil, fmt.Errorf("decode rpc response: %w", err)
	}
	if rr.Result != "success" {
		return nil, fmt.Errorf("transmission rpc: %s", rr.Result)
	}
	return rr.Arguments, nil
}

func (c *rpcClient) do(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build rpc request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	if sessionID != "" {
		req.Header.Set(sessionHeader, sessionID)
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return c.httpClient.Do(req)
}
