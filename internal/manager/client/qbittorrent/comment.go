package qbittorrent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"ripperr/internal/model"
)

func (c *Client) SetComment(ctx context.Context, hash, comment string) error {
	status, err := c.apiPost(ctx, "torrents/setComment", url.Values{"hashes": {hash}, "comment": {comment}})
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return model.ErrCommentUnsupported
	default:
		return fmt.Errorf("setComment: status %d", status)
	}
	// неизвестный хеш qbit пропускает молча с тем же 200, поэтому верим только перечитанному
	_, got, err := c.GetTorrentDetails(ctx, hash)
	if err != nil {
		return fmt.Errorf("read comment back: %w", err)
	}
	if got != strings.TrimSpace(comment) {
		return fmt.Errorf("comment of %s not set: got %q", hash, got)
	}
	return nil
}

func (c *Client) ExportTorrent(ctx context.Context, hash string) ([]byte, error) {
	return c.client.ExportTorrentCtx(ctx, hash)
}

func (c *Client) DeleteEnabled() bool {
	return c.config.DeleteEnabled
}

func (c *Client) apiPost(ctx context.Context, endpoint string, form url.Values) (status int, err error) {
	hc := c.client.GetHTTPClient()
	if hc == nil {
		return 0, fmt.Errorf("no http client")
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
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode, nil
}
