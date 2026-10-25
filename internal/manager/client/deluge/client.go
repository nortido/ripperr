package deluge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

var (
	magnetFetch        = magnetmeta.Fetch
	magnetFetchTimeout = time.Minute
)

var ErrRedownloadUnsupported = errors.New("force redownload is not supported for deluge: its API cannot restore the torrent comment, so the tracker link would be lost")

var (
	presenceCheckAttempts = 5
	presenceCheckDelay    = time.Second
	startupProbeTimeout   = 15 * time.Second
	checkWaitDelay        = time.Second
	checkWaitBudget       = blackhole.CheckWaitBudget
)

const selectionCleanupTimeout = 10 * time.Second

const (
	stateChecking   = "Checking"
	stateAllocating = "Allocating"
	stateMoving     = "Moving"
	statePaused     = "Paused"
	stateError      = "Error"

	priorityNormal = 4
)

var listKeys = []string{"hash", "name", "comment", "save_path", "time_added", "state", "progress", "label"}

type torrentStatus struct {
	Hash           string       `json:"hash"`
	Name           string       `json:"name"`
	Comment        string       `json:"comment"`
	SavePath       string       `json:"save_path"`
	TimeAdded      float64      `json:"time_added"`
	State          string       `json:"state"`
	Progress       float64      `json:"progress"`
	Label          string       `json:"label"`
	Files          []statusFile `json:"files"`
	FilePriorities []int        `json:"file_priorities"`
}

type statusFile struct {
	Index int    `json:"index"`
	Path  string `json:"path"`
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
		Resume(ctx context.Context, hash string) error
		ReplacePending(hash string) bool
		Files(ctx context.Context, hash string) ([]model.TorrentFile, error)
		SetComment(ctx context.Context, hash, comment string) error
		ExportTorrent(ctx context.Context, hash string) ([]byte, error)
		DeleteEnabled() bool
		GetCategories(ctx context.Context) ([]string, error)
		ForceRedownload(ctx context.Context, torrentHash string, opts model.RedownloadOptions) error
		SweepBlackhole(ctx context.Context)
	} = (*Client)(nil)
	_ interface {
		Reachable(ctx context.Context) bool
	} = (*Client)(nil)
)

type Client struct {
	rpc    *rpcClient
	config model.ManagerConfig
	bh     *blackhole.Store
}

func New(config model.ManagerConfig) (*Client, error) {
	if config.BlackholeDir != "" {
		if err := os.MkdirAll(config.BlackholeDir, 0o755); err != nil {
			return nil, fmt.Errorf("create blackhole dir: %w", err)
		}
	}
	c := &Client{rpc: newRPCClient(config.Host, config.Password), config: config}
	c.bh = blackhole.New(config.BlackholeDir, c)
	// у New нет контекста, а молчащий хост не должен держать вызывающего минуту
	startCtx, cancel := context.WithTimeout(context.Background(), startupProbeTimeout)
	defer cancel()
	if err := c.rpc.login(startCtx); err != nil {
		return nil, err
	}
	if err := c.rpc.ensureConnected(startCtx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) mapEntry(id string, st *torrentStatus) *model.Entry {
	ts := time.Unix(int64(st.TimeAdded), 0)
	hash := st.Hash
	if hash == "" {
		hash = id
	}
	e := &model.Entry{
		Hash:      strings.ToLower(hash),
		Name:      st.Name,
		URL:       st.Comment,
		TopicID:   idRegexp.FindString(st.Comment),
		SavePath:  st.SavePath,
		Category:  st.Label,
		Timestamp: &ts,
		// у Deluge нет тегов, лимиты раздачи не переносим
		MaxRatio:  -2,
		Stopped:   st.State == statePaused,
		Unhealthy: st.State == stateError || st.State == stateChecking || st.State == stateMoving || st.State == stateAllocating,
	}
	if e.Stopped && st.Progress >= 100 {
		e.MaxRatio = c.config.PausedMaxRatio
	}
	return e
}

func (c *Client) List(ctx context.Context, _ model.FilterOptions) ([]*model.Entry, error) {
	raw, err := c.rpc.call(ctx, "core.get_torrents_status", map[string]any{}, listKeys)
	if err != nil {
		return nil, err
	}
	var all map[string]torrentStatus
	if err = json.Unmarshal(raw, &all); err != nil || all == nil {
		return nil, fmt.Errorf("deluge: decode torrents status: %w, result %.100s", err, raw)
	}
	entries := make([]*model.Entry, 0, len(all))
	for id, st := range all {
		entries = append(entries, c.mapEntry(id, &st))
	}
	slices.SortFunc(entries, func(a, b *model.Entry) int { return strings.Compare(a.Hash, b.Hash) })
	return entries, nil
}

func (c *Client) status(ctx context.Context, hash string, keys []string) (*torrentStatus, error) {
	raw, err := c.rpc.call(ctx, "core.get_torrent_status", strings.ToLower(hash), keys)
	if err != nil {
		return nil, err
	}
	// отсутствие торрента это только ответ {}: null или мусор не повод решить, что его нет
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("deluge: decode status of %s: %w, result %.100s", hash, err, raw)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	var st torrentStatus
	if err = json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("deluge: decode status of %s: %w", hash, err)
	}
	return &st, nil
}

