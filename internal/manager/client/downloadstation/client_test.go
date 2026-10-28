package downloadstation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ripperr/internal/model"
)

const topicURL = "https://rutracker.org/forum/viewtopic.php?t=123456"

func TestNewLogsIn(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)

	info := f.callsTo("SYNO.API.Info", "query")
	if len(info) != 1 || info[0].HTTPMethod != "GET" {
		t.Fatalf("discovery calls = %+v, want one GET", info)
	}
	for _, api := range []string{apiAuth, apiTask, apiTask2, apiTaskList, apiTaskPolling, apiTaskSource, apiFileList} {
		if !strings.Contains(info[0].Query.Get("query"), api) {
			t.Errorf("discovery query %q lacks %s", info[0].Query.Get("query"), api)
		}
	}
	logins := f.callsTo(apiAuth, "login")
	if len(logins) != 1 {
		t.Fatalf("login calls = %d, want 1", len(logins))
	}
	l := logins[0]
	if l.HTTPMethod != "POST" {
		t.Errorf("login method = %s, want POST", l.HTTPMethod)
	}
	want := map[string]string{"version": "6", "session": "DownloadStation", "format": "sid", "account": f.user, "passwd": f.pass}
	for k, v := range want {
		if l.Form.Get(k) != v {
			t.Errorf("login form %s = %q, want %q", k, l.Form.Get(k), v)
		}
	}
	if strings.Contains(l.RawQuery, "passwd") || strings.Contains(l.RawQuery, f.pass) || l.Form.Has("otp_code") {
		t.Errorf("login query %q carries the password or otp_code", l.RawQuery)
	}

	f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "A", Status: "seeding"}, topicURL, "a")
	if _, err := c.List(context.Background(), model.FilterOptions{}); err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, api := range []string{apiTask, apiTaskSource} {
		calls := f.callsTo(api, map[string]string{apiTask: "list", apiTaskSource: "download"}[api])
		if len(calls) == 0 {
			t.Fatalf("no %s calls", api)
		}
		for _, call := range calls {
			if call.Query.Get("_sid") != "sid-1" {
				t.Errorf("%s %s _sid = %q, want sid-1", call.API, call.Method, call.Query.Get("_sid"))
			}
		}
	}
	if v := f.callsTo(apiTask, "list")[0].Query.Get("version"); v != "1" {
		t.Errorf("task list version = %s, want pinned 1 while max is 3", v)
	}
	if v := f.callsTo(apiTaskSource, "download")[0].Query.Get("version"); v != "2" {
		t.Errorf("source version = %s, want discovered max 2", v)
	}
}

func TestNewAuthVersion(t *testing.T) {
	for maxV, want := range map[int]string{7: "6", 8: "6", 6: "2", 3: "2"} {
		f := newFake(t)
		f.apis[apiAuth] = fakeAPI{Path: "auth.cgi", Max: maxV}
		newClient(t, f)
		logins := f.callsTo(apiAuth, "login")
		if len(logins) != 1 || logins[0].Form.Get("version") != want || logins[0].Path != "/webapi/auth.cgi" {
			t.Fatalf("max %d: logins = %+v, want version %s at auth.cgi", maxV, logins, want)
		}
	}
}

func TestNewRequiresTaskV1(t *testing.T) {
	f := newFake(t)
	f.apis[apiTask] = fakeAPI{Path: "entry.cgi", Min: 2, Max: 3}
	_, err := New(model.ManagerConfig{Host: f.server.URL, Username: f.user, Password: f.pass})
	if err == nil || !strings.Contains(err.Error(), apiTask) {
		t.Fatalf("err = %v, want %s version 1 unsupported", err, apiTask)
	}
}

func TestNewRefusesCrossHostRedirect(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(other.Close)
	f := newFake(t)
	f.redirectAuth = other.URL
	_, err := New(model.ManagerConfig{Host: f.server.URL, Username: f.user, Password: f.pass})
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err = %v, want refused redirect", err)
	}
	if strings.Contains(err.Error(), other.URL) || strings.Contains(err.Error(), f.pass) {
		t.Fatalf("err %q carries the URL or the password", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("other host got %d requests", n)
	}
}

