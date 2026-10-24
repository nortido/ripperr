package model

import "errors"

var (
	ErrAlreadyAdded       = errors.New("torrent already added")
	ErrPartialList        = errors.New("partial torrent list")
	ErrHashUnchanged      = errors.New("torrent hash unchanged, existing torrent kept as is")
	ErrReplacePending     = errors.New("previous replace not finished")
	ErrCommentUnsupported = errors.New("client cannot set torrent comment")
	ErrExportUnsupported  = errors.New("client cannot export torrent")
)

type FilterOptions struct{}

type ManagerAddFileOptions struct {
	Hash           string
	Name           string
	Category       string
	SavePath       string
	Tag            string
	AutoManaged    bool
	MaxRatio       float64
	MaxSeedingTime int64
	Comment        string
	Stopped        bool
	ReplacesHash   string
	// выбор файлов прежней раздачи, nil = качать все
	FileSelection []TorrentFile
}

type RedownloadOptions struct {
	// пусто: комментарий торрента остается прежним
	Comment string
}

type TorrentFile struct {
	Path   string
	Wanted bool
}

type ManagerDeleteOptions struct {
	Hash     string
	Category string
	Name     string
	// решение принимает вызывающий один раз, клиент может только сузить его своей проверкой
	DeleteFiles bool
}

type ManagerConfig struct {
	Host               string
	Username           string
	Password           string
	PausedMaxRatio     float64
	DeleteEnabled      bool
	DeleteFilesEnabled bool
	DeleteCategories   []string
	BlackholeDir       string
}