func (c *Client) GetTorrentDetails(ctx context.Context, hash string) (name, comment string, err error) {
	if hash == "" {
		return
	}
	st, err := c.status(ctx, hash, []string{"name", "comment"})
	if err != nil || st == nil {
		return "", "", err
	}
	return st.Name, st.Comment, nil
}

func (c *Client) GetCategories(ctx context.Context) ([]string, error) {
	raw, err := c.rpc.call(ctx, "label.get_labels")
	if errorCode(err) == codeUnknownMethod {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var labels []string
	if err = json.Unmarshal(raw, &labels); err != nil {
		return nil, fmt.Errorf("deluge: decode labels: %w", err)
	}
	if labels == nil {
		labels = []string{}
	}
	slices.Sort(labels)
	return labels, nil
}

func (c *Client) Exists(ctx context.Context, hash string) (bool, error) {
	st, err := c.status(ctx, hash, []string{"hash"})
	if err != nil {
		return false, err
	}
	return st != nil, nil
}

func (c *Client) remove(ctx context.Context, hash string, removeData bool) (bool, error) {
	raw, err := c.rpc.call(ctx, "core.remove_torrent", strings.ToLower(hash), removeData)
	if err != nil {
		return false, err
	}
	var ok bool
	if err = json.Unmarshal(raw, &ok); err != nil {
		return false, fmt.Errorf("deluge: decode remove_torrent: %w", err)
	}
	return ok, nil
}

func (c *Client) Delete(ctx context.Context, opts model.ManagerDeleteOptions) error {
	if !c.config.DeleteEnabled {
		return nil
	}
	logger.Info(fmt.Sprint("deleting torrent: ", opts.Name))
	ok, err := c.remove(ctx, opts.Hash, opts.DeleteFiles && c.DeletesFiles(ctx, opts))
	if err != nil {
		// на неизвестный id Deluge бросает исключение; как qbit и transmission
		// считаем это удалением, но только когда демон сам ответил, что торрента нет
		if present, perr := c.Exists(ctx, opts.Hash); perr == nil && !present {
			return nil
		}
		return err
	}
	if !ok {
		return fmt.Errorf("deluge: remove_torrent %s returned false", opts.Hash)
	}
	return nil
}

func (c *Client) DeletesFiles(_ context.Context, opts model.ManagerDeleteOptions) bool {
	// "tv," из env дает пустую категорию, она совпала бы с раздачами без категории
	return c.config.DeleteEnabled && c.config.DeleteFilesEnabled && opts.Category != "" &&
		slices.Contains(c.config.DeleteCategories, opts.Category)
}

func (c *Client) Discard(ctx context.Context, hash string) error {
	logger.Info(fmt.Sprint("discarding torrent without files: ", hash))
	ok, err := c.remove(ctx, hash, false)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("deluge: remove_torrent %s returned false", hash)
	}
	return nil
}

