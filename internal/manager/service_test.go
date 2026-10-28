package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"ripperr/internal/manager/blackhole"
	"ripperr/internal/model"
)

type stubClient struct {
	calls              []string
	detName            string
	detCom             string
	detErr             error
	addOpts            model.ManagerAddFileOptions
	incomingSame       bool
	incomingKnown      bool
	addErr             error
	blackholeWriteErr  error
	blackholeData      []byte
	blackholeOpts      model.ManagerAddFileOptions
	confirmSettled     bool
	exists             bool
	existsErr          error
	deletesFiles       bool
	deletesFilesSeq    []bool
	deletesFilesCalls  int
	deleteOpts         []model.ManagerDeleteOptions
	deleteErr          error
	waitAbsent         bool
	waitAbsentErr      error
	waitHash           string
	settleHash         string
	existsByHash       map[string]bool
	existsErrByHash    map[string]error
	existsHonorsCtx    bool
	deleteCancel       context.CancelFunc
	waitCancel         context.CancelFunc
	blackholeWriteHook context.CancelFunc
	removeHash         string
	existsSeq          map[string][]existsAnswer
	pending            map[string]bool
	addCancel          context.CancelFunc
	addStopped         []bool
	discardErr         error
	discardHashes      []string
	discardCtxErr      error
	purgeHashes        []string
	purgeErr           error
	resumeErr          error
	resumeHashes       []string
	files              []model.TorrentFile
	filesErr           error
	filesHashes        []string
	filesAt            []int
	entries            []*model.Entry
	addData            []byte
	setCommentErr      error
	setComments        []string
	exportData         []byte
	exportErr          error
	deleteEnabled      bool
	applyHashes        []string
	applyOpts          []model.ManagerAddFileOptions
	applyErr           error
	addDelay           time.Duration
	addDeadline        time.Time
	addHasDeadline     bool
	waitBlocks         bool
	filesByHash        map[string][]model.TorrentFile
	filesErrByHash     map[string]error
	freeBytes          int64
	freeErr            error
	freeSet            bool
	freePaths          []string
}

type existsAnswer struct {
	present bool
	err     error
}

func (s *stubClient) IncomingHashSameAs(buf *bytes.Buffer, magnet string, existing string) (bool, bool) {
	s.calls = append(s.calls, "incoming")
	if existing == "" {
		return false, false
	}
	return s.incomingSame, s.incomingKnown
}

func (s *stubClient) List(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error) {
	if s.entries != nil {
		return s.entries, nil
	}
	return nil, nil
}

func (s *stubClient) GetTorrentDetails(ctx context.Context, hash string) (string, string, error) {
	s.calls = append(s.calls, "details")
	return s.detName, s.detCom, s.detErr
}

func (s *stubClient) AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error {
	s.calls = append(s.calls, "add")
	s.addData = append([]byte(nil), buf.Bytes()...)
	s.addDeadline, s.addHasDeadline = ctx.Deadline()
	time.Sleep(s.addDelay)
	s.addOpts = opts
	s.addStopped = append(s.addStopped, opts.Stopped)
	if s.addCancel != nil {
		s.addCancel()
	}
	return s.addErr
}

func (s *stubClient) AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	s.calls = append(s.calls, "add")
	s.addOpts = opts
	s.addStopped = append(s.addStopped, opts.Stopped)
	if s.addCancel != nil {
		s.addCancel()
	}
	return s.addErr
}

func (s *stubClient) Delete(ctx context.Context, opts model.ManagerDeleteOptions) error {
	s.calls = append(s.calls, "delete")
	s.deleteOpts = append(s.deleteOpts, opts)
	if s.deleteCancel != nil {
		s.deleteCancel()
		return ctx.Err()
	}
	return s.deleteErr
}

func (s *stubClient) WriteBlackholePair(data []byte, magnet string, opts model.ManagerAddFileOptions) error {
	s.calls = append(s.calls, "blackhole-write")
	s.blackholeData = data
	s.blackholeOpts = opts
	if s.blackholeWriteHook != nil {
		s.blackholeWriteHook()
	}
	return s.blackholeWriteErr
}

func (s *stubClient) RemoveBlackholePair(hash string) {
	s.calls = append(s.calls, "blackhole-remove")
	s.removeHash = hash
}

func (s *stubClient) ConfirmSettled(ctx context.Context, hash string) bool {
	s.calls = append(s.calls, "confirm-settled")
	s.settleHash = hash
	return s.confirmSettled
}

func (s *stubClient) Exists(ctx context.Context, hash string) (bool, error) {
	s.calls = append(s.calls, "exists")
	if s.existsHonorsCtx && ctx.Err() != nil {
		return false, ctx.Err()
	}
	if seq := s.existsSeq[hash]; len(seq) > 0 {
		s.existsSeq[hash] = seq[1:]
		return seq[0].present, seq[0].err
	}
	if err, ok := s.existsErrByHash[hash]; ok {
		return false, err
	}
	if v, ok := s.existsByHash[hash]; ok {
		return v, nil
	}
	return s.exists, s.existsErr
}

func (s *stubClient) Discard(ctx context.Context, hash string) error {
	s.calls = append(s.calls, "discard")
	s.discardHashes = append(s.discardHashes, hash)
	s.discardCtxErr = ctx.Err()
	return s.discardErr
}

func (s *stubClient) Purge(ctx context.Context, hash string) error {
	s.calls = append(s.calls, "purge")
	s.purgeHashes = append(s.purgeHashes, hash)
	return s.purgeErr
}

func (s *stubClient) Resume(ctx context.Context, hash string) error {
	s.calls = append(s.calls, "resume")
	s.resumeHashes = append(s.resumeHashes, hash)
	return s.resumeErr
}

func (s *stubClient) Files(ctx context.Context, hash string) ([]model.TorrentFile, error) {
	s.filesHashes = append(s.filesHashes, hash)
	s.filesAt = append(s.filesAt, len(s.calls))
	if err, ok := s.filesErrByHash[hash]; ok {
		return nil, err
	}
	if files, ok := s.filesByHash[hash]; ok {
		return files, nil
	}
	return s.files, s.filesErr
}

