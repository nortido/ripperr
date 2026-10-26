package transmission

import (
	"context"
	"testing"

	"ripperr/internal/model"
)

func newConvergeStub(t *testing.T, status int, stats []rpcFileStat) (*stubServer, *Client, string) {
	t.Helper()
	stub := newStubServer(t, "/downloads")
	hash := "abc123"
	stub.putTorrent(rpcTorrent{
		HashString: hash,
		Status:     status,
		Files:      []rpcFile{{Name: "R2/a"}, {Name: "R2/b"}},
		FileStats:  stats,
	})
	c := newTestClient(t, stub, model.ManagerConfig{})
	return stub, c, hash
}

func allWanted() []rpcFileStat {
	return []rpcFileStat{{Wanted: true}, {Wanted: true}}
}

func TestConvergeAddedAppliesSelectionToUntouchedStopped(t *testing.T) {
	stub, c, hash := newConvergeStub(t, 0, allWanted())

	if err := c.ConvergeAdded(context.Background(), hash, nil, selectionOpts()); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	sets := unwantedSets(stub)
	if len(sets) != 1 || !idsAre(sets[0]["ids"], hash) {
		t.Fatalf("expected one files-unwanted torrent-set for %s, got %v", hash, stub.setCalls)
	}
	idx, _ := sets[0]["files-unwanted"].([]interface{})
	if len(idx) != 1 || idx[0] != float64(1) {
		t.Errorf("files-unwanted: got %v, want [1]", sets[0]["files-unwanted"])
	}
	if len(stub.startCalls) != 1 || !idsAre(stub.startCalls[0]["ids"], hash) {
		t.Fatalf("expected one torrent-start for %s, got %v", hash, stub.startCalls)
	}
	setAt, startAt := -1, -1
	for i, m := range stub.methods {
		switch m {
		case "torrent-set":
			setAt = i
		case "torrent-start":
			startAt = i
		}
	}
	if setAt < 0 || startAt < setAt {
		t.Errorf("expected torrent-start after torrent-set, methods %v", stub.methods)
	}
	if len(stub.removeCalls) != 0 {
		t.Errorf("expected no torrent-remove, got %v", stub.removeCalls)
	}
}

func TestConvergeAddedLeavesRunningAlone(t *testing.T) {
	stub, c, hash := newConvergeStub(t, 4, allWanted())

	if err := c.ConvergeAdded(context.Background(), hash, nil, selectionOpts()); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if len(stub.setCalls) != 0 || len(stub.startCalls) != 0 || len(stub.removeCalls) != 0 {
		t.Errorf("expected no set, start or remove, got sets %v starts %v removes %v", stub.setCalls, stub.startCalls, stub.removeCalls)
	}
}

func TestConvergeAddedStoppedWithOurSelectionStarts(t *testing.T) {
	stub, c, hash := newConvergeStub(t, 0, []rpcFileStat{{Wanted: true}, {Wanted: false}})

	if err := c.ConvergeAdded(context.Background(), hash, nil, selectionOpts()); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if len(stub.startCalls) != 1 || !idsAre(stub.startCalls[0]["ids"], hash) {
		t.Fatalf("expected one torrent-start for %s, got %v", hash, stub.startCalls)
	}
	if len(stub.setCalls) != 0 || len(stub.removeCalls) != 0 {
		t.Errorf("expected no set or remove, got sets %v removes %v", stub.setCalls, stub.removeCalls)
	}
}