func TestNewAuthErrors(t *testing.T) {
	cases := []struct {
		name  string
		fault int
		pass  string
		want  string
	}{
		{"wrong password", 0, "wrong-pass-42", "account or password"},
		{"fault 400", 400, "", "account or password"},
		{"2-step required", 403, "", "2-step"},
		{"2-step failed", 404, "", "2-step"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.authFault = tc.fault
			pass := f.pass
			if tc.pass != "" {
				pass = tc.pass
			}
			_, err := New(model.ManagerConfig{Host: f.server.URL, Username: f.user, Password: pass})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), pass) || strings.Contains(err.Error(), "passwd") {
				t.Fatalf("err %q carries the password", err)
			}
		})
	}
}

func TestNewRequiresDS2(t *testing.T) {
	f := newFake(t)
	delete(f.apis, apiTaskSource)
	_, err := New(model.ManagerConfig{Host: f.server.URL, Username: f.user, Password: f.pass})
	if err == nil || !strings.Contains(err.Error(), "SYNO.DownloadStation2.Task.Source not available") {
		t.Fatalf("err = %v, want Task.Source not available", err)
	}
	if n := len(f.callsTo(apiAuth, "login")); n != 0 {
		t.Fatalf("login calls = %d, want 0", n)
	}
}

func TestNewHostWithoutScheme(t *testing.T) {
	f := newFake(t)
	host := strings.TrimPrefix(f.server.URL, "http://") + "/"
	if _, err := New(model.ManagerConfig{Host: host, Username: f.user, Password: f.pass}); err != nil {
		t.Fatalf("New(%q): %v", host, err)
	}
}

func TestListMapsBTTasks(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	h1 := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "Show S01", Status: "paused", Destination: "downloads/tv", CreateTime: 1700000000}, topicURL, "e1", "e2")
	h2 := f.addTask(t, "dbid_2", &fakeTask{Type: "BT", Title: "Movie", Status: "error", Destination: "downloads/movies", CreateTime: 1700000100}, "https://tracker.example/t=777")
	f.addTask(t, "dbid_3", &fakeTask{Type: "http", Title: "file.iso", Status: "downloading", Destination: "downloads"}, "")

	entries, err := c.List(context.Background(), model.FilterOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	byHash := map[string]*model.Entry{}
	for _, e := range entries {
		byHash[e.Hash] = e
	}
	check := func(hash, name, url, topic, dest string, ts int64, stopped, unhealthy bool) {
		t.Helper()
		e := byHash[hash]
		if e == nil {
			t.Fatalf("no entry for %s", hash)
		}
		if e.Name != name || e.URL != url || e.TopicID != topic || e.SavePath != dest || e.Category != dest ||
			e.Stopped != stopped || e.Unhealthy != unhealthy || e.MaxRatio != -2 || e.ContentPath != "" ||
			e.Timestamp == nil || !e.Timestamp.Equal(time.Unix(ts, 0)) {
			t.Fatalf("entry %s = %+v", hash, e)
		}
	}
	check(h1, "Show S01", topicURL, "123456", "downloads/tv", 1700000000, true, false)
	check(h2, "Movie", "https://tracker.example/t=777", "777", "downloads/movies", 1700000100, false, true)
	if f.sourceCalls("dbid_3") != 0 {
		t.Fatalf("source of http task downloaded")
	}
	if !slices.IsSortedFunc(entries, func(a, b *model.Entry) int { return strings.Compare(a.Hash, b.Hash) }) {
		t.Fatalf("entries not sorted by hash")
	}
}

func TestListUnhealthyStatuses(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	status := map[string]bool{"downloading": false, "seeding": false, "finished": false, "waiting": false,
		"error": true, "hash_checking": true, "finishing": true, "extracting": true}
	hashes := map[string]string{}
	i := 0
	for s := range status {
		i++
		hashes[s] = f.addTask(t, "dbid_"+s, &fakeTask{Type: "bt", Title: s, Status: s}, "", s)
	}
	entries, err := c.List(context.Background(), model.FilterOptions{})
	if err != nil || len(entries) != i {
		t.Fatalf("List = %d entries, %v", len(entries), err)
	}
	for _, e := range entries {
		if e.Unhealthy != status[e.Name] {
			t.Errorf("status %s: Unhealthy = %v", e.Name, e.Unhealthy)
		}
	}
}