func (s *stubClient) FreeSpace(ctx context.Context, path string) (int64, error) {
	s.freePaths = append(s.freePaths, path)
	if s.freeErr != nil {
		return 0, s.freeErr
	}
	if !s.freeSet {
		return 0, model.ErrFreeSpaceUnknown
	}
	return s.freeBytes, nil
}

func (s *stubClient) ReplacePending(hash string) bool {
	return s.pending[hash]
}

func (s *stubClient) DeletesFiles(ctx context.Context, opts model.ManagerDeleteOptions) bool {
	s.deletesFilesCalls++
	if len(s.deletesFilesSeq) > 0 {
		v := s.deletesFilesSeq[0]
		s.deletesFilesSeq = s.deletesFilesSeq[1:]
		return v
	}
	return s.deletesFiles
}

func (s *stubClient) WaitAbsent(ctx context.Context, hash string) (bool, error) {
	s.calls = append(s.calls, "wait-absent")
	s.waitHash = hash
	if s.waitBlocks {
		<-ctx.Done()
		return false, ctx.Err()
	}
	if s.waitCancel != nil {
		s.waitCancel()
	}
	return s.waitAbsent, s.waitAbsentErr
}

func buildTestTorrent(t *testing.T) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{
		PieceLength: 16384,
		Pieces:      make([]byte, 20),
		Name:        "test.txt",
		Length:      0,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{
		InfoBytes: infoBytes,
		Announce:  "http://tracker.example/announce",
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes(), mi.HashInfoBytes().HexString()
}

func withFastSettle(t *testing.T) {
	t.Helper()
	orig := blackhole.SettleDelay
	blackhole.SettleDelay = time.Millisecond
	t.Cleanup(func() { blackhole.SettleDelay = orig })
}

func deleteFirstStub() *stubClient {
	return &stubClient{incomingKnown: true, deletesFiles: true, waitAbsent: true, confirmSettled: true, existsByHash: map[string]bool{}, existsErrByHash: map[string]error{}, existsSeq: map[string][]existsAnswer{}, pending: map[string]bool{}}
}

func submitDeleteFirst(t *testing.T, stub *stubClient) (string, error) {
	t.Helper()
	return submitDeleteFirstCtx(t, context.Background(), stub)
}

func submitDeleteFirstCtx(t *testing.T, ctx context.Context, stub *stubClient) (string, error) {
	t.Helper()
	withFastSettle(t)
	data, newHash := buildTestTorrent(t)
	if _, ok := stub.existsErrByHash[newHash]; !ok {
		if _, ok = stub.existsByHash[newHash]; !ok {
			stub.existsByHash[newHash] = false
		}
	}
	err := NewManager(stub).Submit(ctx, bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: "oldhash", Name: "old name"})
	return newHash, err
}

func assertCalls(t *testing.T, stub *stubClient, want ...string) {
	t.Helper()
	if !equalStrings(stub.calls, want) {
		t.Errorf("call order = %v, want %v", stub.calls, want)
	}
}

func afterStoppedAdd(tail ...string) []string {
	return append([]string{"details", "incoming", "exists", "blackhole-write", "add"}, tail...)
}

func assertNoDiscardNoResume(t *testing.T, stub *stubClient) {
	t.Helper()
	if len(stub.discardHashes) != 0 || len(stub.resumeHashes) != 0 {
		t.Errorf("discard %v, resume %v, want none", stub.discardHashes, stub.resumeHashes)
	}
}

