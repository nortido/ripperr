package deluge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
)

func withFastTimings(t *testing.T) {
	t.Helper()
	a, d, cd, cb := presenceCheckAttempts, presenceCheckDelay, checkWaitDelay, checkWaitBudget
	presenceCheckAttempts, presenceCheckDelay, checkWaitDelay, checkWaitBudget = 2, 5*time.Millisecond, 5*time.Millisecond, 2*time.Second
	t.Cleanup(func() { presenceCheckAttempts, presenceCheckDelay, checkWaitDelay, checkWaitBudget = a, d, cd, cb })
}

func hashN(i int) string {
	return fmt.Sprintf("%040x", 0xabc000+i)
}

func failWith(method string, code int, msg string) func(string, []json.RawMessage) (any, *fakeFault, bool) {
	return func(m string, _ []json.RawMessage) (any, *fakeFault, bool) {
		if m == method {
			return nil, &fakeFault{code, msg}, true
		}
		return nil, nil, false
	}
}

func TestNewLoginAndConnect(t *testing.T) {
	t.Run("wrong password", func(t *testing.T) {
		f := newFake(t)
		_, err := New(model.ManagerConfig{Host: f.server.URL, Password: "bad"})
		if err == nil || !strings.Contains(err.Error(), "login failed") {
			t.Fatalf("err = %v, want login failed", err)
		}
	})
	t.Run("connects web to first daemon", func(t *testing.T) {
		f := newFake(t)
		f.connected = false
		f.hosts = []string{"host1", "host2"}
		newClient(t, f, model.ManagerConfig{})
		calls := f.callsTo("web.connect")
		if len(calls) != 1 || arg[string](t, calls[0].Params, 0) != "host1" {
			t.Fatalf("web.connect calls = %+v, want one with host1", calls)
		}
		if len(f.callsTo("web.get_hosts")) != 1 || !f.connected {
			t.Fatalf("get_hosts not used or not connected")
		}
	})
	t.Run("already connected", func(t *testing.T) {
		f := newFake(t)
		newClient(t, f, model.ManagerConfig{})
		if len(f.callsTo("web.connect")) != 0 {
			t.Fatalf("web.connect called while connected")
		}
	})
	t.Run("no daemon hosts", func(t *testing.T) {
		f := newFake(t)
		f.connected = false
		f.hosts = nil
		if _, err := New(model.ManagerConfig{Host: f.server.URL, Password: f.password}); err == nil {
			t.Fatalf("New succeeded without a daemon")
		}
	})
	t.Run("host without scheme", func(t *testing.T) {
		f := newFake(t)
		host := strings.TrimPrefix(f.server.URL, "http://") + "/"
		if _, err := New(model.ManagerConfig{Host: host, Password: f.password}); err != nil {
			t.Fatalf("New: %v", err)
		}
	})
}

func TestNewBlackholeDir(t *testing.T) {
	f := newFake(t)
	dir := filepath.Join(t.TempDir(), "bh")
	newClient(t, f, model.ManagerConfig{BlackholeDir: dir})
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("blackhole dir not created: %v", err)
	}
	newClient(t, f, model.ManagerConfig{})
}

func TestCallReloginOnCode1(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{})
	f.lock(func() { f.sessions = map[string]bool{} })
	if _, err := c.List(context.Background(), model.FilterOptions{}); err != nil {
		t.Fatalf("List after session expiry: %v", err)
	}
	if n := len(f.callsTo("auth.login")); n != 2 {
		t.Fatalf("auth.login calls = %d, want 2", n)
	}
	f.lock(func() { f.sessions = map[string]bool{}; f.password = "changed" })
	if _, err := c.List(context.Background(), model.FilterOptions{}); err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("err = %v, want login failed", err)
	}
	if n := len(f.callsTo("auth.login")); n != 3 {
		t.Fatalf("auth.login calls = %d, want 3 (one relogin per call)", n)
	}
}

func TestCallReconnectsDaemon(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{})
	h := hashN(1)
	f.put(h, &fakeTorrent{Name: "a", State: "Seeding"})
	f.lock(func() { f.connected = false })
	present, err := c.Exists(context.Background(), h)
	if err != nil || !present {
		t.Fatalf("Exists after daemon drop = %v, %v", present, err)
	}
	if n := len(f.callsTo("web.connect")); n != 1 {
		t.Fatalf("web.connect calls = %d, want 1", n)
	}
	f.lock(func() { f.hook = failWith("core.get_torrents_status", 3, "Exception: boom") })
	_, err = c.List(context.Background(), model.FilterOptions{})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want original boom", err)
	}
	if n := len(f.callsTo("web.connect")); n != 1 {
		t.Fatalf("reconnected while connected: web.connect calls = %d", n)
	}
}