func TestListCachesSource(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "A"}, topicURL, "a")
	f.addTask(t, "dbid_2", &fakeTask{Type: "bt", Title: "B"}, topicURL, "b")
	ctx := context.Background()
	for range 2 {
		if entries, err := c.List(ctx, model.FilterOptions{}); err != nil || len(entries) != 2 {
			t.Fatalf("List = %d, %v", len(entries), err)
		}
	}
	if f.sourceCalls("dbid_1") != 1 || f.sourceCalls("dbid_2") != 1 {
		t.Fatalf("source calls = %d, %d, want 1, 1", f.sourceCalls("dbid_1"), f.sourceCalls("dbid_2"))
	}

	var gone *fakeTask
	f.lock(func() { gone = f.tasks["dbid_2"]; delete(f.tasks, "dbid_2") })
	if entries, err := c.List(ctx, model.FilterOptions{}); err != nil || len(entries) != 1 {
		t.Fatalf("List after removal = %d, %v", len(entries), err)
	}
	c.mu.Lock()
	_, cached := c.sources["dbid_2"]
	c.mu.Unlock()
	if cached {
		t.Fatalf("vanished task still cached")
	}
	f.lock(func() { f.tasks["dbid_2"] = gone })
	if entries, err := c.List(ctx, model.FilterOptions{}); err != nil || len(entries) != 2 {
		t.Fatalf("List after return = %d, %v", len(entries), err)
	}
	if f.sourceCalls("dbid_2") != 2 || f.sourceCalls("dbid_1") != 1 {
		t.Fatalf("source calls = %d, %d, want 1, 2", f.sourceCalls("dbid_1"), f.sourceCalls("dbid_2"))
	}
}

func TestListSourceFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("http 500", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f)
		h1 := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "A"}, topicURL, "a")
		f.addTask(t, "dbid_2", &fakeTask{Type: "bt", Title: "B"}, topicURL, "b")
		f.lock(func() { f.sources["dbid_2"] = fakeSource{status: 500} })
		for i := 1; i <= 2; i++ {
			entries, err := c.List(ctx, model.FilterOptions{})
			if !errors.Is(err, model.ErrPartialList) {
				t.Fatalf("err = %v, want ErrPartialList", err)
			}
			if strings.Contains(err.Error(), "_sid") || strings.Contains(err.Error(), "sid-1") {
				t.Fatalf("err %q carries the session id", err)
			}
			if len(entries) != 1 || entries[0].Hash != h1 {
				t.Fatalf("entries = %+v, want only %s", entries, h1)
			}
			if n := f.sourceCalls("dbid_2"); n != i {
				t.Fatalf("source calls = %d, want %d (no cache on HTTP errors)", n, i)
			}
		}
	})
	t.Run("relogin fails", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f)
		h1 := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "A"}, topicURL, "a")
		f.lock(func() { f.sources["dbid_1"] = fakeSource{code: 119}; f.authFault = 407 })
		if _, err := c.List(ctx, model.FilterOptions{}); !errors.Is(err, model.ErrPartialList) {
			t.Fatalf("List err = %v, want ErrPartialList", err)
		}
		c.mu.Lock()
		_, cached := c.sources["dbid_1"]
		c.mu.Unlock()
		if cached {
			t.Fatalf("failed relogin put the task into the negative cache")
		}
		if ok, err := c.Exists(ctx, h1); !errors.Is(err, model.ErrPartialList) || ok {
			t.Fatalf("Exists = %v, %v, want ErrPartialList", ok, err)
		}
	})
	for _, tc := range []struct {
		name string
		src  fakeSource
	}{
		{"error envelope", fakeSource{code: 105}},
		{"not a torrent", fakeSource{data: []byte("garbage")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := sourceRetry
			t.Cleanup(func() { sourceRetry = old })
			sourceRetry = time.Hour
			f := newFake(t)
			c := newClient(t, f)
			f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "A"}, topicURL, "a")
			f.addTask(t, "dbid_2", &fakeTask{Type: "bt", Title: "B"}, topicURL, "b")
			f.lock(func() { f.sources["dbid_2"] = tc.src })
			for range 2 {
				entries, err := c.List(ctx, model.FilterOptions{})
				if err != nil || len(entries) != 1 {
					t.Fatalf("List = %d, %v, want 1 entry and nil", len(entries), err)
				}
			}
			if n := f.sourceCalls("dbid_2"); n != 1 {
				t.Fatalf("source calls = %d, want 1 before sourceRetry", n)
			}
			sourceRetry = 0
			if _, err := c.List(ctx, model.FilterOptions{}); err != nil {
				t.Fatalf("List: %v", err)
			}
			if n := f.sourceCalls("dbid_2"); n != 2 {
				t.Fatalf("source calls = %d, want 2 after sourceRetry", n)
			}
		})
	}
}

