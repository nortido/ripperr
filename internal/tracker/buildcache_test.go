package tracker

import (
	"context"
	"net/http"
	"testing"
	"time"

	"trackerr/internal/helper"
	proxyModel "trackerr/internal/model/proxy"
	trackerModel "trackerr/internal/model/tracker"
	"trackerr/internal/tracker/client"
	"trackerr/internal/tracker/client/kinozal"
	"trackerr/internal/tracker/client/rutor"
)

type buildCacheDB struct{}

func (buildCacheDB) GetTracker(_ context.Context, id int) (*trackerModel.Tracker, error) {
	return &trackerModel.Tracker{TypeID: id, Exists: true}, nil
}

func (buildCacheDB) SaveTracker(context.Context, *trackerModel.Tracker) error { return nil }

func newBuildCacheService(registry map[int]client.ITracker) *service {
	return &service{
		hc:                 &http.Client{Timeout: 5 * time.Second},
		downloadHC:         &http.Client{Timeout: 5 * time.Second},
		px:                 stubFlareProxy{},
		db:                 buildCacheDB{},
		cbCfg:              &helper.CircuitBreakerConfig{},
		handlerRegistry:    registry,
		clientIndex:        make(map[int]*CachedTracker),
		flareTimeoutCounts: make(map[int]int),
		staleUseCounts:     make(map[int]int),
		domainAlive:        func(context.Context, string) bool { return true },
	}
}

func publicRutor() client.ITracker {
	return rutor.New(trackerModel.Tracker{
		Name:      "rutor",
		Domain:    "rutor.info",
		Publicity: trackerModel.PublicityPublic,
		Enabled:   true,
	})
}

func failingKinozal() client.ITracker {
	return kinozal.New(trackerModel.Tracker{
		Name:        "kinozal",
		Domain:      "kinozal.tv",
		ProxyType:   proxyModel.TypeFlare,
		Publicity:   "private",
		LoginURL:    "https://kinozal.tv/takelogin.php",
		Credentials: "login=x&login_password=y",
		Enabled:     true,
	})
}

func TestBuildCachePartialSuccess(t *testing.T) {
	s := newBuildCacheService(map[int]client.ITracker{
		RutorID:   publicRutor(),
		KinozalID: failingKinozal(),
	})
	if err := s.BuildCache(context.Background()); err != nil {
		t.Fatalf("expected partial success, got err: %v", err)
	}
	if ct := s.clientIndex[RutorID]; ct == nil || ct.IClient == nil {
		t.Fatal("expected rutor client in cache")
	}
}

func TestBuildCacheAllFail(t *testing.T) {
	s := newBuildCacheService(map[int]client.ITracker{
		KinozalID: failingKinozal(),
		RutorID:   failingKinozal(),
	})
	if err := s.BuildCache(context.Background()); err == nil {
		t.Fatal("expected error when all trackers fail")
	}
}
