package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/runner"
	"ripperr/internal/storage/client/sqlite"
)

type flakyReader struct {
	failures int32
	calls    atomic.Int32
}

func (f *flakyReader) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	n := f.calls.Add(1)
	if f.failures < 0 || n <= f.failures {
		return nil, false, errors.New("decrypt failed")
	}
	if key == sqlite.SettingSchedule {
		return json.RawMessage(`{"list_refresh_per_day":8}`), true, nil
	}
	return nil, false, nil
}

func TestOverlaidConfigRetriesUntilReadable(t *testing.T) {
	old := overrideRetryBase
	overrideRetryBase = time.Millisecond
	t.Cleanup(func() { overrideRetryBase = old })

	base := config.DefaultApp()
	base.Runner.ListRefreshPerDay = 2
	r := &flakyReader{failures: 2}

	cfg, ok := overlaidConfig(context.Background(), base, r, make(chan struct{}), nil)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if cfg.Runner.ListRefreshPerDay != 8 {
		t.Fatalf("perDay = %d, want 8 (overlay applied after retries)", cfg.Runner.ListRefreshPerDay)
	}
	if got := r.calls.Load(); got < 3 {
		t.Fatalf("GetSetting calls = %d, want >= 3", got)
	}
}

func TestOverlaidConfigStopsOnShutdown(t *testing.T) {
	old := overrideRetryBase
	overrideRetryBase = time.Millisecond
	t.Cleanup(func() { overrideRetryBase = old })

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	r := &flakyReader{failures: -1}

	done := make(chan bool, 1)
	go func() {
		_, ok := overlaidConfig(ctx, config.DefaultApp(), r, make(chan struct{}), nil)
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("ok = true after shutdown, want false")
		}
	case <-time.After(time.Second):
		t.Fatal("overlaidConfig did not return within 1s of shutdown")
	}
}

func TestOverlaidConfigReloadRetriesAtOnce(t *testing.T) {
	old := overrideRetryBase
	overrideRetryBase = time.Hour
	t.Cleanup(func() { overrideRetryBase = old })

	reloadCh := make(chan struct{}, 1)
	reloadCh <- struct{}{}
	r := &flakyReader{failures: 1}

	done := make(chan config.App, 1)
	go func() {
		cfg, ok := overlaidConfig(context.Background(), config.DefaultApp(), r, reloadCh, nil)
		if !ok {
			cfg.Runner.ListRefreshPerDay = -1
		}
		done <- cfg
	}()
	select {
	case cfg := <-done:
		if cfg.Runner.ListRefreshPerDay != 8 {
			t.Fatalf("perDay = %d, want 8", cfg.Runner.ListRefreshPerDay)
		}
	case <-time.After(time.Second):
		t.Fatal("reload did not cut the backoff")
	}
}

func TestOverlaidConfigNormalizationErrorRunsWithOverlay(t *testing.T) {
	old := overrideRetryBase
	overrideRetryBase = time.Hour
	t.Cleanup(func() { overrideRetryBase = old })

	base := config.DefaultApp()
	base.Runner.ListRefreshPerDay = 2
	base.Notify.WebhookURL = "ftp://example.com/hook"
	r := &flakyReader{}

	done := make(chan config.App, 1)
	go func() {
		cfg, ok := overlaidConfig(context.Background(), base, r, make(chan struct{}), nil)
		if !ok {
			cfg.Runner.ListRefreshPerDay = -1
		}
		done <- cfg
	}()
	select {
	case cfg := <-done:
		if cfg.Runner.ListRefreshPerDay != 8 {
			t.Fatalf("perDay = %d, want 8 (overlay applied, no retry)", cfg.Runner.ListRefreshPerDay)
		}
	case <-time.After(time.Second):
		t.Fatal("validation error was retried instead of running with the overlaid config")
	}
}

type flagSpyReader struct {
	flakyReader
	state *runner.State
	mu    sync.Mutex
	seen  []bool
}

func (f *flagSpyReader) GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error) {
	f.mu.Lock()
	f.seen = append(f.seen, f.state.WaitingSettings())
	f.mu.Unlock()
	return f.flakyReader.GetSetting(ctx, key)
}

func TestOverlaidConfigMarksWaiting(t *testing.T) {
	old := overrideRetryBase
	overrideRetryBase = time.Millisecond
	t.Cleanup(func() { overrideRetryBase = old })

	state := runner.NewState()
	r := &flagSpyReader{flakyReader: flakyReader{failures: 2}, state: state}

	if _, ok := overlaidConfig(context.Background(), config.DefaultApp(), r, make(chan struct{}), state); !ok {
		t.Fatal("ok = false, want true")
	}
	r.mu.Lock()
	seen := append([]bool(nil), r.seen...)
	r.mu.Unlock()
	if len(seen) < 3 || seen[0] || !seen[1] || !seen[2] {
		t.Fatalf("flag seen at GetSetting calls = %v, want false then true while waiting", seen)
	}
	if state.WaitingSettings() {
		t.Fatal("waiting flag still set after overlaidConfig returned")
	}
}

