package storage

import "time"

type EventEntry struct {
	ID      int64
	At      time.Time
	Source  string
	Kind    string
	Text    string
	Subject string
	AppPath string
}
