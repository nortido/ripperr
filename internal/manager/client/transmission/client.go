package transmission

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"trackerr/internal/helper/torrentmeta"
	"trackerr/internal/manager/blackhole"
	"trackerr/internal/model"
	"trackerr/internal/system/logger"
)

var idRegexp = regexp.MustCompile(`(\d+)`)

var ErrRedownloadUnsupported = errors.New("force redownload is not supported for transmission: its RPC cannot restore the torrent comment, so the tracker link would be lost")

var (
	// presenceCheckAttempts и presenceCheckDelay это число попыток и пауза
	// между ними при ожидании появления/исчезновения торрента; переменные
	// пакета, чтобы тесты могли их ужать (тот же приём, что у blackhole.SettleDelay).
	presenceCheckAttempts = 5
	presenceCheckDelay    = time.Second
	// startupProbeTimeout ограничивает session-get в New, см. комментарий там.
	startupProbeTimeout = 15 * time.Second
)

var torrentFields = []string{
	"hashString", "name", "comment", "downloadDir", "labels",
	"addedDate", "percentDone", "status", "seedRatioLimit", "seedRatioMode", "magnetLink",
}

type rpcTorrent struct {
	HashString     string   `json:"hashString"`
	Name           string   `json:"name"`
	Comment        string   `json:"comment"`
	DownloadDir    string   `json:"downloadDir"`
	Labels         []string `json:"labels"`
	AddedDate      int64    `json:"addedDate"`
	PercentDone    float64  `json:"percentDone"`
	Status         int      `json:"status"`
	SeedRatioLimit float64  `json:"seedRatioLimit"`
	SeedRatioMode  int      `json:"seedRatioMode"`
	MagnetLink     string   `json:"magnetLink"`
}

type torrentGetArgs struct {
	Torrents []rpcTorrent `json:"torrents"`
}

type sessionGetArgs struct {
	DownloadDir string `json:"download-dir"`
}

// torrentAddArgs это ответ torrent-add. На дубликат Transmission всё равно
// шлёт result:"success", но arguments несёт torrent-duplicate вместо
// torrent-added; без этой проверки ensurePresentAfterAdd видит СТАРЫЙ торрент
// по тому же хешу и молча рапортует успех, хотя новые download-dir/labels не
// применились.
type torrentAddArgs struct {
	TorrentAdded     *addedTorrent `json:"torrent-added"`
	TorrentDuplicate *addedTorrent `json:"torrent-duplicate"`
}

type addedTorrent struct {
	HashString string `json:"hashString"`
}

func checkAddDuplicate(raw json.RawMessage, name string) error {
	var ta torrentAddArgs
	if err := json.Unmarshal(raw, &ta); err != nil {
		return fmt.Errorf("decode torrent-add: %w", err)
	}
	if ta.TorrentDuplicate != nil {
		return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, name)
	}
	return nil
}

// проверка соответствия сигнатур интерфейсу client из internal/manager/service.go
// (тот неэкспортирован, поэтому сверяем литералом) и интерфейсу client из
// internal/manager/blackhole/blackhole.go.
var (
	_ interface {
		List(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error)
		GetTorrentDetails(ctx context.Context, hash string) (name, comment string, err error)
		IncomingHashSameAs(buf *bytes.Buffer, magnet string, existing string) (same bool, known bool)
		AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error
		AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error
		Delete(ctx context.Context, opts model.ManagerDeleteOptions) error
		WriteBlackholePair(data []byte, magnet string, opts model.ManagerAddFileOptions) error
		RemoveBlackholePair(hash string)
		ConfirmSettled(ctx context.Context, hash string) bool
	} = (*Client)(nil)
	_ interface {
		AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error
		AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error
		Exists(ctx context.Context, hash string) (bool, error)
		Reachable(ctx context.Context) bool
	} = (*Client)(nil)
)

type Client struct {
	rpc         *rpcClient
	config      model.ManagerConfig
	downloadDir string
	bh          *blackhole.Store
}

func New(config model.ManagerConfig) (*Client, error) {
	if config.BlackholeDir != "" {
		if err := os.MkdirAll(config.BlackholeDir, 0o755); err != nil {
			return nil, fmt.Errorf("create blackhole dir: %w", err)
		}
	}
	c := &Client{
		rpc:    newRPCClient(config.Host, config.Username, config.Password),
		config: config,
	}
	c.bh = blackhole.New(config.BlackholeDir, c)

	// сигнатура New (совместимая с qbittorrent.New) не принимает контекст, а
	// session-get на старте не относится ни к одному пользовательскому запросу,
	// поэтому это единственная в пакете точка с background-контекстом. Свой
	// таймаут обязателен: без него потолок только defaultTimeout (60с) из
	// rpc.go, и хост, который принимает TCP и молчит, держит вызывающий
	// HTTP-обработчик (например, проверку интеграции) минуту вместо разумных
	// секунд.
	startCtx, cancel := context.WithTimeout(context.Background(), startupProbeTimeout)
	defer cancel()
	raw, err := c.rpc.call(startCtx, "session-get", nil)
	if err != nil {
		return nil, err
	}
	var sg sessionGetArgs
	if err = json.Unmarshal(raw, &sg); err != nil {
		return nil, fmt.Errorf("decode session-get: %w", err)
	}
	c.downloadDir = sg.DownloadDir

	return c, nil
}

