package downloadstation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
)

type fakeAPI struct {
	Path string
	Min  int
	Max  int
}

type fakeCall struct {
	HTTPMethod string
	Path       string
	API        string
	Method     string
	RawQuery   string
	Query      url.Values
	Form       url.Values
	Parts      []fakePart
}

type fakePart struct {
	Name     string
	FileName string
	Data     []byte
}

func (c fakeCall) param(key string) (string, bool) {
	if v, ok := c.Form[key]; ok {
		return v[0], true
	}
	if v, ok := c.Query[key]; ok {
		return v[0], true
	}
	return "", false
}

type fakeFile struct {
	Name     string
	Size     int64
	Priority string
}

type fakeTask struct {
	Type        string
	Title       string
	Status      string
	Destination string
	CreateTime  int64
	Files       []fakeFile
}

type fakeList struct {
	Title string
	Files []fakeFile
	Data  []byte
}

type fakeShare struct {
	Name string
	Free any
}

type fakeSource struct {
	data   []byte
	code   int
	status int
}

type fakeDS struct {
	t      *testing.T
	server *httptest.Server

	mu        sync.Mutex
	apis      map[string]fakeAPI
	user      string
	pass      string
	authFault int
	sessions  map[string]bool
	nextSID   int
	// столько ближайших вызовов после входа получат 119
	force119 int
	failConn map[string]bool
	// вход отвечает 307 на этот адрес
	redirectAuth string
	tasks        map[string]*fakeTask
	sources      map[string]fakeSource
	calls        []fakeCall
	lists        map[string]*fakeList
	nextList     int
	nextTask     int
	// list_id и task_id приходят массивом из одной строки
	arrayIDs bool
	// метод SYNO.DownloadStation.Task -> код ошибки вместо ответа
	actionFault map[string]int
	shares      []fakeShare
	shareFault  int
	// новая задача не видна в первом списке после создания, Source отвечает 101
	lagNewTask bool
	hidden     map[string]int
}

