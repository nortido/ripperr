package qbittorrent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"torr/internal/manager/pkg/go-qbittorrent"
	"torr/internal/model"
)

type Client interface {
	GetEntries(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error)
	AddTorrentFromFile(ctx context.Context, path string, opts model.ManagerAddFileOptions) (err error)
	DeleteTorrent(ctx context.Context, hash string) (err error)
}

type client struct {
	client *qbittorrent.Client
}

func (c *client) GetEntries(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error) {
	var (
		ch    chan *model.Entry
		cherr = make(chan error)
		qts   []qbittorrent.Torrent
		wg    sync.WaitGroup
		i     int
	)

	if qts, err = c.client.GetTorrents(qbittorrent.TorrentFilterOptions{Tag: opts.Tag, Filter: qbittorrent.TorrentFilterCompleted}); err != nil {
		return
	}
	ch = make(chan *model.Entry, len(qts))

	for _, t := range qts {
		select {
		case <-ctx.Done():
			cherr <- ctx.Err()
			return
		case err = <-cherr:
			return
		default:
			wg.Add(1)
			go func(t qbittorrent.Torrent) {
				var (
					p *qbittorrent.TorrentProperties
				)
				defer wg.Done()
				if p, err = c.getProperties(&t); err != nil {
					cherr <- err
					return
				}
				e := c.mapEntry(&t, p)
				ch <- e
				fmt.Println("got entry: ", e.Name)
			}(t)
		}
	}
	//go func() {
	wg.Wait()
	close(ch)
	close(cherr)
	//}()
	if err = <-cherr; err != nil {
		return
	}
	entries = make([]*model.Entry, len(qts))

	for t := range ch {
		entries[i] = t
		i++
	}
	return
}

func (c *client) AddTorrentFromFile(ctx context.Context, path string, opts model.ManagerAddFileOptions) (err error) {
	op := qbittorrent.TorrentAddOptions{Tags: opts.Tag, Category: opts.Category}
	err = c.client.AddTorrentFromFileCtx(ctx, path, op.Prepare())

	return
}

func (c *client) DeleteTorrent(ctx context.Context, hash string) (err error) {
	return c.client.DeleteTorrentsCtx(ctx, []string{hash}, true)
}

func (c *client) getProperties(t *qbittorrent.Torrent) (p *qbittorrent.TorrentProperties, err error) {
	if p, err = c.client.GetTorrentProperties(t.Hash); err != nil {
		return
	}
	return
}

func (c *client) mapEntry(t *qbittorrent.Torrent, p *qbittorrent.TorrentProperties) (res *model.Entry) {
	res = &model.Entry{
		ID:        strings.Split(p.Comment, "=")[1],
		Category:  t.Category,
		Timestamp: p.CreationDate,
		Hash:      t.Hash,
		Name:      t.Name,
		URL:       p.Comment,
	}
	return
}

func (c *client) getClient(config model.ManagerConfig) (err error) {
	c.client = qbittorrent.NewClient(qbittorrent.Config{
		Host:     config.Host,
		Username: config.Username,
		Password: config.Password,
	})
	err = c.client.Login()

	return
}

func New(config model.ManagerConfig) (c Client, err error) {
	cl := &client{}
	if err = cl.getClient(config); err != nil {
		return
	}
	c = cl
	return
}
