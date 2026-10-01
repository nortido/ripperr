package manager

import (
	"context"
	"errors"
	"fmt"

	"ripperr/internal/config"
	"ripperr/internal/manager/client/qbittorrent"
	"ripperr/internal/manager/client/transmission"
	"ripperr/internal/model"
)

type Client interface {
	client
	GetCategories(ctx context.Context) ([]string, error)
	ForceRedownload(ctx context.Context, torrentHash string) error
	SweepBlackhole(ctx context.Context)
}

const (
	ClientQbit         = "qbit"
	ClientTransmission = "transmission"
)

// ErrUnknownClient сигнализирует вызывающему коду про вид клиента, который никогда не
// подключится: в отличие от ошибки соединения, повторять попытку бессмысленно
var ErrUnknownClient = errors.New("unknown manager client")

// NormalizeClient проксирует config.NormalizeClient: реализация переехала в
// config (нормализация значения конфига, а не менеджера), но сюда уже привязаны
// все вызовы (run.go, web-обработчики), поэтому имя и сигнатуру в этом пакете
// сохраняем, а саму нормализацию не дублируем.
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
	default:
		return nil, fmt.Errorf("%w: %q (must be %q or %q)", ErrUnknownClient, kind, ClientQbit, ClientTransmission)
	}
}