func TestSubmit_DeleteFirstAddsStoppedBeforeDelete(t *testing.T) {
	stub := deleteFirstStub()
	newHash, err := submitDeleteFirst(t, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCalls(t, stub, afterStoppedAdd("delete", "wait-absent", "resume", "confirm-settled", "blackhole-remove")...)
	if stub.blackholeOpts.Hash != newHash || stub.blackholeOpts.ReplacesHash != "oldhash" || stub.blackholeOpts.Stopped || len(stub.blackholeData) == 0 {
		t.Errorf("pair opts = %+v (data %d bytes), want Hash %q, ReplacesHash oldhash, Stopped false, torrent bytes", stub.blackholeOpts, len(stub.blackholeData), newHash)
	}
	if !equalBools(stub.addStopped, []bool{true}) || stub.addOpts.Hash != "oldhash" {
		t.Errorf("add stopped = %v, hash %q, want [true] and oldhash", stub.addStopped, stub.addOpts.Hash)
	}
	if stub.waitHash != "oldhash" {
		t.Errorf("wait hash = %q, want oldhash", stub.waitHash)
	}
	if !equalStrings(stub.resumeHashes, []string{newHash}) || len(stub.discardHashes) != 0 {
		t.Errorf("resume %v, discard %v, want resume [%s] and no discard", stub.resumeHashes, stub.discardHashes, newHash)
	}
	if stub.settleHash != newHash || stub.removeHash != newHash {
		t.Errorf("settle/remove hash = %q/%q, want %q", stub.settleHash, stub.removeHash, newHash)
	}
}

func TestSubmit_DeleteFirstAddErrorNewPresentDiscardsIt(t *testing.T) {
	stub := deleteFirstStub()
	_, newHash := buildTestTorrent(t)
	stub.addErr = fmt.Errorf("add failed")
	stub.existsSeq[newHash] = []existsAnswer{{}, {present: true}}
	if _, err := submitDeleteFirst(t, stub); err == nil || !strings.Contains(err.Error(), "add failed") {
		t.Fatalf("err = %v, want add failed", err)
	}
	assertCalls(t, stub, afterStoppedAdd("exists", "discard", "blackhole-remove")...)
	if !equalStrings(stub.discardHashes, []string{newHash}) || stub.removeHash != newHash {
		t.Errorf("discard %v, remove %q, want %s", stub.discardHashes, stub.removeHash, newHash)
	}
}

func TestSubmit_DeleteFirstAddErrorNewAbsentKeepsPair(t *testing.T) {
	// qbit добавляет асинхронно: отсутствие сразу после ошибки не значит, что
	// торрент не появится, такую пару разбирает подметальщик
	stub := deleteFirstStub()
	stub.addErr = fmt.Errorf("add failed")
	if _, err := submitDeleteFirst(t, stub); err == nil {
		t.Fatal("expected add error")
	}
	assertCalls(t, stub, afterStoppedAdd("exists")...)
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_DeleteFirstAddAlreadyAddedRemovesPairOnly(t *testing.T) {
	stub := deleteFirstStub()
	stub.addErr = fmt.Errorf("%w: race", model.ErrAlreadyAdded)
	newHash, err := submitDeleteFirst(t, stub)
	if !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("err = %v, want ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, afterStoppedAdd("blackhole-remove")...)
	assertNoDiscardNoResume(t, stub)
	if stub.removeHash != newHash {
		t.Errorf("remove hash = %q, want %q", stub.removeHash, newHash)
	}
}

func TestSubmit_DeleteFirstAddErrorProbeErrorKeepsPair(t *testing.T) {
	stub := deleteFirstStub()
	_, newHash := buildTestTorrent(t)
	stub.addErr = fmt.Errorf("add failed")
	stub.existsSeq[newHash] = []existsAnswer{{}, {err: fmt.Errorf("connection refused")}}
	if _, err := submitDeleteFirst(t, stub); err == nil || !strings.Contains(err.Error(), "add failed") {
		t.Fatalf("err = %v, want add failed", err)
	}
	assertCalls(t, stub, afterStoppedAdd("exists")...)
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_DeleteFirstCancelledDuringAddDiscardsNew(t *testing.T) {
	stub := deleteFirstStub()
	_, newHash := buildTestTorrent(t)
	ctx, cancel := context.WithCancel(context.Background())
	stub.addCancel = cancel
	stub.existsHonorsCtx = true
	stub.existsSeq[newHash] = []existsAnswer{{}, {present: true}}
	if _, err := submitDeleteFirstCtx(t, ctx, stub); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	assertCalls(t, stub, afterStoppedAdd("exists", "discard", "blackhole-remove")...)
	if !equalStrings(stub.discardHashes, []string{newHash}) || stub.discardCtxErr != nil {
		t.Errorf("discard %v (ctx err %v), want [%s] on a live probe ctx", stub.discardHashes, stub.discardCtxErr, newHash)
	}
}

func TestSubmit_DeleteFirstDeleteErrorKeepsPair(t *testing.T) {
	// старый мог уходить асинхронно, судьбу пары решает подметальщик двойной пробой.
	// Основной путь ничего не откатывает
	for name, oldPresent := range map[string]bool{"old present": true, "old absent": false} {
		t.Run(name, func(t *testing.T) {
			stub := deleteFirstStub()
			stub.deleteErr = fmt.Errorf("delete failed")
			stub.existsByHash["oldhash"] = oldPresent
			if _, err := submitDeleteFirst(t, stub); err == nil {
				t.Fatal("expected delete error")
			}
			assertCalls(t, stub, afterStoppedAdd("delete")...)
			assertNoDiscardNoResume(t, stub)
		})
	}
}

func TestSubmit_DeleteFirstDeleteCancelledKeepsPair(t *testing.T) {
	stub := deleteFirstStub()
	ctx, cancel := context.WithCancel(context.Background())
	stub.deleteCancel = cancel
	stub.existsHonorsCtx = true
	stub.existsByHash["oldhash"] = true
	if _, err := submitDeleteFirstCtx(t, ctx, stub); err == nil {
		t.Fatal("expected delete error")
	}
	assertCalls(t, stub, afterStoppedAdd("delete")...)
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_DeleteFirstOldStillPresentKeepsPair(t *testing.T) {
	stub := deleteFirstStub()
	stub.waitAbsent = false
	if _, err := submitDeleteFirst(t, stub); err == nil || !strings.Contains(err.Error(), "still present") {
		t.Fatalf("err = %v, want still present", err)
	}
	assertCalls(t, stub, afterStoppedAdd("delete", "wait-absent")...)
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_DeleteFirstWaitErrorKeepsPair(t *testing.T) {
	stub := deleteFirstStub()
	stub.waitAbsentErr = fmt.Errorf("connection refused")
	if _, err := submitDeleteFirst(t, stub); err == nil {
		t.Fatal("expected wait error")
	}
	assertCalls(t, stub, afterStoppedAdd("delete", "wait-absent")...)
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_DeleteFirstSettleCancelKeepsPair(t *testing.T) {
	stub := deleteFirstStub()
	ctx, cancel := context.WithCancel(context.Background())
	stub.waitCancel = cancel
	if _, err := submitDeleteFirstCtx(t, ctx, stub); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	assertCalls(t, stub, afterStoppedAdd("delete", "wait-absent")...)
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_DeleteFirstResumeErrorKeepsPair(t *testing.T) {
	stub := deleteFirstStub()
	stub.resumeErr = fmt.Errorf("no data found")
	_, err := submitDeleteFirst(t, stub)
	if !errors.Is(err, stub.resumeErr) || !strings.Contains(err.Error(), "start new torrent") {
		t.Fatalf("err = %v, want start new torrent wrapping resume error", err)
	}
	assertCalls(t, stub, afterStoppedAdd("delete", "wait-absent", "resume")...)
}

func TestSubmit_DeleteFirstUnsettledKeepsPairNoError(t *testing.T) {
	stub := deleteFirstStub()
	stub.confirmSettled = false
	if _, err := submitDeleteFirst(t, stub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCalls(t, stub, afterStoppedAdd("delete", "wait-absent", "resume", "confirm-settled")...)
}

func TestSubmit_DeleteFirstDiscardErrorKeepsPair(t *testing.T) {
	stub := deleteFirstStub()
	_, newHash := buildTestTorrent(t)
	stub.addErr = fmt.Errorf("add failed")
	stub.discardErr = fmt.Errorf("discard failed")
	stub.existsSeq[newHash] = []existsAnswer{{}, {present: true}}
	if _, err := submitDeleteFirst(t, stub); err == nil {
		t.Fatal("expected add error")
	}
	assertCalls(t, stub, afterStoppedAdd("exists", "discard")...)
}

func TestSubmit_DeleteFirstNewAlreadyPresentDeletesNothing(t *testing.T) {
	// старый и новый торренты раздачи уже оба в клиенте: удаление старого с
	// файлами снесло бы общие файлы нового
	stub := deleteFirstStub()
	_, newHash := buildTestTorrent(t)
	stub.existsByHash[newHash] = true
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("err = %v, want ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists")
}

func TestSubmit_DeleteFirstNewPresenceUnknownDeletesNothing(t *testing.T) {
	stub := deleteFirstStub()
	_, newHash := buildTestTorrent(t)
	stub.existsErrByHash[newHash] = fmt.Errorf("connection refused")
	if _, err := submitDeleteFirst(t, stub); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want connection refused", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists")
}

func TestSubmit_DeleteFirstCancelledBeforeAddRemovesPair(t *testing.T) {
	stub := deleteFirstStub()
	ctx, cancel := context.WithCancel(context.Background())
	stub.blackholeWriteHook = cancel
	newHash, err := submitDeleteFirstCtx(t, ctx, stub)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists", "blackhole-write", "blackhole-remove")
	if stub.removeHash != newHash {
		t.Errorf("remove hash = %q, want %q", stub.removeHash, newHash)
	}
}

func TestSubmit_DeleteFirstBlackholeWriteErrorAborts(t *testing.T) {
	stub := deleteFirstStub()
	stub.blackholeWriteErr = fmt.Errorf("disk full")
	if _, err := submitDeleteFirst(t, stub); err == nil {
		t.Fatal("expected blackhole write error")
	}
	assertCalls(t, stub, "details", "incoming", "exists", "blackhole-write")
}

func TestSubmit_ReplacePendingStopsDeleteFirst(t *testing.T) {
	stub := deleteFirstStub()
	_, newHash := buildTestTorrent(t)
	stub.pending[newHash] = true
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrReplacePending) {
		t.Fatalf("err = %v, want ErrReplacePending", err)
	}
	assertCalls(t, stub, "details", "incoming")
}

func TestSubmit_ReplacePendingStopsSameHash(t *testing.T) {
	data, newHash := buildTestTorrent(t)
	stub := &stubClient{incomingSame: true, incomingKnown: true, exists: true, pending: map[string]bool{newHash: true}}
	err := NewManager(stub).Submit(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: newHash})
	if !errors.Is(err, model.ErrReplacePending) {
		t.Fatalf("err = %v, want ErrReplacePending", err)
	}
	assertCalls(t, stub, "details", "incoming")
}

func TestSubmit_ReplacePendingStopsFilesKept(t *testing.T) {
	stub := deleteFirstStub()
	stub.deletesFiles = false
	_, newHash := buildTestTorrent(t)
	stub.pending[newHash] = true
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrReplacePending) {
		t.Fatalf("err = %v, want ErrReplacePending", err)
	}
	assertCalls(t, stub, "details", "incoming")
}

func TestSubmit_DifferentHashFilesKeptAddsBeforeDelete(t *testing.T) {
	stub := deleteFirstStub()
	stub.deletesFiles = false
	if _, err := submitDeleteFirst(t, stub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{"details", "incoming", "add", "delete"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", stub.calls, wantOrder)
	}
}

func TestSubmit_PreservesNameAndCommentFromExistingTorrent(t *testing.T) {
	stub := &stubClient{detName: "existing name", detCom: "existing comment", incomingSame: true, incomingKnown: true, confirmSettled: true}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "abc", Name: "new name"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stub.addOpts.Name != "existing name" {
		t.Errorf("expected name preserved from existing torrent, got %q", stub.addOpts.Name)
	}
	if stub.addOpts.Comment != "existing comment" {
		t.Errorf("expected comment filled from existing torrent, got %q", stub.addOpts.Comment)
	}
	wantOrder := []string{"details", "incoming", "exists", "blackhole-write", "delete", "add", "confirm-settled", "blackhole-remove"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", stub.calls, wantOrder)
	}
}

func TestSubmit_NoHashSkipsDetailsLookup(t *testing.T) {
	stub := &stubClient{}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Name: "some name", Comment: "some comment"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stub.addOpts.Name != "some name" || stub.addOpts.Comment != "some comment" {
		t.Errorf("expected caller-provided opts unchanged, got %+v", stub.addOpts)
	}
	wantOrder := []string{"incoming", "add", "delete"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", stub.calls, wantOrder)
	}
}

func TestSubmit_DifferentHashAddsBeforeDelete(t *testing.T) {
	stub := &stubClient{incomingSame: false, incomingKnown: true}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "oldhash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{"details", "incoming", "add", "delete"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", stub.calls, wantOrder)
	}
}

func TestSubmit_DifferentHashAddFailureSkipsDelete(t *testing.T) {
	stub := &stubClient{incomingSame: false, incomingKnown: true, addErr: fmt.Errorf("add failed")}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "oldhash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err == nil {
		t.Fatal("expected add error")
	}
	for _, call := range stub.calls {
		if call == "delete" {
			t.Fatalf("delete must not run when add fails, calls = %v", stub.calls)
		}
	}
}

func TestSubmit_SameHashBlackholesBeforeDeleteAndRemovesAfterAdd(t *testing.T) {
	// торрент с тем же хешем отсутствует в клиенте (пользователь удалил руками
	// или прошлый прогон не довел дело до конца): идем обычным защищенным путем
	stub := &stubClient{incomingSame: true, incomingKnown: true, confirmSettled: true, exists: false}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "samehash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{"details", "incoming", "exists", "blackhole-write", "delete", "add", "confirm-settled", "blackhole-remove"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v", stub.calls, wantOrder)
	}
}

func TestSubmit_SameHashPresentSkipsReplace(t *testing.T) {
	// торрент с тем же хешем уже есть в клиенте: перекачивать нечего, delete+add
	// заставили бы клиент впустую перепроверить весь контент на диске
	stub := &stubClient{incomingSame: true, incomingKnown: true, exists: true}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "samehash"}
	err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts)
	if !errors.Is(err, model.ErrHashUnchanged) {
		t.Fatalf("err = %v, want ErrHashUnchanged", err)
	}
	wantOrder := []string{"details", "incoming", "exists"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v (no delete, no add, no blackhole)", stub.calls, wantOrder)
	}
}

