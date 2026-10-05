package web

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"ripperr/internal/metrics"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	if cfg.Metrics.Token == "" {
		http.NotFound(w, r)
		return
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	// сравнение за константное время, чтобы токен нельзя было подобрать по задержке ответа
	if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(cfg.Metrics.Token)) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	open := make(map[string]struct{})
	if s.alerts != nil {
		for _, inc := range s.alerts.List() {
			open[inc.Tracker] = struct{}{}
		}
	}
	circuit := metrics.Family{
		Name:       "ripperr_tracker_circuit_open",
		Help:       "1 when the tracker circuit breaker is open",
		Type:       "gauge",
		LabelNames: []string{"tracker"},
	}
	for key, it := range cfg.Tracker {
		if it == nil || !it.Enabled {
			continue
		}
		// предохранитель открывает тревогу под именем трекера (config Name), оно может отличаться от ключа
		var v uint64
		_, byKey := open[key]
		_, byName := open[it.Name]
		if byKey || byName {
			v = 1
		}
		circuit.Samples = append(circuit.Samples, metrics.Sample{Values: []string{key}, Value: v})
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_ = metrics.WriteText(w, []metrics.Family{metrics.Checks.Family(), metrics.Notifications.Family(), circuit})
}