func TestOverlaidConfigClearsWaitingOnShutdown(t *testing.T) {
	old := overrideRetryBase
	overrideRetryBase = time.Millisecond
	t.Cleanup(func() { overrideRetryBase = old })

	state := runner.NewState()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	if _, ok := overlaidConfig(ctx, config.DefaultApp(), &flakyReader{failures: -1}, make(chan struct{}), state); ok {
		t.Fatal("ok = true after shutdown, want false")
	}
	if state.WaitingSettings() {
		t.Fatal("waiting flag still set after shutdown")
	}
}

func TestReloadMarksRestartDuringPass(t *testing.T) {
	tests := []struct {
		name  string
		setup func(s *runner.State, gen uint32)
		want  bool
	}{
		{"normal pass", func(s *runner.State, gen uint32) { s.Begin(gen); s.BeginNormal(gen) }, true},
		{"forced push only", func(s *runner.State, gen uint32) { s.Begin(gen) }, false},
		{"normal pass while stopping", func(s *runner.State, gen uint32) { s.Begin(gen); s.BeginNormal(gen); s.MarkStopping() }, false},
		{"idle", func(*runner.State, uint32) {}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := runner.NewState()
			gen := state.Reset()
			tc.setup(state, gen)
			ch := make(chan struct{}, 1)

			newReload(state, ch)()

			if got := state.ConsumeRestart(); got != tc.want {
				t.Fatalf("ConsumeRestart = %v, want %v", got, tc.want)
			}
			select {
			case <-ch:
			default:
				t.Fatal("reload signal not sent")
			}
		})
	}
}

func effectiveCfg(magnet int64, proxy string) config.App {
	cfg := config.DefaultApp()
	cfg.Runner.ListRefreshPerDay = 1
	cfg.Runner.MagnetTimeout = magnet
	cfg.Runner.DownloadProxy = proxy
	return cfg
}

func TestValidateEffectiveStoredOverridesBadEnv(t *testing.T) {
	if err := validateEffective(effectiveCfg(1, ""), effectiveCfg(60, "")); err != nil {
		t.Fatalf("stored magnet timeout overrides bad env, got %v", err)
	}
}

func TestValidateEffectiveStoredProxyOverridesBadEnv(t *testing.T) {
	if err := validateEffective(effectiveCfg(60, "proxy.lan:3128"), effectiveCfg(60, "http://db-proxy:3128")); err != nil {
		t.Fatalf("stored proxy overrides bad env, got %v", err)
	}
}

func TestValidateEffectiveStopsOnBadEnvInEffect(t *testing.T) {
	err := validateEffective(effectiveCfg(1, ""), effectiveCfg(1, ""))
	if err == nil || !strings.Contains(err.Error(), "RUNNER_MAGNET_TIMEOUT") {
		t.Fatalf("bad env magnet timeout in effect must fail, got %v", err)
	}
	err = validateEffective(effectiveCfg(60, "proxy.lan:3128"), effectiveCfg(60, "proxy.lan:3128"))
	if err == nil || !strings.HasPrefix(err.Error(), "RUNNER_DOWNLOAD_PROXY: ") {
		t.Fatalf("bad env proxy in effect must fail with prefix, got %v", err)
	}
}

func TestValidateEffectiveChecksSchedule(t *testing.T) {
	cfg := effectiveCfg(60, "")
	cfg.Runner.ListRefreshPerDay = 5
	err := validateEffective(effectiveCfg(60, ""), cfg)
	if err == nil || !strings.Contains(err.Error(), "RUNNER_LIST_REFRESH_PER_DAY") {
		t.Fatalf("bad refresh per day must fail, got %v", err)
	}
}

func TestValidateEffectiveStopsOnBadEnvMirror(t *testing.T) {
	base := effectiveCfg(60, "")
	base.MirrorSource = config.MirrorSource{Type: "custom", URL: " https://mirror.example/list "}
	cfg := base
	cfg.MirrorSource.URL = "https://mirror.example/list"
	err := validateEffective(base, cfg)
	if err == nil || !strings.HasPrefix(err.Error(), "MIRROR_SOURCE: ") {
		t.Fatalf("bad env mirror in effect must fail with prefix, got %v", err)
	}
	base.MirrorSource = config.MirrorSource{Type: "bogus"}
	err = validateEffective(base, base)
	if err == nil || !strings.HasPrefix(err.Error(), "MIRROR_SOURCE: ") {
		t.Fatalf("bad env mirror type in effect must fail with prefix, got %v", err)
	}
}

func TestValidateEffectiveStoredMirrorOverridesBadEnv(t *testing.T) {
	base := effectiveCfg(60, "")
	base.MirrorSource = config.MirrorSource{Type: "custom", URL: "https://mirror.example/list"}
	cfg := base
	cfg.MirrorSource.URL = "https://mirror.example/{path}"
	if err := validateEffective(base, cfg); err != nil {
		t.Fatalf("stored mirror url overrides bad env, got %v", err)
	}
	base.MirrorSource = config.MirrorSource{Type: "bogus"}
	cfg.MirrorSource = config.MirrorSource{Type: "github"}
	if err := validateEffective(base, cfg); err != nil {
		t.Fatalf("stored mirror type overrides bad env, got %v", err)
	}
	base.MirrorSource = config.MirrorSource{Type: "custom", URL: "https://mirror.example/list"}
	cfg.MirrorSource = base.MirrorSource
	cfg.MirrorSource.Direct = true
	if err := validateEffective(base, cfg); err == nil {
		t.Fatal("Direct alone must not skip checking a bad env mirror")
	}
}