func TestSubmit_SameHashExistsErrorChangesNothing(t *testing.T) {
	// присутствие не выяснили: ничего не трогаем, следующий прогон повторит проверку
	stub := &stubClient{incomingSame: true, incomingKnown: true, confirmSettled: true, deletesFiles: true, existsErr: fmt.Errorf("connection refused")}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "samehash"}
	err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want the Exists error", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists")
}

func TestSubmit_SameHashAbsentDeletesWithoutFiles(t *testing.T) {
	stub := &stubClient{incomingSame: true, incomingKnown: true, confirmSettled: true, exists: false, deletesFiles: true}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "samehash", Category: "movies"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists", "blackhole-write", "delete", "add", "confirm-settled", "blackhole-remove")
	if len(stub.deleteOpts) != 1 || stub.deleteOpts[0].DeleteFiles {
		t.Errorf("delete opts = %+v, want one delete without files", stub.deleteOpts)
	}
}

func TestSubmit_DeleteFirstPassesDecision(t *testing.T) {
	stub := deleteFirstStub()
	if _, err := submitDeleteFirst(t, stub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.deleteOpts) != 1 || !stub.deleteOpts[0].DeleteFiles {
		t.Errorf("delete opts = %+v, want one delete with files", stub.deleteOpts)
	}
}

