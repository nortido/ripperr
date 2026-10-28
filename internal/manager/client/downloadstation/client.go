package downloadstation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/manager/blackhole"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

var idRegexp = regexp.MustCompile(`(\d+)`)

var (
	startupTimeout = 15 * time.Second
	sourceRetry    = 30 * time.Minute
	// список v1 может отставать от только что созданной задачи
	seedKeep = time.Minute
)

const (
	typeBT         = "bt"
	statusPaused   = "paused"
	prioritySkip   = "skip"
	detailAddition = "detail"
)

var unhealthyStatuses = []string{"error", "hash_checking", "finishing", "extracting"}

type taskFile struct {
	Filename string `json:"filename"`
	Priority string `json:"priority"`
}

type task struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Additional struct {
		Detail struct {
			Destination string `json:"destination"`
			CreateTime  int64  `json:"create_time"`
		} `json:"detail"`
		File []taskFile `json:"file"`
	} `json:"additional"`
}

type source struct {
	hash    string
	comment string
	failed  time.Time
	seeded  time.Time
}

type resolved struct {
	task
	hash    string
	comment string
}

var (
	_ interface {
		List(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error)
		GetTorrentDetails(ctx context.Context, hash string) (name, comment string, err error)
		IncomingHashSameAs(buf *bytes.Buffer, magnet string, existing string) (same bool, known bool)
		Exists(ctx context.Context, hash string) (bool, error)
		AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error
		AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error
		Delete(ctx context.Context, opts model.ManagerDeleteOptions) error
		DeletesFiles(ctx context.Context, opts model.ManagerDeleteOptions) bool
		WaitAbsent(ctx context.Context, hash string) (bool, error)
		WriteBlackholePair(data []byte, magnet string, opts model.ManagerAddFileOptions) error
		RemoveBlackholePair(hash string)
		ConfirmSettled(ctx context.Context, hash string) bool
		Discard(ctx context.Context, hash string) error
		Purge(ctx context.Context, hash string) error
		Resume(ctx context.Context, hash string) error
		ReplacePending(hash string) bool
		Files(ctx context.Context, hash string) ([]model.TorrentFile, error)
		FreeSpace(ctx context.Context, path string) (int64, error)
		SetComment(ctx context.Context, hash, comment string) error
		ExportTorrent(ctx context.Context, hash string) ([]byte, error)
		ApplySelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error
		DeleteEnabled() bool
		GetCategories(ctx context.Context) ([]string, error)
		ForceRedownload(ctx context.Context, torrentHash string, opts model.RedownloadOptions) error
		SweepBlackhole(ctx context.Context)
	} = (*Client)(nil)
	_ interface {
		Reachable(ctx context.Context) bool
		ConvergeAdded(ctx context.Context, hash string, data []byte, opts model.ManagerAddFileOptions) error
	} = (*Client)(nil)
)

type Client struct {
	api    *apiClient
	config model.ManagerConfig
	bh     *blackhole.Store

	mu      sync.Mutex
	sources map[string]source
}

func New(config model.ManagerConfig) (*Client, error) {
	if config.BlackholeDir != "" {
		if err := os.MkdirAll(config.BlackholeDir, 0o755); err != nil {
			return nil, fmt.Errorf("create blackhole dir: %w", err)
		}
	}
	api, err := newAPIClient(config.Host, config.Username, config.Password)
	if err != nil {
		return nil, err
	}
	c := &Client{api: api, config: config, sources: map[string]source{}}
	c.bh = blackhole.New(config.BlackholeDir, c)
	// у New нет контекста, а молчащий NAS не должен держать вызывающего минуту
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	if err = api.discover(ctx); err != nil {
		return nil, err
	}
	if err = api.login(ctx, ""); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) listTasks(ctx context.Context, params url.Values) ([]task, error) {
	var data struct {
		Tasks []task `json:"tasks"`
	}
	if err := c.api.call(ctx, apiTask, "list", params, &data); err != nil {
		return nil, err
	}
	return data.Tasks, nil
}

func (c *Client) btTasks(ctx context.Context) ([]task, error) {
	tasks, err := c.listTasks(ctx, url.Values{"additional": {detailAddition}})
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(tasks, func(t task) bool { return !strings.EqualFold(t.Type, typeBT) }), nil
}

func (c *Client) prune(tasks []task) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.sources {
		if !slices.ContainsFunc(tasks, func(t task) bool { return t.ID == id }) && !c.freshSeed(id) {
			delete(c.sources, id)
		}
	}
}

func (c *Client) freshSeed(id string) bool {
	s, ok := c.sources[id]
	return ok && !s.seeded.IsZero() && time.Since(s.seeded) < seedKeep
}

func (c *Client) remember(id string, s source) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !s.failed.IsZero() && c.freshSeed(id) {
		return
	}
	c.sources[id] = s
}

func parseSource(data []byte) (source, error) {
	hash, err := torrentmeta.InfoHash(data)
	if err != nil {
		return source{}, err
	}
	comment, err := torrentmeta.Comment(data)
	if err != nil {
		return source{}, err
	}
	return source{hash: strings.ToLower(hash), comment: comment}, nil
}

