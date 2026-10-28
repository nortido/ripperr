package downloadstation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"ripperr/internal/helper/fileselect"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

var ErrRedownloadUnsupported = errors.New("force redownload is not supported for download station: it cannot recheck files or restore the torrent comment")

var (
	presenceCheckAttempts = 5
	presenceCheckDelay    = time.Second
)

const (
	partTorrent   = "torrent"
	createVersion = 2
)

type firstID string

func (id *firstID) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*id = firstID(s)
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	if len(list) > 0 {
		*id = firstID(list[0])
	}
	return nil
}

type listFile struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
}

func (c *apiClient) post(ctx context.Context, api, method string, version int, build func() (io.Reader, string, error), out any) error {
	for attempt := 0; ; attempt++ {
		sid := c.session()
		q := url.Values{"api": {api}, "version": {strconv.Itoa(version)}, "method": {method}, "_sid": {sid}}
		body, contentType, err := build()
		if err != nil {
			return fmt.Errorf("download station %s %s: build body: %w", api, method, err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+c.apis[api].Path+"?"+q.Encode(), body)
		if err != nil {
			return fmt.Errorf("download station %s %s: cannot build request", api, method)
		}
		req.Header.Set("Content-Type", contentType)
		data, err := c.do(req, api, method)
		if err != nil {
			return err
		}
		code, failed := failure(data)
		if !failed {
			return decode(api, method, data, out)
		}
		if !sessionLost(code) || attempt > 0 {
			return &apiError{API: api, Method: method, Code: code}
		}
		if err = c.login(ctx, sid); err != nil {
			return err
		}
	}
}

func (c *apiClient) do(req *http.Request, api, method string) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, transportError(api, method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("download station %s %s: unexpected status %d", api, method, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, transportError(api, method, err)
	}
	return data, nil
}

func (c *Client) createList(ctx context.Context, hash string, data []byte) (string, error) {
	build := func() (io.Reader, string, error) {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		for _, kv := range [][2]string{{"type", jsonString("file")}, {"file", `["` + partTorrent + `"]`}, {"create_list", "true"}} {
			if err := w.WriteField(kv[0], kv[1]); err != nil {
				return nil, "", err
			}
		}
		// файл последним полем, как требует API
		part, err := w.CreateFormFile(partTorrent, hash+".torrent")
		if err != nil {
			return nil, "", err
		}
		if _, err = part.Write(data); err != nil {
			return nil, "", err
		}
		if err = w.Close(); err != nil {
			return nil, "", err
		}
		return &b, w.FormDataContentType(), nil
	}
	var out struct {
		ListID firstID `json:"list_id"`
	}
	if err := c.api.post(ctx, apiTask2, "create", createVersion, build, &out); err != nil {
		return "", err
	}
	if out.ListID == "" {
		return "", errors.New("download station: create returned no list id")
	}
	return string(out.ListID), nil
}

func (c *Client) listFiles(ctx context.Context, listID string) ([]listFile, error) {
	var out struct {
		Files []listFile `json:"files"`
	}
	if err := c.api.call(ctx, apiTaskList, "get", url.Values{"list_id": {jsonString(listID)}}, &out); err != nil {
		return nil, err
	}
	return out.Files, nil
}

func (c *Client) startList(ctx context.Context, listID string, indexes []int, destination string) (string, error) {
	idx, err := json.Marshal(indexes)
	if err != nil {
		return "", err
	}
	form := url.Values{"list_id": {jsonString(listID)}, "file_indexes": {string(idx)}, "create_subfolder": {"true"}}
	if destination != "" {
		form.Set("destination", jsonString(destination))
	}
	build := func() (io.Reader, string, error) {
		return strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", nil
	}
	var out struct {
		TaskID firstID `json:"task_id"`
	}
	if err = c.api.post(ctx, apiTaskPolling, "download", c.api.version(apiTaskPolling), build, &out); err != nil {
		return "", err
	}
	if out.TaskID == "" {
		return "", errors.New("download station: download returned no task id")
	}
	return string(out.TaskID), nil
}

func (c *Client) AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error {
	if buf == nil || buf.Len() == 0 {
		return errors.New("download station: empty torrent data")
	}
	data := buf.Bytes()
	src, err := parseSource(data)
	if err != nil {
		return err
	}
	present, err := c.Exists(ctx, src.hash)
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, opts.Name)
	}
	listID, err := c.createList(ctx, src.hash, data)
	if err != nil {
		return err
	}
	files, err := c.listFiles(ctx, listID)
	if err != nil {
		return err
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	unwanted := fileselect.Unwanted(opts.FileSelection, names)
	var wanted []int
	for i, f := range files {
		if !slices.Contains(unwanted, i) {
			wanted = append(wanted, f.Index)
		}
	}
	if len(wanted) == 0 {
		return fmt.Errorf("download station: no files to download in %s", opts.Name)
	}
	slices.Sort(wanted)
	destination := opts.SavePath
	if destination == "" {
		destination = opts.Category
	}
	taskID, err := c.startList(ctx, listID, wanted, destination)
	if err != nil {
		return err
	}
	src.seeded = time.Now()
	c.remember(taskID, src)
	if opts.Stopped {
		if err = c.taskAction(ctx, "pause", url.Values{"id": {taskID}}); err != nil {
			return fmt.Errorf("pause new torrent %s: %w", opts.Name, err)
		}
	}
	if err = c.ensurePresentAfterAdd(ctx, src.hash); err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("download station: added %s, %d of %d files skipped", opts.Name, len(files)-len(wanted), len(files)))
	return nil
}