func (c *Client) WaitAbsent(ctx context.Context, hash string) (bool, error) {
	for i := 0; i < presenceCheckAttempts; i++ {
		present, err := c.Exists(ctx, hash)
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
		present, err := c.Exists(ctx, hash)
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
	return fmt.Errorf("deluge: torrent %s not present after add", hash)
}

func (c *Client) preAddPresenceCheck(ctx context.Context, hash, name, optsHash string) error {
	if strings.EqualFold(hash, optsHash) && c.config.DeleteEnabled {
		absent, err := c.WaitAbsent(ctx, hash)
		if err != nil {
			return err
		}
		if !absent {
			return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, name)
		}
		return nil
	}
	present, err := c.Exists(ctx, hash)
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, name)
	}
	return nil
}

func addOptions(opts model.ManagerAddFileOptions, paused bool) map[string]any {
	o := map[string]any{"add_paused": paused}
	if opts.SavePath != "" {
		o["download_location"] = opts.SavePath
	}
	return o
}

func addError(err error, name string) error {
	var re *rpcError
	if errors.As(err, &re) && strings.Contains(strings.ToLower(re.Message), "already in session") {
		return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, name)
	}
	return err
}

func (c *Client) addTorrentVerified(ctx context.Context, data []byte, opts model.ManagerAddFileOptions) error {
	hash, err := torrentmeta.InfoHash(data)
	if err != nil {
		return err
	}
	hash = strings.ToLower(hash)
	if err = c.preAddPresenceCheck(ctx, hash, opts.Name, opts.Hash); err != nil {
		return err
	}
	// выбор файлов ставим до старта, иначе успеет качнуться лишнее
	paused := opts.Stopped || len(opts.FileSelection) > 0
	_, err = c.rpc.call(ctx, "core.add_torrent_file", hash+".torrent", base64.StdEncoding.EncodeToString(data), addOptions(opts, paused))
	if err != nil {
		return addError(err, opts.Name)
	}
	if err = c.ensurePresentAfterAdd(ctx, hash); err != nil {
		return err
	}
	return c.finishAdd(ctx, hash, opts)
}

func (c *Client) setUp(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	err := c.applyLabel(ctx, hash, opts)
	if err == nil && len(opts.FileSelection) > 0 {
		err = c.applySelection(ctx, hash, opts)
	}
	return err
}

func (c *Client) finishAdd(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	err := c.setUp(ctx, hash, opts)
	if err == nil {
		return nil
	}
	// без метки торрент выпадет из отслеживаемых категорий, без выбора скачает все:
	// убираем без файлов, старый остается, следующий прогон повторит
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), selectionCleanupTimeout)
	defer cancel()
	if derr := c.Discard(dctx, hash); derr != nil {
		logger.Warn(fmt.Sprintf("deluge: discard %s after failed setup: %v", hash, derr))
	}
	return fmt.Errorf("set up new torrent %s: %w", opts.Name, err)
}

func (c *Client) ConvergeAdded(ctx context.Context, hash string, _ []byte, opts model.ManagerAddFileOptions) error {
	if len(opts.FileSelection) == 0 {
		return nil
	}
	hash = strings.ToLower(hash)
	state, err := c.state(ctx, hash)
	if err != nil {
		return err
	}
	switch state {
	case stateChecking, stateMoving, stateAllocating:
		return fmt.Errorf("deluge: torrent %s is %s, not settled yet", hash, state)
	case statePaused:
	default:
		logger.Info(fmt.Sprintf("deluge: converge %s skipped: state %s", opts.Name, state))
		return nil
	}
	files, err := c.Files(ctx, hash)
	if err != nil {
		return err
	}
	// снятый файл значит, что выбор уже ставили мы или сам пользователь: не трогаем
	if slices.ContainsFunc(files, func(f model.TorrentFile) bool { return !f.Wanted }) {
		return nil
	}
	logger.Info(fmt.Sprintf("deluge: converge %s: applying label and file selection", opts.Name))
	// торрент с тем же хешем может быть чужим, живой загрузкой: при сбое не удаляем, пара останется для повтора
	if err = c.setUp(ctx, hash, opts); err != nil {
		return fmt.Errorf("converge %s: %w", opts.Name, err)
	}
	return nil
}

