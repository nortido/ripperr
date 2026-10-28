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
	Location             *time.Location // nil = time.Local
	Immediate            bool
	ErrorStreakThreshold int
	MagnetTimeout        int64 // сколько секунд ждать метаданные магнета, см. runner.handler.buildTorrentFromMagnet
}
