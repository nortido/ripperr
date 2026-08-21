package qbittorrent

import (
	"context"

	qbt "github.com/autobrr/go-qbittorrent"

	"trackerr/internal/model"
)

func (c *Client) WriteBlackholePair(data []byte, magnet string, opts model.ManagerAddFileOptions) error {
	return c.bh.Write(data, magnet, opts)
}

func (c *Client) RemoveBlackholePair(hash string) {
	c.bh.Remove(hash)
}

func (c *Client) ConfirmSettled(ctx context.Context, hash string) bool {
	return c.bh.Settled(ctx, hash)
}

func (c *Client) SweepBlackhole(ctx context.Context) {
	c.bh.Sweep(ctx)
}

func (c *Client) Exists(ctx context.Context, hash string) (bool, error) {
	return c.torrentExists(ctx, hash)
}

func (c *Client) Reachable(ctx context.Context) bool {
	_, err := c.client.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{})
	return err == nil
}
