package model

type TorrentFilterOptions struct {
	Tag string
}

type ManagerAddFileOptions struct {
	Tag      string
	Category string
}

type ManagerConfig struct {
	Host     string
	Username string
	Password string
}
