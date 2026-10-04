package qbittorrent

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"
	"golang.org/x/sync/errgroup"

	"ripperr/internal/helper"
	"ripperr/internal/helper/magnetmeta"
	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/manager/blackhole"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

var (
	idRegexp = regexp.MustCompile(`(\d+)`)
	// var, чтобы тесты могли подменить добычу
	magnetFetch = magnetmeta.Fetch
	// весь AddFromMagnet идет под blackhole.AddTimeout (6 минут). Без своего потолка мертвый рой
	// съест его целиком и addMagnetWithComment получит истекший ctx, поэтому заметно меньше.
	// var, чтобы тесты могли ужать
	magnetFetchTimeout = time.Minute
	checkWaitDelay     = time.Second
	checkWaitBudget    = blackhole.CheckWaitBudget
)

type Client struct {
	client *qbt.Client
	config model.ManagerConfig
	bh     *blackhole.Store
}

func (c *Client) List(ctx context.Context, _ model.FilterOptions) (entries []*model.Entry, err error) {
	qts, err := c.client.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{
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
		// без комментария Ripperr торрент не увидит, а fetchMetadata есть не во всех сборках qbit.
		// Поэтому сперва сами собираем .torrent с комментарием, не вышло, идем старым путем.
		// Подконтекст свой, иначе мертвый рой съест весь ctx и запасному пути ничего не останется
		fctx, cancel := context.WithTimeout(ctx, magnetFetchTimeout)
		data, ferr := magnetFetch(fctx, magnet)
		cancel()
		if ferr == nil {
			if data, ferr = torrentmeta.SetComment(data, opts.Comment); ferr == nil {
				return c.addTorrentVerified(ctx, data, opts)
			}
		}
		logger.Warn(fmt.Sprintf("qbit: own magnet metadata fetch for %s failed: %v, falling back to qbit fetchMetadata", opts.Name, ferr))
		return c.addMagnetWithComment(ctx, magnet, opts)
	}
	return c.addMagnetURLVerified(ctx, magnet, opts)
}

func addOptions(opts model.ManagerAddFileOptions) map[string]string {
	op := &qbt.TorrentAddOptions{
		Tags:          opts.Tag,
		Category:      opts.Category,
		LimitRatio:    opts.MaxRatio,
		LimitSeedTime: opts.MaxSeedingTime,
		Rename:        opts.Name,
		Stopped:       opts.Stopped,
	}
	if !opts.AutoManaged {
		op.SavePath = opts.SavePath
	}
	// Prepare шлет autoTMM только вместе с savepath, а пропущенный флаг qbit
	// берет из глобального умолчания, поэтому режим задаем явно
	m := op.Prepare()
	m["autoTMM"] = strconv.FormatBool(opts.AutoManaged)
	return m
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
	return c.client.DeleteTorrentsCtx(ctx, []string{opts.Hash}, c.DeletesFiles(ctx, opts))
}

func (c *Client) DeletesFiles(_ context.Context, opts model.ManagerDeleteOptions) bool {
	return c.config.DeleteEnabled && c.config.DeleteFilesEnabled && slices.Contains(c.config.DeleteCategories, opts.Category)
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
		SavePath:       t.SavePath,
		Stopped:        isStopped(t.State),
		Unhealthy:      isChecking(t.State) || t.State == qbt.TorrentStateError || t.State == qbt.TorrentStateMissingFiles || t.State == qbt.TorrentStateMoving,
	}
	if t.State == qbt.TorrentStatePausedUp {
		res.MaxRatio = c.config.PausedMaxRatio
	}
	return
}

func (c *Client) getClient() (err error) {
	host, basicUser, basicPass, hasUserinfo := splitHostUserinfo(c.config.Host)
	c.client = qbt.NewClient(qbt.Config{
		Host:      host,
		Username:  c.config.Username,
		Password:  c.config.Password,
		BasicUser: basicUser,
		BasicPass: basicPass,
	})
	// qbt ставит транспорту http.ProxyFromEnvironment, а торрент-клиент стоит рядом и прокси ему не нужен.
	// docker-имя хоста Go из прокси не исключает, localhost исключает
	hc := c.client.GetHTTPClient()
	if tr, ok := hc.Transport.(*http.Transport); ok {
		tr.Proxy = nil
	} else {
		// с чужим транспортом остался бы ProxyFromEnvironment и пароль от веб-морды ушел бы в прокси
		hc.Transport = helper.DirectTransport()
	}
	if hasUserinfo && (basicUser == "" || basicPass == "") {
		hu, _ := url.Parse(host)
		hc.Transport = &basicAuthTransport{base: hc.Transport, host: hu.Host, user: basicUser, pass: basicPass}
	}
	err = c.client.Login()

	return
}

