package blackhole

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
)

func buildNamedTestTorrent(t *testing.T, name string) ([]byte, string) {
	t.Helper()
	// одно имя дает один infohash, а тестам подметания нужны разные пары сразу
	info := metainfo.Info{
		PieceLength: 16384,
		Pieces:      make([]byte, 20),
		Name:        name,
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

type fakeClient struct {
	mu          sync.Mutex
	present     map[string]bool
	addFail     bool
	unreachable bool
	// следующий Exists, увидевший этот хеш, сразу убирает его из present
	vanishHash string
	calls      []string
	existsSeq  map[string][]bool
	existsErr  map[string]error
	discardErr error
	resumeErr  error
	addOpts    []model.ManagerAddFileOptions
}

func newFakeClient() *fakeClient {
	return &fakeClient{present: map[string]bool{}, existsSeq: map[string][]bool{}, existsErr: map[string]error{}}
}

func (f *fakeClient) markPresent(hash string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.present[strings.ToLower(hash)] = true
}

func (f *fakeClient) vanishAfterFirstQuery(hash string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vanishHash = strings.ToLower(hash)
}

func (f *fakeClient) Exists(_ context.Context, hash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hash = strings.ToLower(hash)
	f.calls = append(f.calls, "exists:"+hash)
	if err := f.existsErr[hash]; err != nil {
		return false, err
	}
	if seq := f.existsSeq[hash]; len(seq) > 0 {
		f.existsSeq[hash] = seq[1:]
		return seq[0], nil
	}
	present := f.present[hash]
	if present && f.vanishHash == hash {
		delete(f.present, hash)
	}
	return present, nil
}

func (f *fakeClient) Reachable(_ context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.unreachable
}

func (f *fakeClient) add(ctx context.Context, hash string) error {
	// как qbittorrent.Client.addTorrentVerified: Exists до и после добавления. Так vanishHash
	// задевает оба шага, как асинхроное удаление в живом клиенте
	hash = strings.ToLower(hash)
	present, _ := f.Exists(ctx, hash)
	if present {
		return model.ErrAlreadyAdded
	}
	f.mu.Lock()
	fail := f.addFail
	f.mu.Unlock()
	if fail {
		return fmt.Errorf("simulated add failure")
	}
	f.mu.Lock()
	f.present[hash] = true
	f.mu.Unlock()
	stillPresent, _ := f.Exists(ctx, hash)
	if !stillPresent {
		return fmt.Errorf("torrent add failed (not present after add)")
	}
	return nil
}

func (f *fakeClient) AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error {
	f.recordAdd(opts)
	hash, err := torrentmeta.InfoHash(buf.Bytes())
	if err != nil {
		return err
	}
	return f.add(ctx, hash)
}

func (f *fakeClient) AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	f.recordAdd(opts)
	hash, ok := torrentmeta.BtihFromMagnet(magnet)
	if !ok {
		hash = opts.Hash
	}
	return f.add(ctx, hash)
}

func (f *fakeClient) recordAdd(opts model.ManagerAddFileOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "add:"+strings.ToLower(opts.Hash))
	f.addOpts = append(f.addOpts, opts)
}

func (f *fakeClient) Discard(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	hash = strings.ToLower(hash)
	f.calls = append(f.calls, "discard:"+hash)
	if f.discardErr != nil {
		return f.discardErr
	}
	delete(f.present, hash)
	return nil
}

func (f *fakeClient) Resume(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "resume:"+strings.ToLower(hash))
	return f.resumeErr
}

func (f *fakeClient) callsOf(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func writePair(t *testing.T, dir string, data []byte, opts model.ManagerAddFileOptions) {
	t.Helper()
	s := New(dir, newFakeClient())
	if err := s.Write(data, "", opts); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ageJSON(t, dir, opts.Hash, time.Hour)
}

func writeMagnetPair(t *testing.T, dir, magnet string, opts model.ManagerAddFileOptions) {
	t.Helper()
	s := New(dir, newFakeClient())
	if err := s.Write(nil, magnet, opts); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ageJSON(t, dir, opts.Hash, time.Hour)
}

func assertJSONGone(t *testing.T, dir, hash string) {
	t.Helper()
	_, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("expected json file removed, stat err = %v", err)
	}
}

func assertJSONRemains(t *testing.T, dir, hash string) {
	t.Helper()
	_, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("expected json file to remain: %v", err)
	}
}

func assertPairGone(t *testing.T, dir, hash string) {
	t.Helper()
	torrentPath, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("expected torrent file removed, stat err = %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("expected json file removed, stat err = %v", err)
	}
}

func assertPairRemains(t *testing.T, dir, hash string) {
	t.Helper()
	torrentPath, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(torrentPath); err != nil {
		t.Errorf("expected torrent file to remain: %v", err)
	}
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("expected json file to remain: %v", err)
	}
}

