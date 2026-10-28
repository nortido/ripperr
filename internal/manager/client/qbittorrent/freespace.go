package qbittorrent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"ripperr/internal/model"
)

const freeSpaceBodyMax = 1024

func (c *Client) FreeSpace(ctx context.Context, path string) (int64, error) {
	status, body, err := c.apiPostBody(ctx, "app/getFreeSpaceAtPath", url.Values{"path": {path}})
	if err != nil {
		return 0, err
	}
	switch status {
	case http.StatusOK:
		return parseFreeSpace(body)
	case http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed:
		return c.freeSpaceDefaultPath(ctx, path)
	default:
		return 0, fmt.Errorf("getFreeSpaceAtPath: status %d", status)
	}
}

func parseFreeSpace(body []byte) (int64, error) {
	s := strings.Trim(strings.TrimSpace(string(body)), `"`)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("getFreeSpaceAtPath: parse %q: %w", s, err)
	}
	if n < 0 {
		return 0, model.ErrFreeSpaceUnknown
	}
	return n, nil
}

func (c *Client) freeSpaceDefaultPath(ctx context.Context, path string) (int64, error) {
	// старый qbit не знает getFreeSpaceAtPath, а maindata отдает место только для каталога по умолчанию
	def, err := c.client.GetDefaultSavePathCtx(ctx)
	if err != nil {
		return 0, err
	}
	def = strings.TrimRight(strings.TrimSpace(def), `/\`)
	p := strings.TrimRight(path, `/\`)
	if def == "" || (p != def && !strings.HasPrefix(p, def+"/") && !strings.HasPrefix(p, def+`\`)) {
		return 0, fmt.Errorf("%w: %s is outside the default save path", model.ErrFreeSpaceUnknown, path)
	}
	n, err := c.client.GetFreeSpaceOnDiskCtx(ctx)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, model.ErrFreeSpaceUnknown
	}
	return n, nil
}

func (c *Client) apiPostBody(ctx context.Context, endpoint string, form url.Values) (status int, body []byte, err error) {
	hc := c.client.GetHTTPClient()
	if hc == nil {
		return 0, nil, fmt.Errorf("no http client")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL(endpoint), strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := hc.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = res.Body.Close() }()
	body, err = io.ReadAll(io.LimitReader(res.Body, freeSpaceBodyMax+1))
	if err == nil && len(body) > freeSpaceBodyMax {
		return res.StatusCode, nil, fmt.Errorf("%s: response too large", endpoint)
	}
	return res.StatusCode, body, err
}