func TestListMapsEntries(t *testing.T) {
	f := newFake(t)
	f.labels = map[string]bool{"movies": true}
	c := newClient(t, f, model.ManagerConfig{PausedMaxRatio: 1.5})
	states := map[string]struct{ stopped, unhealthy bool }{
		"Seeding": {false, false}, "Downloading": {false, false}, "Queued": {false, false},
		"Paused": {true, false}, "Error": {false, true}, "Checking": {false, true},
		"Moving": {false, true}, "Allocating": {false, true},
	}
	i := 0
	ids := map[string]string{}
	for st := range states {
		i++
		ids[hashN(i)] = st
		f.put(hashN(i), &fakeTorrent{Name: "n" + st, Comment: "https://tracker.example/viewtopic.php?t=12345", SavePath: "/data/" + st,
			Label: "movies", State: st, Progress: 100, TimeAdded: 1700000000.7})
	}
	entries, err := c.List(context.Background(), model.FilterOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != len(states) {
		t.Fatalf("entries = %d, want %d", len(entries), len(states))
	}
	for _, e := range entries {
		st := ids[e.Hash]
		want := states[st]
		if e.Name != "n"+st || e.URL != "https://tracker.example/viewtopic.php?t=12345" || e.TopicID != "12345" ||
			e.Category != "movies" || e.SavePath != "/data/"+st || e.Tag != "" || e.Timestamp == nil || e.Timestamp.Unix() != 1700000000 {
			t.Errorf("%s: bad entry %+v", st, e)
		}
		if e.Stopped != want.stopped || e.Unhealthy != want.unhealthy {
			t.Errorf("%s: stopped %v unhealthy %v, want %+v", st, e.Stopped, e.Unhealthy, want)
		}
		wantRatio := -2.0
		if st == "Paused" {
			wantRatio = 1.5
		}
		if e.MaxRatio != wantRatio {
			t.Errorf("%s: MaxRatio %v, want %v", st, e.MaxRatio, wantRatio)
		}
	}
	f.lock(func() { f.labels = nil })
	entries, err = c.List(context.Background(), model.FilterOptions{})
	if err != nil || entries[0].Category != "" {
		t.Fatalf("without Label plugin: %v, %+v", err, entries)
	}
}

func TestGetTorrentDetails(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{})
	h := hashN(1)
	f.put(h, &fakeTorrent{Name: "a", Comment: "https://x/t=1"})
	name, comment, err := c.GetTorrentDetails(context.Background(), strings.ToUpper(h))
	if err != nil || name != "a" || comment != "https://x/t=1" {
		t.Fatalf("details = %q %q %v", name, comment, err)
	}
	name, comment, err = c.GetTorrentDetails(context.Background(), hashN(2))
	if err != nil || name != "" || comment != "" {
		t.Fatalf("absent details = %q %q %v", name, comment, err)
	}
}

func TestExistsNeverFalseOnError(t *testing.T) {
	h := hashN(1)
	t.Run("absent and present", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		if present, err := c.Exists(context.Background(), h); err != nil || present {
			t.Fatalf("absent: %v %v", present, err)
		}
		f.put(h, &fakeTorrent{State: "Seeding"})
		if present, err := c.Exists(context.Background(), strings.ToUpper(h)); err != nil || !present {
			t.Fatalf("present upper-case: %v %v", present, err)
		}
	})
	cases := map[string]func(f *fakeDeluge){
		"rpc exception": func(f *fakeDeluge) { f.hook = failWith("core.get_torrent_status", 3, "Exception: x") },
		"deferred fail": func(f *fakeDeluge) { f.hook = failWith("core.get_torrent_status", 4, "x") },
		"http 500":      func(f *fakeDeluge) { f.failHTTP["core.get_torrent_status"] = true },
		"null result": func(f *fakeDeluge) {
			f.hook = func(m string, _ []json.RawMessage) (any, *fakeFault, bool) {
				return nil, nil, m == "core.get_torrent_status"
			}
		},
		"array result": func(f *fakeDeluge) {
			f.hook = func(m string, _ []json.RawMessage) (any, *fakeFault, bool) {
				return []int{}, nil, m == "core.get_torrent_status"
			}
		},
		"no daemon":      func(f *fakeDeluge) { f.connected = false; f.hosts = nil },
		"login refused":  func(f *fakeDeluge) { f.sessions = map[string]bool{}; f.password = "changed" },
		"unknown method": func(f *fakeDeluge) { f.hook = failWith("core.get_torrent_status", 2, "Unknown method") },
		"web.connect err": func(f *fakeDeluge) {
			f.connected = false
			f.hosts = []string{"gone"}
			f.hook = failWith("web.connect", 4, "refused")
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			c := newClient(t, f, model.ManagerConfig{})
			f.lock(func() { breakIt(f) })
			present, err := c.Exists(context.Background(), h)
			if err == nil || present {
				t.Fatalf("Exists = %v, %v; want error", present, err)
			}
		})
	}
}