func (c *Client) AddFromMagnet(context.Context, string, model.ManagerAddFileOptions) error {
	return errors.New("download station: magnet add is not supported")
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
	return fmt.Errorf("download station: torrent %s not present after add", hash)
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

func (c *Client) taskAction(ctx context.Context, method string, params url.Values) error {
	var results []struct {
		ID    string `json:"id"`
		Error int    `json:"error"`
	}
	if err := c.api.call(ctx, apiTask, method, params, &results); err != nil {
		return err
	}
	for _, r := range results {
		if r.Error != 0 {
			return &apiError{API: apiTask, Method: method, Code: r.Error}
		}
	}
	return nil
}

func (c *Client) removeTask(ctx context.Context, id string) error {
	// только false: файлы на NAS этот клиент не трогает никогда
	return c.taskAction(ctx, "delete", url.Values{"id": {id}, "force_complete": {"false"}})
}

func (c *Client) taskID(ctx context.Context, hash string) (string, error) {
	r, err := c.find(ctx, hash)
	if r != nil {
		return r.ID, nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("torrent %s not found", hash)
}

func (c *Client) Delete(ctx context.Context, opts model.ManagerDeleteOptions) error {
	if !c.config.DeleteEnabled {
		return nil
	}
	r, err := c.find(ctx, opts.Hash)
	if r == nil {
		return err
	}
	logger.Info(fmt.Sprint("deleting torrent: ", opts.Name))
	return c.removeTask(ctx, r.ID)
}

func (c *Client) DeletesFiles(context.Context, model.ManagerDeleteOptions) bool {
	return false
}

func (c *Client) Discard(ctx context.Context, hash string) error {
	logger.Info(fmt.Sprint("discarding torrent without files: ", hash))
	id, err := c.taskID(ctx, hash)
	if err != nil {
		return err
	}
	return c.removeTask(ctx, id)
}

func (c *Client) Purge(context.Context, string) error {
	return fmt.Errorf("download station keeps files: %w", model.ErrFilesNotAllowed)
}

func (c *Client) Resume(ctx context.Context, hash string) error {
	id, err := c.taskID(ctx, hash)
	if err != nil {
		return err
	}
	return c.taskAction(ctx, "resume", url.Values{"id": {id}})
}

func (c *Client) ApplySelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	files, err := c.Files(ctx, hash)
	if err != nil {
		return err
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	if fileselect.Applied(opts.FileSelection, files, paths) {
		return nil
	}
	return errors.New("download station cannot change the file selection of a task")
}

func (c *Client) ConvergeAdded(context.Context, string, []byte, model.ManagerAddFileOptions) error {
	// выбор файлов задается только при создании задачи
	return nil
}

func (c *Client) SetComment(context.Context, string, string) error {
	// не ErrCommentUnsupported: на него manager.AttachComment перезальет задачу, и DS скачает ее заново
	return errors.New("download station cannot set torrent comment")
}

func (c *Client) ExportTorrent(context.Context, string) ([]byte, error) {
	return nil, model.ErrExportUnsupported
}

func (c *Client) ForceRedownload(context.Context, string, model.RedownloadOptions) error {
	return ErrRedownloadUnsupported
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