func (c *Client) source(ctx context.Context, id string) (source, bool, error) {
	c.mu.Lock()
	s, cached := c.sources[id]
	c.mu.Unlock()
	if cached && s.failed.IsZero() {
		return s, true, nil
	}
	if cached && time.Since(s.failed) < sourceRetry {
		return source{}, false, nil
	}
	data, err := c.api.get(ctx, apiTaskSource, "download", url.Values{"id": {jsonString(id)}})
	var ae *apiError
	if err != nil && (!errors.As(err, &ae) || ae.API != apiTaskSource || sessionLost(ae.Code)) {
		return source{}, false, err
	}
	if err == nil {
		s, err = parseSource(data)
	}
	if err != nil {
		// ответ DSM без .torrent не исправится сам, не качаем его каждый проход
		logger.Warn(fmt.Sprintf("download station: task %s skipped, no usable .torrent: %v", id, err))
		c.remember(id, source{failed: time.Now()})
		return source{}, false, nil
	}
	c.remember(id, s)
	return s, true, nil
}

func (c *Client) resolve(ctx context.Context) ([]resolved, error) {
	tasks, err := c.btTasks(ctx)
	if err != nil {
		return nil, err
	}
	c.prune(tasks)
	out := make([]resolved, 0, len(tasks))
	var skipped int
	var firstErr error
	for _, t := range tasks {
		s, ok, err := c.source(ctx, t.ID)
		if err != nil {
			skipped++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if ok {
			out = append(out, resolved{task: t, hash: s.hash, comment: s.comment})
		}
	}
	if skipped > 0 {
		return out, fmt.Errorf("%w: %d torrents skipped due to errors, first: %v", model.ErrPartialList, skipped, firstErr)
	}
	return out, nil
}

func (c *Client) find(ctx context.Context, hash string) (*resolved, error) {
	all, err := c.resolve(ctx)
	if err != nil && !errors.Is(err, model.ErrPartialList) {
		return nil, err
	}
	for i := range all {
		if strings.EqualFold(all[i].hash, hash) {
			return &all[i], nil
		}
	}
	// при неполном списке отсутствие не доказано
	return nil, err
}

func mapEntry(r *resolved) *model.Entry {
	ts := time.Unix(r.Additional.Detail.CreateTime, 0)
	status := strings.ToLower(r.Status)
	return &model.Entry{
		Hash:      r.hash,
		Name:      r.Title,
		URL:       r.comment,
		TopicID:   idRegexp.FindString(r.comment),
		SavePath:  r.Additional.Detail.Destination,
		Category:  r.Additional.Detail.Destination,
		Timestamp: &ts,
		// у Download Station нет тегов и лимитов раздачи
		MaxRatio:  -2,
		Stopped:   status == statusPaused,
		Unhealthy: slices.Contains(unhealthyStatuses, status),
	}
}

func (c *Client) List(ctx context.Context, _ model.FilterOptions) ([]*model.Entry, error) {
	all, err := c.resolve(ctx)
	if err != nil && !errors.Is(err, model.ErrPartialList) {
		return nil, err
	}
	entries := make([]*model.Entry, 0, len(all))
	for i := range all {
		entries = append(entries, mapEntry(&all[i]))
	}
	slices.SortFunc(entries, func(a, b *model.Entry) int { return strings.Compare(a.Hash, b.Hash) })
	return entries, err
}

func (c *Client) Exists(ctx context.Context, hash string) (bool, error) {
	r, err := c.find(ctx, hash)
	if r != nil {
		return true, nil
	}
	return false, err
}

func (c *Client) GetTorrentDetails(ctx context.Context, hash string) (name, comment string, err error) {
	if hash == "" {
		return
	}
	r, err := c.find(ctx, hash)
	if r == nil {
		return "", "", err
	}
	return r.Title, r.comment, nil
}

func (c *Client) GetCategories(ctx context.Context) ([]string, error) {
	tasks, err := c.btTasks(ctx)
	if err != nil {
		return nil, err
	}
	cats := []string{}
	for _, t := range tasks {
		if d := t.Additional.Detail.Destination; d != "" && !slices.Contains(cats, d) {
			cats = append(cats, d)
		}
	}
	slices.Sort(cats)
	return cats, nil
}

func (c *Client) Files(ctx context.Context, hash string) ([]model.TorrentFile, error) {
	r, err := c.find(ctx, hash)
	if r == nil {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("torrent %s not found", hash)
	}
	var data struct {
		Tasks []task `json:"tasks"`
	}
	if err = c.api.call(ctx, apiTask, "getinfo", url.Values{"id": {r.ID}, "additional": {"file"}}, &data); err != nil {
		return nil, err
	}
	if len(data.Tasks) == 0 {
		return nil, fmt.Errorf("torrent %s not found", hash)
	}
	files := make([]model.TorrentFile, 0, len(data.Tasks[0].Additional.File))
	for _, f := range data.Tasks[0].Additional.File {
		// DS не перепроверяет файлы, новая задача качает все заново: зачесть старые данные нельзя
		files = append(files, model.TorrentFile{Path: f.Filename, Wanted: f.Priority != prioritySkip, Done: 0})
	}
	return files, nil
}

func (c *Client) IncomingHashSameAs(buf *bytes.Buffer, magnet string, existing string) (same bool, known bool) {
	var data []byte
	if buf != nil {
		data = buf.Bytes()
	}
	return torrentmeta.SameHash(data, magnet, existing)
}

func (c *Client) Reachable(ctx context.Context) bool {
	_, err := c.listTasks(ctx, url.Values{"limit": {"1"}})
	return err == nil
}

func (c *Client) DeleteEnabled() bool {
	return c.config.DeleteEnabled
}
