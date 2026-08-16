package storage

import "time"

type Tracker struct {
	TypeID      int
	Name        string
	Domain      string
	LoginURL    string
	DownloadURL string
	Credentials string
	Cookies     string
	Publicity   string
	Exists      bool
}

type Item struct {
	URLHash       string
	TrackerTypeID int
	TopicID       string
	Category      string
	Tag           string
	Name          string
	URL           string
	CreatedAt     *time.Time
	RecheckAt     *time.Time
	ForceCheck    bool
	Exists        bool
}
