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

	"trackerr/internal/helper/torrentmeta"
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
	_, err := Fetch(context.Background(), "not a magnet")
	if err == nil {
		t.Fatal("ожидалась ошибка для мусорной строки")
	}
}

func TestFetch_CanceledContext(t *testing.T) {
	// подменяем TMPDIR уникальным каталогом теста, чтобы после Fetch
	// проверить, что временный каталог клиента библиотеки был убран через defer
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	const validMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"

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
	const validMagnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
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