func withFastSettle(t *testing.T) {
	t.Helper()
	orig := SettleDelay
	SettleDelay = 10 * time.Millisecond
	t.Cleanup(func() { SettleDelay = orig })
}

func TestWriteBlackholePair_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, newFakeClient())
	data, hash := buildNamedTestTorrent(t, "one.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "one", Category: "movies"}

	if err := s.Write(data, "", opts); err != nil {
		t.Fatalf("Write: %v", err)
	}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	got, err := os.ReadFile(torrentPath)
	if err != nil {
		t.Fatalf("read torrent file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("torrent bytes mismatch")
	}
	rawJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json file: %v", err)
	}
	var gotOpts model.ManagerAddFileOptions
	if err = json.Unmarshal(rawJSON, &gotOpts); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if !reflect.DeepEqual(gotOpts, opts) {
		t.Errorf("opts mismatch: got %+v, want %+v", gotOpts, opts)
	}
}

func TestWriteBlackholePair_NoDirConfigured(t *testing.T) {
	s := New("", newFakeClient())
	data, hash := buildNamedTestTorrent(t, "two.txt")
	if err := s.Write(data, "", model.ManagerAddFileOptions{Hash: hash}); err == nil {
		t.Fatal("expected error when BlackholeDir is not configured")
	}
}

func TestRemoveBlackholePair_DeletesBothFiles(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, newFakeClient())
	data, hash := buildNamedTestTorrent(t, "three.txt")
	if err := s.Write(data, "", model.ManagerAddFileOptions{Hash: hash}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	s.Remove(hash)

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("expected torrent file removed, stat err = %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("expected json file removed, stat err = %v", err)
	}
}

func TestWriteBlackholePair_MagnetOnly_CreatesJSONOnlyWithMagnetField(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, newFakeClient())
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	magnet := "magnet:?xt=urn:btih:" + hash
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "magnet-item", Category: "movies"}

	if err := s.Write(nil, magnet, opts); err != nil {
		t.Fatalf("Write: %v", err)
	}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("expected no .torrent file for a magnet-only pair, stat err = %v", err)
	}
	rawJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json file: %v", err)
	}
	var gotMeta blackholeMeta
	if err = json.Unmarshal(rawJSON, &gotMeta); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if gotMeta.Magnet != magnet {
		t.Errorf("magnet mismatch: got %q, want %q", gotMeta.Magnet, magnet)
	}
	if !reflect.DeepEqual(gotMeta.ManagerAddFileOptions, opts) {
		t.Errorf("opts mismatch: got %+v, want %+v", gotMeta.ManagerAddFileOptions, opts)
	}
}

func TestWriteBlackholePair_NoDataNoMagnet_ErrorsAndCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, newFakeClient())
	hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	if err := s.Write(nil, "", model.ManagerAddFileOptions{Hash: hash}); err == nil {
		t.Fatal("expected error when there is neither data nor magnet to protect")
	}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("expected no .torrent file, stat err = %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("expected no .json file, stat err = %v", err)
	}
}

func TestSweepBlackholeOnce_SuccessRemovesPair(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)

	data, hash := buildNamedTestTorrent(t, "success.txt")
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "success"})

	s.SweepOnce(context.Background())

	assertPairGone(t, dir, hash)
}

func TestSweepBlackholeOnce_ErrorLeavesPair(t *testing.T) {
	dir := t.TempDir()
	fc := newFakeClient()
	fc.addFail = true
	s := New(dir, fc)

	data, hash := buildNamedTestTorrent(t, "fail.txt")
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "fail"})

	s.SweepOnce(context.Background())

	assertPairRemains(t, dir, hash)
}

func TestSweepBlackholeOnce_AlreadyAddedRemovesPair(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)

	data, hash := buildNamedTestTorrent(t, "already.txt")
	fc.markPresent(hash)
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "already"})

	s.SweepOnce(context.Background())

	assertPairGone(t, dir, hash)
}

func TestSweepBlackholeOnce_TorrentWithoutJSONSkipped(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, newFakeClient())

	data, hash := buildNamedTestTorrent(t, "orphan.txt")
	torrentPath, _ := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	s.SweepOnce(context.Background())

	if _, err := os.Stat(torrentPath); err != nil {
		t.Errorf("expected orphan .torrent file to remain: %v", err)
	}
}

func TestSweepBlackholeOnce_ForeignExtensionUntouched(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, newFakeClient())

	foreignPath := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(foreignPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}

	s.SweepOnce(context.Background())

	if _, err := os.Stat(foreignPath); err != nil {
		t.Errorf("expected foreign file to remain untouched: %v", err)
	}
}

