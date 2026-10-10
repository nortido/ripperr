package tracker

import (
	"context"
	"errors"
	"testing"

	"ripperr/internal/tracker/client"
)

func TestGetCachedClientFailureIsTrackerUnavailable(t *testing.T) {
	s := newBuildCacheService(map[int]client.ITracker{KinozalID: failingKinozal()})
	if _, err := s.GetCachedClient(context.Background(), KinozalID); !errors.Is(err, client.ErrTrackerUnavailable) {
		t.Fatalf("login failure err = %v, want ErrTrackerUnavailable", err)
	}
}

func TestGetCachedClientUnknownTrackerIsTrackerUnavailable(t *testing.T) {
	s := newBuildCacheService(map[int]client.ITracker{KinozalID: failingKinozal()})
	for i := 1; i <= 2; i++ {
		if _, err := s.GetCachedClient(context.Background(), 99); !errors.Is(err, client.ErrTrackerUnavailable) {
			t.Fatalf("call %d err = %v, want ErrTrackerUnavailable", i, err)
		}
	}
}

func TestResponseFailureIsTrackerUnavailable(t *testing.T) {
	s := newBuildCacheService(map[int]client.ITracker{KinozalID: failingKinozal()})
	if _, err := s.Response(context.Background(), KinozalID, "https://kinozal.tv/details.php?id=1", false); !errors.Is(err, client.ErrTrackerUnavailable) {
		t.Fatalf("Response err = %v, want ErrTrackerUnavailable", err)
	}
}