func TestWaitAbsent(t *testing.T) {
	withFastTimings(t)
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{})
	h := hashN(1)
	if absent, err := c.WaitAbsent(context.Background(), h); err != nil || !absent {
		t.Fatalf("absent: %v %v", absent, err)
	}
	f.put(h, &fakeTorrent{State: "Seeding"})
	before := len(f.callsTo("core.get_torrent_status"))
	if absent, err := c.WaitAbsent(context.Background(), h); err != nil || absent {
		t.Fatalf("still present: %v %v", absent, err)
	}
	if n := len(f.callsTo("core.get_torrent_status")) - before; n != presenceCheckAttempts {
		t.Fatalf("polls = %d, want %d", n, presenceCheckAttempts)
	}
	f.lock(func() { f.hook = failWith("core.get_torrent_status", 3, "Exception: x") })
	if absent, err := c.WaitAbsent(context.Background(), h); err == nil || absent {
		t.Fatalf("on error: %v %v", absent, err)
	}
}

type removeCall struct {
	id   string
	data bool
}

func removes(t *testing.T, f *fakeDeluge) []removeCall {
	t.Helper()
	var out []removeCall
	for _, c := range f.callsTo("core.remove_torrent") {
		out = append(out, removeCall{arg[string](t, c.Params, 0), arg[bool](t, c.Params, 1)})
	}
	return out
}

func TestDelete(t *testing.T) {
	h, other := hashN(1), hashN(2)
	full := model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"movies"}}

	t.Run("disabled makes no rpc", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{DeleteFilesEnabled: true, DeleteCategories: []string{"movies"}})
		f.put(h, &fakeTorrent{})
		n := f.callCount()
		if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: h, Category: "movies"}); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if f.callCount() != n || !f.has(h) {
			t.Fatalf("disabled Delete touched the client: %v", f.methodsSince(n))
		}
		if c.DeletesFiles(context.Background(), model.ManagerDeleteOptions{Hash: h, Category: "movies"}) {
			t.Fatalf("DeletesFiles true while delete disabled")
		}
	})

	flags := []struct {
		name string
		cfg  model.ManagerConfig
		cat  string
		want bool
	}{
		{"category listed", full, "movies", true},
		{"category not listed", full, "tv", false},
		{"category case differs", full, "Movies", false},
		{"files disabled", model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}}, "movies", false},
		{"no categories", model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true}, "movies", false},
	}
	for _, tc := range flags {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			c := newClient(t, f, tc.cfg)
			f.put(h, &fakeTorrent{})
			f.put(other, &fakeTorrent{})
			opts := model.ManagerDeleteOptions{Hash: strings.ToUpper(h), Category: tc.cat, Name: "a", DeleteFiles: true}
			if got := c.DeletesFiles(context.Background(), opts); got != tc.want {
				t.Fatalf("DeletesFiles = %v, want %v", got, tc.want)
			}
			if err := c.Delete(context.Background(), opts); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			rm := removes(t, f)
			if len(rm) != 1 || rm[0] != (removeCall{h, tc.want}) {
				t.Fatalf("remove calls = %+v, want [{%s %v}]", rm, h, tc.want)
			}
			if f.has(h) || !f.has(other) {
				t.Fatalf("wrong torrent removed")
			}
		})
	}

	t.Run("absent id confirmed by Exists", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, full)
		if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: h, Category: "movies"}); err != nil {
			t.Fatalf("Delete of absent: %v", err)
		}
		if len(f.callsTo("core.get_torrent_status")) != 1 {
			t.Fatalf("absence not probed")
		}
	})
	t.Run("error while present", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, full)
		f.put(h, &fakeTorrent{})
		f.lock(func() { f.hook = failWith("core.remove_torrent", 3, "Exception: disk busy") })
		err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: h, Category: "movies"})
		if err == nil || !strings.Contains(err.Error(), "disk busy") {
			t.Fatalf("err = %v, want disk busy", err)
		}
	})
	t.Run("error and probe fails", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, full)
		f.lock(func() {
			f.hook = func(m string, _ []json.RawMessage) (any, *fakeFault, bool) {
				if m == "core.remove_torrent" || m == "core.get_torrent_status" {
					return nil, &fakeFault{3, "Exception: " + m}, true
				}
				return nil, nil, false
			}
		})
		err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: h})
		if err == nil || !strings.Contains(err.Error(), "core.remove_torrent") {
			t.Fatalf("err = %v, want original remove error", err)
		}
	})
	t.Run("result false", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, full)
		f.lock(func() {
			f.hook = func(m string, _ []json.RawMessage) (any, *fakeFault, bool) {
				return false, nil, m == "core.remove_torrent"
			}
		})
		if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: h}); err == nil {
			t.Fatalf("Delete with result false succeeded")
		}
	})
}

