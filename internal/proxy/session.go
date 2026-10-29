package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const cmdDestroySession = "sessions.destroy"

type gateEntry struct {
	ch   chan struct{}
	refs int
}

type sessionGate struct {
	mu sync.Mutex
	m  map[string]*gateEntry
}

var sessionLocks sessionGate

// FlareSolverr обслуживает 4 запроса разом и делит их с Prowlarr; Chrome на каждый запрос тяжелый,
// поэтому все трекеры вместе держат не больше одного
var flareSlots = make(chan struct{}, 1)

func acquireFlareSlot(ctx context.Context) (release func(), err error) {
	select {
	case flareSlots <- struct{}{}:
		return func() { <-flareSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *sessionGate) lock(ctx context.Context, id string) (unlock func(), err error) {
	// KeyedMutex после Delete отдает новичку свежий мьютекс рядом с ожидающим старого, поэтому свой счетчик ссылок
	g.mu.Lock()
	if g.m == nil {
		g.m = map[string]*gateEntry{}
	}
	e := g.m[id]
	if e == nil {
		e = &gateEntry{ch: make(chan struct{}, 1)}
		g.m[id] = e
	}
	e.refs++
	g.mu.Unlock()

	select {
	case e.ch <- struct{}{}:
		return func() {
			<-e.ch
			g.release(id, e)
		}, nil
	case <-ctx.Done():
		g.release(id, e)
		return nil, ctx.Err()
	}
}

func (g *sessionGate) release(id string, e *gateEntry) {
	g.mu.Lock()
	e.refs--
	if e.refs == 0 {
		delete(g.m, id)
	}
	g.mu.Unlock()
}

func (f *client) DestroyFlareSession(ctx context.Context, sessionID string) (err error) {
	if sessionID == "" {
		return
	}
	unlock, err := sessionLocks.lock(ctx, sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	release, err := acquireFlareSlot(ctx)
	if err != nil {
		return err
	}
	defer release()

	var (
		data []byte
		resp *http.Response
		req  *http.Request
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
	if req, err = http.NewRequestWithContext(ctx, http.MethodPost, f.conf.Flaresolverr.URL, bytes.NewReader(data)); err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err = f.hc.Do(req); err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return
	}
	if body.Status != "ok" {
		if strings.Contains(body.Message, "doesn't exist") {
			return
		}
		err = fmt.Errorf("flaresolverr sessions.destroy: %s", body.Message)
		return
	}
	return
}
