package qbittorrent

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sync"

	qbt "github.com/autobrr/go-qbittorrent"

	"torr/internal/model"
)

var (
	idRegexp = regexp.MustCompile(`(\d+)`)
)

type Client interface {
	List(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error)
	AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
	Delete(ctx context.Context, hash string) (err error)
}

type client struct {
	client *qbt.Client
}

func (c *client) List(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error) {
	var (
		ch    chan *model.Entry
		cherr = make(chan error)
		qts   []qbt.Torrent
		wg    sync.WaitGroup
		i     int
	)

	if qts, err = c.client.GetTorrents(qbt.TorrentFilterOptions{
		Tag:    opts.Tag,
		Filter: qbt.TorrentFilterCompleted,
		Sort:   "added_on",
		//Category: "switch",
	}); err != nil {
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
			go func(t qbt.Torrent) {
				var (
					p *qbt.TorrentProperties
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
	go func() {
		wg.Wait()
		close(ch)
		close(cherr)
	}()
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

func (c *client) AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error) {
	op := qbt.TorrentAddOptions{Tags: opts.Tag, Category: opts.Category}
	err = c.client.AddTorrentFromMemoryCtx(ctx, buf.Bytes(), op.Prepare())

	return
}

func (c *client) Delete(ctx context.Context, hash string) (err error) {
	return c.client.DeleteTorrentsCtx(ctx, []string{hash}, true)
}

func (c *client) getProperties(t *qbt.Torrent) (p *qbt.TorrentProperties, err error) {
	var tp qbt.TorrentProperties
	if tp, err = c.client.GetTorrentProperties(t.Hash); err != nil {
		return
	}
	p = &tp
	return
}

func (c *client) mapEntry(t *qbt.Torrent, p *qbt.TorrentProperties) (res *model.Entry) {
	res = &model.Entry{
		ID:        idRegexp.FindString(p.Comment),
		Category:  t.Category,
		Timestamp: int64(p.AdditionDate),
		Hash:      t.Hash,
		Name:      t.Name,
		URL:       p.Comment,
	}
	return
}

func (c *client) getClient(config model.ManagerConfig) (err error) {
	c.client = qbt.NewClient(qbt.Config{
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