func TestDiscardNeverRemovesData(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{""}})
	h := hashN(1)
	f.put(h, &fakeTorrent{})
	if err := c.Discard(context.Background(), strings.ToUpper(h)); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if rm := removes(t, f); len(rm) != 1 || rm[0] != (removeCall{h, false}) {
		t.Fatalf("remove calls = %+v", rm)
	}
	if err := c.Discard(context.Background(), h); err == nil {
		t.Fatalf("Discard of absent torrent succeeded")
	}
	f.put(h, &fakeTorrent{})
	f.lock(func() {
		f.hook = func(m string, _ []json.RawMessage) (any, *fakeFault, bool) {
			return false, nil, m == "core.remove_torrent"
		}
	})
	if err := c.Discard(context.Background(), h); err == nil {
		t.Fatalf("Discard with result false succeeded")
	}
}

func sentAddOptions(t *testing.T, f *fakeDeluge) map[string]any {
	t.Helper()
	calls := f.callsTo("core.add_torrent_file")
	if len(calls) != 1 {
		t.Fatalf("add_torrent_file calls = %d, want 1", len(calls))
	}
	return arg[map[string]any](t, calls[0].Params, 2)
}

func TestAddStoppedWithSelection(t *testing.T) {
	withFastTimings(t)
	data, h := buildTorrent(t, "show", "e1.mkv", "e2.mkv", "e3.mkv")
	selection := []model.TorrentFile{{Path: "show/e1.mkv", Wanted: true}, {Path: "show/e2.mkv", Wanted: false}, {Path: "show/e3.mkv", Wanted: true}}

	t.Run("plain add starts", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "show"}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		calls := f.callsTo("core.add_torrent_file")
		dump, err := base64.StdEncoding.DecodeString(arg[string](t, calls[0].Params, 1))
		if err != nil || !bytes.Equal(dump, data) {
			t.Fatalf("filedump is not base64 of the torrent: %v", err)
		}
		opts := sentAddOptions(t, f)
		if opts["add_paused"] != false {
			t.Fatalf("add_paused = %v, want false", opts["add_paused"])
		}
		if _, ok := opts["download_location"]; ok {
			t.Fatalf("download_location sent without SavePath")
		}
		if len(f.callsTo("core.set_torrent_options")) != 0 || len(f.callsTo("core.resume_torrent")) != 0 {
			t.Fatalf("unexpected selection or resume")
		}
	})
	t.Run("stopped stays paused", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "show", Stopped: true, SavePath: "/data/tv"}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		opts := sentAddOptions(t, f)
		if opts["add_paused"] != true || opts["download_location"] != "/data/tv" {
			t.Fatalf("add options = %v", opts)
		}
		if len(f.callsTo("core.resume_torrent")) != 0 || f.torrents[h].State != "Paused" {
			t.Fatalf("stopped add was resumed")
		}
	})
	for _, stopped := range []bool{false, true} {
		t.Run(fmt.Sprintf("selection stopped=%v", stopped), func(t *testing.T) {
			f := newFake(t)
			c := newClient(t, f, model.ManagerConfig{})
			err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "show", Stopped: stopped, FileSelection: selection})
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if sentAddOptions(t, f)["add_paused"] != true {
				t.Fatalf("selection add not paused")
			}
			sets := f.callsTo("core.set_torrent_options")
			if len(sets) != 1 {
				t.Fatalf("set_torrent_options calls = %d", len(sets))
			}
			if ids := arg[[]string](t, sets[0].Params, 0); !slices.Equal(ids, []string{h}) {
				t.Fatalf("ids = %v", ids)
			}
			opts := arg[map[string][]int](t, sets[0].Params, 1)
			if !slices.Equal(opts["file_priorities"], []int{4, 0, 4}) {
				t.Fatalf("file_priorities = %v", opts["file_priorities"])
			}
			resumed := len(f.callsTo("core.resume_torrent")) == 1
			if resumed == stopped {
				t.Fatalf("resumed = %v with Stopped = %v", resumed, stopped)
			}
			methods := f.methodsSince(0)
			if resumed && slices.Index(methods, "core.resume_torrent") < slices.Index(methods, "core.set_torrent_options") {
				t.Fatalf("resumed before selection: %v", methods)
			}
		})
	}
	t.Run("read-back mismatch discards", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{""}})
		f.ignorePriorities = true
		err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "show", FileSelection: selection})
		if err == nil {
			t.Fatalf("Add succeeded with priorities not applied")
		}
		if rm := removes(t, f); len(rm) != 1 || rm[0] != (removeCall{h, false}) {
			t.Fatalf("remove calls = %+v, want discard without data", rm)
		}
		if len(f.callsTo("core.resume_torrent")) != 0 {
			t.Fatalf("resumed after failed selection")
		}
	})
	t.Run("selection failure discards on cancelled ctx", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.lock(func() {
			f.hook = func(m string, _ []json.RawMessage) (any, *fakeFault, bool) {
				if m == "core.set_torrent_options" {
					cancel()
					return nil, &fakeFault{3, "Exception: nope"}, true
				}
				return nil, nil, false
			}
		})
		if err := c.AddFromBuffer(ctx, bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "show", FileSelection: selection}); err == nil {
			t.Fatalf("Add succeeded")
		}
		if rm := removes(t, f); len(rm) != 1 || rm[0] != (removeCall{h, false}) || f.has(h) {
			t.Fatalf("remove calls = %+v, want discard without data", rm)
		}
	})
}

