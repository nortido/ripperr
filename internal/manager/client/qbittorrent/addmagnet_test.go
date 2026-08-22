package qbittorrent

import (
	"bytes"
	"context"
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
	stub := newSweepStubServer(t)
	// metadataTorrent намеренно не задан: если бы AddFromMagnet всё-таки
	// свалился в старый путь qbit fetchMetadata/saveMetadata, saveMetadata
	// ответил бы 404, а сам путь ушёл бы магнит-URL без комментария
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
	// прежнее поведение: fetchAndSaveMetadata через qbit, затем файл с
	// комментарием, вшитым addTorrentWithComment
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
	// регресс: magnetFetch без своего потолка съедал весь ctx AddFromMagnet
	// целиком (в бою это blackhole.AddTimeout), и запасной путь получал уже
	// истёкший контекст. Ужимаем потолок до долей секунды и проверяем, что
	// заглушка, висящая до конца СВОЕГО контекста, всё равно оставляет
	// запасному пути достаточно времени на успешное добавление.
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

	// внешний контекст с дедлайном порядка трёх потолков добычи, а не
	// context.Background(): мутация "убрать собственный подконтекст" (звать
	// magnetFetch(ctx, ...) вместо magnetFetch(fctx, ...)) с Background()
	// вешала бы заглушку навечно, и тест падал бы 10-минутной паникой
	// раннера вместо быстрого и внятного assert'а
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
	// регресс: своя добыча съедала magnetFetchTimeout, а запасной путь через
	// qbit упирался дедлайном внешнего контекста РАНЬШЕ своего
	// metadataFetchTimeout - waitForMetadata возвращал context.DeadlineExceeded
	// вместо errMetadataFetchTimeout, addMagnetWithComment эту ошибку не
	// распознавал и не падал в addMagnetURLVerified, торрент не добавлялся
	// никак. Чинится арифметикой blackhole.AddTimeout (см. тест-страж
	// TestBlackholeAddTimeoutOutlivesMetadataFetch); этот тест стережёт, что
	// с достаточным бюджетом фолбэк реально срабатывает, когда рой мёртв для
	// ОБОИХ путей добычи разом.
	origMagnet, origMeta, origPoll := magnetFetchTimeout, metadataFetchTimeout, metadataFetchPollInterval
	magnetFetchTimeout = 20 * time.Millisecond
	metadataFetchTimeout = 60 * time.Millisecond
	metadataFetchPollInterval = 5 * time.Millisecond // мельче таймаута, иначе деталь не успеет сработать раньше внешнего ctx
	t.Cleanup(func() {
		magnetFetchTimeout, metadataFetchTimeout, metadataFetchPollInterval = origMagnet, origMeta, origPoll
	})
	// сумма обоих потолков плюс запас - пропорция та же, что у
	// blackhole.AddTimeout в бою (см. комментарий там)
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
	// сторож на происхождение fctx: если бы подконтекст собственной добычи
	// заводился от context.Background() вместо входящего ctx, заранее
	// отменённый внешний контекст игнорировался бы, и заглушка провисела бы
	// весь magnetFetchTimeout вместо мгновенного возврата
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