func newFake(t *testing.T) *fakeDS {
	t.Helper()
	f := &fakeDS{
		t:    t,
		user: "ripperr",
		pass: "s3cr3t-pass",
		apis: map[string]fakeAPI{
			apiAuth:        {Path: "entry.cgi", Max: 7},
			apiTask:        {Path: "DownloadStation/task.cgi", Max: 3},
			apiTask2:       {Path: "entry.cgi", Max: 2},
			apiTaskList:    {Path: "entry.cgi", Max: 2},
			apiTaskPolling: {Path: "entry.cgi", Max: 2},
			apiTaskSource:  {Path: "entry.cgi", Max: 2},
			apiFileList:    {Path: "entry.cgi", Max: 2},
		},
		sessions:    map[string]bool{},
		failConn:    map[string]bool{},
		tasks:       map[string]*fakeTask{},
		sources:     map[string]fakeSource{},
		lists:       map[string]*fakeList{},
		actionFault: map[string]int{},
		hidden:      map[string]int{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func newClient(t *testing.T, f *fakeDS) *Client {
	t.Helper()
	c, err := New(model.ManagerConfig{Host: f.server.URL, Username: f.user, Password: f.pass})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, map[string]any{"success": true, "data": data})
}

func writeFail(w http.ResponseWriter, code int) {
	writeJSON(w, map[string]any{"success": false, "error": map[string]any{"code": code}})
}

func (f *fakeDS) serve(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("parse form: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	api := r.Form.Get("api")
	call := fakeCall{
		HTTPMethod: r.Method, Path: r.URL.Path, API: api, Method: r.Form.Get("method"),
		RawQuery: r.URL.RawQuery, Query: r.URL.Query(), Form: r.PostForm,
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		parts, err := readParts(r)
		if err != nil {
			f.t.Errorf("read multipart: %v", err)
		}
		call.Parts = parts
	}
	f.calls = append(f.calls, call)
	if r.URL.Path == "/webapi/query.cgi" && api == "SYNO.API.Info" {
		f.serveInfo(w, r)
		return
	}
	info, ok := f.apis[api]
	if !ok || r.URL.Path != "/webapi/"+info.Path {
		writeFail(w, 102)
		return
	}
	if v, err := strconv.Atoi(r.Form.Get("version")); err != nil || v < 1 || v > info.Max {
		writeFail(w, 104)
		return
	}
	if api == apiAuth && f.redirectAuth != "" {
		http.Redirect(w, r, f.redirectAuth+r.URL.Path, http.StatusTemporaryRedirect)
		return
	}
	if api == apiAuth {
		f.serveAuth(w, r)
		return
	}
	if f.failConn[api] {
		hj, _ := w.(http.Hijacker)
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	if f.force119 > 0 {
		f.force119--
		writeFail(w, 119)
		return
	}
	if !f.sessions[r.URL.Query().Get("_sid")] {
		writeFail(w, 119)
		return
	}
	switch api + " " + call.Method {
	case apiTask + " list":
		f.serveList(w, r)
	case apiTask + " getinfo":
		f.serveGetinfo(w, r)
	case apiTaskSource + " download":
		f.serveSource(w, r)
	case apiTask2 + " create":
		f.serveCreate(w, call)
	case apiTaskList + " get":
		f.serveListGet(w, r)
	case apiTaskPolling + " download":
		f.servePolling(w, r)
	case apiTask + " pause", apiTask + " resume", apiTask + " delete":
		f.serveAction(w, r, call.Method)
	case apiFileList + " list_share":
		f.serveShares(w)
	default:
		writeFail(w, 103)
	}
}

func (f *fakeDS) serveInfo(w http.ResponseWriter, r *http.Request) {
	if r.Form.Get("method") != "query" || r.Form.Get("version") != "1" {
		writeFail(w, 101)
		return
	}
	out := map[string]any{}
	for _, name := range strings.Split(r.Form.Get("query"), ",") {
		if a, ok := f.apis[name]; ok {
			lo := max(a.Min, 1)
			out[name] = map[string]any{"path": a.Path, "minVersion": lo, "maxVersion": a.Max}
		}
	}
	writeOK(w, out)
}

func (f *fakeDS) serveAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.PostForm.Get("method") != "login" {
		writeFail(w, 101)
		return
	}
	if f.authFault != 0 {
		writeFail(w, f.authFault)
		return
	}
	if r.PostForm.Get("account") != f.user || r.PostForm.Get("passwd") != f.pass {
		writeFail(w, 400)
		return
	}
	f.nextSID++
	sid := fmt.Sprintf("sid-%d", f.nextSID)
	f.sessions[sid] = true
	writeOK(w, map[string]any{"sid": sid})
}

func (f *fakeDS) taskJSON(id string, tk *fakeTask, additional string) map[string]any {
	out := map[string]any{"id": id, "type": tk.Type, "title": tk.Title, "status": tk.Status, "username": f.user, "size": 100}
	add := map[string]any{}
	if strings.Contains(additional, "detail") {
		add["detail"] = map[string]any{"destination": tk.Destination, "create_time": tk.CreateTime, "uri": tk.Title, "priority": "auto"}
	}
	if strings.Contains(additional, "file") {
		files := []map[string]any{}
		for _, fl := range tk.Files {
			files = append(files, map[string]any{"filename": fl.Name, "size": fl.Size, "size_downloaded": 0, "priority": fl.Priority})
		}
		add["file"] = files
	}
	if len(add) > 0 {
		out["additional"] = add
	}
	return out
}

func (f *fakeDS) sortedIDs() []string {
	ids := make([]string, 0, len(f.tasks))
	for id := range f.tasks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func (f *fakeDS) serveList(w http.ResponseWriter, r *http.Request) {
	ids := f.sortedIDs()
	if r.Form.Get("limit") == "1" && len(ids) > 1 {
		ids = ids[:1]
	}
	tasks := []map[string]any{}
	for _, id := range ids {
		if f.hidden[id] > 0 {
			f.hidden[id]--
			continue
		}
		tasks = append(tasks, f.taskJSON(id, f.tasks[id], r.Form.Get("additional")))
	}
	writeOK(w, map[string]any{"offset": 0, "total": len(f.tasks), "tasks": tasks})
}

func (f *fakeDS) serveGetinfo(w http.ResponseWriter, r *http.Request) {
	tasks := []map[string]any{}
	for _, id := range strings.Split(r.Form.Get("id"), ",") {
		if tk, ok := f.tasks[id]; ok {
			tasks = append(tasks, f.taskJSON(id, tk, r.Form.Get("additional")))
		}
	}
	writeOK(w, map[string]any{"tasks": tasks})
}

func (f *fakeDS) serveSource(w http.ResponseWriter, r *http.Request) {
	var id string
	if err := json.Unmarshal([]byte(r.Form.Get("id")), &id); err != nil {
		writeFail(w, 101)
		return
	}
	src, ok := f.sources[id]
	switch {
	case !ok:
		writeFail(w, 101)
	case src.status != 0:
		w.WriteHeader(src.status)
	case src.code != 0:
		writeFail(w, src.code)
	default:
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(src.data)
	}
}

func (f *fakeDS) lock(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeDS) callsTo(api, method string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.API == api && c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeDS) sourceCalls(id string) int {
	n := 0
	for _, c := range f.callsTo(apiTaskSource, "download") {
		if c.Query.Get("id") == `"`+id+`"` {
			n++
		}
	}
	return n
}

func (f *fakeDS) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeDS) addTask(t *testing.T, id string, tk *fakeTask, comment string, files ...string) string {
	t.Helper()
	data, hash := buildTorrent(t, tk.Title, comment, files...)
	f.lock(func() {
		f.tasks[id] = tk
		f.sources[id] = fakeSource{data: data}
	})
	return hash
}

func buildTorrent(t *testing.T, name, comment string, files ...string) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{PieceLength: 16384, Pieces: make([]byte, 20), Name: name}
	for _, p := range files {
		info.Files = append(info.Files, metainfo.FileInfo{Path: strings.Split(p, "/"), Length: 1})
	}
	if len(files) == 0 {
		info.Length = 1
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes, Announce: "http://tracker.example/announce", Comment: comment}
	var buf bytes.Buffer
	if err = mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes(), mi.HashInfoBytes().HexString()
}

func readParts(r *http.Request) ([]fakePart, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	var parts []fakePart
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return parts, nil
		}
		if err != nil {
			return parts, err
		}
		data, err := io.ReadAll(p)
		if err != nil {
			return parts, err
		}
		parts = append(parts, fakePart{Name: p.FormName(), FileName: p.FileName(), Data: data})
	}
}

