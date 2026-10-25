package deluge

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"ripperr/internal/model"
)

func convergeSetup(t *testing.T, state string, prios []int) (*fakeDeluge, *Client, string, model.ManagerAddFileOptions) {
	t.Helper()
	withFastTimings(t)
	h := hashN(1)
	f := newFake(t)
	f.labels = map[string]bool{"tv": true}
	c := newClient(t, f, model.ManagerConfig{})
	f.put(h, &fakeTorrent{
		Name:  "show",
		State: state,
		Files: []fakeFile{{Index: 0, Path: "show/e1.mkv"}, {Index: 1, Path: "show/e2.mkv"}},
		Prios: prios,
	})
	opts := model.ManagerAddFileOptions{
		Name:          "show",
		Category:      "TV",
		FileSelection: []model.TorrentFile{{Path: "show/e1.mkv", Wanted: true}, {Path: "show/e2.mkv", Wanted: false}},
	}
	return f, c, h, opts
}

func mutatingCalls(f *fakeDeluge) []string {
	var out []string
	for _, m := range f.methodsSince(0) {
		if strings.HasPrefix(m, "core.") && m != "core.get_torrent_status" || strings.HasPrefix(m, "label.") {
			out = append(out, m)
		}
	}
	return out
}

func TestConvergeAddedFinishesUntouchedPaused(t *testing.T) {
	f, c, h, opts := convergeSetup(t, "Paused", []int{4, 4})
	if err := c.ConvergeAdded(context.Background(), strings.ToUpper(h), nil, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	labels := f.callsTo("label.set_torrent")
	if len(labels) != 1 || arg[string](t, labels[0].Params, 0) != h || arg[string](t, labels[0].Params, 1) != "tv" {
		t.Fatalf("label.set_torrent calls = %+v, want one for %s with tv", labels, h)
	}
	sets := f.callsTo("core.set_torrent_options")
	if len(sets) != 1 {
		t.Fatalf("core.set_torrent_options calls = %d, want 1", len(sets))
	}
	if p := arg[map[string][]int](t, sets[0].Params, 1)["file_priorities"]; !slices.Equal(p, []int{4, 0}) {
		t.Fatalf("file_priorities = %v, want [4 0]", p)
	}
	if n := len(f.callsTo("core.resume_torrent")); n != 1 {
		t.Fatalf("core.resume_torrent calls = %d, want 1", n)
	}
	if rm := removes(t, f); len(rm) != 0 {
		t.Fatalf("remove calls = %+v, want none", rm)
	}
}

func TestConvergeAddedStoppedKeepsPaused(t *testing.T) {
	f, c, h, opts := convergeSetup(t, "Paused", []int{4, 4})
	opts.Stopped = true
	if err := c.ConvergeAdded(context.Background(), h, nil, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if got := mutatingCalls(f); !slices.Equal(got, []string{"label.set_torrent", "core.set_torrent_options"}) {
		t.Fatalf("calls = %v, want label and selection without resume", got)
	}
}

func TestConvergeAddedLeavesOthersAlone(t *testing.T) {
	cases := []struct {
		name  string
		state string
		prios []int
	}{
		{"running", "Downloading", []int{4, 4}},
		{"seeding", "Seeding", []int{4, 4}},
		{"queued", "Queued", []int{4, 4}},
		{"error", "Error", []int{4, 4}},
		{"paused with an unwanted file", "Paused", []int{4, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c, h, opts := convergeSetup(t, tc.state, tc.prios)
			if err := c.ConvergeAdded(context.Background(), h, nil, opts); err != nil {
				t.Fatalf("ConvergeAdded: %v", err)
			}
			if got := mutatingCalls(f); len(got) != 0 {
				t.Fatalf("calls = %v, want reads only", got)
			}
		})
	}
}

func TestConvergeAddedRefusesTransient(t *testing.T) {
	for _, state := range []string{"Checking", "Moving", "Allocating"} {
		t.Run(state, func(t *testing.T) {
			f, c, h, opts := convergeSetup(t, state, []int{4, 4})
			err := c.ConvergeAdded(context.Background(), h, nil, opts)
			if err == nil || !strings.Contains(err.Error(), "not settled yet") {
				t.Fatalf("err = %v, want not settled yet", err)
			}
			if got := mutatingCalls(f); len(got) != 0 {
				t.Fatalf("calls = %v, want reads only", got)
			}
		})
	}
}

func TestConvergeAddedFailedApplyKeepsTorrent(t *testing.T) {
	for _, method := range []string{"label.set_torrent", "core.set_torrent_options", "core.resume_torrent"} {
		t.Run(method, func(t *testing.T) {
			f, c, h, opts := convergeSetup(t, "Paused", []int{4, 4})
			f.lock(func() { f.hook = failWith(method, 3, "Exception: boom") })
			if err := c.ConvergeAdded(context.Background(), h, nil, opts); err == nil {
				t.Fatalf("ConvergeAdded succeeded with a failed %s", method)
			}
			if rm := removes(t, f); len(rm) != 0 || !f.has(h) {
				t.Fatalf("remove calls = %+v, present = %v, want no remove", rm, f.has(h))
			}
		})
	}
}

func TestConvergeAddedNoSelectionNoRPC(t *testing.T) {
	f, c, h, opts := convergeSetup(t, "Paused", []int{4, 4})
	opts.FileSelection = nil
	n := f.callCount()
	if err := c.ConvergeAdded(context.Background(), h, nil, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if got := f.methodsSince(n); len(got) != 0 {
		t.Fatalf("calls = %v, want none", got)
	}
}

func TestConvergeAddedReadErrorIsError(t *testing.T) {
	f, c, h, opts := convergeSetup(t, "Paused", []int{4, 4})
	f.lock(func() {
		f.hook = func(m string, _ []json.RawMessage) (any, *fakeFault, bool) {
			if m == "core.get_torrent_status" {
				return nil, &fakeFault{3, "Exception: x"}, true
			}
			return nil, nil, false
		}
	})
	if err := c.ConvergeAdded(context.Background(), h, nil, opts); err == nil {
		t.Fatalf("ConvergeAdded succeeded on a failed state read")
	}
	if got := mutatingCalls(f); len(got) != 0 {
		t.Fatalf("calls = %v, want reads only", got)
	}
}
