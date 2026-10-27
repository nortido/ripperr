package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"ripperr/internal/model/storage"
)

func categoryColorLog(pairs ...string) []storage.CheckLogEntry {
	var out []storage.CheckLogEntry
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, storage.CheckLogEntry{
			ID: int64(i/2 + 1), CheckedAt: time.Now(), URLHash: "h" + pairs[i], ItemName: pairs[i],
			Status: "unchanged", Category: pairs[i+1],
		})
	}
	return out
}

func historyItemsErr(srv *Server, target string) (int, []map[string]any, error) {
	w := httptest.NewRecorder()
	srv.handleHistory(w, httptest.NewRequest(http.MethodGet, target, nil))
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		return w.Code, nil, err
	}
	return w.Code, resp.Items, nil
}

func historyItems(t *testing.T, srv *Server, target string) (int, []map[string]any) {
	t.Helper()
	code, items, err := historyItemsErr(srv, target)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return code, items
}

func itemColors(items []map[string]any) map[string]any {
	out := make(map[string]any, len(items))
	for _, it := range items {
		out[it["name"].(string)] = it["category_color"]
	}
	return out
}

func storedColors(t *testing.T, store *fakeStore) map[string]int {
	t.Helper()
	raw, ok := store.settings["category_colors"]
	if !ok {
		t.Fatal("category_colors not stored")
	}
	var doc map[string]int
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("stored doc %s: %v", raw, err)
	}
	return doc
}

func sameColors(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func TestHistoryCategoryColorAssign(t *testing.T) {
	store := &fakeStore{checkLog: categoryColorLog("a", "movies", "b", "tv", "c", "", "d", " movies ")}
	srv := newTestServer(store)
	code, items := historyItems(t, srv, "/api/history")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	got := itemColors(items)
	if got["a"] != float64(0) || got["b"] != float64(1) || got["d"] != float64(0) {
		t.Fatalf("colors = %v, want a=0 b=1 d=0", got)
	}
	for _, it := range items {
		if it["name"] == "c" {
			if _, has := it["category_color"]; has {
				t.Fatalf("empty category has category_color key: %v", it)
			}
		}
	}
	if doc := storedColors(t, store); !sameColors(doc, map[string]int{"movies": 0, "tv": 1}) {
		t.Fatalf("stored = %v", doc)
	}
}

func TestHistoryCategoryColorKeepsStored(t *testing.T) {
	store := &fakeStore{
		checkLog: categoryColorLog("a", "movies", "b", "tv"),
		settings: map[string]json.RawMessage{"category_colors": json.RawMessage(`{"old":0,"tv":2}`)},
	}
	counting := &saveCountStore{fakeStore: store}
	srv := newTestServer(store)
	srv.db = counting
	_, items := historyItems(t, srv, "/api/history")
	got := itemColors(items)
	if got["b"] != float64(2) || got["a"] != float64(1) {
		t.Fatalf("colors = %v, want tv=2 movies=1", got)
	}
	if doc := storedColors(t, store); !sameColors(doc, map[string]int{"old": 0, "tv": 2, "movies": 1}) {
		t.Fatalf("stored = %v", doc)
	}
	counting.saves = 0
	_, items = historyItems(t, srv, "/api/history")
	got = itemColors(items)
	if got["b"] != float64(2) || got["a"] != float64(1) {
		t.Fatalf("second colors = %v", got)
	}
	if counting.saves != 0 {
		t.Fatalf("saves = %d, want 0", counting.saves)
	}
}

func TestHistoryCategoryColorBrokenDoc(t *testing.T) {
	for _, doc := range []string{`{bad`, `null`, `[1,2]`} {
		t.Run(doc, func(t *testing.T) {
			store := &fakeStore{
				checkLog: categoryColorLog("a", "movies"),
				settings: map[string]json.RawMessage{"category_colors": json.RawMessage(doc)},
			}
			srv := newTestServer(store)
			code, items := historyItems(t, srv, "/api/history")
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			if got := itemColors(items); got["a"] != float64(0) {
				t.Fatalf("colors = %v, want a=0", got)
			}
			if stored := storedColors(t, store); !sameColors(stored, map[string]int{"movies": 0}) {
				t.Fatalf("stored = %v", stored)
			}
		})
	}
}

type colorReadErrStore struct {
	*fakeStore
}

func (c colorReadErrStore) GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error) {
	if key == "category_colors" {
		return nil, false, errors.New("db busy")
	}
	return c.fakeStore.GetSetting(ctx, key)
}

func TestHistoryCategoryColorReadError(t *testing.T) {
	store := &fakeStore{checkLog: categoryColorLog("a", "movies")}
	srv := newTestServer(store)
	srv.db = colorReadErrStore{store}
	code, items := historyItems(t, srv, "/api/history")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	if _, has := items[0]["category_color"]; has {
		t.Fatalf("category_color present on read error: %v", items[0])
	}
	if _, has := store.settings["category_colors"]; has {
		t.Fatal("document written after read error")
	}
}

type colorGateStore struct {
	lockedSettingStore
	gateMu  sync.Mutex
	arrived int
}

func (g *colorGateStore) GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error) {
	if key != "category_colors" {
		return g.lockedSettingStore.GetSetting(ctx, key)
	}
	raw, ok, err := g.lockedSettingStore.GetSetting(ctx, key)
	g.gateMu.Lock()
	g.arrived++
	g.gateMu.Unlock()
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		g.gateMu.Lock()
		n := g.arrived
		g.gateMu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	return raw, ok, err
}

func TestHistoryCategoryColorConcurrent(t *testing.T) {
	store := &colorGateStore{}
	store.checkLog = categoryColorLog("alpha", "movies", "beta", "tv")
	srv := newTestServer(&store.fakeStore)
	srv.db = store
	var wg sync.WaitGroup
	res := make([]map[string]any, 2)
	for i, name := range []string{"alpha", "beta"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, items, err := historyItemsErr(srv, "/api/history?name="+name)
			if err != nil {
				t.Errorf("decode: %v", err)
				return
			}
			res[i] = itemColors(items)
		}()
	}
	wg.Wait()
	a, b := res[0]["alpha"], res[1]["beta"]
	if a == nil || b == nil || a == b {
		t.Fatalf("indices alpha=%v beta=%v, want two different", a, b)
	}
	doc := storedColors(t, &store.fakeStore)
	if float64(doc["movies"]) != a || float64(doc["tv"]) != b {
		t.Fatalf("stored = %v, returned alpha=%v beta=%v", doc, a, b)
	}
}
