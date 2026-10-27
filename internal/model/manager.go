package model

import "errors"

var (
	ErrAlreadyAdded       = errors.New("torrent already added")
	ErrPartialList        = errors.New("partial torrent list")
	ErrHashUnchanged      = errors.New("torrent hash unchanged, existing torrent kept as is")
	ErrReplacePending     = errors.New("previous replace not finished")
	ErrCommentUnsupported = errors.New("client cannot set torrent comment")
	ErrExportUnsupported  = errors.New("client cannot export torrent")
	ErrFilesNotAllowed    = errors.New("deleting files is not allowed for this torrent")
	ErrRemoveUnconfirmed  = errors.New("client still lists the removed torrent")
)

type FilterOptions struct {
	// qBittorrent зовет его до запроса свойств и берет их только для оставленных торрентов,
	// остальные клиенты поле не читают
	Narrow func([]*Entry) []*Entry
}

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
	// имена файлов прежней раздачи по позиции, nil = переименований не было
	FilePaths []string
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
	// решение принимает вызывающий: замена сужает его настройками клиента, а manager.Remove
	// удаляет файлы без проверок, общую папку проверяет runner
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