func TestSubmit_FilesKeptUsesTheSingleDecision(t *testing.T) {
	// второй ответ DeletesFiles отличается: удаление должно взять первый, по нему выбран путь
	stub := deleteFirstStub()
	stub.deletesFilesSeq = []bool{false, true}
	if _, err := submitDeleteFirst(t, stub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCalls(t, stub, "details", "incoming", "add", "delete")
	if stub.deletesFilesCalls != 1 {
		t.Errorf("DeletesFiles calls = %d, want 1", stub.deletesFilesCalls)
	}
	if len(stub.deleteOpts) != 1 || stub.deleteOpts[0].DeleteFiles {
		t.Errorf("delete opts = %+v, want one delete without files", stub.deleteOpts)
	}
}

func TestSubmit_SameHashUnconfirmedSettleLeavesBlackholePairNoError(t *testing.T) {
	stub := &stubClient{incomingSame: true, incomingKnown: true, confirmSettled: false}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "samehash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{"details", "incoming", "exists", "blackhole-write", "delete", "add", "confirm-settled"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v (unsettled: pair left on disk, no remove)", stub.calls, wantOrder)
	}
}

func TestSubmit_SameHashBlackholeWriteFailureAbortsReplace(t *testing.T) {
	stub := &stubClient{incomingSame: true, incomingKnown: true, blackholeWriteErr: fmt.Errorf("disk full")}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "samehash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err == nil {
		t.Fatal("expected blackhole write error")
	}
	wantOrder := []string{"details", "incoming", "exists", "blackhole-write"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v (no delete, no add)", stub.calls, wantOrder)
	}
}

func TestSubmit_SameHashAddFailureLeavesBlackholePair(t *testing.T) {
	stub := &stubClient{incomingSame: true, incomingKnown: true, addErr: fmt.Errorf("add failed")}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "samehash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err == nil {
		t.Fatal("expected add error")
	}
	wantOrder := []string{"details", "incoming", "exists", "blackhole-write", "delete", "add"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v (pair left on disk, no remove)", stub.calls, wantOrder)
	}
}

