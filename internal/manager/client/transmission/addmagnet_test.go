package transmission

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"trackerr/internal/model"
)

// withMagnetFetchStub подменяет magnetFetch и попутно запоминает магнит,
// с которым его вызвали - тесты сверяют, что в добытчик уехал именно тот
// магнит, который передали AddFromMagnet, а не пустая строка или чужой.
func withMagnetFetchStub(t *testing.T, fn func(ctx context.Context, magnet string) ([]byte, error)) *string {
	t.Helper()
	var got string
	orig := magnetFetch
	magnetFetch = func(ctx context.Context, magnet string) ([]byte, error) {
		got = magnet
		return fn(ctx, magnet)
	}
	t.Cleanup(func() { magnetFetch = orig })
	return &got
}

func TestAddFromMagnet_CommentSet_OwnFetchSucceeds_AddsFileWithComment(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "")
	c := newTestClient(t, stub, model.ManagerConfig{})

	data, hash := buildTestTorrent(t, "own-fetch-ok.txt")
	magnet := "magnet:?xt=urn:btih:" + hash
	gotMagnet := withMagnetFetchStub(t, func(context.Context, string) ([]byte, error) { return data, nil })

	opts := model.ManagerAddFileOptions{Hash: hash, Name: "own-fetch-ok", Comment: "http://tracker.example/topic/1"}

	if err := c.AddFromMagnet(context.Background(), magnet, opts); err != nil {
		t.Fatalf("AddFromMagnet: %v", err)
	}
	if *gotMagnet != magnet {
		t.Errorf("magnetFetch получил %q, want %q", *gotMagnet, magnet)
	}
	if len(stub.addCalls) != 1 {
		t.Fatalf("addCalls = %d, want 1", len(stub.addCalls))
	}
	raw, ok := stub.addCalls[0]["metainfo"].(string)
	if !ok || raw == "" {
		t.Fatalf("expected metainfo (file) add, got %#v", stub.addCalls[0])
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("decode metainfo: %v", err)
	}
	mi, err := metainfo.Load(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("load metainfo: %v", err)
	}
	if mi.Comment != opts.Comment {
		t.Errorf("comment = %q, want %q", mi.Comment, opts.Comment)
	}
}

func TestAddFromMagnet_CommentSet_OwnFetchFails_FallsBackToMagnetAdd(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "")
	c := newTestClient(t, stub, model.ManagerConfig{})

	_, hash := buildTestTorrent(t, "fallback.txt")
	magnet := "magnet:?xt=urn:btih:" + hash
	gotMagnet := withMagnetFetchStub(t, func(context.Context, string) ([]byte, error) { return nil, errors.New("no peers") })

	opts := model.ManagerAddFileOptions{Hash: hash, Name: "fallback", Comment: "http://tracker.example/topic/2"}

	// текущее поведение при неудачной добыче: добавление магнитом без
	// комментария (RPC Transmission не умеет задать его при добавлении по
	// магниту), тот же addMagnetVerified, что и раньше
	if err := c.AddFromMagnet(context.Background(), magnet, opts); err != nil {
		t.Fatalf("AddFromMagnet: %v", err)
	}
	if *gotMagnet != magnet {
		t.Errorf("magnetFetch получил %q, want %q", *gotMagnet, magnet)
	}
	if len(stub.addCalls) != 1 {
		t.Fatalf("addCalls = %d, want 1", len(stub.addCalls))
	}
	if _, ok := stub.addCalls[0]["filename"]; !ok {
		t.Errorf("expected magnet-url add on fetch failure, args = %#v", stub.addCalls[0])
	}
}

func TestAddFromMagnet_OwnFetchHangs_FallbackStillGetsBudget(t *testing.T) {
	// регресс: magnetFetch без своего потолка съедал весь ctx AddFromMagnet
	// целиком (в бою это blackhole.AddTimeout), и запасной путь (обычное
	// добавление магнитом) получал уже истёкший контекст. Ужимаем потолок до
	// долей секунды и проверяем, что заглушка, висящая до конца СВОЕГО
	// контекста, всё равно оставляет запасному пути время на успех.
	withFastPresenceTimings(t)
	orig := magnetFetchTimeout
	magnetFetchTimeout = 50 * time.Millisecond
	t.Cleanup(func() { magnetFetchTimeout = orig })

	stub := newStubServer(t, "")
	c := newTestClient(t, stub, model.ManagerConfig{})

	_, hash := buildTestTorrent(t, "hang-then-fallback.txt")
	magnet := "magnet:?xt=urn:btih:" + hash
	withMagnetFetchStub(t, func(ctx context.Context, magnet string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	opts := model.ManagerAddFileOptions{Hash: hash, Name: "hang-then-fallback", Comment: "http://tracker.example/topic/3"}

	// внешний контекст с дедлайном порядка трёх потолков добычи, а не
	// context.Background(): мутация "убрать собственный подконтекст" с
	// Background() вешала бы заглушку навечно, и тест падал бы 10-минутной
	// паникой раннера вместо быстрого и внятного assert'а
	ctx, cancel := context.WithTimeout(context.Background(), 3*magnetFetchTimeout)
	defer cancel()
	if err := c.AddFromMagnet(ctx, magnet, opts); err != nil {
		t.Fatalf("AddFromMagnet: %v (fallback должен был отработать)", err)
	}
	if len(stub.addCalls) != 1 {
		t.Fatalf("addCalls = %d, want 1 (запасной путь должен был добавить магнитом)", len(stub.addCalls))
	}
}

func TestAddFromMagnet_CanceledContext_ReturnsQuicklyNotAfterTimeout(t *testing.T) {
	// сторож на происхождение fctx: если бы подконтекст собственной добычи
	// заводился от context.Background() вместо входящего ctx, заранее
	// отменённый внешний контекст игнорировался бы, и заглушка провисела бы
	// весь magnetFetchTimeout вместо мгновенного возврата
	withFastPresenceTimings(t)
	orig := magnetFetchTimeout
	magnetFetchTimeout = time.Second
	t.Cleanup(func() { magnetFetchTimeout = orig })

	stub := newStubServer(t, "")
	c := newTestClient(t, stub, model.ManagerConfig{})

	_, hash := buildTestTorrent(t, "canceled-ctx.txt")
	magnet := "magnet:?xt=urn:btih:" + hash
	withMagnetFetchStub(t, func(ctx context.Context, magnet string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := model.ManagerAddFileOptions{Hash: hash, Name: "canceled-ctx", Comment: "http://tracker.example/topic/4"}

	start := time.Now()
	_ = c.AddFromMagnet(ctx, magnet, opts)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("AddFromMagnet с заранее отменённым ctx занял %v, ожидался быстрый возврат (fctx обязан наследовать отмену родителя, а не ждать свой magnetFetchTimeout=%v)", elapsed, magnetFetchTimeout)
	}
}
