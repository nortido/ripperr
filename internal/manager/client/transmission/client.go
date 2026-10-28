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

	"ripperr/internal/helper/fileselect"
	"ripperr/internal/helper/magnetmeta"
	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/manager/blackhole"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

var idRegexp = regexp.MustCompile(`(\d+)`)

var magnetFetch = magnetmeta.Fetch

var magnetFetchTimeout = time.Minute

var ErrRedownloadUnsupported = errors.New("force redownload is not supported for transmission: its RPC cannot restore the torrent comment, so the tracker link would be lost")

var (
	// не const: тесты их ужимают
	presenceCheckAttempts = 5
	presenceCheckDelay    = time.Second
	startupProbeTimeout   = 15 * time.Second
	checkWaitDelay        = time.Second
	checkWaitBudget       = blackhole.CheckWaitBudget
)

var torrentFields = []string{
	"hashString", "name", "comment", "downloadDir", "labels",
	"addedDate", "percentDone", "status", "seedRatioLimit", "seedRatioMode", "magnetLink", "error",
}

type rpcTorrent struct {
	HashString     string        `json:"hashString"`
	Name           string        `json:"name"`
	Comment        string        `json:"comment"`
	DownloadDir    string        `json:"downloadDir"`
	Labels         []string      `json:"labels"`
	AddedDate      int64         `json:"addedDate"`
	PercentDone    float64       `json:"percentDone"`
	Status         int           `json:"status"`
	SeedRatioLimit float64       `json:"seedRatioLimit"`
	SeedRatioMode  int           `json:"seedRatioMode"`
	MagnetLink     string        `json:"magnetLink"`
	Error          int           `json:"error"`
	ErrorString    string        `json:"errorString"`
	Files          []rpcFile     `json:"files"`
	FileStats      []rpcFileStat `json:"fileStats"`
}

type rpcFile struct {
	Name           string `json:"name"`
	BytesCompleted int64  `json:"bytesCompleted"`
}

type rpcFileStat struct {
	Wanted wantedFlag `json:"wanted"`
}

type wantedFlag bool

func (w *wantedFlag) UnmarshalJSON(b []byte) error {
	// в коде Transmission wanted это bool, но спецификация пишет 0/1; читаем оба,
	// иначе Files ломается на каждом вызове
	switch string(bytes.TrimSpace(b)) {
	case "true", "1":
		*w = true
	case "false", "0":
		*w = false
	default:
		return fmt.Errorf("decode wanted: unexpected value %s", b)
	}
	return nil
}

type torrentGetArgs struct {
	Torrents []rpcTorrent `json:"torrents"`
}

type sessionGetArgs struct {
	DownloadDir string `json:"download-dir"`
}

