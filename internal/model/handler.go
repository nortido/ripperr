package model

import "time"

type HandlerConfig struct {
	ProcessDelay time.Duration
	ChunkSize    int
	WatchTag     string
	MinDateDiff  int64
}