func TestRelogin(t *testing.T) {
	ctx := context.Background()
	t.Run("119 once", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f)
		f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "A", Destination: "downloads/tv"}, topicURL, "a")
		f.lock(func() { f.force119 = 1 })
		cats, err := c.GetCategories(ctx)
		if err != nil || !slices.Equal(cats, []string{"downloads/tv"}) {
			t.Fatalf("GetCategories = %v, %v", cats, err)
		}
		if n := len(f.callsTo(apiAuth, "login")); n != 2 {
			t.Fatalf("logins = %d, want 2", n)
		}
		lists := f.callsTo(apiTask, "list")
		if got := lists[len(lists)-1].Query.Get("_sid"); got != "sid-2" {
			t.Fatalf("retry _sid = %q, want sid-2", got)
		}
	})
	t.Run("119 twice", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f)
		f.lock(func() { f.force119 = 2 })
		_, err := c.GetCategories(ctx)
		var ae *apiError
		if !errors.As(err, &ae) || ae.Code != 119 {
			t.Fatalf("err = %v, want api error 119", err)
		}
		if n := len(f.callsTo(apiAuth, "login")); n != 2 {
			t.Fatalf("logins = %d, want 2", n)
		}
	})
	t.Run("source relogin", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f)
		f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "A"}, topicURL, "a")
		f.lock(func() { f.sessions = map[string]bool{} })
		entries, err := c.List(ctx, model.FilterOptions{})
		if err != nil || len(entries) != 1 {
			t.Fatalf("List = %d, %v", len(entries), err)
		}
		if n := len(f.callsTo(apiAuth, "login")); n != 2 {
			t.Fatalf("logins = %d, want 2", n)
		}
	})
	t.Run("transport error", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f)
		f.lock(func() { f.failConn[apiTask] = true })
		_, err := c.GetCategories(ctx)
		if err == nil {
			t.Fatalf("GetCategories succeeded on a dropped connection")
		}
		if strings.Contains(err.Error(), "_sid") || strings.Contains(err.Error(), "sid-1") || strings.Contains(err.Error(), f.server.URL+"/") {
			t.Fatalf("err %q carries the request URL", err)
		}
		if !strings.Contains(err.Error(), apiTask) {
			t.Fatalf("err %q lacks the API name", err)
		}
		if c.Reachable(ctx) {
			t.Fatalf("Reachable on a dropped connection")
		}
	})
}

func TestFilesFromGetinfo(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	hash := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "R", Files: []fakeFile{
		{Name: "R/a.mkv", Size: 10, Priority: "normal"},
		{Name: "R/b.mkv", Size: 5, Priority: "skip"},
	}}, topicURL, "a.mkv", "b.mkv")
	files, err := c.Files(context.Background(), strings.ToUpper(hash))
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []model.TorrentFile{{Path: "R/a.mkv", Wanted: true}, {Path: "R/b.mkv", Wanted: false}}
	if !slices.Equal(files, want) {
		t.Fatalf("Files = %+v, want %+v", files, want)
	}
	calls := f.callsTo(apiTask, "getinfo")
	if len(calls) != 1 || calls[0].Query.Get("id") != "dbid_1" || calls[0].Query.Get("version") != "1" || calls[0].Query.Get("additional") != "file" {
		t.Fatalf("getinfo calls = %+v", calls)
	}
	if _, err = c.Files(context.Background(), strings.Repeat("0", 40)); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Files of absent = %v, want not found", err)
	}
}