func TestAddDuplicate(t *testing.T) {
	withFastTimings(t)
	data, h := buildTorrent(t, "dup")
	t.Run("present before add", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		f.put(h, &fakeTorrent{State: "Seeding"})
		err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "dup", Hash: hashN(9)})
		if !errors.Is(err, model.ErrAlreadyAdded) {
			t.Fatalf("err = %v, want ErrAlreadyAdded", err)
		}
		if len(f.callsTo("core.add_torrent_file")) != 0 {
			t.Fatalf("add sent for present torrent")
		}
	})
	t.Run("same hash with delete waits absent", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{DeleteEnabled: true})
		f.put(h, &fakeTorrent{State: "Seeding"})
		err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "dup", Hash: strings.ToUpper(h)})
		if !errors.Is(err, model.ErrAlreadyAdded) {
			t.Fatalf("err = %v, want ErrAlreadyAdded", err)
		}
		if n := len(f.callsTo("core.get_torrent_status")); n != presenceCheckAttempts {
			t.Fatalf("polls = %d, want %d", n, presenceCheckAttempts)
		}
	})
	t.Run("same hash absent adds", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{DeleteEnabled: true})
		if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "dup", Hash: h}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	})
	t.Run("already in session", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		f.lock(func() {
			f.hook = failWith("core.add_torrent_file", 3, "AddTorrentError: Torrent already in session ("+h+").")
		})
		err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "dup"})
		if !errors.Is(err, model.ErrAlreadyAdded) {
			t.Fatalf("err = %v, want ErrAlreadyAdded", err)
		}
	})
	t.Run("other add error", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		f.lock(func() { f.hook = failWith("core.add_torrent_file", 3, "AddTorrentError: bad") })
		err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "dup"})
		if err == nil || errors.Is(err, model.ErrAlreadyAdded) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestAddConfirmsPresence(t *testing.T) {
	withFastTimings(t)
	data, _ := buildTorrent(t, "ghost")
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{})
	f.dropAdds = true
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "ghost"}); err == nil {
		t.Fatalf("Add succeeded but torrent never appeared")
	}
	if n := len(f.callsTo("core.get_torrent_status")); n != 1+presenceCheckAttempts {
		t.Fatalf("status polls = %d, want pre-check + %d", n, presenceCheckAttempts)
	}
}

func TestAddLabel(t *testing.T) {
	withFastTimings(t)
	data, h := buildTorrent(t, "lbl")
	add := func(c *Client) error {
		return c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "lbl", Category: "Movies"})
	}
	t.Run("existing label", func(t *testing.T) {
		f := newFake(t)
		f.labels = map[string]bool{"movies": true}
		c := newClient(t, f, model.ManagerConfig{})
		if err := add(c); err != nil {
			t.Fatalf("Add: %v", err)
		}
		sets := f.callsTo("label.set_torrent")
		if len(sets) != 1 || arg[string](t, sets[0].Params, 0) != h || arg[string](t, sets[0].Params, 1) != "movies" {
			t.Fatalf("set_torrent calls = %+v", sets)
		}
		if len(f.callsTo("label.add")) != 0 || f.torrents[h].Label != "movies" {
			t.Fatalf("label not applied as is")
		}
	})
	t.Run("unknown label is created", func(t *testing.T) {
		f := newFake(t)
		f.labels = map[string]bool{}
		c := newClient(t, f, model.ManagerConfig{})
		if err := add(c); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if len(f.callsTo("label.add")) != 1 || len(f.callsTo("label.set_torrent")) != 2 || f.torrents[h].Label != "movies" {
			t.Fatalf("label not created and set")
		}
	})
	t.Run("plugin off skips", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		if err := add(c); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if !f.has(h) || len(removes(t, f)) != 0 {
			t.Fatalf("torrent removed although only the plugin is missing")
		}
	})
	t.Run("no category no label calls", func(t *testing.T) {
		f := newFake(t)
		f.labels = map[string]bool{}
		c := newClient(t, f, model.ManagerConfig{})
		if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "lbl"}); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if len(f.callsTo("label.set_torrent")) != 0 {
			t.Fatalf("label set without category")
		}
	})
	for name, method := range map[string]string{"set fails": "label.set_torrent", "create fails": "label.add"} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.labels = map[string]bool{}
			if method == "label.set_torrent" {
				f.labels["movies"] = true
			}
			c := newClient(t, f, model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"Movies", "movies"}})
			f.lock(func() { f.hook = failWith(method, 3, "Exception: boom") })
			if err := add(c); err == nil {
				t.Fatalf("Add succeeded without label")
			}
			if rm := removes(t, f); len(rm) != 1 || rm[0] != (removeCall{h, false}) || f.has(h) {
				t.Fatalf("remove calls = %+v, want discard without data", rm)
			}
		})
	}
}