func TestSubmit_DifferentHashWritesNoBlackholePair(t *testing.T) {
	stub := &stubClient{incomingSame: false, incomingKnown: true}
	m := NewManager(stub)

	opts := model.ManagerAddFileOptions{Hash: "oldhash"}
	if err := m.Submit(context.Background(), bytes.NewBufferString("data"), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantOrder := []string{"details", "incoming", "add", "delete"}
	if !equalStrings(stub.calls, wantOrder) {
		t.Errorf("call order = %v, want %v (add-first branch never blackholes)", stub.calls, wantOrder)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalBools(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSubmit_ReplacePendingForOwnHashStops(t *testing.T) {
	// запись сама может быть остановленным новым торрентом незаконченной замены
	stub := deleteFirstStub()
	stub.pending["oldhash"] = true
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrReplacePending) {
		t.Fatalf("err = %v, want ErrReplacePending", err)
	}
	assertCalls(t, stub, "details", "incoming")
}

func partialSelection() []model.TorrentFile {
	return []model.TorrentFile{{Path: "R/a", Wanted: true}, {Path: "R/b", Wanted: false}}
}

func TestSubmit_DeleteFirstCarriesOldFileSelection(t *testing.T) {
	stub := deleteFirstStub()
	stub.files = partialSelection()
	if _, err := submitDeleteFirst(t, stub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCalls(t, stub, afterStoppedAdd("delete", "wait-absent", "resume", "confirm-settled", "blackhole-remove")...)
	if !equalStrings(stub.filesHashes, []string{"oldhash"}) || !slices.Equal(stub.filesAt, []int{2}) {
		t.Errorf("files read hashes %v at %v, want [oldhash] at [2]", stub.filesHashes, stub.filesAt)
	}
	if !slices.Equal(stub.blackholeOpts.FileSelection, stub.files) || !slices.Equal(stub.addOpts.FileSelection, stub.files) {
		t.Errorf("pair selection %v, add selection %v, want %v", stub.blackholeOpts.FileSelection, stub.addOpts.FileSelection, stub.files)
	}
	if !equalBools(stub.addStopped, []bool{true}) {
		t.Errorf("add stopped = %v, want [true]", stub.addStopped)
	}
}

func TestSubmit_FilesKeptCarriesOldFileSelection(t *testing.T) {
	stub := deleteFirstStub()
	stub.deletesFiles = false
	stub.files = partialSelection()
	if _, err := submitDeleteFirst(t, stub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists", "blackhole-write", "add", "blackhole-remove", "delete")
	if !equalStrings(stub.filesHashes, []string{"oldhash"}) || !slices.Equal(stub.addOpts.FileSelection, stub.files) {
		t.Errorf("files read %v, add selection %v, want [oldhash] and %v", stub.filesHashes, stub.addOpts.FileSelection, stub.files)
	}
}

func TestSubmit_AllWantedOldSelectionNotCarried(t *testing.T) {
	stub := deleteFirstStub()
	stub.files = []model.TorrentFile{{Path: "R/a", Wanted: true}, {Path: "R/b", Wanted: true}}
	if _, err := submitDeleteFirst(t, stub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalStrings(stub.filesHashes, []string{"oldhash"}) {
		t.Errorf("files read %v, want [oldhash]", stub.filesHashes)
	}
	if stub.blackholeOpts.FileSelection != nil || stub.addOpts.FileSelection != nil {
		t.Errorf("pair selection %v, add selection %v, want nil", stub.blackholeOpts.FileSelection, stub.addOpts.FileSelection)
	}
}

func TestSubmit_FileSelectionReadErrorAbortsBeforeChanges(t *testing.T) {
	stub := deleteFirstStub()
	stub.filesErr = fmt.Errorf("connection refused")
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, stub.filesErr) {
		t.Fatalf("err = %v, want %v", err, stub.filesErr)
	}
	assertCalls(t, stub, "details", "incoming")
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_FileSelectionNotReadOffReplace(t *testing.T) {
	cases := []struct {
		name   string
		submit func(t *testing.T, stub *stubClient) error
	}{
		{"same hash", func(t *testing.T, stub *stubClient) error {
			stub.incomingSame = true
			return NewManager(stub).Submit(context.Background(), bytes.NewBufferString("data"), model.ManagerAddFileOptions{Hash: "samehash"})
		}},
		{"no old hash", func(t *testing.T, stub *stubClient) error {
			data, _ := buildTestTorrent(t)
			return NewManager(stub).Submit(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := deleteFirstStub()
			stub.files = partialSelection()
			if err := tc.submit(t, stub); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(stub.filesHashes) != 0 || stub.addOpts.FileSelection != nil {
				t.Errorf("files read %v, add selection %v, want no read and nil", stub.filesHashes, stub.addOpts.FileSelection)
			}
		})
	}
}

func TestSubmit_FilesKeptAddErrorWithSelectionDiscardsNew(t *testing.T) {
	cases := []struct {
		name      string
		files     []model.TorrentFile
		addErr    error
		wantCalls []string
		discard   bool
		newSeq    []existsAnswer
	}{
		{"selection", partialSelection(), fmt.Errorf("add failed"), []string{"details", "incoming", "exists", "blackhole-write", "add", "exists", "discard", "blackhole-remove"}, true, []existsAnswer{{present: false}, {present: true}}},
		{"no selection", nil, fmt.Errorf("add failed"), []string{"details", "incoming", "add"}, false, nil},
		{"selection already added", partialSelection(), model.ErrAlreadyAdded, []string{"details", "incoming", "exists"}, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := deleteFirstStub()
			stub.deletesFiles = false
			stub.files = tc.files
			stub.addErr = tc.addErr
			_, newHash := buildTestTorrent(t)
			stub.existsByHash[newHash] = true
			stub.existsSeq[newHash] = tc.newSeq
			if _, err := submitDeleteFirst(t, stub); !errors.Is(err, tc.addErr) {
				t.Fatalf("err = %v, want %v", err, tc.addErr)
			}
			assertCalls(t, stub, tc.wantCalls...)
			if tc.discard != equalStrings(stub.discardHashes, []string{newHash}) {
				t.Errorf("discard %v, want discard of new %v", stub.discardHashes, tc.discard)
			}
		})
	}
}

func TestSubmit_AddFirstAlreadyAddedDropsOldKeepingFiles(t *testing.T) {
	// новый уже в клиенте (поздний add или упавшее удаление старого): старый уходит без файлов
	stub := deleteFirstStub()
	stub.deletesFiles = false
	stub.addErr = fmt.Errorf("%w: race", model.ErrAlreadyAdded)
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("err = %v, want ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, "details", "incoming", "add", "delete")
	if len(stub.deleteOpts) != 1 || stub.deleteOpts[0].DeleteFiles || stub.deleteOpts[0].Hash != "oldhash" {
		t.Errorf("delete opts = %+v, want one delete of oldhash without files", stub.deleteOpts)
	}
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_AddFirstAlreadyAddedDeleteErrorSurfaces(t *testing.T) {
	stub := deleteFirstStub()
	stub.deletesFiles = false
	stub.addErr = fmt.Errorf("%w: race", model.ErrAlreadyAdded)
	stub.deleteErr = fmt.Errorf("connection refused")
	_, err := submitDeleteFirst(t, stub)
	if errors.Is(err, model.ErrAlreadyAdded) || !errors.Is(err, stub.deleteErr) {
		t.Fatalf("err = %v, want delete error without ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, "details", "incoming", "add", "delete")
}

func TestSubmit_AddFirstAlreadyAddedUnknownOrEqualHashUntouched(t *testing.T) {
	data, dataHash := buildTestTorrent(t)
	cases := []struct {
		name string
		data []byte
		hash string
	}{
		{"hash unknown", []byte("data"), "oldhash"},
		{"equal hash", data, strings.ToUpper(dataHash)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubClient{incomingKnown: false, addErr: fmt.Errorf("%w: race", model.ErrAlreadyAdded)}
			err := NewManager(stub).Submit(context.Background(), bytes.NewBuffer(tc.data), model.ManagerAddFileOptions{Hash: tc.hash})
			if !errors.Is(err, model.ErrAlreadyAdded) {
				t.Fatalf("err = %v, want ErrAlreadyAdded", err)
			}
			assertCalls(t, stub, "details", "incoming", "add")
		})
	}
}

func TestSubmit_SelectionProbeErrorAddsNothing(t *testing.T) {
	stub := deleteFirstStub()
	stub.deletesFiles = false
	stub.files = partialSelection()
	_, newHash := buildTestTorrent(t)
	probeErr := fmt.Errorf("connection refused")
	stub.existsErrByHash[newHash] = probeErr
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, probeErr) {
		t.Fatalf("err = %v, want %v", err, probeErr)
	}
	assertCalls(t, stub, "details", "incoming", "exists")
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_SelectionNewAlreadyPresentKeepsIt(t *testing.T) {
	// найденный новый мог остаться остановленным и без выбора файлов (поздний add,
	// сбой Discard в клиенте): старый должен раздавать дальше
	stub := deleteFirstStub()
	stub.deletesFiles = false
	stub.files = partialSelection()
	_, newHash := buildTestTorrent(t)
	stub.existsByHash[newHash] = true
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("err = %v, want ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists")
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_SelectionAlreadyAddedOnAddKeepsOld(t *testing.T) {
	stub := deleteFirstStub()
	stub.deletesFiles = false
	stub.files = partialSelection()
	stub.addErr = fmt.Errorf("%w: race", model.ErrAlreadyAdded)
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("err = %v, want ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists", "blackhole-write", "add", "blackhole-remove")
	assertNoDiscardNoResume(t, stub)
}

func (s *stubClient) ApplySelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	s.calls = append(s.calls, "apply")
	s.applyHashes = append(s.applyHashes, hash)
	s.applyOpts = append(s.applyOpts, opts)
	return s.applyErr
}

func withReplaceBudgets(t *testing.T, add, start time.Duration) {
	t.Helper()
	origAdd, origStart := replaceAddBudget, replaceStartBudget
	replaceAddBudget, replaceStartBudget = add, start
	t.Cleanup(func() { replaceAddBudget, replaceStartBudget = origAdd, origStart })
}

func TestSubmit_DeleteFirstAddHasBudget(t *testing.T) {
	withReplaceBudgets(t, 50*time.Millisecond, time.Minute)
	stub := deleteFirstStub()
	before := time.Now()
	if _, err := submitDeleteFirst(t, stub); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stub.addHasDeadline || stub.addDeadline.After(before.Add(50*time.Millisecond+10*time.Millisecond)) {
		t.Errorf("add deadline %v (set %v), want at most 50ms after %v", stub.addDeadline, stub.addHasDeadline, before)
	}
}

func TestSubmit_DeleteFirstSlowAddKeepsOld(t *testing.T) {
	withReplaceBudgets(t, 50*time.Millisecond, time.Minute)
	stub := deleteFirstStub()
	stub.addDelay = 100 * time.Millisecond
	_, newHash := buildTestTorrent(t)
	stub.existsSeq[newHash] = []existsAnswer{{present: false}, {present: true}}
	_, err := submitDeleteFirst(t, stub)
	if err == nil || !strings.Contains(err.Error(), "exceeded 50ms") {
		t.Fatalf("err = %v, want budget error", err)
	}
	assertCalls(t, stub, afterStoppedAdd("exists", "discard", "blackhole-remove")...)
	if len(stub.deleteOpts) != 0 || !equalStrings(stub.discardHashes, []string{newHash}) {
		t.Errorf("delete %+v, discard %v, want no delete and discard of new %s", stub.deleteOpts, stub.discardHashes, newHash)
	}
}

func TestSubmit_SelectionLateAddLeavesReplacePair(t *testing.T) {
	stub := deleteFirstStub()
	stub.deletesFiles = false
	stub.files = partialSelection()
	stub.addErr = fmt.Errorf("torrent add failed (not present after add)")
	newHash, err := submitDeleteFirst(t, stub)
	if !errors.Is(err, stub.addErr) {
		t.Fatalf("err = %v, want %v", err, stub.addErr)
	}
	assertCalls(t, stub, afterStoppedAdd("exists")...)
	o := stub.blackholeOpts
	if o.Hash != newHash || o.ReplacesHash != "oldhash" || o.Stopped || !slices.Equal(o.FileSelection, partialSelection()) {
		t.Errorf("pair opts %+v, want hash %s replacing oldhash, not stopped, old selection", o, newHash)
	}
	if len(stub.deleteOpts) != 0 {
		t.Errorf("delete %+v, want none", stub.deleteOpts)
	}
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_SelectionAddOverBudgetRollsBack(t *testing.T) {
	withReplaceBudgets(t, 50*time.Millisecond, time.Minute)
	stub := deleteFirstStub()
	stub.deletesFiles = false
	stub.files = partialSelection()
	stub.addDelay = 100 * time.Millisecond
	_, newHash := buildTestTorrent(t)
	stub.existsSeq[newHash] = []existsAnswer{{present: false}, {present: true}}
	before := time.Now()
	_, err := submitDeleteFirst(t, stub)
	if err == nil || !strings.Contains(err.Error(), "exceeded 50ms") {
		t.Fatalf("err = %v, want budget error", err)
	}
	assertCalls(t, stub, afterStoppedAdd("exists", "discard", "blackhole-remove")...)
	if len(stub.deleteOpts) != 0 || !equalStrings(stub.discardHashes, []string{newHash}) {
		t.Errorf("delete %+v, discard %v, want no delete and discard of new %s", stub.deleteOpts, stub.discardHashes, newHash)
	}
	if !stub.addHasDeadline || stub.addDeadline.After(before.Add(50*time.Millisecond+10*time.Millisecond)) {
		t.Errorf("add deadline %v (set %v), want at most 50ms after %v", stub.addDeadline, stub.addHasDeadline, before)
	}
}

func TestSubmit_DeleteFirstFinishPhaseBounded(t *testing.T) {
	withReplaceBudgets(t, 50*time.Millisecond, 50*time.Millisecond)
	stub := deleteFirstStub()
	stub.waitBlocks = true
	done := make(chan error, 1)
	go func() {
		_, err := submitDeleteFirst(t, stub)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("finish phase not bounded by the start budget")
	}
	assertCalls(t, stub, afterStoppedAdd("delete", "wait-absent")...)
	assertNoDiscardNoResume(t, stub)
}

func selectionConvergeStub(t *testing.T, newEntry *model.Entry) (*stubClient, string) {
	t.Helper()
	stub := deleteFirstStub()
	stub.deletesFiles = false
	stub.files = partialSelection()
	_, newHash := buildTestTorrent(t)
	stub.entries = []*model.Entry{{Hash: "oldhash", Name: "old name"}}
	if newEntry != nil {
		newEntry.Hash = strings.ToUpper(newHash)
		stub.entries = append(stub.entries, newEntry)
	}
	return stub, newHash
}

func assertConverged(t *testing.T, stub *stubClient, newHash string) {
	t.Helper()
	if !equalStrings(stub.applyHashes, []string{newHash}) || !slices.Equal(stub.applyOpts[0].FileSelection, stub.files) {
		t.Errorf("apply %v %+v, want new %s with the old selection", stub.applyHashes, stub.applyOpts, newHash)
	}
	if len(stub.deleteOpts) != 1 || stub.deleteOpts[0].Hash != "oldhash" || stub.deleteOpts[0].DeleteFiles {
		t.Errorf("delete opts = %+v, want one delete of oldhash without files", stub.deleteOpts)
	}
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_SelectionStoppedNewConverges(t *testing.T) {
	stub, newHash := selectionConvergeStub(t, &model.Entry{Stopped: true})
	stub.existsByHash[newHash] = true
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("err = %v, want ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists", "apply", "delete")
	assertConverged(t, stub, newHash)
}

func TestSubmit_SelectionAlreadyAddedOnAddConverges(t *testing.T) {
	stub, newHash := selectionConvergeStub(t, &model.Entry{Stopped: true})
	stub.addErr = fmt.Errorf("%w: race", model.ErrAlreadyAdded)
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("err = %v, want ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists", "blackhole-write", "add", "blackhole-remove", "apply", "delete")
	assertConverged(t, stub, newHash)
}

func TestSubmit_SelectionConvergeApplyErrorKeepsOld(t *testing.T) {
	stub, newHash := selectionConvergeStub(t, &model.Entry{Stopped: true})
	stub.existsByHash[newHash] = true
	stub.applyErr = fmt.Errorf("connection refused")
	_, err := submitDeleteFirst(t, stub)
	if !errors.Is(err, stub.applyErr) || errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("err = %v, want apply error", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists", "apply")
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_SelectionConvergeGuards(t *testing.T) {
	cases := []struct {
		name  string
		entry *model.Entry
	}{
		{"running", &model.Entry{}},
		{"unhealthy", &model.Entry{Stopped: true, Unhealthy: true}},
		{"absent", nil},
	}
	for _, tc := range cases {
		for _, onAdd := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/onAdd=%v", tc.name, onAdd), func(t *testing.T) {
				stub, newHash := selectionConvergeStub(t, tc.entry)
				// запущенный новый качает все файлы: выбор старого не совпадает
				stub.filesByHash = map[string][]model.TorrentFile{newHash: {{Path: "R/a", Wanted: true}, {Path: "R/b", Wanted: true}}}
				want := []string{"details", "incoming", "exists"}
				if onAdd {
					stub.addErr = fmt.Errorf("%w: race", model.ErrAlreadyAdded)
					want = append(want, "blackhole-write", "add", "blackhole-remove")
				} else {
					stub.existsByHash[newHash] = true
				}
				if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrAlreadyAdded) {
					t.Fatalf("err = %v, want ErrAlreadyAdded", err)
				}
				assertCalls(t, stub, want...)
				if len(stub.applyHashes) != 0 || len(stub.deleteOpts) != 0 {
					t.Errorf("apply %v, delete %+v, want none", stub.applyHashes, stub.deleteOpts)
				}
				assertNoDiscardNoResume(t, stub)
			})
		}
	}
}

func TestSubmit_SelectionConvergeRetriesFailedDrop(t *testing.T) {
	newEntry := &model.Entry{Stopped: true}
	stub, newHash := selectionConvergeStub(t, newEntry)
	stub.existsByHash[newHash] = true
	stub.deleteErr = fmt.Errorf("connection refused")
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, stub.deleteErr) {
		t.Fatalf("first pass err = %v, want delete error", err)
	}
	// ApplySelection запустил новый, а удаление старого сорвалось:
	// следующий прогон только удаляет старый без файлов
	newEntry.Stopped = false
	stub.deleteErr = nil
	stub.calls, stub.applyHashes, stub.applyOpts, stub.deleteOpts = nil, nil, nil, nil
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("second pass err = %v, want ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists", "delete")
	if len(stub.applyHashes) != 0 || len(stub.deleteOpts) != 1 || stub.deleteOpts[0].Hash != "oldhash" || stub.deleteOpts[0].DeleteFiles {
		t.Errorf("apply %v, delete opts %+v, want no apply and one delete of oldhash without files", stub.applyHashes, stub.deleteOpts)
	}
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_SelectionRunningNewAllUnwantedDropsOld(t *testing.T) {
	stub, newHash := selectionConvergeStub(t, &model.Entry{})
	all := []model.TorrentFile{{Path: "R/a"}, {Path: "R/b"}}
	stub.files = all
	stub.filesByHash = map[string][]model.TorrentFile{newHash: all}
	stub.existsByHash[newHash] = true
	if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("err = %v, want ErrAlreadyAdded", err)
	}
	assertCalls(t, stub, "details", "incoming", "exists", "delete")
	if len(stub.applyHashes) != 0 || len(stub.deleteOpts) != 1 || stub.deleteOpts[0].Hash != "oldhash" || stub.deleteOpts[0].DeleteFiles {
		t.Errorf("apply %v, delete opts %+v, want no apply and one delete of oldhash without files", stub.applyHashes, stub.deleteOpts)
	}
	assertNoDiscardNoResume(t, stub)
}

func TestSubmit_SelectionRunningNewKeepsOldUnlessMatching(t *testing.T) {
	cases := []struct {
		name     string
		files    []model.TorrentFile
		filesErr error
	}{
		{"different selection", []model.TorrentFile{{Path: "R/a", Wanted: false}, {Path: "R/b", Wanted: true}}, nil},
		{"files error", nil, fmt.Errorf("connection refused")},
		{"empty file list", []model.TorrentFile{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub, newHash := selectionConvergeStub(t, &model.Entry{})
			stub.existsByHash[newHash] = true
			stub.filesByHash = map[string][]model.TorrentFile{newHash: tc.files}
			if tc.filesErr != nil {
				stub.filesErrByHash = map[string]error{newHash: tc.filesErr}
			}
			if _, err := submitDeleteFirst(t, stub); !errors.Is(err, model.ErrAlreadyAdded) {
				t.Fatalf("err = %v, want ErrAlreadyAdded", err)
			}
			assertCalls(t, stub, "details", "incoming", "exists")
			if len(stub.applyHashes) != 0 || len(stub.deleteOpts) != 0 {
				t.Errorf("apply %v, delete %+v, want none", stub.applyHashes, stub.deleteOpts)
			}
		})
	}
}
