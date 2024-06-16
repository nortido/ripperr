package model

import "time"

type HandlerConfig struct {
	ProcessDelay time.Duration
	RunsPerDay   int
	WatchTag     string
	MinDateDiff  int64
}
