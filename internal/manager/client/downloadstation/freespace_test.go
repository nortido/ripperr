package downloadstation

import (
	"context"
	"errors"
	"testing"

	"ripperr/internal/model"
)

func TestFreeSpaceShare(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	f.shares = []fakeShare{{Name: "downloads", Free: 123456789}, {Name: "video", Free: "987"}}
	c := newClient(t, f)
	for _, tc := range []struct {
		path string
		want int64
	}{{"downloads/tv", 123456789}, {"/downloads/tv", 123456789}, {"Downloads", 123456789}, {"video/a", 987}} {
		if got, err := c.FreeSpace(ctx, tc.path); err != nil || got != tc.want {
			t.Fatalf("FreeSpace(%q) = %d, %v, want %d", tc.path, got, err, tc.want)
		}
	}
	calls := f.callsTo(apiFileList, "list_share")
	if len(calls) == 0 || calls[0].Query.Get("additional") != `["volume_status"]` || calls[0].Query.Get("version") != "2" {
		t.Fatalf("list_share calls = %+v", calls)
	}
	if _, err := c.FreeSpace(ctx, "other/x"); !errors.Is(err, model.ErrFreeSpaceUnknown) {
		t.Fatalf("FreeSpace(other/x) = %v, want ErrFreeSpaceUnknown", err)
	}
	f.lock(func() { f.shareFault = 105 })
	if _, err := c.FreeSpace(ctx, "downloads/tv"); !errors.Is(err, model.ErrFreeSpaceUnknown) {
		t.Fatalf("FreeSpace on 105 = %v, want ErrFreeSpaceUnknown", err)
	}

	f2 := newFake(t)
	delete(f2.apis, apiFileList)
	c2 := newClient(t, f2)
	n := f2.callCount()
	if _, err := c2.FreeSpace(ctx, "downloads/tv"); !errors.Is(err, model.ErrFreeSpaceUnknown) || f2.callCount() != n {
		t.Fatalf("FreeSpace without FileStation = %v, calls %d, want unknown and no calls", err, f2.callCount()-n)
	}
}