func TestResume(t *testing.T) {
	withFastTimings(t)
	h := hashN(1)
	for _, st := range []string{"Downloading", "Seeding", "Queued", "Moving"} {
		t.Run("running "+st, func(t *testing.T) {
			f := newFake(t)
			c := newClient(t, f, model.ManagerConfig{})
			f.put(h, &fakeTorrent{State: st})
			n := f.callCount()
			if err := c.Resume(context.Background(), h); err != nil {
				t.Fatalf("Resume: %v", err)
			}
			if m := f.methodsSince(n); !slices.Equal(m, []string{"core.get_torrent_status"}) {
				t.Fatalf("calls = %v, want only one status read", m)
			}
		})
	}
	t.Run("checking then running", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		f.put(h, &fakeTorrent{State: "Checking", script: []string{"Checking", "Allocating", "Seeding"}})
		if err := c.Resume(context.Background(), h); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if len(f.callsTo("core.force_recheck")) != 0 || len(f.callsTo("core.resume_torrent")) != 0 {
			t.Fatalf("running torrent rechecked or resumed")
		}
	})
	for _, st := range []string{"Paused", "Error"} {
		t.Run("recheck then start from "+st, func(t *testing.T) {
			f := newFake(t)
			c := newClient(t, f, model.ManagerConfig{})
			f.put(h, &fakeTorrent{State: st})
			if err := c.Resume(context.Background(), strings.ToUpper(h)); err != nil {
				t.Fatalf("Resume: %v", err)
			}
			methods := f.methodsSince(0)
			ri, si := slices.Index(methods, "core.force_recheck"), slices.Index(methods, "core.resume_torrent")
			if ri < 0 || si < ri {
				t.Fatalf("want force_recheck before resume_torrent: %v", methods)
			}
			if f.torrents[h].State != "Downloading" || len(f.torrents[h].script) != 0 {
				t.Fatalf("resumed before recheck finished: state %s script %v", f.torrents[h].State, f.torrents[h].script)
			}
		})
	}
	t.Run("error after start", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		f.put(h, &fakeTorrent{State: "Paused"})
		f.resumeTo = "Error"
		if err := c.Resume(context.Background(), h); err == nil {
			t.Fatalf("Resume succeeded into Error")
		}
	})
	t.Run("still paused", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		f.put(h, &fakeTorrent{State: "Paused"})
		f.resumeTo = "Paused"
		checkWaitBudget = 60 * time.Millisecond
		if err := c.Resume(context.Background(), h); err == nil {
			t.Fatalf("Resume succeeded while still paused")
		}
	})
	t.Run("not found", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		if err := c.Resume(context.Background(), h); err == nil {
			t.Fatalf("Resume of absent torrent succeeded")
		}
	})
}

func TestFilesFromPriorities(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{})
	h := hashN(1)
	f.put(h, &fakeTorrent{Files: []fakeFile{{1, "x/b"}, {2, "x/c"}, {0, "x/a"}}, Prios: []int{0, 1, 7}})
	files, err := c.Files(context.Background(), strings.ToUpper(h))
	want := []model.TorrentFile{{Path: "x/a", Wanted: false}, {Path: "x/b", Wanted: true}, {Path: "x/c", Wanted: true}}
	if err != nil || !slices.Equal(files, want) {
		t.Fatalf("Files = %+v, %v; want %+v", files, err, want)
	}
	f.put(h, &fakeTorrent{})
	if files, err = c.Files(context.Background(), h); err != nil || len(files) != 0 {
		t.Fatalf("without metadata: %+v, %v", files, err)
	}
	f.put(h, &fakeTorrent{Files: []fakeFile{{0, "a"}, {1, "b"}}, Prios: []int{4}})
	if _, err = c.Files(context.Background(), h); err == nil {
		t.Fatalf("priority count mismatch accepted")
	}
	f.put(h, &fakeTorrent{Files: []fakeFile{{0, "a"}, {0, "b"}}, Prios: []int{4, 4}})
	if _, err = c.Files(context.Background(), h); err == nil {
		t.Fatalf("duplicate index accepted")
	}
	if _, err = c.Files(context.Background(), hashN(2)); err == nil {
		t.Fatalf("absent torrent accepted")
	}
}

func withMagnetFetch(t *testing.T, fn func(context.Context, string) ([]byte, error)) *[]string {
	t.Helper()
	var got []string
	orig := magnetFetch
	magnetFetch = func(ctx context.Context, m string) ([]byte, error) {
		got = append(got, m)
		return fn(ctx, m)
	}
	t.Cleanup(func() { magnetFetch = orig })
	return &got
}

