package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"ripperr/internal/system/logger"
	"ripperr/internal/webpush"
)

const pushNeedsHTTPS = "web push needs the HTTPS address of Ripperr"

func pushDeviceDTO(d webpush.Device) pushDeviceResp {
	return pushDeviceResp{ID: d.ID, Label: d.Label, CreatedAt: d.CreatedAt}
}

func (s *Server) pushStorageError(w http.ResponseWriter, err error) {
	logger.Error(fmt.Sprintf("web push: storage: %v", err))
	writeError(w, http.StatusInternalServerError, "push storage error")
}

func pushOrigin(r *http.Request, fromBody string) (string, bool) {
	// withAuth пропускает пустой Origin, поэтому https проверяем здесь.
	// Прокси keenetic срезает Origin и Referer, тогда берем адрес, который прислала сама страница
	src := r.Header.Get("Origin")
	if src == "" {
		src = r.Header.Get("Referer")
	}
	if src == "" {
		src = fromBody
	}
	u, err := url.Parse(src)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	// Subscribe не принимает путь, режем до scheme://host
	return "https://" + u.Host, true
}

func originOnly(v string) string {
	if v == "" {
		return "-"
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "?"
	}
	return u.Scheme + "://" + u.Host
}

func (s *Server) handleGetPush(w http.ResponseWriter, r *http.Request) {
	store := webpush.NewStore(s.db)
	key, err := store.PublicKey(r.Context())
	if err != nil {
		s.pushStorageError(w, err)
		return
	}
	_, devs, err := store.Load(r.Context())
	if err != nil {
		s.pushStorageError(w, err)
		return
	}
	resp := pushStateResp{PublicKey: key, Devices: make([]pushDeviceResp, 0, len(devs))}
	for _, d := range devs {
		resp.Devices = append(resp.Devices, pushDeviceDTO(d))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePostPushDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		webpush.Subscription
		Origin string `json:"origin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid subscription JSON")
		return
	}
	origin, ok := pushOrigin(r, body.Origin)
	if !ok {
		logger.Warn(fmt.Sprintf("web push: subscribe refused, page is not https: origin %s, referer %s, page %s",
			originOnly(r.Header.Get("Origin")), originOnly(r.Header.Get("Referer")), originOnly(body.Origin)))
		writeError(w, http.StatusBadRequest, pushNeedsHTTPS)
		return
	}
	dev, err := webpush.NewStore(s.db).Subscribe(r.Context(), body.Subscription, webpush.Label(r.UserAgent()), origin, time.Now())
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, pushDeviceDTO(dev))
	case errors.Is(err, webpush.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, webpush.ErrTooManyDevices):
		writeError(w, http.StatusConflict, fmt.Sprintf("device limit reached (%d): remove a device first", webpush.MaxDevices))
	case errors.Is(err, webpush.ErrNoKeys):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.pushStorageError(w, err)
	}
}

func (s *Server) handleDeletePushDevice(w http.ResponseWriter, r *http.Request) {
	if _, err := webpush.NewStore(s.db).Remove(r.Context(), r.PathValue("id")); err != nil {
		s.pushStorageError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
