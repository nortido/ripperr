package qbittorrent

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
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
	GetTorrentDetails(ctx context.Context, hash string) (name, comment string, err error)
	AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
	AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error)
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
	return c.client.AddTorrentFromMemoryCtx(ctx, buf.Bytes(), torrentAddOptions(opts).Prepare())
}

func (c *client) AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error) {
	if opts.Comment != "" {
		return c.addMagnetWithComment(ctx, magnet, opts)
	}
	return c.client.AddTorrentFromUrlCtx(ctx, magnet, torrentAddOptions(opts).Prepare())
}

func torrentAddOptions(opts model.ManagerAddFileOptions) *qbt.TorrentAddOptions {
	op := &qbt.TorrentAddOptions{
		Tags:          opts.Tag,
		Category:      opts.Category,
		AutoTMM:       opts.AutoManaged,
		LimitRatio:    opts.MaxRatio,
		LimitSeedTime: opts.MaxSeedingTime,
		Rename:        opts.Name,
	}
	return op
}

func (c *client) GetTorrentDetails(ctx context.Context, hash string) (name, comment string, err error) {
	if hash == "" {
		return
	}
	var torrents []qbt.Torrent
	if torrents, err = c.client.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{Hashes: []string{hash}}); err != nil {
		return
	}
	if len(torrents) == 0 {
		return
	}
	name = torrents[0].Name

	var props qbt.TorrentProperties
	if props, err = c.client.GetTorrentPropertiesCtx(ctx, hash); err != nil {
		return
	}
	comment = props.Comment
	return
}

func (c *client) apiURL(endpoint string) string {
	host := strings.TrimRight(c.config.Host, "/")
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return fmt.Sprintf("%s/api/v2/%s", host, endpoint)
	}
	return fmt.Sprintf("http://%s/api/v2/%s", host, endpoint)
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