func TestAddFromMagnetFetchesMetadata(t *testing.T) {
	withFastTimings(t)
	data, h := buildTorrent(t, "mag")
	magnet := "magnet:?xt=urn:btih:" + strings.ToUpper(h)
	opts := model.ManagerAddFileOptions{Name: "mag", Comment: "https://tracker.example/t=7", Stopped: true}

	t.Run("fetched file carries comment", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		got := withMagnetFetch(t, func(context.Context, string) ([]byte, error) { return data, nil })
		if err := c.AddFromMagnet(context.Background(), magnet, opts); err != nil {
			t.Fatalf("AddFromMagnet: %v", err)
		}
		if !slices.Equal(*got, []string{magnet}) || len(f.callsTo("core.add_torrent_magnet")) != 0 {
			t.Fatalf("fetch calls %v, magnet adds %d", *got, len(f.callsTo("core.add_torrent_magnet")))
		}
		calls := f.callsTo("core.add_torrent_file")
		dump, _ := base64.StdEncoding.DecodeString(arg[string](t, calls[0].Params, 1))
		if cm, err := torrentmeta.Comment(dump); err != nil || cm != opts.Comment {
			t.Fatalf("comment = %q, %v", cm, err)
		}
		if f.torrents[h].State != "Paused" {
			t.Fatalf("stopped magnet add started")
		}
	})
	t.Run("fetch error falls back to magnet", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		withMagnetFetch(t, func(context.Context, string) ([]byte, error) { return nil, errors.New("no peers") })
		if err := c.AddFromMagnet(context.Background(), magnet, opts); err != nil {
			t.Fatalf("AddFromMagnet: %v", err)
		}
		calls := f.callsTo("core.add_torrent_magnet")
		if len(calls) != 1 || arg[string](t, calls[0].Params, 0) != magnet || !f.has(h) {
			t.Fatalf("magnet add calls = %+v", calls)
		}
		if arg[map[string]any](t, calls[0].Params, 1)["add_paused"] != true {
			t.Fatalf("stopped magnet add not paused")
		}
	})
	t.Run("no comment skips fetch", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		got := withMagnetFetch(t, func(context.Context, string) ([]byte, error) { return data, nil })
		if err := c.AddFromMagnet(context.Background(), magnet, model.ManagerAddFileOptions{Name: "mag"}); err != nil {
			t.Fatalf("AddFromMagnet: %v", err)
		}
		if len(*got) != 0 || len(f.callsTo("core.add_torrent_magnet")) != 1 {
			t.Fatalf("fetch used without comment")
		}
	})
	t.Run("magnet duplicate", func(t *testing.T) {
		f := newFake(t)
		c := newClient(t, f, model.ManagerConfig{})
		f.put(h, &fakeTorrent{State: "Seeding"})
		err := c.AddFromMagnet(context.Background(), magnet, model.ManagerAddFileOptions{Name: "mag"})
		if !errors.Is(err, model.ErrAlreadyAdded) || len(f.callsTo("core.add_torrent_magnet")) != 0 {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestGetCategoriesNoLabelPlugin(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{})
	cats, err := c.GetCategories(context.Background())
	if err != nil || cats == nil || len(cats) != 0 {
		t.Fatalf("without plugin: %#v, %v", cats, err)
	}
	f.lock(func() { f.labels = map[string]bool{"tv": true, "movies": true} })
	if cats, err = c.GetCategories(context.Background()); err != nil || !slices.Equal(cats, []string{"movies", "tv"}) {
		t.Fatalf("labels: %v, %v", cats, err)
	}
	f.lock(func() { f.hook = failWith("label.get_labels", 3, "Exception: x") })
	if _, err = c.GetCategories(context.Background()); err == nil {
		t.Fatalf("label error swallowed")
	}
}

func TestUnsupportedOps(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{DeleteEnabled: true})
	h := hashN(1)
	f.put(h, &fakeTorrent{State: "Seeding"})
	n := f.callCount()
	if err := c.SetComment(context.Background(), h, "x"); !errors.Is(err, model.ErrCommentUnsupported) {
		t.Fatalf("SetComment = %v", err)
	}
	if data, err := c.ExportTorrent(context.Background(), h); !errors.Is(err, model.ErrExportUnsupported) || data != nil {
		t.Fatalf("ExportTorrent = %v", err)
	}
	if err := c.ForceRedownload(context.Background(), h, model.RedownloadOptions{}); !errors.Is(err, ErrRedownloadUnsupported) {
		t.Fatalf("ForceRedownload = %v", err)
	}
	if f.callCount() != n || !f.has(h) {
		t.Fatalf("unsupported ops made calls: %v", f.methodsSince(n))
	}
	if !c.DeleteEnabled() {
		t.Fatalf("DeleteEnabled = false")
	}
}

func TestReachable(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{})
	if !c.Reachable(context.Background()) {
		t.Fatalf("connected web not reachable")
	}
	f.lock(func() { f.connected = false })
	if !c.Reachable(context.Background()) || !f.connected {
		t.Fatalf("reconnect not tried")
	}
	f.lock(func() { f.connected = false; f.hosts = nil })
	if c.Reachable(context.Background()) {
		t.Fatalf("reachable without daemon")
	}
	f.lock(func() { f.failHTTP["web.connected"] = true })
	if c.Reachable(context.Background()) {
		t.Fatalf("reachable on http error")
	}
}

