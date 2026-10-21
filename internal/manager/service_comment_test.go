package manager

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
)

const attachLink = "https://aniliberty.top/anime/releases/release/x"

func (s *stubClient) SetComment(ctx context.Context, hash, comment string) error {
	s.calls = append(s.calls, "set-comment")
	s.setComments = append(s.setComments, hash+"|"+comment)
	return s.setCommentErr
}

func (s *stubClient) ExportTorrent(ctx context.Context, hash string) ([]byte, error) {
	s.calls = append(s.calls, "export")
	return s.exportData, s.exportErr
}

func (s *stubClient) DeleteEnabled() bool {
	return s.deleteEnabled
}

func namedTestTorrent(t *testing.T, name string) ([]byte, string) {
	t.Helper()
	infoBytes, err := bencode.Marshal(metainfo.Info{PieceLength: 16384, Pieces: make([]byte, 20), Name: name})
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

func fallbackStub(t *testing.T) (*stubClient, string) {
	t.Helper()
	withFastSettle(t)
	data, hash := buildTestTorrent(t)
	return &stubClient{
		setCommentErr:  model.ErrCommentUnsupported,
		deleteEnabled:  true,
		exportData:     data,
		waitAbsent:     true,
		confirmSettled: true,
		files:          []model.TorrentFile{{Path: "test.txt", Wanted: true}},
		entries: []*model.Entry{
			{Hash: "other", Name: "other"},
			{Hash: strings.ToUpper(hash), Name: "X - AniLiberty [WEB-DL]", Category: "anime", SavePath: "/data/anime", Tag: "t1", MaxRatio: 2, MaxSeedingTime: 60},
		},
	}, hash
}

func noFetch(t *testing.T) func(context.Context) ([]byte, error) {
	return func(context.Context) ([]byte, error) {
		t.Error("fetch must not be called")
		return nil, errors.New("unexpected fetch")
	}
}

func TestAttachCommentUsesSetComment(t *testing.T) {
	stub := &stubClient{deleteEnabled: true}
	if err := NewManager(stub).AttachComment(context.Background(), "h1", attachLink, noFetch(t)); err != nil {
		t.Fatalf("AttachComment: %v", err)
	}
	if !equalStrings(stub.calls, []string{"set-comment"}) || !equalStrings(stub.setComments, []string{"h1|" + attachLink}) {
		t.Fatalf("calls %v, comments %v", stub.calls, stub.setComments)
	}
}

func TestAttachCommentFallbackKeepsFiles(t *testing.T) {
	stub, hash := fallbackStub(t)
	if err := NewManager(stub).AttachComment(context.Background(), hash, attachLink, noFetch(t)); err != nil {
		t.Fatalf("AttachComment: %v", err)
	}
	want := []string{"set-comment", "export", "blackhole-write", "discard", "wait-absent", "add", "confirm-settled", "blackhole-remove"}
	if !equalStrings(stub.calls, want) {
		t.Fatalf("calls = %v, want %v", stub.calls, want)
	}
	if !equalStrings(stub.discardHashes, []string{hash}) {
		t.Errorf("discarded %v, want only %s", stub.discardHashes, hash)
	}
	if comment, _ := torrentmeta.Comment(stub.addData); comment != attachLink {
		t.Errorf("added comment = %q, want %q", comment, attachLink)
	}
	if got, _ := torrentmeta.InfoHash(stub.addData); got != hash {
		t.Errorf("added infohash = %s, want %s", got, hash)
	}
	if !bytes.Equal(stub.blackholeData, stub.addData) || stub.blackholeOpts.Hash != hash {
		t.Errorf("blackhole pair does not protect the added bytes (hash %q)", stub.blackholeOpts.Hash)
	}
	o := stub.addOpts
	if o.Hash != hash || o.Name != "X - AniLiberty [WEB-DL]" || o.Category != "anime" || o.SavePath != "/data/anime" || o.Tag != "t1" || o.MaxRatio != 2 || o.MaxSeedingTime != 60 || o.Comment != attachLink || o.Stopped || o.ReplacesHash != "" || o.FileSelection != nil {
		t.Errorf("add opts = %+v", o)
	}
}

func TestAttachCommentFallbackFetchesWhenExportUnsupported(t *testing.T) {
	stub, hash := fallbackStub(t)
	data := stub.exportData
	stub.exportData, stub.exportErr = nil, model.ErrExportUnsupported
	fetched := 0
	fetch := func(context.Context) ([]byte, error) {
		fetched++
		return data, nil
	}
	if err := NewManager(stub).AttachComment(context.Background(), hash, attachLink, fetch); err != nil {
		t.Fatalf("AttachComment: %v", err)
	}
	if fetched != 1 || !slices.Contains(stub.calls, "add") {
		t.Fatalf("fetched %d, calls %v", fetched, stub.calls)
	}
	if comment, _ := torrentmeta.Comment(stub.addData); comment != attachLink {
		t.Errorf("added comment = %q", comment)
	}
}

func TestAttachCommentFallbackSkippedWhenDeleteDisabled(t *testing.T) {
	stub, hash := fallbackStub(t)
	stub.deleteEnabled = false
	err := NewManager(stub).AttachComment(context.Background(), hash, attachLink, noFetch(t))
	if !errors.Is(err, model.ErrCommentUnsupported) {
		t.Fatalf("err = %v, want ErrCommentUnsupported", err)
	}
	if !equalStrings(stub.calls, []string{"set-comment"}) {
		t.Fatalf("calls = %v, want only set-comment", stub.calls)
	}
}

func TestAttachCommentAbortsOnHashMismatch(t *testing.T) {
	stub, hash := fallbackStub(t)
	stub.exportData, _ = namedTestTorrent(t, "another.txt")
	if err := NewManager(stub).AttachComment(context.Background(), hash, attachLink, noFetch(t)); err == nil {
		t.Fatal("want an error on infohash mismatch")
	}
	if !equalStrings(stub.calls, []string{"set-comment", "export"}) {
		t.Fatalf("calls = %v, nothing may be written or removed", stub.calls)
	}
}

func TestAttachCommentCarriesFileSelection(t *testing.T) {
	stub, hash := fallbackStub(t)
	stub.files = []model.TorrentFile{{Path: "r/a.mkv", Wanted: true}, {Path: "r/b.mkv", Wanted: false}}
	if err := NewManager(stub).AttachComment(context.Background(), hash, attachLink, noFetch(t)); err != nil {
		t.Fatalf("AttachComment: %v", err)
	}
	if !slices.Equal(stub.addOpts.FileSelection, stub.files) || !slices.Equal(stub.blackholeOpts.FileSelection, stub.files) {
		t.Errorf("selection add %v pair %v, want %v", stub.addOpts.FileSelection, stub.blackholeOpts.FileSelection, stub.files)
	}
	discardAt := slices.Index(stub.calls, "discard")
	if len(stub.filesAt) != 1 || stub.filesAt[0] > discardAt {
		t.Errorf("files read at %v, discard at %d: selection must be read before removal", stub.filesAt, discardAt)
	}
}

func TestAttachCommentSkipsAllUnwanted(t *testing.T) {
	stub, hash := fallbackStub(t)
	stub.files = []model.TorrentFile{{Path: "r/a.mkv"}, {Path: "r/b.mkv"}}
	if err := NewManager(stub).AttachComment(context.Background(), hash, attachLink, noFetch(t)); err == nil {
		t.Fatal("want an error when every file is unchecked")
	}
	if slices.Contains(stub.calls, "discard") || slices.Contains(stub.calls, "add") {
		t.Fatalf("calls = %v, torrent must stay untouched", stub.calls)
	}
}

func TestAttachCommentKeepsStoppedState(t *testing.T) {
	stub, hash := fallbackStub(t)
	stub.entries[1].Stopped = true
	if err := NewManager(stub).AttachComment(context.Background(), hash, attachLink, noFetch(t)); err != nil {
		t.Fatalf("AttachComment: %v", err)
	}
	if !stub.addOpts.Stopped {
		t.Error("stopped torrent re-added running")
	}
}

func TestAttachCommentPendingReplaceUntouched(t *testing.T) {
	stub, hash := fallbackStub(t)
	stub.pending = map[string]bool{hash: true}
	err := NewManager(stub).AttachComment(context.Background(), hash, attachLink, noFetch(t))
	if !errors.Is(err, model.ErrReplacePending) {
		t.Fatalf("err = %v, want ErrReplacePending", err)
	}
	if !equalStrings(stub.calls, []string{"set-comment"}) {
		t.Fatalf("calls = %v", stub.calls)
	}
}

func TestAttachCommentAddFailureKeepsPair(t *testing.T) {
	stub, hash := fallbackStub(t)
	stub.addErr = errors.New("add failed")
	if err := NewManager(stub).AttachComment(context.Background(), hash, attachLink, noFetch(t)); err == nil {
		t.Fatal("want the add error")
	}
	if slices.Contains(stub.calls, "blackhole-remove") {
		t.Fatalf("calls = %v, the pair must stay for the sweeper", stub.calls)
	}
}

func TestAttachCommentSkipsUnhealthy(t *testing.T) {
	stub, hash := fallbackStub(t)
	stub.entries[1].Unhealthy = true
	if err := NewManager(stub).AttachComment(context.Background(), hash, attachLink, noFetch(t)); err == nil {
		t.Fatal("want an error for an errored or busy torrent")
	}
	if !equalStrings(stub.calls, []string{"set-comment"}) {
		t.Fatalf("calls = %v, torrent must stay untouched", stub.calls)
	}
}
