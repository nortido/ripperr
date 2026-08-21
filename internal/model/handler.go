package model

import "time"

type HandlerConfig struct {
	WatchTags            []string
	ThresholdMonth       int
	ThresholdHalfYear    int
	ThresholdYear        int
	ThresholdOld         int
	ListRefreshPerDay    int
	StartHour            int
	Categories           []string
	Location             *time.Location // nil = time.Local (локальная зона процесса)
	Immediate            bool
	ErrorStreakThreshold int
	MagnetTimeout        int64 // секунды на добычу метаданных по магниту, см. runner.handler.submit
}