func TestSweepBlackholeOnce_UnreachableSkipsEntirely(t *testing.T) {
	dir := t.TempDir()
	fc := newFakeClient()
	fc.unreachable = true
	s := New(dir, fc)

	data, hash := buildNamedTestTorrent(t, "down.txt")
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "down"})

	s.SweepOnce(context.Background())

	assertPairRemains(t, dir, hash)
}

func TestSweepBlackhole_StopsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, newFakeClient())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Sweep(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Sweep did not stop after context cancellation")
	}
}

func TestSweepBlackholeOnce_MagnetPair_ReAddsAndRemovesJSON(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)

	hash := "cccccccccccccccccccccccccccccccccccccccc"
	magnet := "magnet:?xt=urn:btih:" + hash
	writeMagnetPair(t, dir, magnet, model.ManagerAddFileOptions{Hash: hash, Name: "magnet-item"})

	s.SweepOnce(context.Background())

	assertJSONGone(t, dir, hash)
}

func TestSweepBlackholeOnce_MagnetAddError_LeavesJSON(t *testing.T) {
	dir := t.TempDir()
	fc := newFakeClient()
	fc.addFail = true
	s := New(dir, fc)

	hash := "dddddddddddddddddddddddddddddddddddddddd"
	magnet := "magnet:?xt=urn:btih:" + hash
	writeMagnetPair(t, dir, magnet, model.ManagerAddFileOptions{Hash: hash, Name: "magnet-item"})

	s.SweepOnce(context.Background())

	assertJSONRemains(t, dir, hash)
}

func TestScanBlackholeDir_JSONWithoutTorrentOrMagnet_Broken(t *testing.T) {
	dir := t.TempDir()
	hash := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	_, jsonPath := blackholePaths(dir, hash)
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: model.ManagerAddFileOptions{Hash: hash}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected no valid pairs, got %+v", pairs)
	}
	if len(broken) != 1 || broken[0].hash != hash || broken[0].reason != "missing .torrent" {
		t.Errorf("expected one broken entry with reason %q, got %+v", "missing .torrent", broken)
	}
}

func TestScanBlackholeDir_OldFormatJSONWithTorrent_StillReadable(t *testing.T) {
	dir := t.TempDir()
	data, hash := buildNamedTestTorrent(t, "legacy.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "legacy", Category: "movies"}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	// старый формат: json.Marshal(opts) напрямую, без обертки и без ключа Magnet
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal opts: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(broken) != 0 {
		t.Errorf("expected no broken entries, got %+v", broken)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one pair, got %+v", pairs)
	}
	if !bytes.Equal(pairs[0].torrent, data) {
		t.Errorf("torrent bytes mismatch")
	}
	if pairs[0].magnet != "" {
		t.Errorf("expected empty magnet for an old-format pair, got %q", pairs[0].magnet)
	}
	if !reflect.DeepEqual(pairs[0].opts, opts) {
		t.Errorf("opts mismatch: got %+v, want %+v", pairs[0].opts, opts)
	}
}

func TestScanBlackholeDir_EmptyTorrentWithMagnet_FallsBackToMagnet(t *testing.T) {
	dir := t.TempDir()
	hash := "ffffffffffffffffffffffffffffffffffffffff"
	magnet := "magnet:?xt=urn:btih:" + hash
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "half-written"}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, nil, 0o644); err != nil {
		t.Fatalf("write empty torrent: %v", err)
	}
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: opts, Magnet: magnet})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(broken) != 0 {
		t.Errorf("expected an empty .torrent with a working magnet not to be broken, got %+v", broken)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one pair, got %+v", pairs)
	}
	if len(pairs[0].torrent) != 0 {
		t.Errorf("expected no torrent bytes, got %d", len(pairs[0].torrent))
	}
	if pairs[0].magnet != magnet {
		t.Errorf("magnet mismatch: got %q, want %q", pairs[0].magnet, magnet)
	}
}

func TestScanBlackholeDir_MagnetJSONWithFullTorrent_BytesWin(t *testing.T) {
	dir := t.TempDir()
	data, hash := buildNamedTestTorrent(t, "reoverwritten.txt")
	magnet := "magnet:?xt=urn:btih:" + hash
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "reoverwritten"}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: opts, Magnet: magnet})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(broken) != 0 {
		t.Errorf("expected no broken entries, got %+v", broken)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one pair, got %+v", pairs)
	}
	if !bytes.Equal(pairs[0].torrent, data) {
		t.Errorf("expected torrent bytes to win over the magnet, got %v", pairs[0].torrent)
	}
}