// categoryFromPath отдаёт категорию раздачи и признак, что downloadDir реально
// лежит внутри базового каталога загрузок. Второе важно для Delete: раздача в
// /mnt/archive/Movies и раздача в <base>/Movies дают одну и ту же категорию
// Movies (по filepath.Base), но только вторая на самом деле под управлением
// базового каталога, и только для неё позволительно удалять файлы по совпадению
// с DeleteCategories.
func categoryFromPath(downloadDir, base string) (category string, insideBase bool) {
	downloadDir = filepath.Clean(downloadDir)
	base = filepath.Clean(base)
	if downloadDir == base {
		return "", true
	}
	rel, err := filepath.Rel(base, downloadDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Base(downloadDir), false
	}
	// клиенты вроде Sonarr/Radarr кладут категорию ровно одним подкаталогом;
	// берём только первый сегмент, иначе у tv/show1 и tv/show2 была бы своя
	// категория на каждый торрент и GetCategories возвращал бы мусор
	rel = filepath.ToSlash(rel)
	if idx := strings.Index(rel, "/"); idx >= 0 {
		rel = rel[:idx]
	}
	return rel, true
}

func destinationDir(opts model.ManagerAddFileOptions, downloadDir string) string {
	dir := downloadDir
	if opts.SavePath != "" {
		dir = opts.SavePath
	} else if opts.Category != "" {
		dir = filepath.Join(downloadDir, opts.Category)
	}
	if !filepath.IsAbs(dir) {
		// пустой базовый каталог (session-get ничего не отдал) даёт здесь
		// относительный путь; Transmission такое не ждёт, поэтому лучше не
		// слать download-dir вовсе и пусть клиент возьмёт свой умолчательный каталог
		return ""
	}
	return dir
}

func addArgs(opts model.ManagerAddFileOptions, downloadDir string) map[string]interface{} {
	args := map[string]interface{}{}
	if dir := destinationDir(opts, downloadDir); dir != "" {
		args["download-dir"] = dir
	}
	if labels := splitLabels(opts.Tag); len(labels) > 0 {
		args["labels"] = labels
	}
	return args
}

func splitLabels(tag string) []string {
	if tag == "" {
		return nil
	}
	parts := strings.Split(tag, ",")
	labels := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			labels = append(labels, p)
		}
	}
	return labels
}

func (c *Client) mapEntry(t *rpcTorrent) *model.Entry {
	ts := time.Unix(t.AddedDate, 0)
	category, _ := categoryFromPath(t.DownloadDir, c.downloadDir)
	res := &model.Entry{
		Hash:      strings.ToLower(t.HashString),
		Name:      t.Name,
		URL:       t.Comment,
		TopicID:   idRegexp.FindString(t.Comment),
		SavePath:  t.DownloadDir,
		Category:  category,
		Timestamp: &ts,
		Tag:       strings.Join(t.Labels, ","),
		// у Transmission нет аналога автоматического режима qBittorrent
		AutoManaged: false,
		// ponytail: у Transmission есть только лимит простоя (seedIdleLimit), это
		// не суммарное время раздачи, подменять одно другим нельзя, поэтому 0
		MaxSeedingTime: 0,
	}
	// соглашение qBittorrent: -1 без ограничения, -2 глобальный лимит клиента
	switch t.SeedRatioMode {
	case 1:
		res.MaxRatio = t.SeedRatioLimit
	case 2:
		res.MaxRatio = -1
	default:
		res.MaxRatio = -2
	}
	if t.Status == 0 && t.PercentDone >= 1 {
		res.MaxRatio = c.config.PausedMaxRatio
	}
	return res
}

func (c *Client) List(ctx context.Context, _ model.FilterOptions) ([]*model.Entry, error) {
	raw, err := c.rpc.call(ctx, "torrent-get", map[string]interface{}{"fields": torrentFields})
	if err != nil {
		return nil, err
	}
	var tg torrentGetArgs
	if err = json.Unmarshal(raw, &tg); err != nil {
		return nil, fmt.Errorf("decode torrent-get: %w", err)
	}
	entries := make([]*model.Entry, 0, len(tg.Torrents))
	for i := range tg.Torrents {
		entries = append(entries, c.mapEntry(&tg.Torrents[i]))
	}
	return entries, nil
}

