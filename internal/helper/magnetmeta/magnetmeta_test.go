package magnetmeta

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"ripperr/internal/helper/torrentmeta"
)

func buildTestTorrentBytes(t *testing.T) []byte {
	t.Helper()
	info := metainfo.Info{PieceLength: 16384, Pieces: make([]byte, 20), Name: "test.txt", Length: 0}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes()
}

func TestVerifyInfoHash_Match(t *testing.T) {
	built := buildTestTorrentBytes(t)
	hash, err := torrentmeta.InfoHash(built)
	if err != nil {
		t.Fatalf("InfoHash: %v", err)
	}
	magnet := "magnet:?xt=urn:btih:" + hash
	if err := verifyInfoHash(built, magnet); err != nil {
		t.Fatalf("verifyInfoHash: %v", err)
	}
}

func TestVerifyInfoHash_Mismatch(t *testing.T) {
	built := buildTestTorrentBytes(t)
	// strings.Repeat, а не хардкод строки: ошибиться в счёте f-ов руками легко
	// (уже случилось), а неверная длина хеша тихо меняет проверяемый сценарий
	// с "расхождение" на "магнит без разбираемого btih"
	other := strings.Repeat("f", 40)
	err := verifyInfoHash(built, "magnet:?xt=urn:btih:"+other)
	if err == nil {
		t.Fatal("ожидалась ошибка при расхождении инфохешей")
	}
	gotHash, hashErr := torrentmeta.InfoHash(built)
	if hashErr != nil {
		t.Fatalf("InfoHash: %v", hashErr)
	}
	if !strings.Contains(err.Error(), gotHash) || !strings.Contains(err.Error(), other) {
		t.Errorf("текст ошибки %q не содержит оба хеша (%s, %s)", err.Error(), gotHash, other)
	}
}

func TestVerifyInfoHash_UnparsableMagnet(t *testing.T) {
	built := buildTestTorrentBytes(t)
	if err := verifyInfoHash(built, "magnet:?dn=test"); err == nil {
		t.Fatal("ожидалась ошибка для магнита без разбираемого btih")
	}
}

func TestFetch_EmptyMagnet(t *testing.T) {
	_, err := Fetch(context.Background(), "")
	if err == nil {
		t.Fatal("ожидалась ошибка для пустой магнет-строки")
	}
}

func TestFetch_GarbageMagnet(t *testing.T) {
	// с tr, иначе мусорную строку отсекает hasAnnounceURL раньше, чем она
	// доходит до cl.AddMagnet, и тест перестаёт проверять то, для чего
	// написан - отказ библиотеки на мусорном URI (схема не "magnet",
	// а разбирается net/url без ошибки, поэтому guard её пропускает)
	_, err := Fetch(context.Background(), "garbage://not-a-real-magnet?tr=http://tracker.example/announce")
	if err == nil {
		t.Fatal("ожидалась ошибка для мусорной строки")
	}
}

func TestHasAnnounceURL_WithTracker(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=http://tracker.example/announce"
	if !hasAnnounceURL(magnet) {
		t.Fatal("магнит с tr не должен отсекаться этой проверкой")
	}
}

func TestHasAnnounceURL_WithoutTracker(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	if hasAnnounceURL(magnet) {
		t.Fatal("магнит без tr не должен считаться имеющим анонсер")
	}
}

func TestHasAnnounceURL_EmptyTrackerValue(t *testing.T) {
	// tr= с пустым значением (или только из пробелов) это тот же случай, что и
	// отсутствие параметра вовсе - разбирать такую "ссылку" клиенту нечем
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=%20"
	if hasAnnounceURL(magnet) {
		t.Fatal("пустой tr не должен считаться анонсером")
	}
}

func TestFetch_NoAnnounce_FastFailWithoutClient(t *testing.T) {
	// проверка каталога ПОСЛЕ возврата Fetch ничего не доказывает: defer
	// os.RemoveAll(dir) убирает временный каталог клиента в любом случае,
	// независимо от того, где по коду стоит guard. Считаем реальные вызовы
	// mkdirTemp через подменяемую переменную пакета - только так видно, что
	// клиент вообще не создаётся, а не просто убирается за собой
	var calls int
	orig := mkdirTemp
	mkdirTemp = func(dir, pattern string) (string, error) {
		calls++
		return orig(dir, pattern)
	}
	t.Cleanup(func() { mkdirTemp = orig })

	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	_, err := Fetch(context.Background(), magnet)
	if err == nil {
		t.Fatal("ожидалась ошибка для магнита без tr при выключенном DHT")
	}
	if !strings.Contains(err.Error(), "анонсера") {
		t.Errorf("текст ошибки %q не поясняет причину (нет анонсера)", err.Error())
	}
	if calls != 0 {
		t.Errorf("клиент torrent не должен подниматься вовсе: mkdirTemp вызван %d раз(а)", calls)
	}
}

