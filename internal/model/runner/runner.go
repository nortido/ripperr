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
	CreatedAt     *time.Time
	RecheckAt     *time.Time
	ForceCheck    bool
}