func (f *fakeDS) idValue(id string) any {
	if f.arrayIDs {
		return []string{id}
	}
	return id
}

func (f *fakeDS) serveCreate(w http.ResponseWriter, call fakeCall) {
	var data []byte
	for _, p := range call.Parts {
		if p.Name == "torrent" && p.FileName != "" {
			data = p.Data
		}
	}
	paths, sizes, err := torrentmeta.FileSizes(data)
	if call.HTTPMethod != http.MethodPost || err != nil {
		writeFail(w, 101)
		return
	}
	title := paths[0]
	if i := strings.Index(title, "/"); i > 0 {
		title = title[:i]
	}
	l := &fakeList{Title: title, Data: data}
	for i, p := range paths {
		l.Files = append(l.Files, fakeFile{Name: p, Size: sizes[i]})
	}
	f.nextList++
	id := fmt.Sprintf("list_%d", f.nextList)
	f.lists[id] = l
	writeOK(w, map[string]any{"list_id": f.idValue(id)})
}

func (f *fakeDS) serveListGet(w http.ResponseWriter, r *http.Request) {
	var id string
	var l *fakeList
	if json.Unmarshal([]byte(r.Form.Get("list_id")), &id) == nil {
		l = f.lists[id]
	}
	if l == nil {
		writeFail(w, 101)
		return
	}
	// обратный порядок: клиент обязан слать index файла, а не его позицию в ответе
	files := []map[string]any{}
	for i := len(l.Files) - 1; i >= 0; i-- {
		files = append(files, map[string]any{"index": i, "name": l.Files[i].Name, "size": l.Files[i].Size})
	}
	writeOK(w, map[string]any{"title": l.Title, "type": "bt", "files": files})
}

