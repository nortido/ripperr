package storage

import "time"

type CheckLogEntry struct {
	ID            int64
	CheckedAt     time.Time
	URLHash       string
	ItemName      string
	ItemURL       string
	TrackerTypeID int
	Status        string
	Error         string
	ErrorStreak   int
	RecheckAt     *time.Time
	ForceCheck    bool
}

type CheckLogFilter struct {
	Limit        int
	Offset       int
	Status       string
	IgnoredOnly  bool
	SortBy       string
	Name         string
	BrokenStreak int
	TrackerID    int
	Category     string
}

type CheckLogSummary struct {
	Updated int
	Errors  int
}

type IgnoreEntry struct {
	URLHash  string
	ItemName string
	ItemURL  string
	AddedAt  time.Time
}

type DueItem struct {
	URLHash  string
	ItemName string
}