func TestFillAnnounce_SetsFirstNonEmptyFromAnnounceList(t *testing.T) {
	mi := &metainfo.MetaInfo{AnnounceList: metainfo.AnnounceList{{"", "http://a.example/announce"}, {"http://b.example/announce"}}}
	fillAnnounce(mi)
	if mi.Announce != "http://a.example/announce" {
		t.Errorf("Announce = %q, want http://a.example/announce", mi.Announce)
	}
}

func TestFillAnnounce_DoesNotOverwriteExisting(t *testing.T) {
	mi := &metainfo.MetaInfo{Announce: "http://existing.example/announce", AnnounceList: metainfo.AnnounceList{{"http://other.example/announce"}}}
	fillAnnounce(mi)
	if mi.Announce != "http://existing.example/announce" {
		t.Errorf("Announce = %q, want untouched http://existing.example/announce", mi.Announce)
	}
}

func TestFillAnnounce_RoundTripPreservesInfoHash(t *testing.T) {
	info := metainfo.Info{PieceLength: 16384, Pieces: make([]byte, 20), Name: "test.txt", Length: 0}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: infoBytes, AnnounceList: metainfo.AnnounceList{{"http://a.example/announce"}}}
	hashBefore := mi.HashInfoBytes().HexString()

	fillAnnounce(mi)

	var buf bytes.Buffer
	if err = mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	loaded, err := metainfo.Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("load metainfo: %v", err)
	}
	if loaded.Announce != "http://a.example/announce" {
		t.Errorf("Announce = %q, want http://a.example/announce", loaded.Announce)
	}
	if hashAfter := loaded.HashInfoBytes().HexString(); hashAfter != hashBefore {
		t.Errorf("infohash changed: before=%s after=%s (announce лежит вне словаря info)", hashBefore, hashAfter)
	}
}

func TestFetch_CanceledContext(t *testing.T) {
	// подменяем TMPDIR уникальным каталогом теста, чтобы после Fetch
	// проверить, что временный каталог клиента библиотеки был убран через defer
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// с tr, иначе новая проверка hasAnnounceURL отсекает магнит раньше, чем
	// поднимается клиент, и тест перестаёт проверять то, для чего он написан -
	// уборку временного каталога клиента библиотеки через defer
	const validMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=http://tracker.example/announce"

	_, err := Fetch(ctx, validMagnet)
	if err == nil {
		t.Fatal("ожидалась ошибка при отменённом контексте")
	}

	entries, readErr := os.ReadDir(tmpDir)
	if readErr != nil {
		t.Fatalf("не удалось прочитать %s: %v", tmpDir, readErr)
	}
	for _, e := range entries {
		t.Errorf("временный каталог не убран: %s", filepath.Join(tmpDir, e.Name()))
	}
}

func TestFetch_NoGoroutineLeak(t *testing.T) {
	// DHT-серверы и UPnP-проброс порта (оба по умолчанию включены в
	// torrent.NewDefaultClientConfig) заводят фоновые горутины с живыми
	// сокетами, которые не гарантированно останавливаются к моменту возврата
	// Fetch; демон вызывает Fetch на каждом магнитном сабмите неделями подряд,
	// так что счёт горутин обязан вернуться к исходному после каждого вызова
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// с tr, чтобы реально пройти до создания клиента (без tr магнит теперь
	// отсекается hasAnnounceURL раньше, и тест не проверял бы горутины клиента)
	const validMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=http://tracker.example/announce"
	for i := 0; i < 5; i++ {
		_, _ = Fetch(ctx, validMagnet)
	}
	for i := 0; i < 5; i++ {
		_, _ = Fetch(context.Background(), "not a magnet")
	}

	// библиотека гасит announcer/uTP/порт-форвардинг асинхронно после Close/Drop,
	// даём им время реально завершиться, прежде чем считать горутины
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()
	if after > before {
		t.Errorf("goroutine leak: before=%d after=%d delta=%d", before, after, after-before)
	}
}
