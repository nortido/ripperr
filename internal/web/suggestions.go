package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"ripperr/internal/settings"
)

const errMirrorNotUnconfirmed = "mirror is not unconfirmed"

func decodeStrict(r io.Reader, dst any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after JSON body")
	}
	return nil
}

func (s *Server) handleDismissSuggestion(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !trackerKnown(key) {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	var req struct {
		Kind string `json:"kind"`
		Host string `json:"host"`
	}
	if err := decodeStrict(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Kind != settings.MirrorKindRemove && req.Kind != settings.MirrorKindMain {
		writeError(w, http.StatusBadRequest, "invalid suggestion kind")
		return
	}
	if !settings.ValidMirrorHost(req.Host) {
		writeError(w, http.StatusBadRequest, "invalid mirror host")
		return
	}
	if added, err := s.trackerAdded(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !added {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	var found bool
	if err := settings.UpdateMirrorSync(r.Context(), s.db, func(d *settings.MirrorSync) {
		found = d.Dismiss(key, req.Kind, req.Host)
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "suggestion not found")
		return
	}
	// без reload: отказ меняет только mirror_sync, конфиг трекеров тот же
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConfirmMirror(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !trackerKnown(key) {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	var req struct {
		Host string `json:"host"`
	}
	if err := decodeStrict(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !settings.ValidMirrorHost(req.Host) {
		writeError(w, http.StatusBadRequest, "invalid mirror host")
		return
	}
	notFound, err := s.confirmTrackerMirror(r.Context(), key, req.Host)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if notFound != "" {
		writeError(w, http.StatusNotFound, notFound)
		return
	}
	// пауза снимается уже без замка трекеров: два замка сразу не держим
	want := settings.MirrorHostKey(req.Host)
	if err = settings.UpdateMirrorSync(r.Context(), s.db, func(d *settings.MirrorSync) {
		if p, ok := d.Paused[key]; ok && settings.MirrorHostKey(p.Host) == want {
			delete(d.Paused, key)
		}
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) confirmTrackerMirror(ctx context.Context, key, host string) (notFound string, err error) {
	unlock := settings.LockTrackers()
	defer unlock()
	added, err := s.trackerAdded(ctx, key)
	if err != nil {
		return "", err
	}
	if !added {
		return "unknown tracker key", nil
	}
	cfg := s.effective()
	items, err := settings.TrackersEffective(ctx, &cfg, s.db)
	if err != nil {
		return "", err
	}
	want := settings.MirrorHostKey(host)
	if !slices.ContainsFunc(items[key].Mirrors, func(h string) bool { return settings.MirrorHostKey(h) == want }) {
		return errMirrorNotUnconfirmed, nil
	}
	ok, err := settings.ConfirmTrackerMirror(ctx, s.db, key, host)
	if err != nil {
		return "", err
	}
	if !ok {
		return errMirrorNotUnconfirmed, nil
	}
	return "", nil
}