func TestScanBlackholeDir_TruncatedTorrentWithMagnet_FallsBackToMagnet(t *testing.T) {
	dir := t.TempDir()
	data, hash := buildNamedTestTorrent(t, "truncated.txt")
	magnet := "magnet:?xt=urn:btih:" + hash
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "truncated"}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	// непустой, но невалидный bencode: то, что остается от оборванной записи
	if err := os.WriteFile(torrentPath, data[:5], 0o644); err != nil {
		t.Fatalf("write truncated torrent: %v", err)
	}
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: opts, Magnet: magnet})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(broken) != 0 {
		t.Errorf("expected a truncated .torrent with a working magnet not to be broken, got %+v", broken)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one pair, got %+v", pairs)
	}
	if len(pairs[0].torrent) != 0 {
		t.Errorf("expected no torrent bytes from the truncated file, got %d", len(pairs[0].torrent))
	}
	if pairs[0].magnet != magnet {
		t.Errorf("magnet mismatch: got %q, want %q", pairs[0].magnet, magnet)
	}
}

func TestScanBlackholeDir_TruncatedTorrentNoMagnet_Broken(t *testing.T) {
	dir := t.TempDir()
	data, hash := buildNamedTestTorrent(t, "truncated-no-magnet.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "truncated-no-magnet"}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data[:5], 0o644); err != nil {
		t.Fatalf("write truncated torrent: %v", err)
	}
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: opts})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected no valid pairs, got %+v", pairs)
	}
	if len(broken) != 1 || broken[0].hash != hash || !strings.HasPrefix(broken[0].reason, "parse torrent:") {
		t.Errorf("expected one broken entry for %s with reason prefix %q, got %+v", hash, "parse torrent:", broken)
	}
}

func TestScanBlackholeDir_HashMismatch_Broken(t *testing.T) {
	dir := t.TempDir()
	// .torrent на месте и валиден: без проверки meta.Hash он прошел бы как обычная пара
	data, hash := buildNamedTestTorrent(t, "ghost.txt")
	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	// meta.Hash пустой, как после тела json "null" или "{}"
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: model.ManagerAddFileOptions{Name: "ghost"}})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected no valid pairs for a hash mismatch, got %+v", pairs)
	}
	wantReason := `hash mismatch: meta hash "", file ` + hash
	if len(broken) != 1 || broken[0].hash != hash || broken[0].reason != wantReason {
		t.Errorf("expected one broken entry for %s with reason %q, got %+v", hash, wantReason, broken)
	}
}

func TestSweepBlackholeOnce_SuccessButAbsentAfterSettle_LeavesPair(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)

	data, hash := buildNamedTestTorrent(t, "vanish-add.txt")
	fc.vanishAfterFirstQuery(hash)
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "vanish-add"})

	s.SweepOnce(context.Background())

	assertPairRemains(t, dir, hash)
}

func TestSweepBlackholeOnce_AlreadyAddedButAbsentAfterSettle_LeavesPair(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)

	data, hash := buildNamedTestTorrent(t, "vanish-already.txt")
	fc.markPresent(hash)
	fc.vanishAfterFirstQuery(hash)
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "vanish-already"})

	s.SweepOnce(context.Background())

	assertPairRemains(t, dir, hash)
}

func TestSweepBlackholeOnce_BrokenPairPresentInClient_RemovesFiles(t *testing.T) {
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)

	// осиротевший .torrent (без .json) сломан с причиной "missing .json"
	data, hash := buildNamedTestTorrent(t, "broken-present.txt")
	torrentPath, _ := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	fc.markPresent(hash)

	s.SweepOnce(context.Background())

	if _, err := os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("expected broken pair's torrent file removed since torrent is present in client, stat err = %v", err)
	}
}

func TestSweepBlackholeOnce_BrokenHashMismatchPresentInClient_KeepsFiles(t *testing.T) {
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)

	// байты принадлежат этому хешу, но json заявляет другой, поэтому наличие в
	// клиенте торрента из имени файла ничего не говорит про эти байты
	data, hash := buildNamedTestTorrent(t, "mismatch-present.txt")
	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: model.ManagerAddFileOptions{Name: "mismatch"}})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	fc.markPresent(hash)

	s.SweepOnce(context.Background())

	assertPairRemains(t, dir, hash)
}

func TestWriteFileAtomic_OverwritesExistingFileNoLeftoverTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("target content = %q, want %q", got, "new")
	}
	assertNoTmp(t, dir)
}

func TestWriteFileAtomic_FailedWriteLeavesTargetAndNoTmp(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, directory permissions don't block writes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := writeFileAtomic(path, []byte("new")); err == nil {
		t.Fatal("expected an error writing into a read-only directory")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "old" {
		t.Errorf("expected the original target untouched, got %q", got)
	}
	assertNoTmp(t, dir)
}

