package transmission

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"ripperr/internal/model"
)

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

	// добыча не удалась: просто добавляем магнитом, комментарий так не задать
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
	// регресс: без своего потолка magnetFetch съедал весь ctx (в бою это
	// blackhole.AddTimeout), и запасной путь получал уже истекший контекст
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

	// с дедлайном: если сломать свой подконтекст, на Background() заглушка висела
	// бы вечно и тест падал бы тольок по 10-минутному таймауту раннера
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
	// если fctx заводить от context.Background(), отмененный ctx игнорируется
	// и заглушка висит весь magnetFetchTimeout
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
