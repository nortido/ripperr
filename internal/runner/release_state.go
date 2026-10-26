package runner

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrUnknownTracker  = errors.New("link does not belong to an enabled tracker")
	ErrNotWatched      = errors.New("category or tag is not watched")
	ErrNoTorrent       = errors.New("no torrent or magnet link on the topic page")
	ErrReleaseBusy     = errors.New("torrent is being updated by a check, try again later")
	ErrTorrentsChanged = errors.New("torrents of this release changed in the client")
)

type AddRequest struct {
	URL      string
	Category string
	Tag      string
}

type AddResult struct {
	Unignored bool
}

type RemovalInfo struct {
	Torrents int
	Files    bool
}

type RemoveRequest struct {
	URLHash  string
	Files    bool
	Torrents int
}

type ReleaseActions struct {
	Add     func(context.Context, AddRequest) (AddResult, error)
	Removal func(context.Context, string) (RemovalInfo, error)
	Remove  func(context.Context, RemoveRequest) error
}

func (s *State) SetReleaseActions(gen uint32, a *ReleaseActions) {
	if s == nil {
		return
	}
	// замок поколения как у SetPoller: Reset между проверкой и записью оставил бы действия мертвого поколения
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actionsMu.Lock()
	defer s.actionsMu.Unlock()
	if a != nil {
		if gen != s.gen.Load() {
			return
		}
	} else if s.actions == nil || s.actionsGen != gen {
		return
	}
	s.actions = a
	s.actionsGen = gen
}

func (s *State) releaseActions() ReleaseActions {
	if s == nil {
		return ReleaseActions{}
	}
	s.actionsMu.Lock()
	defer s.actionsMu.Unlock()
	if s.actions == nil {
		return ReleaseActions{}
	}
	return *s.actions
}

func (s *State) AddRelease(ctx context.Context, req AddRequest) (AddResult, error) {
	fn := s.releaseActions().Add
	if fn == nil {
		return AddResult{}, ErrPollUnavailable
	}
	return fn(ctx, req)
}

func (s *State) ReleaseRemoval(ctx context.Context, urlHash string) (RemovalInfo, error) {
	fn := s.releaseActions().Removal
	if fn == nil {
		return RemovalInfo{}, ErrPollUnavailable
	}
	return fn(ctx, urlHash)
}

func (s *State) RemoveRelease(ctx context.Context, req RemoveRequest) error {
	fn := s.releaseActions().Remove
	if fn == nil {
		return ErrPollUnavailable
	}
	return fn(ctx, req)
}

func (s *State) MarkRemoved(hashes ...string) {
	if s == nil {
		return
	}
	now := time.Now()
	s.removedMu.Lock()
	defer s.removedMu.Unlock()
	for _, h := range hashes {
		if h == "" {
			continue
		}
		if s.removed == nil {
			s.removed = make(map[string]time.Time)
		}
		s.removed[strings.ToLower(h)] = now
	}
}

func (s *State) Removed(hash string) bool {
	if s == nil {
		return false
	}
	s.removedMu.Lock()
	defer s.removedMu.Unlock()
	_, ok := s.removed[strings.ToLower(hash)]
	return ok
}

func (s *State) ClearRemoved(listedAt time.Time, hashes []string) {
	if s == nil {
		return
	}
	s.removedMu.Lock()
	defer s.removedMu.Unlock()
	for _, h := range hashes {
		key := strings.ToLower(h)
		// список, начатый до метки, мог еще содержать удаленный торрент
		if at, ok := s.removed[key]; ok && at.Before(listedAt) {
			delete(s.removed, key)
		}
	}
}
