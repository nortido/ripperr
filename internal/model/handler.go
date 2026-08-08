package model

type HandlerConfig struct {
	WatchTag          string
	ThresholdMonth    int
	ThresholdHalfYear int
	ThresholdYear     int
	ThresholdOld      int
	ListRefreshPerDay int
	Categories        []string
}
