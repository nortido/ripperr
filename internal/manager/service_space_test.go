package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"ripperr/internal/model"
)

const gib = int64(1) << 30

const spaceSavePath = "/data/tv"

func spaceFile(size int64, path ...string) metainfo.FileInfo {
	return metainfo.FileInfo{Length: size, Path: path}
}

func buildSpaceTorrent(t *testing.T, name string, length int64, files ...metainfo.FileInfo) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{PieceLength: 1 << 24, Pieces: make([]byte, 20), Name: name, Length: length, Files: files}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes, Announce: "http://tracker.example/announce"}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes(), mi.HashInfoBytes().HexString()
}

func twoFileTorrent(t *testing.T) ([]byte, string) {
	t.Helper()
	return buildSpaceTorrent(t, "R", 0, spaceFile(10*gib, "a"), spaceFile(8*gib, "b"))
}

func spaceStub(deletesFiles bool, free int64) *stubClient {
	stub := deleteFirstStub()
	stub.deletesFiles = deletesFiles
	stub.files = []model.TorrentFile{{Path: "R/a", Wanted: true, Done: 10 * gib}}
	stub.freeBytes, stub.freeSet = free, true
	return stub
}

func submitSpace(t *testing.T, ctx context.Context, stub *stubClient, data []byte, savePath string) error {
	t.Helper()
	withFastSettle(t)
	return NewManager(stub).Submit(ctx, bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: "oldhash", Name: "old name", SavePath: savePath})
}

func assertNoMutation(t *testing.T, stub *stubClient) {
	t.Helper()
	for _, c := range stub.calls {
		switch c {
		case "blackhole-write", "add", "delete", "discard", "purge", "resume":
			t.Errorf("call %q in %v, want no mutation", c, stub.calls)
		}
	}
}

func assertFreePaths(t *testing.T, stub *stubClient, want ...string) {
	t.Helper()
	if !slices.Equal(stub.freePaths, want) {
		t.Errorf("free space paths = %v, want %v", stub.freePaths, want)
	}
}

func TestSubmitSkipsReplaceWhenSpaceShort(t *testing.T) {
	stub := spaceStub(false, 5*gib)
	data, newHash := twoFileTorrent(t)
	err := submitSpace(t, context.Background(), stub, data, spaceSavePath)
	var ns *model.NoSpaceError
	if !errors.As(err, &ns) {
		t.Fatalf("err = %v, want NoSpaceError", err)
	}
	want := model.NoSpaceError{Path: spaceSavePath, Need: 8 * gib, Free: 5 * gib, Hash: newHash}
	if *ns != want {
		t.Errorf("NoSpaceError = %+v, want %+v", *ns, want)
	}
	assertCalls(t, stub, "details", "incoming", "exists")
	assertNoMutation(t, stub)
	assertFreePaths(t, stub, spaceSavePath)
}

