package tracker

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/tracker/client"
)

func TestStaleOnErrorLimit(t *testing.T) {
	tr := publicRutor()
	hc := &http.Client{Timeout: 5 * time.Second}
	ic := client.New(hc, hc, tr, stubFlareProxy{}, helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{}))
	prev := &CachedTracker{IClient: ic, Created: time.Now().Add(-2 * TTL)}

	s := &service{
		staleUseCounts: make(map[int]int),
	}
	cacheErr := errors.New("login failed")

	for i := 1; i <= maxStaleUses; i++ {
		ct, err := s.staleOnError(RutorID, prev, "rutor", cacheErr)
		if err != nil {
			t.Fatalf("attempt %d: expected stale client, got err %v", i, err)
		}
		if ct != prev {
			t.Fatalf("attempt %d: expected previous cached client", i)
		}
	}

	ct, err := s.staleOnError(RutorID, prev, "rutor", cacheErr)
	if !errors.Is(err, cacheErr) {
		t.Fatalf("expected cache error after limit, got %v", err)
	}
	if ct != nil {
		t.Fatal("expected no client after stale limit exceeded")
	}
}

func TestStaleOnErrorResetsAfterSuccessfulCache(t *testing.T) {
	s := newBuildCacheService(map[int]client.ITracker{RutorID: publicRutor()})
	tr := publicRutor()
	hc := &http.Client{Timeout: 5 * time.Second}
	ic := client.New(hc, hc, tr, stubFlareProxy{}, helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{}))
	prev := &CachedTracker{IClient: ic, Created: time.Now().Add(-2 * TTL)}
	s.clientIndex[RutorID] = prev

	cacheErr := errors.New("login failed")
	for range maxStaleUses {
		if _, err := s.staleOnError(RutorID, prev, "rutor", cacheErr); err != nil {
			t.Fatalf("expected stale client before limit: %v", err)
		}
	}

	if _, err := s.getCached(context.Background(), RutorID); err != nil {
		t.Fatalf("expected successful cache refresh: %v", err)
	}
	s.flareMx.Lock()
	count := s.staleUseCounts[RutorID]
	s.flareMx.Unlock()
	if count != 0 {
		t.Fatalf("expected stale counter reset after cache refresh, got %d", count)
	}

	ct, err := s.staleOnError(RutorID, prev, "rutor", cacheErr)
	if err != nil {
		t.Fatalf("expected stale client again after reset: %v", err)
	}
	if ct != prev {
		t.Fatal("expected previous cached client after reset")
	}
}

func TestGetCachedRefreshFailureUsesStaleThenErrors(t *testing.T) {
	s := newBuildCacheService(map[int]client.ITracker{KinozalID: failingKinozal()})
	hc := &http.Client{Timeout: 5 * time.Second}
	ic := client.New(hc, hc, failingKinozal(), stubFlareProxy{}, helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{}))
	prev := &CachedTracker{IClient: ic, Created: time.Now().Add(-2 * TTL)}
	s.clientIndex[KinozalID] = prev

	for i := 1; i <= maxStaleUses; i++ {
		ct, err := s.getCached(context.Background(), KinozalID)
		if err != nil {
			t.Fatalf("attempt %d: expected stale client, got err %v", i, err)
		}
		if ct != prev {
			t.Fatalf("attempt %d: expected previous cached client", i)
		}
	}

	ct, err := s.getCached(context.Background(), KinozalID)
	if err == nil {
		t.Fatal("expected error after stale limit exceeded")
	}
	if ct != nil {
		t.Fatal("expected no client after stale limit exceeded")
	}
}
