package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"trackerr/internal/helper"
)

const cmdDestroySession = "sessions.destroy"

var sessionLocks helper.KeyedMutex

func (f *client) DestroyFlareSession(sessionID string) (err error) {
	if sessionID == "" {
		return
	}
	mu := sessionLocks.Mutex(sessionID)
	mu.Lock()
	defer mu.Unlock()

	var (
		data []byte
		resp *http.Response
		body struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}
	)
	payload := map[string]string{
		"cmd":     cmdDestroySession,
		"session": sessionID,
	}
	if data, err = json.Marshal(payload); err != nil {
		return
	}
	if resp, err = f.hc.Post(f.conf.Flaresolverr.URL, "application/json", bytes.NewBuffer(data)); err != nil {
		return
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return
	}
	if body.Status != "ok" {
		if strings.Contains(body.Message, "doesn't exist") {
			sessionLocks.Delete(sessionID)
			return
		}
		err = fmt.Errorf("flaresolverr sessions.destroy: %s", body.Message)
		return
	}
	sessionLocks.Delete(sessionID)
	return
}
