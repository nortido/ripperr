package qbittorrent

import (
	"bytes"
	"context"
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
	stub := newSweepStubServer(t)
	// metadataTorrent пустой: если AddFromMagnet свалится на путь через qbit,
	// saveMetadata ответит 404 и торрент уйдет магнитом без комментария
	c, err := New(model.ManagerConfig{Host: stub.server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	data, hash := buildNamedTestTorrent(t, "own-fetch-ok.txt")
	gotMagnet := withMagnetFetchStub(t, func(context.Context, string) ([]byte, error) { return data, nil })

	opts := model.ManagerAddFileOptions{Hash: hash, Name: "own-fetch-ok", Comment: "http://tracker.example/topic/1"}
	magnet := "magnet:?xt=urn:btih:" + hash

	if err := c.AddFromMagnet(context.Background(), magnet, opts); err != nil {
		t.Fatalf("AddFromMagnet: %v", err)
	}
	if *gotMagnet != magnet {
		t.Errorf("magnetFetch получил %q, want %q", *gotMagnet, magnet)
	}
	if stub.lastAddedURL != "" {
		t.Errorf("expected no magnet-url add, got %q", stub.lastAddedURL)
	}
	if stub.lastAddedFile == nil {
		t.Fatal("expected torrent uploaded as file, got none")
	}
	mi, err := metainfo.Load(bytes.NewReader(stub.lastAddedFile))
	if err != nil {
		t.Fatalf("decode uploaded torrent: %v", err)
	}
	if mi.Comment != opts.Comment {
		t.Errorf("comment = %q, want %q", mi.Comment, opts.Comment)
	}
}

func TestAddFromMagnet_CommentSet_OwnFetchFails_FallsBackToQbitMetadataFetch(t *testing.T) {
	stub := newSweepStubServer(t)
	metaData, metaHash := buildNamedTestTorrent(t, "qbit-fallback.txt")
	stub.metadataTorrent = metaData
	c, err := New(model.ManagerConfig{Host: stub.server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	magnet := "magnet:?xt=urn:btih:" + metaHash
	gotMagnet := withMagnetFetchStub(t, func(context.Context, string) ([]byte, error) { return nil, errors.New("no peers") })

	opts := model.ManagerAddFileOptions{Hash: metaHash, Name: "qbit-fallback", Comment: "http://tracker.example/topic/2"}

	if err := c.AddFromMagnet(context.Background(), magnet, opts); err != nil {
		t.Fatalf("AddFromMagnet: %v", err)
	}
	if *gotMagnet != magnet {
		t.Errorf("magnetFetch получил %q, want %q", *gotMagnet, magnet)
	}
	// запасной путь: метаданные через qbit, комментарий вшивает addTorrentWithComment
	if stub.lastAddedFile == nil {
		t.Fatal("expected fallback to add via qbit fetchMetadata/saveMetadata")
	}
	mi, err := metainfo.Load(bytes.NewReader(stub.lastAddedFile))
	if err != nil {
		t.Fatalf("decode uploaded torrent: %v", err)
	}
	if mi.Comment != opts.Comment {
		t.Errorf("comment = %q, want %q", mi.Comment, opts.Comment)
	}
}

func TestAddFromMagnet_OwnFetchHangs_FallbackStillGetsBudget(t *testing.T) {
	// регресс: добыча без своего потолка съедала весь ctx, и запасной путь получал истекший ctx.
	// Заглушка висит до конца своего ctx, фолбэк все равно должен успеть
	orig := magnetFetchTimeout
	magnetFetchTimeout = 50 * time.Millisecond
	t.Cleanup(func() { magnetFetchTimeout = orig })

	stub := newSweepStubServer(t)
	metaData, metaHash := buildNamedTestTorrent(t, "hang-then-fallback.txt")
	stub.metadataTorrent = metaData
	c, err := New(model.ManagerConfig{Host: stub.server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	withMagnetFetchStub(t, func(ctx context.Context, magnet string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	opts := model.ManagerAddFileOptions{Hash: metaHash, Name: "hang-then-fallback", Comment: "http://tracker.example/topic/3"}
	magnet := "magnet:?xt=urn:btih:" + metaHash

	// дедлайн на случай, если кто-то уберет свой подконтекст: с Background() заглушка
	// повисла бы навсегда и тест умер бы через 10 минут по таймауту раннера
	ctx, cancel := context.WithTimeout(context.Background(), 3*magnetFetchTimeout)
	defer cancel()
	if err := c.AddFromMagnet(ctx, magnet, opts); err != nil {
		t.Fatalf("AddFromMagnet: %v (fallback должен был отработать)", err)
	}
	if stub.lastAddedFile == nil {
		t.Fatal("expected fallback add via qbit fetchMetadata despite hanging own fetch")
	}
}

func TestAddFromMagnet_DeadSwarmBothPaths_FallsBackToMagnetAdd(t *testing.T) {
	// регресс: своя добыча съедала magnetFetchTimeout, и путь через qbit упирался во внешний
	// дедлайн раньше своего таймаута, DeadlineExceeded никто не ловил. Рой мертв для обоих путей,
	// и при нормальном бюджете фолбэк должен сработать
	origMagnet, origMeta, origPoll := magnetFetchTimeout, metadataFetchTimeout, metadataFetchPollInterval
	magnetFetchTimeout = 20 * time.Millisecond
	metadataFetchTimeout = 60 * time.Millisecond
	metadataFetchPollInterval = 5 * time.Millisecond // меньше таймаута, иначе он не сработает раньше внешнего ctx
	t.Cleanup(func() {
		magnetFetchTimeout, metadataFetchTimeout, metadataFetchPollInterval = origMagnet, origMeta, origPoll
	})
	// оба потолка плюс запас, как в blackhole.AddTimeout
	budget := magnetFetchTimeout + metadataFetchTimeout + 20*time.Millisecond

	stub := newSweepStubServer(t)
	stub.fetchMetadataForeverPending = true // qbit тоже никогда не находит метаданные
	_, hash := buildNamedTestTorrent(t, "dead-swarm-both-paths.txt")
	c, err := New(model.ManagerConfig{Host: stub.server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	withMagnetFetchStub(t, func(ctx context.Context, magnet string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	opts := model.ManagerAddFileOptions{Hash: hash, Name: "dead-swarm-both-paths", Comment: "http://tracker.example/topic/5"}
	magnet := "magnet:?xt=urn:btih:" + hash

	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	if err := c.AddFromMagnet(ctx, magnet, opts); err != nil {
		t.Fatalf("AddFromMagnet: %v (при бюджете magnetFetchTimeout+metadataFetchTimeout+запас фолбэк на добавление без комментария обязан успеть)", err)
	}
	if stub.lastAddedURL == "" {
		t.Fatal("expected fallback magnet-url add (без комментария), got none")
	}
	if stub.lastAddedFile != nil {
		t.Error("не ожидался файловый путь: metadataFetchTimeout обязан был сработать раньше saveMetadata")
	}
}

func TestAddFromMagnet_CanceledContext_ReturnsQuicklyNotAfterTimeout(t *testing.T) {
	// fctx должен наследовать отмену входящего ctx. Заведи его от Background(),
	// и заглушка провисела бы весь magnetFetchTimeout
	orig := magnetFetchTimeout
	magnetFetchTimeout = time.Second
	t.Cleanup(func() { magnetFetchTimeout = orig })

	stub := newSweepStubServer(t)
	_, hash := buildNamedTestTorrent(t, "canceled-ctx.txt")
	c, err := New(model.ManagerConfig{Host: stub.server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	withMagnetFetchStub(t, func(ctx context.Context, magnet string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := model.ManagerAddFileOptions{Hash: hash, Name: "canceled-ctx", Comment: "http://tracker.example/topic/6"}
	magnet := "magnet:?xt=urn:btih:" + hash

	start := time.Now()
	_ = c.AddFromMagnet(ctx, magnet, opts)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("AddFromMagnet с заранее отменённым ctx занял %v, ожидался быстрый возврат (fctx обязан наследовать отмену родителя, а не ждать свой magnetFetchTimeout=%v)", elapsed, magnetFetchTimeout)
	}
}
