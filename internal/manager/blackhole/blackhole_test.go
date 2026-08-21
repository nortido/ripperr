package blackhole

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"trackerr/internal/helper/torrentmeta"
	"trackerr/internal/model"
)

// buildNamedTestTorrent параметризован именем, потому что два вызова с
// одинаковым именем дают один и тот же infohash, а нескольким тестам
// подметания нужны разные пары на диске одновременно.
func buildNamedTestTorrent(t *testing.T, name string) ([]byte, string) {
	t.Helper()
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

// fakeClient это минимальный поддельный client для подметальщика: Exists
// сообщает то, что лежит в present, AddFromBuffer/AddFromMagnet переводят хеш
// в present (или сразу отказывают, когда выставлен addFail), повторяя
// двойную проверку присутствия настоящего qbittorrent.Client (до добавления и
// сразу после), чтобы vanishHash вёл себя так же, как асинхронная задача
// удаления в реальном клиенте.
type fakeClient struct {
	mu          sync.Mutex
	present     map[string]bool
	addFail     bool
	unreachable bool
	// vanishHash: следующий Exists, нашедший этот хеш присутствующим, убирает
	// его из present сразу после ответа, поэтому очередной запрос сообщает об
	// отсутствии.
	vanishHash string
}

func newFakeClient() *fakeClient {
	return &fakeClient{present: map[string]bool{}}
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

// add воспроизводит qbittorrent.Client.addTorrentVerified: проверка
// присутствия до добавления (preAddPresenceCheck), сам вызов добавления,
// проверка присутствия после (ensurePresentAfterAdd). Оба обращения идут
// через Exists, поэтому vanishHash одинаково задевает оба этапа.
func (f *fakeClient) add(ctx context.Context, hash string) error {
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

func (f *fakeClient) AddFromBuffer(ctx context.Context, buf *bytes.Buffer, _ model.ManagerAddFileOptions) error {
	hash, err := torrentmeta.InfoHash(buf.Bytes())
	if err != nil {
		return err
	}
	return f.add(ctx, hash)
}

func (f *fakeClient) AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	hash, ok := torrentmeta.BtihFromMagnet(magnet)
	if !ok {
		hash = opts.Hash
	}
	return f.add(ctx, hash)
}

func writePair(t *testing.T, dir string, data []byte, opts model.ManagerAddFileOptions) {
	t.Helper()
	s := New(dir, newFakeClient())
	if err := s.Write(data, "", opts); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func writeMagnetPair(t *testing.T, dir, magnet string, opts model.ManagerAddFileOptions) {
	t.Helper()
	s := New(dir, newFakeClient())
	if err := s.Write(nil, magnet, opts); err != nil {
		t.Fatalf("Write: %v", err)
	}
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
	if gotOpts != opts {
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
	if gotMeta.ManagerAddFileOptions != opts {
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
	// старый формат: json.Marshal(opts) напрямую, без обёртки и без ключа Magnet
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
	if pairs[0].opts != opts {
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
	// непустой, но невалидный bencode: то, что остаётся от оборванной записи
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
	// .torrent намеренно на месте и валиден: без проверки meta.Hash он прошёл бы
	// через loadBlackholePair как обычная валидная пара
	data, hash := buildNamedTestTorrent(t, "ghost.txt")
	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	// meta.Hash оставлен пустым: та же форма, что даёт тело json "null" или "{}"
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
	if _, err = os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("expected no leftover .tmp file, stat err = %v", err)
	}
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
	if _, err = os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("expected no leftover .tmp file, stat err = %v", err)
	}
}