func TestConvergeAddedAllUnwantedStoppedStarts(t *testing.T) {
	stub, c, hash := newConvergeStub(t, 0, []rpcFileStat{{Wanted: false}, {Wanted: false}})
	opts := selectionOpts()
	opts.FileSelection = []model.TorrentFile{{Path: "R1/a", Wanted: false}, {Path: "R1/b", Wanted: false}}

	if err := c.ConvergeAdded(context.Background(), hash, nil, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if len(stub.startCalls) != 1 || !idsAre(stub.startCalls[0]["ids"], hash) {
		t.Fatalf("expected one torrent-start for %s, got %v", hash, stub.startCalls)
	}
	if len(stub.setCalls) != 0 || len(stub.removeCalls) != 0 {
		t.Errorf("expected no set or remove, got sets %v removes %v", stub.setCalls, stub.removeCalls)
	}
}

func TestConvergeAddedStartFailureKeepsTorrent(t *testing.T) {
	stub, c, hash := newConvergeStub(t, 0, []rpcFileStat{{Wanted: true}, {Wanted: false}})
	stub.failStart = true

	if err := c.ConvergeAdded(context.Background(), hash, nil, selectionOpts()); err == nil {
		t.Fatal("expected an error when torrent-start fails")
	}
	if len(stub.removeCalls) != 0 {
		t.Fatalf("expected no torrent-remove, got %v", stub.removeCalls)
	}
	if len(stub.setCalls) != 0 {
		t.Errorf("expected no torrent-set, got %v", stub.setCalls)
	}
}

func TestConvergeAddedKeepsOtherSelection(t *testing.T) {
	stub, c, hash := newConvergeStub(t, 0, []rpcFileStat{{Wanted: false}, {Wanted: true}})

	if err := c.ConvergeAdded(context.Background(), hash, nil, selectionOpts()); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if len(stub.setCalls) != 0 || len(stub.startCalls) != 0 || len(stub.removeCalls) != 0 {
		t.Errorf("expected no set, start or remove, got sets %v starts %v removes %v", stub.setCalls, stub.startCalls, stub.removeCalls)
	}
}

func TestConvergeAddedOurSelectionStoppedWhenAsked(t *testing.T) {
	stub, c, hash := newConvergeStub(t, 0, []rpcFileStat{{Wanted: true}, {Wanted: false}})
	opts := selectionOpts()
	opts.Stopped = true

	if err := c.ConvergeAdded(context.Background(), hash, nil, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if len(stub.setCalls) != 0 || len(stub.startCalls) != 0 || len(stub.removeCalls) != 0 {
		t.Errorf("expected no set, start or remove, got sets %v starts %v removes %v", stub.setCalls, stub.startCalls, stub.removeCalls)
	}
}

func TestConvergeAddedRefusesWhileChecking(t *testing.T) {
	for _, status := range []int{1, 2} {
		stub, c, hash := newConvergeStub(t, status, allWanted())

		if err := c.ConvergeAdded(context.Background(), hash, nil, selectionOpts()); err == nil {
			t.Errorf("status %d: expected an error while the torrent is checking", status)
		}
		if len(stub.setCalls) != 0 || len(stub.startCalls) != 0 || len(stub.removeCalls) != 0 {
			t.Errorf("status %d: expected no set, start or remove, got sets %v starts %v removes %v", status, stub.setCalls, stub.startCalls, stub.removeCalls)
		}
	}
}

func TestConvergeAddedSetFailureKeepsTorrent(t *testing.T) {
	stub, c, hash := newConvergeStub(t, 0, allWanted())
	stub.failSetUnwanted = true

	if err := c.ConvergeAdded(context.Background(), hash, nil, selectionOpts()); err == nil {
		t.Fatal("expected an error when files-unwanted fails")
	}
	if len(stub.removeCalls) != 0 {
		t.Fatalf("expected no torrent-remove, got %v", stub.removeCalls)
	}
	if len(stub.startCalls) != 0 {
		t.Errorf("expected no torrent-start, got %v", stub.startCalls)
	}
}

func TestConvergeAddedNoFilesYetKeepsTorrent(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	hash := "abc123"
	stub.putTorrent(rpcTorrent{HashString: hash, Status: 0})
	c := newTestClient(t, stub, model.ManagerConfig{})

	if err := c.ConvergeAdded(context.Background(), hash, nil, selectionOpts()); err == nil {
		t.Fatal("expected an error for a torrent with no files yet")
	}
	if len(stub.removeCalls) != 0 || len(stub.setCalls) != 0 || len(stub.startCalls) != 0 {
		t.Errorf("expected no remove, set or start, got removes %v sets %v starts %v", stub.removeCalls, stub.setCalls, stub.startCalls)
	}
}

func TestConvergeAddedNoSelectionNoRPC(t *testing.T) {
	stub, c, hash := newConvergeStub(t, 0, allWanted())
	opts := selectionOpts()
	opts.FileSelection = nil
	before := len(stub.methods)

	if err := c.ConvergeAdded(context.Background(), hash, nil, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if got := stub.methods[before:]; len(got) != 0 {
		t.Errorf("expected no RPC, got %v", got)
	}
}
