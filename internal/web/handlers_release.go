package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/manager"
	"ripperr/internal/model"
	"ripperr/internal/runner"
)

const (
	maxReleaseURLLen  = 2048
	maxReleaseTagLen  = 256
	addTimeout        = 11 * time.Minute
	removalTimeout    = time.Minute
	removeTimeout     = 3 * time.Minute
	errBadReleaseHash = "hash must be a decimal uint32"
)

func (s *Server) handleAddOptions(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	supported := config.ClientSupportsTags(cfg.Manager.Client)
	resp := addOptionsResp{Mode: config.WatchModeCategories}
	cats, tags := cfg.Runner.WatchSelection(supported)
	if cfg.Runner.EffectiveWatchMode() == config.WatchModeTags && supported {
		resp.Mode = config.WatchModeTags
		resp.Options = tags
	} else {
		resp.Options = cats
	}
	if len(resp.Options) == 0 {
		resp.Any = true
		resp.Options = []string{}
		if resp.Mode == config.WatchModeCategories {
			resp.Options = s.clientCategories(r.Context(), cfg)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) clientCategories(ctx context.Context, cfg config.App) []string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	mc, err := managerClientFactory(cfg.Manager.Client, s.managerConfig(cfg))
	if err != nil {
		return []string{}
	}
	cats, err := mc.GetCategories(ctx)
	if err != nil || cats == nil {
		return []string{}
	}
	return cats
}

func validReleaseURL(raw string) bool {
	if len(raw) == 0 || len(raw) > maxReleaseURLLen {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	// magnet:? и прочие схемы отсекаем здесь: ссылка нужна на страницу раздачи
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s *Server) handleAddRelease(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL      string `json:"url"`
		Category string `json:"category"`
		Tag      string `json:"tag"`
	}
	if err := decodeStrict(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req := runner.AddRequest{
		URL:      strings.TrimSpace(body.URL),
		Category: strings.TrimSpace(body.Category),
		Tag:      strings.TrimSpace(body.Tag),
	}
	if !validReleaseURL(req.URL) {
		writeReleaseCode(w, http.StatusBadRequest, "bad_url", "url must be an http or https link to a topic page")
		return
	}
	if len(req.Category) > maxReleaseTagLen || len(req.Tag) > maxReleaseTagLen {
		writeError(w, http.StatusBadRequest, "category or tag is too long")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), addTimeout)
	defer cancel()
	res, err := s.runnerState.AddRelease(ctx, req)
	if err != nil {
		writeReleaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, addReleaseResp{OK: true, Unignored: res.Unignored})
}

func releaseHash(w http.ResponseWriter, r *http.Request) (string, bool) {
	hash := r.PathValue("hash")
	if _, err := strconv.ParseUint(hash, 10, 32); err != nil {
		writeError(w, http.StatusBadRequest, errBadReleaseHash)
		return "", false
	}
	return hash, true
}

func (s *Server) clientKeepsFiles() bool {
	return manager.NormalizeClient(s.effective().Manager.Client) == manager.ClientDownloadStation
}

func (s *Server) handleRemovalInfo(w http.ResponseWriter, r *http.Request) {
	hash, ok := releaseHash(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), removalTimeout)
	defer cancel()
	info, err := s.runnerState.ReleaseRemoval(ctx, hash)
	if err != nil {
		writeReleaseError(w, err)
		return
	}
	// Download Station удаляет задачу без файлов, поэтому опцию с файлами UI не показывает
	files := info.Files && !s.clientKeepsFiles()
	writeJSON(w, http.StatusOK, removalResp{Torrents: info.Torrents, Files: files})
}

func (s *Server) handleRemoveRelease(w http.ResponseWriter, r *http.Request) {
	hash, ok := releaseHash(w, r)
	if !ok {
		return
	}
	var body struct {
		Files    bool `json:"files"`
		Torrents *int `json:"torrents"`
	}
	if err := decodeStrict(r.Body, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Torrents == nil || *body.Torrents < 0 {
		writeError(w, http.StatusBadRequest, "torrents must be a non-negative number")
		return
	}
	if body.Files && s.clientKeepsFiles() {
		writeReleaseError(w, model.ErrFilesNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), removeTimeout)
	defer cancel()
	err := s.runnerState.RemoveRelease(ctx, runner.RemoveRequest{URLHash: hash, Files: body.Files, Torrents: *body.Torrents})
	if err != nil {
		writeReleaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeReleaseCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, releaseErrResp{Error: msg, Code: code})
}

func writeSafeMode(w http.ResponseWriter, msg string) {
	writeReleaseCode(w, http.StatusConflict, "safe_mode", msg)
}

func writeReleaseError(w http.ResponseWriter, err error) {
	var (
		status int
		code   string
	)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "timeout"
	case errors.Is(err, runner.ErrPollUnavailable):
		status, code = http.StatusServiceUnavailable, "runner_unavailable"
	case errors.Is(err, runner.ErrNotWatched):
		status, code = http.StatusBadRequest, "not_watched"
	case errors.Is(err, runner.ErrUnknownTracker):
		status, code = http.StatusUnprocessableEntity, "tracker_unknown"
	case errors.Is(err, runner.ErrNoTorrent):
		status, code = http.StatusUnprocessableEntity, "no_torrent"
	case errors.Is(err, model.ErrAlreadyAdded):
		status, code = http.StatusConflict, "already_added"
	case errors.Is(err, runner.ErrSafeMode):
		status, code = http.StatusConflict, "safe_mode"
	case errors.Is(err, runner.ErrReleaseBusy):
		status, code = http.StatusConflict, "torrent_busy"
	case errors.Is(err, model.ErrReplacePending):
		status, code = http.StatusConflict, "replace_pending"
	case errors.Is(err, runner.ErrTorrentsChanged):
		status, code = http.StatusConflict, "torrents_changed"
	case errors.Is(err, model.ErrFilesNotAllowed):
		status, code = http.StatusConflict, "files_not_allowed"
	case errors.Is(err, model.ErrPartialList):
		status, code = http.StatusBadGateway, "partial_list"
	case errors.Is(err, model.ErrRemoveUnconfirmed):
		status, code = http.StatusBadGateway, "remove_unconfirmed"
	default:
		status = http.StatusBadGateway
	}
	writeReleaseCode(w, status, code, err.Error())
}