func TestSubmitSpaceEnough(t *testing.T) {
	stub := spaceStub(false, 9*gib)
	data, _ := twoFileTorrent(t)
	if err := submitSpace(t, context.Background(), stub, data, spaceSavePath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCalls(t, stub, "details", "incoming", "add", "delete")
	assertFreePaths(t, stub, spaceSavePath)
}

func TestSubmitSpaceUnwantedNotCounted(t *testing.T) {
	stub := spaceStub(false, 0)
	stub.files = append(stub.files, model.TorrentFile{Path: "R/b", Wanted: false})
	data, _ := twoFileTorrent(t)
	if err := submitSpace(t, context.Background(), stub, data, spaceSavePath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFreePaths(t, stub)
	assertCalls(t, stub, "details", "incoming", "exists", "blackhole-write", "add", "blackhole-remove", "delete")
}

func TestSubmitSpaceDeletingFirstNoCredit(t *testing.T) {
	t.Run("short", func(t *testing.T) {
		stub := spaceStub(true, 12*gib)
		data, newHash := twoFileTorrent(t)
		err := submitSpace(t, context.Background(), stub, data, spaceSavePath)
		var ns *model.NoSpaceError
		if !errors.As(err, &ns) {
			t.Fatalf("err = %v, want NoSpaceError", err)
		}
		want := model.NoSpaceError{Path: spaceSavePath, Need: 18 * gib, Free: 12 * gib, Hash: newHash}
		if *ns != want {
			t.Errorf("NoSpaceError = %+v, want %+v", *ns, want)
		}
		assertCalls(t, stub, "details", "incoming", "exists")
		assertNoMutation(t, stub)
	})
	t.Run("fits", func(t *testing.T) {
		stub := spaceStub(true, 18*gib)
		data, _ := twoFileTorrent(t)
		if err := submitSpace(t, context.Background(), stub, data, spaceSavePath); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertCalls(t, stub, afterStoppedAdd("delete", "wait-absent", "resume", "confirm-settled", "blackhole-remove")...)
		assertFreePaths(t, stub, spaceSavePath)
	})
}

func TestSubmitSpaceUnknownFreeSpaceProceeds(t *testing.T) {
	for _, freeErr := range []error{fmt.Errorf("stub: %w", model.ErrFreeSpaceUnknown), errors.New("statfs failed")} {
		t.Run(freeErr.Error(), func(t *testing.T) {
			stub := spaceStub(false, 0)
			stub.freeErr = freeErr
			data, _ := twoFileTorrent(t)
			if err := submitSpace(t, context.Background(), stub, data, spaceSavePath); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertCalls(t, stub, "details", "incoming", "add", "delete")
			assertFreePaths(t, stub, spaceSavePath)
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		stub := spaceStub(false, 0)
		stub.freeErr = errors.New("statfs failed")
		data, _ := twoFileTorrent(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := submitSpace(t, ctx, stub, data, spaceSavePath); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		assertCalls(t, stub, "details", "incoming")
	})
}

func TestSubmitSpaceShortButNewPresent(t *testing.T) {
	stub := spaceStub(true, 0)
	data, newHash := twoFileTorrent(t)
	stub.existsByHash[newHash] = true
	err := submitSpace(t, context.Background(), stub, data, spaceSavePath)
	if !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("err = %v, want ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists", "exists")
	assertNoMutation(t, stub)
}

func TestSubmitSpaceShortProbeError(t *testing.T) {
	stub := spaceStub(false, 0)
	data, newHash := twoFileTorrent(t)
	stub.existsErrByHash[newHash] = errors.New("client down")
	err := submitSpace(t, context.Background(), stub, data, spaceSavePath)
	if err == nil || !strings.Contains(err.Error(), "check presence of new torrent") {
		t.Fatalf("err = %v, want check presence of new torrent", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists")
	assertNoMutation(t, stub)
}

func TestSubmitSpaceSameHashNoCheck(t *testing.T) {
	stub := spaceStub(false, 0)
	stub.incomingSame = true
	stub.existsByHash["oldhash"] = true
	data, _ := twoFileTorrent(t)
	if err := submitSpace(t, context.Background(), stub, data, spaceSavePath); !errors.Is(err, model.ErrHashUnchanged) {
		t.Fatalf("err = %v, want ErrHashUnchanged", err)
	}
	assertFreePaths(t, stub)
}

func TestSubmitSpaceEmptySavePathNoCheck(t *testing.T) {
	stub := spaceStub(false, 0)
	data, _ := twoFileTorrent(t)
	if err := submitSpace(t, context.Background(), stub, data, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCalls(t, stub, "details", "incoming", "add", "delete")
	assertFreePaths(t, stub)
}

func TestSpaceNeeded(t *testing.T) {
	single, _ := buildSpaceTorrent(t, "movie.mkv", 4*gib)
	pad := spaceFile(gib, ".pad", "1")
	pad.Attr = "p"
	padded, _ := buildSpaceTorrent(t, "R", 0, spaceFile(2*gib, "a"), pad, spaceFile(gib, "b"))
	renamed, _ := buildSpaceTorrent(t, "R2", 0, spaceFile(10*gib, "a"))
	smaller, _ := buildSpaceTorrent(t, "R", 0, spaceFile(5*gib, "a"))
	two, _ := twoFileTorrent(t)
	newRoot, _ := buildSpaceTorrent(t, "New", 0, spaceFile(10*gib, "a"))
	doneA := model.TorrentFile{Path: "R/a", Wanted: true, Done: 10 * gib}
	skipB := []model.TorrentFile{doneA, {Path: "R/b", Wanted: false}}
	cases := []struct {
		name          string
		data          []byte
		old           []model.TorrentFile
		selection     []model.TorrentFile
		deletingFirst bool
		want          int64
	}{
		{"single file", single, []model.TorrentFile{{Path: "movie.mkv", Wanted: true, Done: gib}}, nil, false, 3 * gib},
		{"pad file", padded, nil, nil, false, 3 * gib},
		{"root differs", renamed, []model.TorrentFile{doneA}, nil, false, 10 * gib},
		{"done over size", smaller, []model.TorrentFile{doneA}, nil, false, 0},
		{"nil selection", two, skipB, nil, false, 8 * gib},
		{"unwanted skipped", two, skipB, skipB, false, 0},
		{"deleting first", two, []model.TorrentFile{doneA}, nil, true, 18 * gib},
		{"parse error", []byte("junk"), nil, nil, false, 0},
		{"no subfolder layout", two, []model.TorrentFile{{Path: "a", Wanted: true, Done: 10 * gib}, {Path: "b", Wanted: true, Done: 8 * gib}}, nil, false, 0},
		{"other old root", newRoot, []model.TorrentFile{{Path: "Old/a", Wanted: true, Done: 10 * gib}}, nil, false, 10 * gib},
		{"single file no root credit", single, []model.TorrentFile{{Path: "movie.mkv/x", Wanted: true, Done: gib}}, nil, false, 4 * gib},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := spaceNeeded(c.data, c.old, c.selection, c.deletingFirst); got != c.want {
				t.Errorf("spaceNeeded = %d, want %d", got, c.want)
			}
		})
	}
}