func splitHostUserinfo(raw string) (host, user, pass string, hasUserinfo bool) {
	// qbt пишет адрес запроса в текст ошибок, поэтому логин и пароль из адреса уносим из него.
	// qbt шлет basic только при обоих непустых полях, иначе его ставит basicAuthTransport, как раньше net/http
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw, "", "", false
	}
	user = u.User.Username()
	pass, _ = u.User.Password()
	u.User = nil
	return u.String(), user, pass, true
}

type basicAuthTransport struct {
	base       http.RoundTripper
	host       string
	user, pass string
}

func (t *basicAuthTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// заголовок идет мимо net/http, который сам снимает его при переходе на другой хост, поэтому хост сверяем здесь
	if r.Header.Get("Authorization") == "" && strings.EqualFold(r.URL.Host, t.host) {
		r = r.Clone(r.Context())
		r.SetBasicAuth(t.user, t.pass)
	}
	return t.base.RoundTrip(r)
}

func New(config model.ManagerConfig) (c *Client, err error) {
	if config.BlackholeDir != "" {
		if err = os.MkdirAll(config.BlackholeDir, 0o755); err != nil {
			return nil, fmt.Errorf("create blackhole dir: %w", err)
		}
	}
	cl := &Client{
		config: config,
	}
	cl.bh = blackhole.New(config.BlackholeDir, cl)
	if err = cl.getClient(); err != nil {
		return nil, redactHostUserinfo(err, config.Host)
	}
	c = cl

	return
}

func redactHostUserinfo(err error, host string) error {
	// библиотека qbt пишет в текст ошибки адрес запроса целиком, вместе с паролем
	// из адреса хоста, и в сыром, и в экранированном виде
	u, perr := url.Parse(host)
	if perr != nil || u.User == nil {
		return err
	}
	authority := host[strings.Index(host, "://")+3:]
	if i := strings.IndexAny(authority, "/?#"); i >= 0 {
		authority = authority[:i]
	}
	raw := authority[:strings.LastIndex(authority, "@")+1]
	msg := strings.ReplaceAll(err.Error(), raw, "")
	msg = strings.ReplaceAll(msg, u.User.String()+"@", "")
	return &redactedError{msg: msg, err: err}
}

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

func (c *Client) ReplacePending(hash string) bool {
	return c.bh.PendingReplace(hash)
}

func (c *Client) Discard(ctx context.Context, hash string) error {
	logger.Info(fmt.Sprint("discarding torrent without files: ", hash))
	return c.client.DeleteTorrentsCtx(ctx, []string{hash}, false)
}

func (c *Client) Resume(ctx context.Context, hash string) error {
	// без recheck: остановленный при добавлении торрент еще не проверялся, и
	// libtorrent проверит его на старте, а recheck сам запускает и потом
	// останавливает торрент, что по кешу состояния webapi не отличить
	waitCtx, cancel := context.WithTimeout(ctx, checkWaitBudget)
	defer cancel()
	state, err := c.torrentState(waitCtx, hash)
	for err == nil && isChecking(state) {
		if err = sleepCtx(waitCtx, checkWaitDelay); err == nil {
			state, err = c.torrentState(waitCtx, hash)
		}
	}
	if err != nil {
		return fmt.Errorf("read state of %s: %w", hash, err)
	}
	if !isStopped(state) && state != qbt.TorrentStateError && state != qbt.TorrentStateMissingFiles {
		return nil
	}
	if err = c.client.StartCtx(ctx, []string{hash}); err != nil {
		return err
	}
	// состояние в webapi обновляется с задержкой, первые чтения после start
	// еще могут показать stopped
	for {
		if err = sleepCtx(waitCtx, checkWaitDelay); err != nil {
			return fmt.Errorf("torrent %s still %s after start: %w", hash, state, err)
		}
		if state, err = c.torrentState(waitCtx, hash); err != nil {
			return fmt.Errorf("read state of %s: %w", hash, err)
		}
		if state == qbt.TorrentStateError || state == qbt.TorrentStateMissingFiles {
			return fmt.Errorf("torrent %s is %s after start", hash, state)
		}
		if !isStopped(state) {
			return nil
		}
	}
}

func isChecking(state qbt.TorrentState) bool {
	return state == qbt.TorrentStateCheckingDl || state == qbt.TorrentStateCheckingUp || state == qbt.TorrentStateCheckingResumeData
}

func isStopped(state qbt.TorrentState) bool {
	return state == qbt.TorrentStateStoppedDl || state == qbt.TorrentStateStoppedUp || state == qbt.TorrentStatePausedDl || state == qbt.TorrentStatePausedUp
}

func (c *Client) torrentState(ctx context.Context, hash string) (qbt.TorrentState, error) {
	torrents, err := c.client.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{Hashes: []string{hash}})
	if err != nil {
		return "", err
	}
	if len(torrents) == 0 {
		return "", fmt.Errorf("torrent %s not found", hash)
	}
	return torrents[0].State, nil
}
