package manager

import (
	"context"
	"errors"
	"fmt"

	"ripperr/internal/config"
	"ripperr/internal/manager/client/deluge"
	"ripperr/internal/manager/client/downloadstation"
	"ripperr/internal/manager/client/qbittorrent"
	"ripperr/internal/manager/client/transmission"
	"ripperr/internal/model"
)

type Client interface {
	client
	GetCategories(ctx context.Context) ([]string, error)
	ForceRedownload(ctx context.Context, torrentHash string, opts model.RedownloadOptions) error
	SweepBlackhole(ctx context.Context)
}

const (
	ClientQbit            = "qbit"
	ClientTransmission    = "transmission"
	ClientDeluge          = "deluge"
	ClientDownloadStation = "downloadstation"
)

var ErrUnknownClient = errors.New("unknown manager client") // такой клиент не подключится никогда, повторять бессмысленно

func NormalizeClient(kind string) string {
	return config.NormalizeClient(kind)
}

func NewClient(kind string, cfg model.ManagerConfig) (Client, error) {
	kind = NormalizeClient(kind)
	switch kind {
	// пустая строка это старые базы и .env без MANAGER_CLIENT, совместимость с qbit
	case "", ClientQbit:
		return qbittorrent.New(cfg)
	case ClientTransmission:
		return transmission.New(cfg)
	case ClientDeluge:
		return deluge.New(cfg)
	case ClientDownloadStation:
		return downloadstation.New(cfg)
	default:
		return nil, fmt.Errorf("%w: %q (must be %q, %q, %q or %q)", ErrUnknownClient, kind, ClientQbit, ClientTransmission, ClientDeluge, ClientDownloadStation)
	}
}

func SupportsTags(kind string) bool {
	return config.ClientSupportsTags(kind)
}
