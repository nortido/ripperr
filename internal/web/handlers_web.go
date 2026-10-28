package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"

	"ripperr/internal/settings"
)

type webSettingsResp struct {
	AllowedHosts []string `json:"allowed_hosts"`
	AppURL       string   `json:"app_url"`
	Port         int      `json:"port"`
	PortLocked   bool     `json:"port_locked"`
}

type webPutResp struct {
	Port  int  `json:"port"`
	Moved bool `json:"moved"`
}

func (s *Server) effectiveWeb() webSettingsResp {
	cfg := s.effective()
	var out webSettingsResp
	_ = json.Unmarshal(settings.WebJSON(&cfg), &out)
	out.PortLocked = cfg.Web.PortLocked
	out.AppURL = cfg.Web.AppURL
	return out
}

func (s *Server) boundPortLocked() int {
	if s.ln == nil {
		return 0
	}
	if a, ok := s.ln.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

func (s *Server) handleGetWeb(w http.ResponseWriter, r *http.Request) {
	out := s.effectiveWeb()
	s.lnMu.Lock()
	if p := s.boundPortLocked(); p != 0 {
		out.Port = p
	}
	s.lnMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutWeb(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	overlay, err := settings.WebPatchFromJSON(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var patch struct {
		Port *int `json:"port"`
	}
	_ = json.Unmarshal(body, &patch)

	s.lnMu.Lock()
	defer s.lnMu.Unlock()
	if cur := s.effectiveWeb(); cur.PortLocked && patch.Port != nil {
		port := s.boundPortLocked()
		if port == 0 {
			port = cur.Port
		}
		if *patch.Port != port {
			writeError(w, http.StatusBadRequest, "web port is fixed in Docker: change the host side of -p instead")
			return
		}
	}
	old := s.ln
	var moved net.Listener
	if old != nil && patch.Port != nil && *patch.Port != s.boundPortLocked() {
		moved, err = net.Listen("tcp", net.JoinHostPort(s.lnHost, strconv.Itoa(*patch.Port)))
		if err != nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("port %d is not available: %v", *patch.Port, err))
			return
		}
	}
	appURLBefore := s.effectiveWeb().AppURL
	if err = settings.MergeWeb(r.Context(), s.db, body); err != nil {
		if moved != nil {
			_ = moved.Close()
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// уведомитель собирается при reload из актуального конфига, без reload ссылка осталась бы старой
	if overlay.AppURL != nil && *overlay.AppURL != appURLBefore {
		s.reload()
	}
	if moved == nil {
		port := s.boundPortLocked()
		if port == 0 {
			port = s.effectiveWeb().Port
		}
		writeJSON(w, http.StatusOK, webPutResp{Port: port, Moved: false})
		return
	}
	s.ln = moved
	s.serve(moved)
	writeJSON(w, http.StatusOK, webPutResp{Port: *patch.Port, Moved: true})
	_ = http.NewResponseController(w).Flush()
	_ = old.Close()
}
