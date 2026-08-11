package model

import "errors"

var (
	ErrAlreadyAdded = errors.New("torrent already added")
	ErrPartialList  = errors.New("partial torrent list")
)

type FilterOptions struct{}

type ManagerAddFileOptions struct {
	Hash           string
	Name           string
	Category       string
	Tag            string
	AutoManaged    bool
	MaxRatio       float64
	MaxSeedingTime int64
	Comment        string
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