func ageJSON(t *testing.T, dir, hash string, age time.Duration) {
	t.Helper()
	_, jsonPath := blackholePaths(dir, hash)
	old := time.Now().Add(-age)
	if err := os.Chtimes(jsonPath, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

func (f *fakeClient) isPresent(hash string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.present[strings.ToLower(hash)]
}

func TestSweepBlackholeOnce_FreshPairSkipped(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)

	data, hash := buildNamedTestTorrent(t, "fresh.txt")
	if err := s.Write(data, "", model.ManagerAddFileOptions{Hash: hash, Name: "fresh"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	s.SweepOnce(context.Background())

	if fc.isPresent(hash) {
		t.Error("fresh pair was re-added during the replace window")
	}
	assertPairRemains(t, dir, hash)
}

func TestSweepBlackholeOnce_AgedPairReAdded(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)

	data, hash := buildNamedTestTorrent(t, "aged.txt")
	if err := s.Write(data, "", model.ManagerAddFileOptions{Hash: hash, Name: "aged"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ageJSON(t, dir, hash, freshPairGrace+time.Minute)

	s.SweepOnce(context.Background())

	if !fc.isPresent(hash) {
		t.Error("aged pair was not re-added")
	}
	assertPairGone(t, dir, hash)
}

func TestSweepBlackholeOnce_FreshMagnetPairSkipped(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)

	hash := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if err := s.Write(nil, "magnet:?xt=urn:btih:"+hash, model.ManagerAddFileOptions{Hash: hash, Name: "fresh-magnet"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	s.SweepOnce(context.Background())

	if fc.isPresent(hash) {
		t.Error("fresh magnet pair was re-added during the replace window")
	}
	assertJSONRemains(t, dir, hash)
}

func writeReplacePair(t *testing.T, s *Store, dir, name string, age time.Duration) string {
	t.Helper()
	data, hash := buildNamedTestTorrent(t, name)
	if err := s.Write(data, "", model.ManagerAddFileOptions{Hash: hash, Name: name, ReplacesHash: "old"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ageJSON(t, dir, hash, age)
	return strings.ToLower(hash)
}

func assertNoCalls(t *testing.T, fc *fakeClient, prefixes ...string) {
	t.Helper()
	for _, p := range prefixes {
		if got := fc.callsOf(p); len(got) != 0 {
			t.Errorf("unexpected %s calls: %v", p, got)
		}
	}
}

func TestSweepReplace_OldPresentNewPresentDiscardsNew(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeReplacePair(t, s, dir, "rp-both.txt", freshPairGrace+time.Minute)
	fc.markPresent("old")
	fc.markPresent(hash)

	s.SweepOnce(context.Background())

	if got := fc.callsOf("discard:"); len(got) != 1 || got[0] != "discard:"+hash {
		t.Errorf("discard calls = %v, want [discard:%s]", got, hash)
	}
	assertNoCalls(t, fc, "add:", "resume:")
	if !fc.isPresent("old") {
		t.Error("old torrent must stay")
	}
	assertPairGone(t, dir, hash)
}

func TestSweepReplace_OldPresentNewAbsentDropsPair(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeReplacePair(t, s, dir, "rp-old.txt", freshPairGrace+time.Minute)
	fc.markPresent("old")

	s.SweepOnce(context.Background())

	assertNoCalls(t, fc, "add:", "resume:", "discard:")
	assertPairGone(t, dir, hash)
}

func TestSweepReplace_OldGoneNewPresentResumes(t *testing.T) {
	withFastSettle(t)
	withConfirmAbsentAfter(t, 0)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeReplacePair(t, s, dir, "rp-new.txt", freshPairGrace+time.Minute)
	fc.markPresent(hash)

	s.SweepOnce(context.Background())
	s.SweepOnce(context.Background())

	// одна проба на первом тике, две (до и после SettleDelay) на подтверждающем
	if got := fc.callsOf("exists:old"); len(got) != 3 {
		t.Errorf("old probes = %v, want three", got)
	}
	if got := fc.callsOf("resume:"); len(got) != 1 || got[0] != "resume:"+hash {
		t.Errorf("resume calls = %v, want [resume:%s]", got, hash)
	}
	assertNoCalls(t, fc, "add:", "discard:")
	assertPairGone(t, dir, hash)
}

func TestSweepReplace_OldBackOnSecondProbeKeepsPair(t *testing.T) {
	// клиент мог еще не дочитать свой список: одной пробе "нет" не верим
	withFastSettle(t)
	withConfirmAbsentAfter(t, 0)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeReplacePair(t, s, dir, "rp-flap.txt", freshPairGrace+time.Minute)
	fc.markPresent(hash)
	fc.existsSeq["old"] = []bool{false, false, true}

	s.SweepOnce(context.Background())
	s.SweepOnce(context.Background())

	assertNoCalls(t, fc, "add:", "resume:", "discard:")
	assertPairRemains(t, dir, hash)
}

func TestSweepReplace_BothGoneReAddsStarted(t *testing.T) {
	withFastSettle(t)
	withConfirmAbsentAfter(t, 0)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	data, hash := buildNamedTestTorrent(t, "rp-none.txt")
	if err := s.Write(data, "", model.ManagerAddFileOptions{Hash: hash, Name: "rp-none", ReplacesHash: "old", Stopped: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ageJSON(t, dir, hash, freshPairGrace+time.Minute)

	s.SweepOnce(context.Background())
	s.SweepOnce(context.Background())

	if len(fc.addOpts) != 1 || fc.addOpts[0].Stopped {
		t.Fatalf("add opts = %+v, want one add with Stopped false", fc.addOpts)
	}
	assertNoCalls(t, fc, "resume:", "discard:")
	if !fc.isPresent(hash) {
		t.Error("new torrent was not re-added")
	}
	assertPairGone(t, dir, hash)
}

func TestSweepReplace_ReaddCarriesFileSelection(t *testing.T) {
	withFastSettle(t)
	withConfirmAbsentAfter(t, 0)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	data, hash := buildNamedTestTorrent(t, "rp-sel.txt")
	sel := []model.TorrentFile{{Path: "R/a", Wanted: true}, {Path: "R/b"}}
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "rp-sel", ReplacesHash: "old", Stopped: true, FileSelection: sel}
	if err := s.Write(data, "", opts); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ageJSON(t, dir, hash, freshPairGrace+time.Minute)

	s.SweepOnce(context.Background())
	s.SweepOnce(context.Background())

	if len(fc.addOpts) != 1 {
		t.Fatalf("add opts = %+v, want one add", fc.addOpts)
	}
	if !reflect.DeepEqual(fc.addOpts[0].FileSelection, sel) {
		t.Errorf("FileSelection = %+v, want %+v", fc.addOpts[0].FileSelection, sel)
	}
	assertPairGone(t, dir, hash)
}

func TestSweepReplace_OldProbeErrorKeepsPair(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeReplacePair(t, s, dir, "rp-err.txt", freshPairGrace+time.Minute)
	fc.markPresent(hash)
	fc.existsErr["old"] = fmt.Errorf("connection refused")

	s.SweepOnce(context.Background())

	if len(fc.calls) != 1 || fc.calls[0] != "exists:old" {
		t.Errorf("calls = %v, want only exists:old", fc.calls)
	}
	assertPairRemains(t, dir, hash)
}

func TestSweepReplace_ResumeErrorKeepsPair(t *testing.T) {
	withFastSettle(t)
	withConfirmAbsentAfter(t, 0)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeReplacePair(t, s, dir, "rp-resume.txt", freshPairGrace+time.Minute)
	fc.markPresent(hash)
	fc.resumeErr = fmt.Errorf("no data found")

	s.SweepOnce(context.Background())
	s.SweepOnce(context.Background())

	if got := fc.callsOf("resume:"); len(got) != 1 {
		t.Errorf("resume calls = %v, want one", got)
	}
	assertPairRemains(t, dir, hash)
}

func TestSweepReplace_DiscardErrorKeepsPair(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeReplacePair(t, s, dir, "rp-discard.txt", freshPairGrace+time.Minute)
	fc.markPresent("old")
	fc.markPresent(hash)
	fc.discardErr = fmt.Errorf("discard failed")

	s.SweepOnce(context.Background())

	if got := fc.callsOf("discard:"); len(got) != 1 {
		t.Errorf("discard calls = %v, want one", got)
	}
	assertPairRemains(t, dir, hash)
}

func TestSweepReplace_PairInsideGraceSkipped(t *testing.T) {
	// основной путь может еще запускать новый торрент (Resume до CheckWaitBudget)
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeReplacePair(t, s, dir, "rp-fresh.txt", AddTimeout+CheckWaitBudget)
	fc.markPresent(hash)

	s.SweepOnce(context.Background())

	if len(fc.calls) != 0 {
		t.Errorf("calls = %v, want none for a pair inside the grace", fc.calls)
	}
	assertPairRemains(t, dir, hash)
}

func TestSweepBlackholeOnce_ClassicReAddKeepsStopped(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	data, hash := buildNamedTestTorrent(t, "classic-stopped.txt")
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "classic", Stopped: true})
	_, magnetHash := buildNamedTestTorrent(t, "classic-stopped-magnet.txt")
	writeMagnetPair(t, dir, "magnet:?xt=urn:btih:"+magnetHash, model.ManagerAddFileOptions{Hash: magnetHash, Name: "classic-magnet", Stopped: true})

	s.SweepOnce(context.Background())

	if len(fc.addOpts) != 2 {
		t.Fatalf("add opts = %+v, want two adds", fc.addOpts)
	}
	for _, o := range fc.addOpts {
		if !o.Stopped {
			t.Errorf("%s re-added with Stopped false, want true", o.Name)
		}
	}
	assertPairGone(t, dir, hash)
	assertJSONGone(t, dir, magnetHash)
}

func TestPendingReplace(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, newFakeClient())
	replaceHash := writeReplacePair(t, s, dir, "pending-replace.txt", 0)
	data, classicHash := buildNamedTestTorrent(t, "pending-classic.txt")
	if err := s.Write(data, "", model.ManagerAddFileOptions{Hash: classicHash, Name: "classic"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	brokenHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	_, brokenJSON := blackholePaths(dir, brokenHash)
	if err := os.WriteFile(brokenJSON, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cases := []struct {
		name string
		hash string
		want bool
	}{
		{"delete-first pair", strings.ToUpper(replaceHash), true},
		{"classic pair", classicHash, false},
		{"no pair", "cccccccccccccccccccccccccccccccccccccccc", false},
		{"unparsable json", brokenHash, true},
	}
	for _, tc := range cases {
		if got := s.PendingReplace(tc.hash); got != tc.want {
			t.Errorf("%s: PendingReplace = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWriteBlackholePair_ReplacesTorrentAtomically(t *testing.T) {
	// .torrent пишется через временный файл и rename, а не усечением на месте
	dir := t.TempDir()
	s := New(dir, newFakeClient())
	data, hash := buildNamedTestTorrent(t, "atomic.txt")
	torrentPath, _ := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, []byte("stale"), 0o444); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := s.Write(data, "", model.ManagerAddFileOptions{Hash: hash, Name: "atomic"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(torrentPath)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("torrent = %d bytes (err %v), want the new %d bytes", len(got), err, len(data))
	}
	assertNoTmp(t, dir)
}

func writeBrokenReplacePair(t *testing.T, s *Store, dir, name string) string {
	t.Helper()
	hash := writeReplacePair(t, s, dir, name, freshPairGrace+time.Minute)
	torrentPath, _ := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, []byte("d8:announce"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return hash
}

func TestSweepReplace_BrokenPairOldGoneNewPresentResumes(t *testing.T) {
	withFastSettle(t)
	withConfirmAbsentAfter(t, 0)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeBrokenReplacePair(t, s, dir, "broken-resume.txt")
	fc.markPresent(hash)

	s.SweepOnce(context.Background())
	s.SweepOnce(context.Background())

	if got := fc.callsOf("resume:"); len(got) != 1 || got[0] != "resume:"+hash {
		t.Errorf("resume calls = %v, want [resume:%s]", got, hash)
	}
	assertNoCalls(t, fc, "add:", "discard:")
	assertPairGone(t, dir, hash)
}

func TestSweepReplace_BrokenPairOldPresentDiscardsNew(t *testing.T) {
	withFastSettle(t)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeBrokenReplacePair(t, s, dir, "broken-discard.txt")
	fc.markPresent("old")
	fc.markPresent(hash)

	s.SweepOnce(context.Background())

	if got := fc.callsOf("discard:"); len(got) != 1 || got[0] != "discard:"+hash {
		t.Errorf("discard calls = %v, want [discard:%s]", got, hash)
	}
	assertPairGone(t, dir, hash)
}

func TestSweepReplace_BrokenPairBothGoneKept(t *testing.T) {
	withFastSettle(t)
	withConfirmAbsentAfter(t, 0)
	dir := t.TempDir()
	fc := newFakeClient()
	s := New(dir, fc)
	hash := writeBrokenReplacePair(t, s, dir, "broken-keep.txt")

	s.SweepOnce(context.Background())
	s.SweepOnce(context.Background())

	assertNoCalls(t, fc, "add:", "resume:", "discard:")
	_, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("expected json to remain: %v", err)
	}
}

func TestPendingReplace_UnreadableDirIsFalse(t *testing.T) {
	// каталог недоступен: пары там нет и быть не может, запись все равно
	// упадет раньше любого удаления
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if New(file, newFakeClient()).PendingReplace("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Error("PendingReplace = true for a dir that cannot hold a pair, want false")
	}
}

func withConfirmAbsentAfter(t *testing.T, d time.Duration) {
	t.Helper()
	orig := confirmAbsentAfter
	confirmAbsentAfter = d
	t.Cleanup(func() { confirmAbsentAfter = orig })
}

func TestWriteCreatesOwnerOnlyFiles(t *testing.T) {
	// в паре лежит ссылка с passkey, другим пользователям хоста ее видеть незачем
	dir := t.TempDir()
	data, hash := buildNamedTestTorrent(t, "perm.txt")
	if err := New(dir, newFakeClient()).Write(data, "", model.ManagerAddFileOptions{Hash: hash, Name: "perm"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	torrentPath, jsonPath := blackholePaths(dir, hash)
	for _, p := range []string{torrentPath, jsonPath} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(p), got)
		}
	}
}

func TestWriteConcurrentSameHash(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, newFakeClient())
	data, hash := buildNamedTestTorrent(t, "race.txt")
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for g := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				opts := model.ManagerAddFileOptions{Hash: hash, Name: fmt.Sprintf("writer-%d-%d", g, i)}
				if err := s.Write(data, "", opts); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Write: %v", err)
	}
	_, jsonPath := blackholePaths(dir, hash)
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	var meta blackholeMeta
	if err = json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("parse json: %v (%q)", err, raw)
	}
	if !strings.EqualFold(meta.Hash, hash) {
		t.Errorf("meta hash = %q, want %q", meta.Hash, hash)
	}
}

func TestFinishReplaceNeedsSecondSweep(t *testing.T) {
	withFastSettle(t)
	ctx := context.Background()

	t.Run("old absent on two sweeps resumes", func(t *testing.T) {
		withConfirmAbsentAfter(t, 0)
		dir := t.TempDir()
		fc := newFakeClient()
		s := New(dir, fc)
		hash := writeReplacePair(t, s, dir, "confirm-resume.txt", freshPairGrace+time.Minute)
		fc.markPresent(hash)

		s.SweepOnce(ctx)
		assertNoCalls(t, fc, "add:", "resume:", "discard:")
		assertPairRemains(t, dir, hash)

		s.SweepOnce(ctx)
		if got := fc.callsOf("resume:"); len(got) != 1 || got[0] != "resume:"+hash {
			t.Errorf("resume calls = %v, want [resume:%s]", got, hash)
		}
		assertPairGone(t, dir, hash)
	})

	t.Run("second sweep too early keeps waiting", func(t *testing.T) {
		withConfirmAbsentAfter(t, time.Hour)
		dir := t.TempDir()
		fc := newFakeClient()
		s := New(dir, fc)
		hash := writeReplacePair(t, s, dir, "confirm-early.txt", freshPairGrace+time.Minute)
		fc.markPresent(hash)

		s.SweepOnce(ctx)
		s.SweepOnce(ctx)
		assertNoCalls(t, fc, "add:", "resume:", "discard:")
		assertPairRemains(t, dir, hash)
	})

	t.Run("old back between sweeps rolls back and clears record", func(t *testing.T) {
		withConfirmAbsentAfter(t, 0)
		dir := t.TempDir()
		fc := newFakeClient()
		s := New(dir, fc)
		hash := writeReplacePair(t, s, dir, "confirm-back.txt", freshPairGrace+time.Minute)
		fc.markPresent(hash)

		s.SweepOnce(ctx)
		fc.markPresent("old")
		s.SweepOnce(ctx)
		if got := fc.callsOf("discard:"); len(got) != 1 || got[0] != "discard:"+hash {
			t.Errorf("discard calls = %v, want [discard:%s]", got, hash)
		}
		assertPairGone(t, dir, hash)

		// та же пара заново: прежняя запись не должна засчитаться первым тиком
		fc.mu.Lock()
		delete(fc.present, "old")
		fc.mu.Unlock()
		fc.markPresent(hash)
		writeReplacePair(t, s, dir, "confirm-back.txt", freshPairGrace+time.Minute)
		s.SweepOnce(ctx)
		assertNoCalls(t, fc, "add:", "resume:")
		assertPairRemains(t, dir, hash)
	})

	t.Run("probe error between sweeps clears record", func(t *testing.T) {
		withConfirmAbsentAfter(t, 0)
		dir := t.TempDir()
		fc := newFakeClient()
		s := New(dir, fc)
		hash := writeReplacePair(t, s, dir, "confirm-err.txt", freshPairGrace+time.Minute)
		fc.markPresent(hash)

		s.SweepOnce(ctx)
		fc.mu.Lock()
		fc.existsErr["old"] = fmt.Errorf("connection refused")
		fc.mu.Unlock()
		s.SweepOnce(ctx)
		fc.mu.Lock()
		delete(fc.existsErr, "old")
		fc.mu.Unlock()
		s.SweepOnce(ctx)
		assertNoCalls(t, fc, "add:", "resume:", "discard:")
		assertPairRemains(t, dir, hash)
	})

	t.Run("unreachable sweep clears record", func(t *testing.T) {
		withConfirmAbsentAfter(t, 0)
		dir := t.TempDir()
		fc := newFakeClient()
		s := New(dir, fc)
		hash := writeReplacePair(t, s, dir, "confirm-down.txt", freshPairGrace+time.Minute)
		fc.markPresent(hash)

		s.SweepOnce(ctx)
		fc.mu.Lock()
		fc.unreachable = true
		fc.mu.Unlock()
		s.SweepOnce(ctx)
		fc.mu.Lock()
		fc.unreachable = false
		fc.mu.Unlock()
		s.SweepOnce(ctx)
		assertNoCalls(t, fc, "add:", "resume:", "discard:")
		assertPairRemains(t, dir, hash)
	})
}

func assertNoTmp(t *testing.T, dir string) {
	t.Helper()
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Errorf("expected no leftover .tmp file, got %v", left)
	}
}
