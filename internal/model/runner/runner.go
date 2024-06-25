package runner

import "time"

type HistoryItem struct {
	URLHash       string
	TrackerTypeID int
	TopicID       string
	Category      string
	Tag           string
	Name          string
	URL           string
	Exists        bool
	RecheckAt     *time.Time
}
