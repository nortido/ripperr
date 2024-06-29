package model

import "time"

type HandlerConfig struct {
	WorkerDelay      time.Duration
	WorkerChunkLimit int
	WatchTag         string
	ThresholdMonth   int
	ThresholdYear    int
	ThresholdOld     int
}
