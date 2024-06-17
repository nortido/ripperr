package model

type TorrentFilterOptions struct {
	Tag string
}

type ManagerAddFileOptions struct {
	Category       string
	Tag            string
	AutoManaged    bool
	MaxRatio       float64
	MaxSeedingTime int64
}

type ManagerConfig struct {
	Host             string
	Username         string
	Password         string
	DeleteEnabled    bool
	DeleteCategories []string
}
