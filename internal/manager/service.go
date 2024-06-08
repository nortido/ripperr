package manager

import (
	"context"
	"os"

	"torr/internal/model"
)

type torrentClient interface {
	GetEntries(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error)
	AddTorrentFromFile(ctx context.Context, path string, opts model.ManagerAddFileOptions) (err error)
	DeleteTorrent(ctx context.Context, hash string) (err error)
}

type TorrentManager interface {
	GetEntries(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error)
	Submit(ctx context.Context, e *model.Entry, path string, opts model.ManagerAddFileOptions) (err error)
}

type manager struct {
	offset int
	client torrentClient
	index  chan *model.Entry
}

func (m *manager) GetEntries(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error) {
	if entries, err = m.client.GetEntries(ctx, opts); err != nil {
		return
	}
	return
}

func (m *manager) Submit(ctx context.Context, e *model.Entry, path string, opts model.ManagerAddFileOptions) (err error) {
	if _, err = os.Stat(path); err != nil {
		return
	}
	if err = m.client.DeleteTorrent(ctx, e.Hash); err != nil {
		return
	}
	err = m.client.AddTorrentFromFile(ctx, path, opts)
	return
}

func NewManager(client torrentClient) TorrentManager {
	return &manager{
		client: client,
	}
}
