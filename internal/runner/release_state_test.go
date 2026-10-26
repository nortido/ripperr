package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func requireUnavailable(t *testing.T, s *State, label string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.AddRelease(ctx, AddRequest{}); !errors.Is(err, ErrPollUnavailable) {
		t.Errorf("%s: AddRelease err = %v, want ErrPollUnavailable", label, err)
	}
	if _, err := s.ReleaseRemoval(ctx, "h"); !errors.Is(err, ErrPollUnavailable) {
		t.Errorf("%s: ReleaseRemoval err = %v, want ErrPollUnavailable", label, err)
	}
	if err := s.RemoveRelease(ctx, RemoveRequest{}); !errors.Is(err, ErrPollUnavailable) {
		t.Errorf("%s: RemoveRelease err = %v, want ErrPollUnavailable", label, err)
	}
}

func TestReleaseActionsUnavailable(t *testing.T) {
	var nilState *State
	requireUnavailable(t, nilState, "nil state")
	requireUnavailable(t, NewState(), "no actions")

	s := NewState()
	s.SetReleaseActions(s.Reset(), &ReleaseActions{
		Add: func(context.Context, AddRequest) (AddResult, error) { return AddResult{}, nil },
	})
	ctx := context.Background()
	if _, err := s.AddRelease(ctx, AddRequest{}); err != nil {
		t.Errorf("AddRelease with Add set: err = %v", err)
	}
	if _, err := s.ReleaseRemoval(ctx, "h"); !errors.Is(err, ErrPollUnavailable) {
		t.Errorf("ReleaseRemoval without func: err = %v, want ErrPollUnavailable", err)
	}
	if err := s.RemoveRelease(ctx, RemoveRequest{}); !errors.Is(err, ErrPollUnavailable) {
		t.Errorf("RemoveRelease without func: err = %v, want ErrPollUnavailable", err)
	}
}

func TestReleaseActionsGeneration(t *testing.T) {
	ctx := context.Background()
	actions := func(n int) *ReleaseActions {
		return &ReleaseActions{
			Removal: func(context.Context, string) (RemovalInfo, error) { return RemovalInfo{Torrents: n}, nil },
		}
	}
	s := NewState()
	old := s.Reset()
	cur := s.Reset()
	s.SetReleaseActions(old, actions(1))
	if _, err := s.ReleaseRemoval(ctx, "h"); !errors.Is(err, ErrPollUnavailable) {
		t.Fatalf("stale gen registered: err = %v", err)
	}

	s.SetReleaseActions(cur, actions(2))
	if info, err := s.ReleaseRemoval(ctx, "h"); err != nil || info.Torrents != 2 {
		t.Fatalf("info = %+v, err = %v, want 2, nil", info, err)
	}
	s.SetReleaseActions(old, nil)
	if info, err := s.ReleaseRemoval(ctx, "h"); err != nil || info.Torrents != 2 {
		t.Fatalf("old gen nil dropped the current actions: info = %+v, err = %v", info, err)
	}

	s.SetReleaseActions(cur, nil)
	if _, err := s.ReleaseRemoval(ctx, "h"); !errors.Is(err, ErrPollUnavailable) {
		t.Fatalf("own nil: err = %v, want ErrPollUnavailable", err)
	}
}

func TestReleaseActionsPassThrough(t *testing.T) {
	boom := errors.New("boom")
	var gotAdd AddRequest
	var gotHash string
	var gotRemove RemoveRequest
	s := NewState()
	s.SetReleaseActions(s.Reset(), &ReleaseActions{
		Add: func(_ context.Context, r AddRequest) (AddResult, error) {
			gotAdd = r
			return AddResult{Unignored: true}, boom
		},
		Removal: func(_ context.Context, h string) (RemovalInfo, error) {
			gotHash = h
			return RemovalInfo{Torrents: 3, Files: true}, ErrReleaseBusy
		},
		Remove: func(_ context.Context, r RemoveRequest) error {
			gotRemove = r
			return ErrTorrentsChanged
		},
	})
	ctx := context.Background()

	wantAdd := AddRequest{URL: "u", Category: "c", Tag: "t"}
	res, err := s.AddRelease(ctx, wantAdd)
	if gotAdd != wantAdd || !res.Unignored || !errors.Is(err, boom) {
		t.Errorf("AddRelease: got req %+v, res %+v, err %v", gotAdd, res, err)
	}
	info, err := s.ReleaseRemoval(ctx, "hash")
	if gotHash != "hash" || info != (RemovalInfo{Torrents: 3, Files: true}) || !errors.Is(err, ErrReleaseBusy) {
		t.Errorf("ReleaseRemoval: got hash %q, info %+v, err %v", gotHash, info, err)
	}
	wantRemove := RemoveRequest{URLHash: "hash", Files: true, Torrents: 3}
	err = s.RemoveRelease(ctx, wantRemove)
	if gotRemove != wantRemove || !errors.Is(err, ErrTorrentsChanged) {
		t.Errorf("RemoveRelease: got req %+v, err %v", gotRemove, err)
	}
}

func TestReleaseActionsCalledWithoutLock(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	entered := make(chan struct{})
	release := make(chan struct{})
	s.SetReleaseActions(gen, &ReleaseActions{
		Remove: func(context.Context, RemoveRequest) error {
			close(entered)
			<-release
			return nil
		},
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.RemoveRelease(context.Background(), RemoveRequest{})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Remove was not called")
	}

	returned := make(chan struct{})
	go func() {
		s.SetReleaseActions(gen, nil)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Error("SetReleaseActions waited for the running action")
	}
	close(release)
	<-done
	<-returned
}

func TestRemovedMarks(t *testing.T) {
	s := NewState()
	s.MarkRemoved("AB")
	if !s.Removed("ab") || !s.Removed("AB") {
		t.Fatal("mark not found by lower or upper case hash")
	}
	markTime := s.removed["ab"]

	s.ClearRemoved(markTime.Add(-time.Second), []string{"ab"})
	if !s.Removed("ab") {
		t.Fatal("listing started before the mark cleared it")
	}
	now := markTime.Add(time.Second)
	s.ClearRemoved(now, []string{"cd"})
	if !s.Removed("ab") {
		t.Fatal("clearing another hash removed the mark")
	}
	s.ClearRemoved(now, []string{"AB"})
	if s.Removed("ab") {
		t.Fatal("listing started after the mark did not clear it")
	}

	s.MarkRemoved("")
	if s.Removed("") || len(s.removed) != 0 {
		t.Fatalf("empty hash left a mark: %v", s.removed)
	}
	s.MarkRemoved("x", "", "Y")
	if !s.Removed("x") || !s.Removed("y") || len(s.removed) != 2 {
		t.Fatalf("variadic marks = %v", s.removed)
	}

	var nilState *State
	nilState.MarkRemoved("ab")
	nilState.ClearRemoved(now, []string{"ab"})
	if nilState.Removed("ab") {
		t.Fatal("nil state reports a mark")
	}
}