func TestGetCategoriesDistinct(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	cats, err := c.GetCategories(context.Background())
	if err != nil || cats == nil || len(cats) != 0 {
		t.Fatalf("empty GetCategories = %#v, %v", cats, err)
	}
	f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "A", Destination: "downloads/tv"}, "", "a")
	f.addTask(t, "dbid_2", &fakeTask{Type: "bt", Title: "B", Destination: "downloads/movies"}, "", "b")
	f.addTask(t, "dbid_3", &fakeTask{Type: "bt", Title: "C", Destination: "downloads/tv"}, "", "c")
	f.addTask(t, "dbid_4", &fakeTask{Type: "http", Title: "D", Destination: "other"}, "")
	cats, err = c.GetCategories(context.Background())
	if err != nil || !slices.Equal(cats, []string{"downloads/movies", "downloads/tv"}) {
		t.Fatalf("GetCategories = %v, %v", cats, err)
	}
	if n := len(f.callsTo(apiTaskSource, "download")); n != 0 {
		t.Fatalf("source downloads = %d, want 0", n)
	}
}

func TestExistsAndDetails(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	c := newClient(t, f)
	hash := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "Show S01"}, topicURL, "a")
	absent := strings.Repeat("0", 40)

	if ok, err := c.Exists(ctx, strings.ToUpper(hash)); err != nil || !ok {
		t.Fatalf("Exists = %v, %v, want true", ok, err)
	}
	if ok, err := c.Exists(ctx, absent); err != nil || ok {
		t.Fatalf("Exists(absent) = %v, %v, want false", ok, err)
	}
	name, comment, err := c.GetTorrentDetails(ctx, hash)
	if err != nil || name != "Show S01" || comment != topicURL {
		t.Fatalf("GetTorrentDetails = %q, %q, %v", name, comment, err)
	}
	if name, comment, err = c.GetTorrentDetails(ctx, absent); err != nil || name != "" || comment != "" {
		t.Fatalf("GetTorrentDetails(absent) = %q, %q, %v", name, comment, err)
	}
	n := f.callCount()
	if name, comment, err = c.GetTorrentDetails(ctx, ""); err != nil || name != "" || comment != "" || f.callCount() != n {
		t.Fatalf("GetTorrentDetails(\"\") = %q, %q, %v, calls %d", name, comment, err, f.callCount()-n)
	}

	f.addTask(t, "dbid_2", &fakeTask{Type: "bt", Title: "B"}, topicURL, "b")
	f.lock(func() { f.sources["dbid_2"] = fakeSource{status: 500} })
	if ok, err := c.Exists(ctx, hash); err != nil || !ok {
		t.Fatalf("Exists on partial list = %v, %v, want true", ok, err)
	}
	if ok, err := c.Exists(ctx, absent); !errors.Is(err, model.ErrPartialList) || ok {
		t.Fatalf("Exists(absent) on partial list = %v, %v, want ErrPartialList", ok, err)
	}
	if _, _, err = c.GetTorrentDetails(ctx, absent); !errors.Is(err, model.ErrPartialList) {
		t.Fatalf("GetTorrentDetails(absent) on partial list = %v, want ErrPartialList", err)
	}
}

func TestReachable(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "A"}, "", "a")
	f.addTask(t, "dbid_2", &fakeTask{Type: "bt", Title: "B"}, "", "b")
	if !c.Reachable(context.Background()) {
		t.Fatalf("Reachable = false")
	}
	calls := f.callsTo(apiTask, "list")
	if len(calls) != 1 || calls[0].Query.Get("limit") != "1" || len(f.callsTo(apiTaskSource, "download")) != 0 {
		t.Fatalf("Reachable calls = %+v", calls)
	}
}
