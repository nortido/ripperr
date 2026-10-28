package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/settings"
	"ripperr/internal/storage/client/sqlite"
	"ripperr/internal/system/logger"
	trackerPkg "ripperr/internal/tracker"
	"ripperr/internal/tracker/registry"
)

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	writeJSON(w, http.StatusOK, json.RawMessage(settings.ScheduleJSON(&cfg)))
}

func (s *Server) handlePutSchedule(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err = settings.MergeSchedule(r.Context(), s.db, body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// категории берем из примененного конфига, не из патча: список мог прийти из env.
	// В режиме тегов чистки нет: устаревший список категорий стер бы релизы, которые отслеживают теги
	if cfg, cerr := s.writableConfig(); cerr != nil {
		logger.Warn("purge disabled categories skipped: settings unreadable: " + cerr.Error())
	} else if cats, _ := cfg.Runner.WatchSelection(config.ClientSupportsTags(cfg.Manager.Client)); len(cats) > 0 {
		if n, err := s.db.PurgeCategoriesExcept(r.Context(), cats); err != nil {
			logger.Warn("purge disabled categories: " + err.Error())
		} else if n > 0 {
			logger.Info(fmt.Sprintf("purged %d releases of disabled categories", n))
		}
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetTrackers(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	items, err := settings.TrackersEffective(r.Context(), &cfg, s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	added, err := s.addedTrackerKeys(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	keys := orderTrackerKeys(added, s.savedTrackerOrder(r.Context()))
	ms, err := settings.GetMirrorSync(r.Context(), s.db)
	if err != nil {
		logger.Warn("mirror sync state: " + err.Error())
	}
	out := make([]trackerListResp, 0, len(keys))
	for _, key := range keys {
		it, ok := items[key]
		if !ok {
			continue
		}
		p := ms.Pending(key, it, cfg.Runner.MirrorConfirm)
		out = append(out, trackerListResp{
			trackerResp: trackerView(key, it),
			Suggestions: p.Suggestions, Paused: p.Paused, Auto: p.Auto,
			ConfirmOn: cfg.Runner.MirrorConfirm,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetTrackerTypes(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	items, err := settings.TrackersEffective(r.Context(), &cfg, s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	added, err := s.addedTrackerKeys(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	keys := registry.Keys()
	out := make([]trackerTypeResp, 0, len(keys))
	for _, key := range keys {
		it, ok := items[key]
		if !ok {
			def, _ := registry.ByKey(key)
			it = def.Defaults
		}
		out = append(out, trackerTypeResp{trackerResp: trackerView(key, it), Added: slices.Contains(added, key)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddTracker(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !trackerKnown(key) {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	unlock := settings.LockTrackers()
	defer unlock()
	list, err := s.storedTrackerList(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if slices.Contains(list, key) {
		writeError(w, http.StatusConflict, "tracker already added")
		return
	}
	cfg, err := s.writableConfig()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, settingsUnreadableMsg)
		return
	}
	if body, err = prepareTrackerPatch(cfg, key, body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body, err = withTrackerEnabled(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err = settings.MergeTrackerKey(r.Context(), s.db, key, body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err = settings.SaveTrackersAdded(r.Context(), s.db, append(list, key)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
	logger.Info("tracker added: " + key)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRemoveTracker(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !trackerKnown(key) {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	ctx := r.Context()
	unlock := settings.LockTrackers()
	defer unlock()
	list, err := s.storedTrackerList(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !slices.Contains(list, key) {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	// сначала данные, список последним: при сбое трекер остается в списке и
	// удаление можно повторить, каждый шаг идемпотентен
	if err = settings.DeleteTrackerKey(ctx, s.db, key); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err = s.db.DeleteTracker(ctx, registry.ID(key)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if order := s.savedTrackerOrder(ctx); slices.Contains(order, key) {
		raw, err := json.Marshal(slices.DeleteFunc(order, func(k string) bool { return k == key }))
		if err == nil {
			err = s.db.SaveSetting(ctx, sqlite.SettingTrackersOrder, raw)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err = settings.SaveTrackersAdded(ctx, s.db, slices.DeleteFunc(list, func(k string) bool { return k == key })); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
	logger.Info("tracker removed: " + key)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRevealTrackerSecret(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !trackerKnown(key) {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	if added, err := s.trackerAdded(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, "tracker settings unavailable")
		return
	} else if !added {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	var req revealReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cfg := s.effective()
	items, err := settings.TrackersEffective(r.Context(), &cfg, s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tracker settings unavailable")
		return
	}
	_, _, password, supported := trackerCredsView(key, items[key])
	switch req.Field {
	case "credentials":
		// у трекера с формой входа строка открыта в GET, секрет там только пароль
		if supported {
			writeError(w, http.StatusBadRequest, "unknown secret field")
			return
		}
		writeRevealed(w, items[key].Credentials)
	case "password":
		if !supported {
			writeError(w, http.StatusBadRequest, "unknown secret field")
			return
		}
		writeRevealed(w, password)
	default:
		writeError(w, http.StatusBadRequest, "unknown secret field")
	}
}

func (s *Server) handlePutTracker(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !trackerKnown(key) {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, status, err := s.putTrackerLocked(r.Context(), key, body)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	if len(info.confirmed) > 0 {
		// пауза снимается уже без замка трекеров: два замка сразу не держим
		if err = settings.UpdateMirrorSync(r.Context(), s.db, func(d *settings.MirrorSync) {
			if p, ok := d.Paused[key]; ok && slices.ContainsFunc(info.confirmed, func(h string) bool { return settings.MirrorHostKey(h) == settings.MirrorHostKey(p.Host) }) {
				delete(d.Paused, key)
			}
		}); err != nil {
			logger.Warn("tracker " + key + ": clear mirror pause: " + err.Error())
		}
	}
	s.reload()
	if len(info.removed) > 0 {
		writeJSON(w, http.StatusOK, struct {
			RemovedMirrors []string `json:"removed_mirrors"`
		}{info.removed})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putTrackerLocked(ctx context.Context, key string, body []byte) (trackerPatchInfo, int, error) {
	unlock := settings.LockTrackers()
	defer unlock()
	if added, err := s.trackerAdded(ctx, key); err != nil {
		return trackerPatchInfo{}, http.StatusInternalServerError, err
	} else if !added {
		return trackerPatchInfo{}, http.StatusNotFound, errors.New("unknown tracker key")
	}
	cfg, err := s.writableConfig()
	if err != nil {
		return trackerPatchInfo{}, http.StatusServiceUnavailable, errors.New(settingsUnreadableMsg)
	}
	body, info, err := prepareTrackerPatchInfo(cfg, key, body)
	if err != nil {
		return info, http.StatusBadRequest, err
	}
	if err = settings.MergeTrackerKey(ctx, s.db, key, body); err != nil {
		return info, http.StatusBadRequest, err
	}
	return info, 0, nil
}

func (s *Server) handleTestTracker(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !trackerKnown(key) {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	var req struct {
		Config json.RawMessage `json:"config"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	cfg := s.effective()
	// патч проверки идет по тем же правилам, что и сохранение: маска пароля
	// заменяется сохраненным, чужой домен отклоняется
	if len(req.Config) > 0 {
		patched, err := prepareTrackerPatch(cfg, key, req.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.Config = patched
	}
	timeout := 30 * time.Second
	if effectiveTrackerProxyType(cfg, key, req.Config) == 1 {
		timeout = time.Duration(cfg.Flaresolverr.MaxTimeout) * time.Millisecond
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	err := trackerPkg.TestLogin(ctx, cfg, s.db, key, req.Config)
	if errors.Is(err, trackerPkg.ErrUnknownTracker) {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	if err != nil {
		logTestFailure(r, "tracker test failed: "+key, err)
		writeJSON(w, http.StatusOK, testResultResp{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, testResultResp{OK: true})
}

func (s *Server) handlePutTrackersOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Order []string `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	unlock := settings.LockTrackers()
	defer unlock()
	added, err := s.addedTrackerKeys(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	seen := make(map[string]struct{}, len(req.Order))
	for _, key := range req.Order {
		if !slices.Contains(added, key) {
			writeError(w, http.StatusBadRequest, "unknown tracker key: "+key)
			return
		}
		if _, dup := seen[key]; dup {
			writeError(w, http.StatusBadRequest, "duplicate tracker key: "+key)
			return
		}
		seen[key] = struct{}{}
	}
	raw, err := json.Marshal(req.Order)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.db.SaveSetting(r.Context(), sqlite.SettingTrackersOrder, raw); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
