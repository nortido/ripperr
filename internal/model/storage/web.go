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
	RecheckAt     *time.Time
	ForceCheck    bool
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
