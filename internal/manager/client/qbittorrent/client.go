package qbittorrent

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"golang.org/x/sync/errgroup"

	"trackerr/internal/model"
	"trackerr/internal/system/logger"
)

var (
	idRegexp = regexp.MustCompile(`(\d+)`)
)

type Client struct {
	client *qbt.Client
	config model.ManagerConfig
}

func (c *Client) List(ctx context.Context, _ model.FilterOptions) (entries []*model.Entry, err error) {
	qts, err := c.client.GetTorrents(qbt.TorrentFilterOptions{
		Filter: qbt.TorrentFilterAll,
		Sort:   "added_on",
	})
	if err != nil {
		return
	}

	raw := make([]*model.Entry, len(qts))
	var skipped atomic.Int32
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i := range qts {
		i, t := i, qts[i]
		g.Go(func() error {
			p, err := c.getProperties(gctx, &t)
			if err != nil {
				if gctx.Err() != nil {
					return gctx.Err()
				}
				logger.Warn(fmt.Sprintf("torrent %s (%s): %v", t.Name, t.Hash, err))
				skipped.Add(1)
				return nil
			}
			raw[i] = c.mapEntry(&t, p)
			return nil
		})
	}
	if err = g.Wait(); err != nil {
		return
	}
	entries = make([]*model.Entry, 0, len(raw))
	for _, e := range raw {
		if e != nil {
			entries = append(entries, e)
		}
	}
	if n := skipped.Load(); n > 0 {
		err = fmt.Errorf("%w: %d torrents skipped due to errors", model.ErrPartialList, n)
	}
	return
}

func (c *Client) AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error) {
	return c.addTorrentVerified(ctx, buf.Bytes(), opts)
}

func (c *Client) AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error) {
	if opts.Comment != "" {
		return c.addMagnetWithComment(ctx, magnet, opts)
	}
	return c.addMagnetURLVerified(ctx, magnet, opts)
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

func (c *Client) GetTorrentDetails(ctx context.Context, hash string) (name, comment string, err error) {
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

func (c *Client) apiURL(endpoint string) string {
	host := strings.TrimRight(c.config.Host, "/")
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return fmt.Sprintf("%s/api/v2/%s", host, endpoint)
	}
	return fmt.Sprintf("http://%s/api/v2/%s", host, endpoint)
}

func (c *Client) GetCategories(ctx context.Context) ([]string, error) {
	cats, err := c.client.GetCategoriesCtx(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cats))
	for name := range cats {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func (c *Client) Delete(ctx context.Context, opts model.ManagerDeleteOptions) (err error) {
	if !c.config.DeleteEnabled {
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

func (c *Client) getProperties(ctx context.Context, t *qbt.Torrent) (p *qbt.TorrentProperties, err error) {
	var tp qbt.TorrentProperties
	if tp, err = c.client.GetTorrentPropertiesCtx(ctx, t.Hash); err != nil {
		return
	}
	p = &tp
	return
}

func (c *Client) mapEntry(t *qbt.Torrent, p *qbt.TorrentProperties) (res *model.Entry) {
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

func (c *Client) getClient() (err error) {
	c.client = qbt.NewClient(qbt.Config{
		Host:     c.config.Host,
		Username: c.config.Username,
		Password: c.config.Password,
	})
	err = c.client.Login()

	return
}

func New(config model.ManagerConfig) (c *Client, err error) {
	cl := &Client{
		config: config,
	}
	if err = cl.getClient(); err != nil {
		return
	}
	c = cl

	return
}
