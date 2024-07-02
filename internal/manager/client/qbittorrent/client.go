package qbittorrent

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"trackerr/internal/model"
	"trackerr/internal/system/logger"
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
		to    qbt.TorrentFilterOptions
		cherr = make(chan error)
	)

	to = qbt.TorrentFilterOptions{
		Filter: qbt.TorrentFilterAll,
		Sort:   "added_on",
	}
	if opts.Tag != "" {
		to.Tag = opts.Tag
	}
	if qts, err = c.client.GetTorrents(to); err != nil {
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
				defer wg.Done()
				p, err := c.getProperties(&t)
				if err != nil {
					cherr <- err
					return
				}
				e := c.mapEntry(&t, p)
				ch <- e
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
	if c.config.DeleteEnabled == false {
		return
	}
	logger.Info(fmt.Sprint("deleting torrent: ", opts.Name))

	for i := range c.config.DeleteCategories {
		if opts.Category == c.config.DeleteCategories[i] {
			return c.client.DeleteTorrentsCtx(ctx, []string{opts.Hash}, c.config.DeleteFilesEnabled)
		}
	}
	return c.client.DeleteTorrentsCtx(ctx, []string{opts.Hash}, false)
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
	var ts = time.Unix(int64(p.AdditionDate), 0)

	res = &model.Entry{
		URL:            p.Comment,
		TopicID:        idRegexp.FindString(p.Comment),
		Category:       t.Category,
		Name:           t.Name,
		Hash:           t.Hash,
		Tag:            t.Tags,
		Timestamp:      &ts,
		AutoManaged:    t.AutoManaged,
		MaxRatio:       t.MaxRatio,
		MaxSeedingTime: t.MaxSeedingTime,
	}
	if t.State == qbt.TorrentStatePausedUp {
		res.MaxRatio = c.config.PausedMaxRatio
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
