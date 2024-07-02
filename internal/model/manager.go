package model

type FilterOptions struct {
	Tag string
}

type ManagerAddFileOptions struct {
	Hash           string
	Name           string
	Category       string
	Tag            string
	AutoManaged    bool
	MaxRatio       float64
	MaxSeedingTime int64
}

type ManagerDeleteOptions struct {
	Hash     string
	Category string
	Name     string
}

type ManagerConfig struct {
	Host               string
	Username           string
	Password           string
	PausedMaxRatio     float64
	DeleteEnabled      bool
	DeleteFilesEnabled bool
	DeleteCategories   []string
}
