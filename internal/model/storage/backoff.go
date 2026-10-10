package storage

import "time"

type ErrorMark struct {
	Streak    int
	CheckedAt time.Time
}