type torrentAddArgs struct {
	TorrentAdded *addedTorrent `json:"torrent-added"`
	// на дубликат Transmission все равно шлет success. Без этого поля
	// ensurePresentAfterAdd увидит старый торрент и молча вернет успех
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

var (
	// client в internal/manager/service.go неэкспортирован, сверяем литералом
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
		Discard(ctx context.Context, hash string) error
		Purge(ctx context.Context, hash string) error
		Resume(ctx context.Context, hash string) error
		ReplacePending(hash string) bool
	} = (*Client)(nil)
	_ interface {
		AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error
		AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error
		Exists(ctx context.Context, hash string) (bool, error)
		Reachable(ctx context.Context) bool
		Discard(ctx context.Context, hash string) error
		Resume(ctx context.Context, hash string) error
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

	// New без контекста: без своего таймаута хост, который принял TCP и молчит,
	// держит проверку интеграции defaultTimeout (60с)
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

func categoryFromPath(downloadDir, base string) (category string, insideBase bool) {
	downloadDir = filepath.Clean(downloadDir)
	base = filepath.Clean(base)
	if downloadDir == base {
		return "", true
	}
	rel, err := filepath.Rel(base, downloadDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		// /mnt/archive/Movies дает ту же категорию, что <base>/Movies. Delete
		// по совпадению с DeleteCategories может удалять файлы только внутри базы
		return filepath.Base(downloadDir), false
	}
	// Sonarr/Radarr кладут категорию одним подкаталогом. Без обрезки у tv/show1
	// и tv/show2 была бы своя категория и GetCategories отдавал бы мусор
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
		// пустой базовый каталог (session-get ничего не отдал) дает относительный
		// путь. Transmission его не ждет, пусть лучше берет свой каталог по умолчанию
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
	if opts.Stopped {
		args["paused"] = true
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
		// у Transmission только лимит простоя (seedIdleLimit). С общим временем
		// раздачи это разные вещи
		MaxSeedingTime: 0,
		Stopped:        t.Status == 0,
		// error 3: локальная ошибка (нет данных, диск отключен), 1-2: проверка
		Unhealthy: t.Error == 3 || t.Status == 1 || t.Status == 2,
	}
	// пустой каталог оставляет путь пустым: иначе "/Name" сошел бы за известное место
	if t.DownloadDir != "" {
		res.ContentPath = t.DownloadDir + "/" + t.Name
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

func (c *Client) categoryInsideBase(ctx context.Context, hash string) bool {
	// в opts от Delete только категория, пути нет. Такая же категория бывает
	// и у каталога снаружи базы, поэтому спрашиваем downloadDir заново
	t, err := c.torrentByHash(ctx, hash, []string{"downloadDir"})
	// раздачу не нашли: лучше оставить файлы, чем удалить чужие
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

	_, err := c.rpc.call(ctx, "torrent-remove", map[string]interface{}{
		"ids":               []string{opts.Hash},
		"delete-local-data": opts.DeleteFiles && c.DeletesFiles(ctx, opts),
	})
	return err
}

func (c *Client) DeletesFiles(ctx context.Context, opts model.ManagerDeleteOptions) bool {
	// "tv," из env дает пустую категорию, она совпала бы с раздачами без категории
	return c.config.DeleteEnabled && c.config.DeleteFilesEnabled &&
		opts.Category != "" &&
		slices.Contains(c.config.DeleteCategories, opts.Category) &&
		c.categoryInsideBase(ctx, opts.Hash)
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

func (c *Client) WaitAbsent(ctx context.Context, hash string) (bool, error) {
	return c.waitUntilAbsent(ctx, hash)
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
	// как в mapEntry, по соглашению qBittorrent: -1 без лимита (режим 2), -2
	// глобальный лимит (режим 0), больше нуля свой (режим 1). Другое у qbit
	// не бывает, режим Transmission тогда не трогаем
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

	// выбор файлов ставим до старта, иначе успеет качнуться лишнее
	addOpts := opts
	addOpts.Stopped = opts.Stopped || len(opts.FileSelection) > 0
	args := addArgs(addOpts, c.downloadDir)
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
	if len(opts.FileSelection) == 0 {
		return nil
	}
	if err = c.applySelection(ctx, hash, opts); err != nil {
		// убираем без файлов и на своем контексте: отмена ctx не должна
		// оставить висеть остановленный торрент без выбора
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), selectionCleanupTimeout)
		defer cancel()
		if derr := c.Discard(dctx, hash); derr != nil {
			logger.Warn(fmt.Sprintf("transmission: discard %s after failed file selection: %v", hash, derr))
		}
		return fmt.Errorf("apply file selection to %s: %w", opts.Name, err)
	}
	return nil
}

const selectionCleanupTimeout = 10 * time.Second

func (c *Client) ConvergeAdded(ctx context.Context, hash string, _ []byte, opts model.ManagerAddFileOptions) error {
	if len(opts.FileSelection) == 0 {
		return nil
	}
	t, err := c.torrentByHash(ctx, hash, []string{"hashString", "status"})
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("torrent %s not found", hash)
	}
	if t.Status == 1 || t.Status == 2 {
		return fmt.Errorf("converge %s: torrent %s is checking, not settled yet", opts.Name, hash)
	}
	// запущенный торрент мог запустить пользователь, выбор на ходу не навязываем
	if t.Status != 0 {
		logger.Info(fmt.Sprintf("transmission: converge %s: torrent %s is not stopped, left as is", opts.Name, hash))
		return nil
	}
	files, err := c.Files(ctx, hash)
	if err != nil {
		return err
	}
	// хоть один снятый файл значит выбор уже ставили мы или пользователь.
	// Ровно наш выбор без Stopped значит, что упал старт после выбора: только стартуем
	if slices.ContainsFunc(files, func(f model.TorrentFile) bool { return !f.Wanted }) {
		paths := make([]string, len(files))
		for i, f := range files {
			paths[i] = f.Path
		}
		if opts.Stopped || !fileselect.Applied(opts.FileSelection, files, paths) {
			return nil
		}
		logger.Info(fmt.Sprintf("transmission: converge %s: file selection already set, starting %s", opts.Name, hash))
		if _, err = c.rpc.call(ctx, "torrent-start", map[string]interface{}{"ids": []string{hash}}); err != nil {
			return fmt.Errorf("converge %s: start %s: %w", opts.Name, hash, err)
		}
		return nil
	}
	// без discard: тот же хеш мог добавить сам пользователь, торрент остается,
	// вызывающий держит пару и повторит
	if err = c.applySelection(ctx, hash, opts); err != nil {
		return fmt.Errorf("converge %s: apply file selection to %s: %w", opts.Name, hash, err)
	}
	return nil
}

func (c *Client) ApplySelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	return c.applySelection(ctx, hash, opts)
}

func (c *Client) applySelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	files, err := c.Files(ctx, hash)
	if err != nil {
		return err
	}
	// без списка файлов (нет метаданных) выбор не применить, а старт скачал бы все
	if len(files) == 0 {
		return fmt.Errorf("torrent %s has no files yet", hash)
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	idx := fileselect.Unwanted(opts.FileSelection, paths)
	// пустой files-unwanted значит все файлы, поэтому шлем только непустой
	if len(idx) > 0 {
		if _, err = c.rpc.call(ctx, "torrent-set", map[string]interface{}{"ids": []string{hash}, "files-unwanted": idx}); err != nil {
			return err
		}
	}
	if !opts.Stopped {
		if _, err = c.rpc.call(ctx, "torrent-start", map[string]interface{}{"ids": []string{hash}}); err != nil {
			return err
		}
	}
	logger.Info(fmt.Sprintf("transmission: file selection for %s: %d of %d files skipped", opts.Name, len(idx), len(paths)))
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
		// RPC Transmission не дает задать комментарий при добавлении магнитом, ссылка на тему
		// потеряется. Если появится способ дописать комментарий после метаданных, сделать тут
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
	// магнитом комментарий не задать, и magnet-пара blackhole осталась бы невидимой для Ripperr.
	// Собираем .torrent с комментарием сами, не вышло: добавляем магнитом
	if opts.Comment != "" {
		// потолок сильно меньше blackhole.AddTimeout: мертвый рой иначе съест весь ctx
		// и запасному пути ниже ничего не останется
		fctx, cancel := context.WithTimeout(ctx, magnetFetchTimeout)
		data, ferr := magnetFetch(fctx, magnet)
		cancel()
		if ferr == nil {
			if data, ferr = torrentmeta.SetComment(data, opts.Comment); ferr == nil {
				return c.addTorrentVerified(ctx, data, opts)
			}
		}
		logger.Warn(fmt.Sprintf("transmission: own magnet metadata fetch for %s failed: %v, falling back to magnet add", opts.Name, ferr))
	}
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

func (c *Client) ForceRedownload(_ context.Context, _ string, _ model.RedownloadOptions) error {
	// RPC не умеет выгрузить .torrent (как ExportTorrentCtx в qbittorrent) и не
	// задает комментарий при повтороном добавлении, а трекинг держится на ссылке
	// в комментарии. Кнопка сносит файлы, так что отказываем сразу
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

func (c *Client) ReplacePending(hash string) bool {
	return c.bh.PendingReplace(hash)
}

func (c *Client) Discard(ctx context.Context, hash string) error {
	logger.Info(fmt.Sprint("discarding torrent without files: ", hash))
	_, err := c.rpc.call(ctx, "torrent-remove", map[string]interface{}{
		"ids":               []string{hash},
		"delete-local-data": false,
	})
	return err
}

func (c *Client) Purge(ctx context.Context, hash string) error {
	logger.Info(fmt.Sprint("deleting torrent with files: ", hash))
	_, err := c.rpc.call(ctx, "torrent-remove", map[string]interface{}{
		"ids":               []string{hash},
		"delete-local-data": true,
	})
	return err
}

func (c *Client) Resume(ctx context.Context, hash string) error {
	waitCtx, cancel := context.WithTimeout(ctx, checkWaitBudget)
	defer cancel()
	// остановленное добавление само сверяет файлы со старыми: ждем ее конца
	status, err := c.waitChecked(waitCtx, hash)
	if err != nil {
		return err
	}
	// запущенный не проверяем заново, иначе каждый тик подметальщика гонял бы диск
	if status != 0 {
		return nil
	}
	// torrent-start не перепроверяет и после удаления файлов старого
	// отказывает "No data found", поэтому сначала torrent-verify
	ids := []string{hash}
	if _, err = c.rpc.call(ctx, "torrent-verify", map[string]interface{}{"ids": ids}); err != nil {
		return err
	}
	if _, err = c.waitChecked(waitCtx, hash); err != nil {
		return err
	}
	if _, err = c.rpc.call(ctx, "torrent-start", map[string]interface{}{"ids": ids}); err != nil {
		return err
	}
	t, err := c.torrentByHash(ctx, hash, []string{"hashString", "error", "errorString"})
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("torrent %s not found after start", hash)
	}
	// 3 это локальная ошибка данных; 1 и 2 про трекер, файлы тут ни при чем
	if t.Error == 3 {
		return fmt.Errorf("torrent %s failed to start: %s", hash, t.ErrorString)
	}
	return nil
}

func (c *Client) waitChecked(ctx context.Context, hash string) (int, error) {
	for {
		t, err := c.torrentByHash(ctx, hash, []string{"hashString", "status"})
		if err == nil && t == nil {
			err = fmt.Errorf("torrent %s not found", hash)
		}
		if err != nil {
			return 0, fmt.Errorf("wait for verify of %s: %w", hash, err)
		}
		// 1 ждет проверки, 2 проверяется
		if t.Status != 1 && t.Status != 2 {
			return t.Status, nil
		}
		if err = sleepCtx(ctx, checkWaitDelay); err != nil {
			return 0, fmt.Errorf("wait for verify of %s: %w", hash, err)
		}
	}
}

func (c *Client) Files(ctx context.Context, hash string) ([]model.TorrentFile, error) {
	t, err := c.torrentByHash(ctx, hash, []string{"hashString", "files", "fileStats"})
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("torrent %s not found", hash)
	}
	if len(t.Files) != len(t.FileStats) {
		return nil, fmt.Errorf("torrent %s: %d files but %d file stats", hash, len(t.Files), len(t.FileStats))
	}
	files := make([]model.TorrentFile, len(t.Files))
	for i := range t.Files {
		files[i] = model.TorrentFile{Path: t.Files[i].Name, Wanted: bool(t.FileStats[i].Wanted), Done: t.Files[i].BytesCompleted}
	}
	return files, nil
}

func (c *Client) FreeSpace(ctx context.Context, path string) (int64, error) {
	raw, err := c.rpc.call(ctx, "free-space", map[string]interface{}{"path": path})
	if err != nil {
		return 0, err
	}
	// старые версии отдают size-bytes, новая спецификация size_bytes
	var res struct {
		Dash  *int64 `json:"size-bytes"`
		Under *int64 `json:"size_bytes"`
	}
	if err = json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("decode free-space: %w", err)
	}
	size := res.Dash
	if size == nil {
		size = res.Under
	}
	if size == nil || *size < 0 {
		return 0, model.ErrFreeSpaceUnknown
	}
	return *size, nil
}