func TestRPCErrorsAreErrors(t *testing.T) {
	withFastTimings(t)
	data, h := buildTorrent(t, "err")
	ops := map[string]func(c *Client) error{
		"List": func(c *Client) error { _, err := c.List(context.Background(), model.FilterOptions{}); return err },
		"GetTorrentDetails": func(c *Client) error {
			_, _, err := c.GetTorrentDetails(context.Background(), h)
			return err
		},
		"Exists":     func(c *Client) error { _, err := c.Exists(context.Background(), h); return err },
		"WaitAbsent": func(c *Client) error { _, err := c.WaitAbsent(context.Background(), h); return err },
		"Add": func(c *Client) error {
			return c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "err"})
		},
		"Delete":  func(c *Client) error { return c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: h}) },
		"Discard": func(c *Client) error { return c.Discard(context.Background(), h) },
		"Resume":  func(c *Client) error { return c.Resume(context.Background(), h) },
		"Files":   func(c *Client) error { _, err := c.Files(context.Background(), h); return err },
		"GetCategories": func(c *Client) error {
			_, err := c.GetCategories(context.Background())
			return err
		},
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.labels = map[string]bool{}
			c := newClient(t, f, model.ManagerConfig{DeleteEnabled: true})
			f.put(h, &fakeTorrent{State: "Paused"})
			f.lock(func() {
				f.hook = func(m string, _ []json.RawMessage) (any, *fakeFault, bool) {
					if strings.HasPrefix(m, "core.") || strings.HasPrefix(m, "label.") {
						return nil, &fakeFault{3, "Exception: broken"}, true
					}
					return nil, nil, false
				}
			})
			if err := op(c); err == nil {
				t.Fatalf("%s returned nil on JSON-RPC error", name)
			}
			if !f.has(h) {
				t.Fatalf("%s removed the torrent", name)
			}
		})
	}
}

var upperHash = regexp.MustCompile(`[0-9a-fA-F]{40}`)

func TestHashesSentLowerCase(t *testing.T) {
	withFastTimings(t)
	data, h := buildTorrent(t, "case")
	f := newFake(t)
	f.labels = map[string]bool{}
	c := newClient(t, f, model.ManagerConfig{DeleteEnabled: true})
	up := strings.ToUpper(h)
	ctx := context.Background()
	if err := c.AddFromBuffer(ctx, bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "case", Hash: up, Category: "tv", Stopped: true}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := c.Resume(ctx, up); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := c.Files(ctx, up); err != nil {
		t.Fatalf("Files: %v", err)
	}
	if _, _, err := c.GetTorrentDetails(ctx, up); err != nil {
		t.Fatalf("details: %v", err)
	}
	entries, err := c.List(ctx, model.FilterOptions{})
	if err != nil || len(entries) != 1 || entries[0].Hash != h {
		t.Fatalf("List hash = %+v, %v", entries, err)
	}
	if err = c.Delete(ctx, model.ManagerDeleteOptions{Hash: up}); err != nil || f.has(h) {
		t.Fatalf("Delete: %v", err)
	}
	if absent, err := c.WaitAbsent(ctx, up); err != nil || !absent {
		t.Fatalf("WaitAbsent: %v %v", absent, err)
	}
	f.lock(func() {
		for _, call := range f.calls {
			for _, p := range call.Params {
				for _, m := range upperHash.FindAllString(string(p), -1) {
					if m != strings.ToLower(m) {
						t.Errorf("%s sent upper-case hash %s", call.Method, m)
					}
				}
			}
		}
	})
}

func TestDelete_CallerKeepFilesWins(t *testing.T) {
	h := hashN(1)
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"movies"}})
	f.put(h, &fakeTorrent{})
	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: h, Category: "movies", Name: "a", DeleteFiles: false}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rm := removes(t, f); len(rm) != 1 || rm[0] != (removeCall{h, false}) {
		t.Fatalf("remove calls = %+v, want [{%s false}]", rm, h)
	}
}

func TestDeletesFilesIgnoresEmptyCategory(t *testing.T) {
	h := hashN(1)
	f := newFake(t)
	c := newClient(t, f, model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"tv", ""}})
	f.put(h, &fakeTorrent{})
	if c.DeletesFiles(context.Background(), model.ManagerDeleteOptions{Hash: h, Category: ""}) {
		t.Fatalf("DeletesFiles true for uncategorized torrent")
	}
	if !c.DeletesFiles(context.Background(), model.ManagerDeleteOptions{Hash: h, Category: "tv"}) {
		t.Fatalf("DeletesFiles false for listed category")
	}
}
