package model

import "time"

type HandlerConfig struct {
	ProcessTimeout time.Duration
	RunsPerDay     int
	WatchTag       string
	MinDateDiff    int64
}
