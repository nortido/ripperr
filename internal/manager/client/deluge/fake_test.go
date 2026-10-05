package deluge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
)

type fakeCall struct {
	Method string
	Params []json.RawMessage
}

type fakeFault struct {
	code int
	msg  string
}

type fakeFile struct {
	Index int
	Path  string
}

type fakeTorrent struct {
	Name      string
	Comment   string
	SavePath  string
	Label     string
	State     string
	Progress  float64
	TimeAdded float64
	Files     []fakeFile
	Prios     []int
	// очередь состояний, каждое чтение state забирает следующее
	script []string
}

type fakeDeluge struct {
	t      *testing.T
	server *httptest.Server

	mu          sync.Mutex
	password    string
	sessions    map[string]bool
	nextSession int
	connected   bool
	hosts       []string
	// ids сравниваются с учетом регистра, как в демоне
	torrents map[string]*fakeTorrent
	labels   map[string]bool
	calls    []fakeCall
	failHTTP map[string]bool
	hook     func(method string, params []json.RawMessage) (result any, fault *fakeFault, handled bool)
	// add_torrent_* отвечает успехом, но торрент не появляется
	dropAdds bool
	// set_torrent_options молча игнорирует file_priorities
	ignorePriorities bool
	resumeTo         string
	afterRecheck     string
}