func (c *Client) applyLabel(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	if opts.Category == "" {
		return nil
	}
	label := strings.ToLower(opts.Category)
	_, err := c.rpc.call(ctx, "label.set_torrent", hash, label)
	if errorCode(err) == codeUnknownMethod {
		logger.Warn(fmt.Sprintf("deluge: Label plugin is off, category %q of %s not set", opts.Category, opts.Name))
		return nil
	}
	var re *rpcError
	if !errors.As(err, &re) || !strings.Contains(re.Message, "Unknown Label") {
		return err
	}
	if _, err = c.rpc.call(ctx, "label.add", label); err != nil {
		return err
	}
	_, err = c.rpc.call(ctx, "label.set_torrent", hash, label)
	return err
}

func (c *Client) ApplySelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	return c.applySelection(ctx, hash, opts)
}

func (c *Client) applySelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	files, err := c.Files(ctx, hash)
	if err != nil {
		return err
	}
	// без списка файлов выбор не применить, а старт скачал бы все
	if len(files) == 0 {
		return fmt.Errorf("torrent %s has no files yet", hash)
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	idx := fileselect.Unwanted(opts.FileSelection, paths)
	if len(idx) > 0 {
		prios := make([]int, len(files))
		for i := range prios {
			prios[i] = priorityNormal
		}
		for _, i := range idx {
			prios[i] = 0
		}
		if _, err = c.rpc.call(ctx, "core.set_torrent_options", []string{hash}, map[string]any{"file_priorities": prios}); err != nil {
			return err
		}
		if err = c.verifyPriorities(ctx, hash, prios); err != nil {
			return err
		}
	}
	if !opts.Stopped {
		if _, err = c.rpc.call(ctx, "core.resume_torrent", hash); err != nil {
			return err
		}
	}
	logger.Info(fmt.Sprintf("deluge: file selection for %s: %d of %d files skipped", opts.Name, len(idx), len(paths)))
	return nil
}

func (c *Client) verifyPriorities(ctx context.Context, hash string, prios []int) error {
	for i := 0; ; i++ {
		files, err := c.Files(ctx, hash)
		if err != nil {
			return err
		}
		if prioritiesMatch(files, prios) {
			return nil
		}
		if i+1 >= presenceCheckAttempts {
			return fmt.Errorf("file priorities of %s not applied", hash)
		}
		if err = sleepCtx(ctx, presenceCheckDelay); err != nil {
			return err
		}
	}
}

func prioritiesMatch(files []model.TorrentFile, prios []int) bool {
	if len(files) != len(prios) {
		return false
	}
	for i, f := range files {
		if f.Wanted != (prios[i] > 0) {
			return false
		}
	}
	return true
}

func (c *Client) addMagnetVerified(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	hash, known := torrentmeta.BtihFromMagnet(magnet)
	if known {
		if err := c.preAddPresenceCheck(ctx, hash, opts.Name, opts.Hash); err != nil {
			return err
		}
	}
	if opts.Comment != "" {
		logger.Warn(fmt.Sprintf("deluge: magnet add for %s has no way to set torrent comment, tracker link will not persist", opts.Name))
	}
	if _, err := c.rpc.call(ctx, "core.add_torrent_magnet", magnet, addOptions(opts, opts.Stopped)); err != nil {
		return addError(err, opts.Name)
	}
	if !known {
		return nil
	}
	if err := c.ensurePresentAfterAdd(ctx, hash); err != nil {
		return err
	}
	// без метаданных выбор файлов не применить, как и у transmission
	opts.FileSelection = nil
	return c.finishAdd(ctx, hash, opts)
}

func (c *Client) AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error {
	return c.addTorrentVerified(ctx, buf.Bytes(), opts)
}

