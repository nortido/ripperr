package qbittorrent

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sync"

	qbt "github.com/autobrr/go-qbittorrent"

	"trackerr/internal/model"
)

var (
	idRegexp = regexp.MustCompile(`(\d+)`)
)

type IClient interface {
	List(ctx context.Context, opts model.FilterOptions) (entries []*model.Entry, err error)
	AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
	Delete(ctx context.Context, opts model.ManagerDeleteOptions) (err error)
}

type client struct {
	client *qbt.Client
	config model.ManagerConfig
}

func (c *client) List(ctx context.Context, opts model.FilterOptions) (entries []*model.Entry, err error) {
	var (
		i     int
		qts   []qbt.Torrent
		wg    sync.WaitGroup
		ch    chan *model.Entry
		cherr = make(chan error)
	)

	if qts, err = c.client.GetTorrents(qbt.TorrentFilterOptions{
		Tag:    opts.Tag,
		Filter: qbt.TorrentFilterCompleted,
		Sort:   "added_on",
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
	op := qbt.TorrentAddOptions{
		Tags:          opts.Tag,
		Category:      opts.Category,
		AutoTMM:       opts.AutoManaged,
		LimitRatio:    opts.MaxRatio,
		LimitSeedTime: opts.MaxSeedingTime,
	}
	err = c.client.AddTorrentFromMemoryCtx(ctx, buf.Bytes(), op.Prepare())

	return
}

func (c *client) Delete(ctx context.Context, opts model.ManagerDeleteOptions) (err error) {
	if !c.config.DeleteEnabled {
		return
	}
	for i := range c.config.DeleteCategories {
		if opts.Category == c.config.DeleteCategories[i] {
			fmt.Println("deleting torrent: ", opts.Name)
			return c.client.DeleteTorrentsCtx(ctx, []string{opts.Hash}, true)
		}
	}
	return
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
	// TODO: set max seed time to 0/1 if already paused
	res = &model.Entry{
		URL:            p.Comment,
		TopicID:        idRegexp.FindString(p.Comment),
		Category:       t.Category,
		Name:           t.Name,
		Hash:           t.Hash,
		Tag:            t.Tags,
		Timestamp:      int64(p.AdditionDate),
		AutoManaged:    t.AutoManaged,
		MaxRatio:       t.MaxRatio,
		MaxSeedingTime: t.MaxSeedingTime,
	}
	return
}

func (c *client) getClient() (err error) {
	c.client = qbt.NewClient(qbt.Config{
		Host:     c.config.Host,
		Username: c.config.Username,
		Password: c.config.Password,
	})
	err = c.client.Login()

	return
}

func New(config model.ManagerConfig) (c IClient, err error) {
	cl := &client{
		config: config,
	}
	if err = cl.getClient(); err != nil {
		return
	}
	c = cl

	return
}