func (c *Client) torrentByHash(ctx context.Context, hash string, fields []string) (*rpcTorrent, error) {
	args := map[string]interface{}{"ids": []string{hash}, "fields": fields}
	raw, err := c.rpc.call(ctx, "torrent-get", args)
	if err != nil {
		return nil, err
	}
	var tg torrentGetArgs
	if err = json.Unmarshal(raw, &tg); err != nil {
		return nil, fmt.Errorf("decode torrent-get: %w", err)
	}
	if len(tg.Torrents) == 0 {
		return nil, nil
	}
	return &tg.Torrents[0], nil
}

func (c *Client) GetTorrentDetails(ctx context.Context, hash string) (name, comment string, err error) {
	if hash == "" {
		return
	}
	t, err := c.torrentByHash(ctx, hash, torrentFields)
	if err != nil || t == nil {
		return "", "", err
	}
	return t.Name, t.Comment, nil
}

func (c *Client) GetCategories(ctx context.Context) ([]string, error) {
	raw, err := c.rpc.call(ctx, "torrent-get", map[string]interface{}{"fields": []string{"downloadDir"}})
	if err != nil {
		return nil, err
	}
	var tg torrentGetArgs
	if err = json.Unmarshal(raw, &tg); err != nil {
		return nil, fmt.Errorf("decode torrent-get: %w", err)
	}
	set := make(map[string]struct{}, len(tg.Torrents))
	for i := range tg.Torrents {
		if cat, _ := categoryFromPath(tg.Torrents[i].DownloadDir, c.downloadDir); cat != "" {
			set[cat] = struct{}{}
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

// categoryInsideBase перезапрашивает текущий downloadDir раздачи по хешу: opts,
// пришедшие в Delete, несут только вычисленную категорию, а не путь, из
// которого её посчитали, и одноимённая категория может означать как каталог
// внутри базы, так и одноимённый каталог снаружи (см. categoryFromPath).
// Ошибка запроса или отсутствие раздачи трактуются как "не внутри": лучше не
// удалить файлы, о которых ничего не известно, чем удалить чужие.
func (c *Client) categoryInsideBase(ctx context.Context, hash string) bool {
	t, err := c.torrentByHash(ctx, hash, []string{"downloadDir"})
	if err != nil || t == nil {
		return false
	}
	_, inside := categoryFromPath(t.DownloadDir, c.downloadDir)
	return inside
}

func (c *Client) Delete(ctx context.Context, opts model.ManagerDeleteOptions) error {
	if !c.config.DeleteEnabled {
		return nil
	}
	logger.Info(fmt.Sprint("deleting torrent: ", opts.Name))

	deleteFiles := false
	for i := range c.config.DeleteCategories {
		if opts.Category == c.config.DeleteCategories[i] {
			if c.categoryInsideBase(ctx, opts.Hash) {
				deleteFiles = c.config.DeleteFilesEnabled
			}
			break
		}
	}
	_, err := c.rpc.call(ctx, "torrent-remove", map[string]interface{}{
		"ids":               []string{opts.Hash},
		"delete-local-data": deleteFiles,
	})
	return err
}

func (c *Client) torrentPresent(ctx context.Context, hash string) (bool, error) {
	t, err := c.torrentByHash(ctx, hash, []string{"hashString"})
	if err != nil {
		return false, err
	}
	return t != nil, nil
}

func (c *Client) ensureNotPresent(ctx context.Context, hash, name string) error {
	present, err := c.torrentPresent(ctx, hash)
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, name)
	}
	return nil
}

func (c *Client) waitUntilAbsent(ctx context.Context, hash string) (bool, error) {
	for i := 0; i < presenceCheckAttempts; i++ {
		present, err := c.torrentPresent(ctx, hash)
		if err != nil {
			return false, err
		}
		if !present {
			return true, nil
		}
		if err = sleepCtx(ctx, presenceCheckDelay); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (c *Client) ensurePresentAfterAdd(ctx context.Context, hash string) error {
	for i := 0; i < presenceCheckAttempts; i++ {
		present, err := c.torrentPresent(ctx, hash)
		if err != nil {
			return err
		}
		if present {
			return nil
		}
		if err = sleepCtx(ctx, presenceCheckDelay); err != nil {
			return err
		}
	}
	return fmt.Errorf("torrent add failed (not present after add), transmission rejected it (possibly duplicate or invalid)")
}

func (c *Client) preAddPresenceCheck(ctx context.Context, hash, name, optsHash string) error {
	if strings.EqualFold(hash, optsHash) && c.config.DeleteEnabled {
		absent, err := c.waitUntilAbsent(ctx, hash)
		if err != nil {
			return err
		}
		if !absent {
			return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, name)
		}
		return nil
	}
	return c.ensureNotPresent(ctx, hash, name)
}

func (c *Client) applyMaxRatio(ctx context.Context, hash string, opts model.ManagerAddFileOptions) {
	// соглашение qBittorrent, зеркально к mapEntry: -1 без ограничения (режим 2),
	// -2 глобальный лимит клиента (режим 0), положительное значение свой лимит
	// (режим 1); 0 или иное не встречается у qbit в норме, оставляем режим
	// Transmission по умолчанию как есть
	args := map[string]interface{}{"ids": []string{hash}}
	switch {
	case opts.MaxRatio > 0:
		args["seedRatioLimit"] = opts.MaxRatio
		args["seedRatioMode"] = 1
	case opts.MaxRatio == -1:
		args["seedRatioMode"] = 2
	case opts.MaxRatio == -2:
		args["seedRatioMode"] = 0
	default:
		return
	}
	// не фатально: торрент уже добавлен, персональный лимит просто не применится
	if _, err := c.rpc.call(ctx, "torrent-set", args); err != nil {
		logger.Warn(fmt.Sprintf("transmission: set seed ratio limit for %s failed: %v", hash, err))
	}
}

func (c *Client) addTorrentVerified(ctx context.Context, data []byte, opts model.ManagerAddFileOptions) error {
	hash, err := torrentmeta.InfoHash(data)
	if err != nil {
		return err
	}
	if err = c.preAddPresenceCheck(ctx, hash, opts.Name, opts.Hash); err != nil {
		return err
	}

	args := addArgs(opts, c.downloadDir)
	args["metainfo"] = base64.StdEncoding.EncodeToString(data)
	raw, err := c.rpc.call(ctx, "torrent-add", args)
	if err != nil {
		return err
	}
	if err = checkAddDuplicate(raw, opts.Name); err != nil {
		return err
	}

	if err = c.ensurePresentAfterAdd(ctx, hash); err != nil {
		return err
	}
	c.applyMaxRatio(ctx, hash, opts)
	return nil
}

func (c *Client) addMagnetVerified(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	hash, ok := torrentmeta.BtihFromMagnet(magnet)
	if ok {
		if err := c.preAddPresenceCheck(ctx, hash, opts.Name, opts.Hash); err != nil {
			return err
		}
	}
	if opts.Comment != "" {
		// ponytail: у RPC Transmission нет способа задать комментарий торрента при
		// добавлении по магниту, поэтому ссылка на тему трекера у него не
		// сохранится; зеркалит запасной путь qbittorrent при недоступном
		// fetchMetadata. Апгрейд: если появится способ прописать комментарий
		// после раздачи метаданных, довести его здесь.
		logger.Warn(fmt.Sprintf("transmission: magnet add for %s has no way to set torrent comment, tracker link will not persist", opts.Name))
	}

	args := addArgs(opts, c.downloadDir)
	args["filename"] = magnet
	raw, err := c.rpc.call(ctx, "torrent-add", args)
	if err != nil {
		return err
	}
	if err = checkAddDuplicate(raw, opts.Name); err != nil {
		return err
	}

	if ok {
		if err := c.ensurePresentAfterAdd(ctx, hash); err != nil {
			return err
		}
		c.applyMaxRatio(ctx, hash, opts)
	}
	return nil
}

func (c *Client) AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error {
	return c.addTorrentVerified(ctx, buf.Bytes(), opts)
}

func (c *Client) AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	return c.addMagnetVerified(ctx, magnet, opts)
}

func (c *Client) IncomingHashSameAs(buf *bytes.Buffer, magnet string, existing string) (same bool, known bool) {
	var data []byte
	if buf != nil {
		data = buf.Bytes()
	}
	return torrentmeta.SameHash(data, magnet, existing)
}

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
	return c.torrentPresent(ctx, hash)
}

func (c *Client) Reachable(ctx context.Context) bool {
	_, err := c.rpc.call(ctx, "session-get", nil)
	return err == nil
}

func (c *Client) ForceRedownload(_ context.Context, _ string) error {
	// Transmission RPC не умеет ни выгрузить .torrent целиком (как
	// ExportTorrentCtx в qbittorrent), ни задать комментарий раздачи при
	// повторном добавлении, а весь трекинг trackerr держится на комментарии
	// со ссылкой на тему трекера. qbit-версия выгружает .torrent и добавляет
	// его обратно вместе с комментарием, здесь такого пути нет. Кнопка
	// удаляет файлы с диска, поэтому молча ломать трекинг нельзя: отказываем
	// сразу, не трогая раздачу.
	return ErrRedownloadUnsupported
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
