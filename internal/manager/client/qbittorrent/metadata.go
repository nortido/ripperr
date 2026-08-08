package qbittorrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"trackerr/internal/helper/torrentmeta"
	"trackerr/internal/model"
	"trackerr/internal/system/logger"
)

const metadataFetchTimeout = 5 * time.Minute

var (
	errFetchMetadataUnsupported = errors.New("torrents/fetchMetadata not supported")
	errMetadataFetchTimeout     = errors.New("metadata fetch timeout")
)

type metadataResponse struct {
	Info *json.RawMessage `json:"info"`
}

func (c *client) fetchAndSaveMetadata(ctx context.Context, source string) (data []byte, err error) {
	if err = c.waitForMetadata(ctx, source); err != nil {
		return
	}
	return c.saveMetadata(ctx, source)
}

func (c *client) waitForMetadata(ctx context.Context, source string) (err error) {
	deadline := time.Now().Add(metadataFetchTimeout)
	query := url.Values{"source": {source}}

	for {
		status, body, err := c.apiGet(ctx, "torrents/fetchMetadata", query)
		if err != nil {
			return err
		}
		if status == http.StatusNotFound {
			return errFetchMetadataUnsupported
		}
		if status == http.StatusOK {
			var meta metadataResponse
			if json.Unmarshal(body, &meta) == nil && meta.Info != nil {
				return nil
			}
		}
		if status != http.StatusAccepted && status != http.StatusOK {
			return fmt.Errorf("fetchMetadata: status %d", status)
		}
		if time.Now().After(deadline) {
			return errMetadataFetchTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *client) saveMetadata(ctx context.Context, source string) (data []byte, err error) {
	query := url.Values{"source": {source}}
	status, body, err := c.apiGet(ctx, "torrents/saveMetadata", query)
	if err != nil {
		return
	}
	switch status {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		return nil, errFetchMetadataUnsupported
	default:
		return nil, fmt.Errorf("saveMetadata: status %d", status)
	}
}

func (c *client) apiGet(ctx context.Context, endpoint string, query url.Values) (status int, body []byte, err error) {
	hc := c.client.GetHTTPClient()
	if hc == nil {
		return 0, nil, fmt.Errorf("no http client")
	}
	reqURL := c.apiURL(endpoint)
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}
	var req *http.Request
	if req, err = http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil); err != nil {
		return
	}
	var res *http.Response
	if res, err = hc.Do(req); err != nil {
		return
	}
	defer res.Body.Close()
	if body, err = io.ReadAll(res.Body); err != nil {
		return
	}
	return res.StatusCode, body, nil
}

func (c *client) addTorrentWithComment(ctx context.Context, data []byte, opts model.ManagerAddFileOptions) (err error) {
	if data, err = torrentmeta.SetComment(data, opts.Comment); err != nil {
		return fmt.Errorf("set torrent comment: %w", err)
	}
	return c.client.AddTorrentFromMemoryCtx(ctx, data, torrentAddOptions(opts).Prepare())
}

func (c *client) addMagnetWithComment(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error) {
	data, err := c.fetchAndSaveMetadata(ctx, magnet)
	if err != nil {
		if !errors.Is(err, errFetchMetadataUnsupported) && !errors.Is(err, errMetadataFetchTimeout) {
			return
		}
		logger.Warn(fmt.Sprintf("qbit metadata fetch: %s, building torrent locally", err))
		data, err = torrentmeta.TorrentFromMagnet(ctx, magnet, opts.Comment)
		if err != nil {
			logger.Warn(fmt.Sprintf("local torrent build: %s, adding without comment", err))
			return c.client.AddTorrentFromUrlCtx(ctx, magnet, torrentAddOptions(opts).Prepare())
		}
		return c.client.AddTorrentFromMemoryCtx(ctx, data, torrentAddOptions(opts).Prepare())
	}
	return c.addTorrentWithComment(ctx, data, opts)
}