func newFake(t *testing.T) *fakeDeluge {
	t.Helper()
	f := &fakeDeluge{
		t:            t,
		password:     "secret",
		sessions:     map[string]bool{},
		connected:    true,
		hosts:        []string{"host1"},
		torrents:     map[string]*fakeTorrent{},
		failHTTP:     map[string]bool{},
		resumeTo:     "Downloading",
		afterRecheck: "Paused",
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func newClient(t *testing.T, f *fakeDeluge, cfg model.ManagerConfig) *Client {
	t.Helper()
	cfg.Host = f.server.URL
	if cfg.Password == "" {
		cfg.Password = f.password
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func (f *fakeDeluge) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/json" || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		f.t.Errorf("bad request %s %s %q", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var req struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
		ID     json.RawMessage   `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("decode request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.Params == nil {
		f.t.Errorf("%s: params must be a JSON array", req.Method)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{Method: req.Method, Params: req.Params})
	if f.failHTTP[req.Method] {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	result, fault := f.handle(w, r, req.Method, req.Params)
	resp := map[string]any{"result": result, "error": nil, "id": req.ID}
	if fault != nil {
		resp["result"] = nil
		resp["error"] = map[string]any{"message": fault.msg, "code": fault.code}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func arg[T any](t *testing.T, params []json.RawMessage, i int) T {
	t.Helper()
	var v T
	if i >= len(params) {
		t.Errorf("missing param %d", i)
		return v
	}
	if err := json.Unmarshal(params[i], &v); err != nil {
		t.Errorf("param %d %s: %v", i, params[i], err)
	}
	return v
}

func (f *fakeDeluge) handle(w http.ResponseWriter, r *http.Request, method string, params []json.RawMessage) (any, *fakeFault) {
	if method == "auth.login" {
		if arg[string](f.t, params, 0) != f.password {
			return false, nil
		}
		f.nextSession++
		id := fmt.Sprintf("s%d", f.nextSession)
		f.sessions[id] = true
		http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: id, Path: "/json"})
		return true, nil
	}
	if ck, err := r.Cookie("_session_id"); err != nil || !f.sessions[ck.Value] {
		return nil, &fakeFault{1, "Not authenticated"}
	}
	if f.hook != nil {
		if res, fault, ok := f.hook(method, params); ok {
			return res, fault
		}
	}
	switch method {
	case "web.connected":
		return f.connected, nil
	case "web.get_hosts":
		hosts := [][]any{}
		for _, h := range f.hosts {
			hosts = append(hosts, []any{h, "127.0.0.1", 58846, "localclient"})
		}
		return hosts, nil
	case "web.connect":
		if !slices.Contains(f.hosts, arg[string](f.t, params, 0)) {
			return nil, &fakeFault{3, "Exception: unknown host"}
		}
		f.connected = true
		return []string{"core.get_torrent_status"}, nil
	}
	daemon := strings.HasPrefix(method, "core.") || strings.HasPrefix(method, "label.")
	if daemon && !f.connected {
		return nil, &fakeFault{2, "Unknown method"}
	}
	if strings.HasPrefix(method, "label.") && f.labels == nil {
		return nil, &fakeFault{2, "Unknown method"}
	}
	switch method {
	case "core.get_torrents_status":
		keys := arg[[]string](f.t, params, 1)
		out := map[string]any{}
		for id, tor := range f.torrents {
			out[id] = f.statusOf(id, tor, keys)
		}
		return out, nil
	case "core.get_torrent_status":
		id := arg[string](f.t, params, 0)
		tor, ok := f.torrents[id]
		if !ok {
			return map[string]any{}, nil
		}
		return f.statusOf(id, tor, arg[[]string](f.t, params, 1)), nil
	case "core.add_torrent_file":
		return f.addFile(params)
	case "core.add_torrent_magnet":
		hash, ok := torrentmeta.BtihFromMagnet(arg[string](f.t, params, 0))
		if !ok {
			return nil, &fakeFault{3, "AddTorrentError: bad magnet"}
		}
		return f.addTorrent(hash, &fakeTorrent{Name: hash}, arg[map[string]any](f.t, params, 1))
	case "core.remove_torrent":
		id := arg[string](f.t, params, 0)
		if _, ok := f.torrents[id]; !ok {
			return nil, &fakeFault{3, "InvalidTorrentError: torrent_id not in session"}
		}
		delete(f.torrents, id)
		return true, nil
	case "core.resume_torrent":
		tor, ok := f.torrents[arg[string](f.t, params, 0)]
		if !ok {
			return nil, &fakeFault{3, "InvalidTorrentError: torrent_id not in session"}
		}
		tor.State = f.resumeTo
		return nil, nil
	case "core.force_recheck":
		for _, id := range arg[[]string](f.t, params, 0) {
			if tor, ok := f.torrents[id]; ok {
				tor.script = []string{"Checking", "Checking", f.afterRecheck}
			}
		}
		return nil, nil
	case "core.set_torrent_options":
		opts := arg[struct {
			FilePriorities []int `json:"file_priorities"`
		}](f.t, params, 1)
		for _, id := range arg[[]string](f.t, params, 0) {
			if tor, ok := f.torrents[id]; ok && !f.ignorePriorities {
				tor.Prios = opts.FilePriorities
			}
		}
		return nil, nil
	case "label.get_labels":
		out := []string{}
		for l := range f.labels {
			out = append(out, l)
		}
		return out, nil
	case "label.add":
		l := strings.ToLower(arg[string](f.t, params, 0))
		if f.labels[l] {
			return nil, &fakeFault{3, "Exception: Label already exists"}
		}
		f.labels[l] = true
		return nil, nil
	case "label.set_torrent":
		id, l := arg[string](f.t, params, 0), arg[string](f.t, params, 1)
		if !f.labels[l] {
			return nil, &fakeFault{3, "Exception: Unknown Label"}
		}
		if tor, ok := f.torrents[id]; ok {
			tor.Label = l
		}
		return nil, nil
	}
	f.t.Errorf("unexpected method %s", method)
	return nil, &fakeFault{2, "Unknown method"}
}

func (f *fakeDeluge) addFile(params []json.RawMessage) (any, *fakeFault) {
	data, err := base64.StdEncoding.DecodeString(arg[string](f.t, params, 1))
	if err != nil {
		return nil, &fakeFault{3, "Exception: bad base64"}
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, &fakeFault{3, "Exception: bad torrent"}
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, &fakeFault{3, "Exception: bad info"}
	}
	tor := &fakeTorrent{Name: info.Name, Comment: mi.Comment}
	if len(info.Files) == 0 {
		tor.Files = []fakeFile{{Index: 0, Path: info.Name}}
	}
	for i, fi := range info.Files {
		tor.Files = append(tor.Files, fakeFile{Index: i, Path: info.Name + "/" + strings.Join(fi.Path, "/")})
	}
	tor.Prios = make([]int, len(tor.Files))
	for i := range tor.Prios {
		tor.Prios[i] = 4
	}
	return f.addTorrent(mi.HashInfoBytes().HexString(), tor, arg[map[string]any](f.t, params, 2))
}

func (f *fakeDeluge) addTorrent(id string, tor *fakeTorrent, opts map[string]any) (any, *fakeFault) {
	if _, ok := f.torrents[id]; ok {
		return nil, &fakeFault{3, fmt.Sprintf("AddTorrentError: Torrent already in session (%s).", id)}
	}
	if f.dropAdds {
		return id, nil
	}
	tor.State = "Downloading"
	if paused, _ := opts["add_paused"].(bool); paused {
		tor.State = "Paused"
	}
	tor.SavePath = "/downloads"
	if dir, ok := opts["download_location"].(string); ok {
		tor.SavePath = dir
	}
	f.torrents[id] = tor
	return id, nil
}

func (f *fakeDeluge) statusOf(id string, tor *fakeTorrent, keys []string) map[string]any {
	if len(tor.script) > 0 && slices.Contains(keys, "state") {
		tor.State, tor.script = tor.script[0], tor.script[1:]
	}
	files := make([]map[string]any, 0, len(tor.Files))
	for _, fl := range tor.Files {
		files = append(files, map[string]any{"index": fl.Index, "path": fl.Path, "size": 1, "offset": 0})
	}
	prios := tor.Prios
	if prios == nil {
		prios = []int{}
	}
	all := map[string]any{
		"hash": id, "name": tor.Name, "comment": tor.Comment, "save_path": tor.SavePath,
		"time_added": tor.TimeAdded, "state": tor.State, "progress": tor.Progress,
		"is_finished": tor.Progress >= 100, "files": files, "file_priorities": prios,
	}
	if f.labels != nil {
		all["label"] = tor.Label
	}
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := all[k]; ok {
			out[k] = v
		}
	}
	return out
}

func (f *fakeDeluge) put(id string, tor *fakeTorrent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.torrents[id] = tor
}

func (f *fakeDeluge) lock(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeDeluge) callsTo(method string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeDeluge) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeDeluge) methodsSince(n int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls[n:] {
		out = append(out, c.Method)
	}
	return out
}

func (f *fakeDeluge) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.torrents[id]
	return ok
}

func buildTorrent(t *testing.T, name string, files ...string) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{PieceLength: 16384, Pieces: make([]byte, 20), Name: name}
	for _, p := range files {
		info.Files = append(info.Files, metainfo.FileInfo{Path: strings.Split(p, "/"), Length: 1})
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes, Announce: "http://tracker.example/announce"}
	var buf bytes.Buffer
	if err = mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes(), mi.HashInfoBytes().HexString()
}