func (c *Client) AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	if opts.Comment != "" {
		// комментарий при добавлении магнитом не задать: собираем .torrent сами,
		// на своем таймауте, чтобы мертвый рой не съел бюджет запасного пути
		fctx, cancel := context.WithTimeout(ctx, magnetFetchTimeout)
		data, ferr := magnetFetch(fctx, magnet)
		cancel()
		if ferr == nil {
			if data, ferr = torrentmeta.SetComment(data, opts.Comment); ferr == nil {
				return c.addTorrentVerified(ctx, data, opts)
			}
		}
		logger.Warn(fmt.Sprintf("deluge: own magnet metadata fetch for %s failed: %v, falling back to magnet add", opts.Name, ferr))
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

func (c *Client) ReplacePending(hash string) bool {
	return c.bh.PendingReplace(hash)
}

func (c *Client) Reachable(ctx context.Context) bool {
	return c.rpc.ensureConnected(ctx) == nil
}

func (c *Client) state(ctx context.Context, hash string) (string, error) {
	st, err := c.status(ctx, hash, []string{"state"})
	if err != nil {
		return "", err
	}
	if st == nil {
		return "", fmt.Errorf("torrent %s not found", hash)
	}
	return st.State, nil
}

func (c *Client) waitChecked(ctx context.Context, hash string, delayFirst bool) (string, error) {
	for {
		if delayFirst {
			if err := sleepCtx(ctx, checkWaitDelay); err != nil {
				return "", fmt.Errorf("wait for check of %s: %w", hash, err)
			}
		}
		delayFirst = true
		state, err := c.state(ctx, hash)
		if err != nil {
			return "", fmt.Errorf("wait for check of %s: %w", hash, err)
		}
		if state != stateChecking && state != stateAllocating {
			return state, nil
		}
	}
}

func (c *Client) Resume(ctx context.Context, hash string) error {
	hash = strings.ToLower(hash)
	waitCtx, cancel := context.WithTimeout(ctx, checkWaitBudget)
	defer cancel()
	state, err := c.waitChecked(waitCtx, hash, false)
	if err != nil {
		return err
	}
	// запущенный не проверяем заново, иначе каждый тик подметальщика гонял бы диск
	if state != statePaused && state != stateError {
		return nil
	}
	// старый торрент мог уйти вместе с файлами, и состояние кусков могло устареть. Сверяем диск до старта,
	// первое чтение после паузы: проверка запускается не сразу
	if _, err = c.rpc.call(ctx, "core.force_recheck", []string{hash}); err != nil {
		return err
	}
	if _, err = c.waitChecked(waitCtx, hash, true); err != nil {
		return err
	}
	if _, err = c.rpc.call(ctx, "core.resume_torrent", hash); err != nil {
		return err
	}
	for {
		if err = sleepCtx(waitCtx, checkWaitDelay); err != nil {
			return fmt.Errorf("torrent %s still paused after start: %w", hash, err)
		}
		if state, err = c.state(waitCtx, hash); err != nil {
			return fmt.Errorf("read state of %s: %w", hash, err)
		}
		if state == stateError {
			return fmt.Errorf("torrent %s is in error after start", hash)
		}
		if state != statePaused {
			return nil
		}
	}
}

func (c *Client) Files(ctx context.Context, hash string) ([]model.TorrentFile, error) {
	st, err := c.status(ctx, hash, []string{"files", "file_priorities"})
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, fmt.Errorf("torrent %s not found", hash)
	}
	if len(st.Files) != len(st.FilePriorities) {
		return nil, fmt.Errorf("torrent %s: %d files but %d priorities", hash, len(st.Files), len(st.FilePriorities))
	}
	files := make([]model.TorrentFile, len(st.Files))
	seen := make([]bool, len(st.Files))
	for _, f := range st.Files {
		if f.Index < 0 || f.Index >= len(files) || seen[f.Index] {
			return nil, fmt.Errorf("torrent %s: bad file index %d", hash, f.Index)
		}
		seen[f.Index] = true
		files[f.Index] = model.TorrentFile{Path: f.Path, Wanted: st.FilePriorities[f.Index] > 0}
	}
	return files, nil
}

func (c *Client) SetComment(context.Context, string, string) error {
	return model.ErrCommentUnsupported
}

func (c *Client) ExportTorrent(context.Context, string) ([]byte, error) {
	return nil, model.ErrExportUnsupported
}

func (c *Client) DeleteEnabled() bool {
	return c.config.DeleteEnabled
}

func (c *Client) ForceRedownload(context.Context, string, model.RedownloadOptions) error {
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