func (f *fakeDS) servePolling(w http.ResponseWriter, r *http.Request) {
	var id, dest string
	var idx []int
	var l *fakeList
	if json.Unmarshal([]byte(r.Form.Get("list_id")), &id) == nil {
		l = f.lists[id]
	}
	if l == nil || json.Unmarshal([]byte(r.Form.Get("file_indexes")), &idx) != nil {
		writeFail(w, 101)
		return
	}
	if d := r.Form.Get("destination"); d != "" && json.Unmarshal([]byte(d), &dest) != nil {
		writeFail(w, 101)
		return
	}
	tk := &fakeTask{Type: "bt", Title: l.Title, Status: "waiting", Destination: dest}
	for i, fl := range l.Files {
		fl.Priority = prioritySkip
		if slices.Contains(idx, i) {
			fl.Priority = "normal"
		}
		tk.Files = append(tk.Files, fl)
	}
	delete(f.lists, id)
	f.nextTask++
	taskID := fmt.Sprintf("dbid_%d", 100+f.nextTask)
	f.tasks[taskID] = tk
	f.sources[taskID] = fakeSource{data: l.Data}
	if f.lagNewTask {
		f.hidden[taskID] = 1
		f.sources[taskID] = fakeSource{code: 101}
	}
	writeOK(w, map[string]any{"task_id": f.idValue(taskID)})
}

func (f *fakeDS) serveAction(w http.ResponseWriter, r *http.Request, method string) {
	if code := f.actionFault[method]; code != 0 {
		writeFail(w, code)
		return
	}
	if method == "delete" && r.Form.Get("force_complete") != "false" {
		f.t.Errorf("delete force_complete = %q, want false", r.Form.Get("force_complete"))
	}
	out := []map[string]any{}
	for _, id := range strings.Split(r.Form.Get("id"), ",") {
		tk, ok := f.tasks[id]
		if !ok {
			out = append(out, map[string]any{"id": id, "error": 404})
			continue
		}
		switch method {
		case "pause":
			tk.Status = statusPaused
		case "resume":
			tk.Status = "downloading"
		case "delete":
			delete(f.tasks, id)
			delete(f.sources, id)
		}
		out = append(out, map[string]any{"id": id, "error": 0})
	}
	writeOK(w, out)
}

func (f *fakeDS) serveShares(w http.ResponseWriter) {
	if f.shareFault != 0 {
		writeFail(w, f.shareFault)
		return
	}
	shares := []map[string]any{}
	for _, s := range f.shares {
		shares = append(shares, map[string]any{
			"isdir": true, "name": s.Name, "path": "/" + s.Name,
			"additional": map[string]any{"volume_status": map[string]any{"freespace": s.Free, "totalspace": 1 << 40, "readonly": false}},
		})
	}
	writeOK(w, map[string]any{"offset": 0, "total": len(shares), "shares": shares})
}

func newClientConfig(t *testing.T, f *fakeDS, cfg model.ManagerConfig) *Client {
	t.Helper()
	cfg.Host, cfg.Username, cfg.Password = f.server.URL, f.user, f.pass
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}
