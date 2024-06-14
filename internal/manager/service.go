package manager

import (
	"bytes"
	"context"

	"torr/internal/model"
)

type torrentClient interface {
	List(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error)
	AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
	Delete(ctx context.Context, hash string) (err error)
}

type TorrentManager interface {
	GetEntries(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error)
	Submit(ctx context.Context, e *model.Entry, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
}

type manager struct {
	offset int
	client torrentClient
	index  chan *model.Entry
}

func (m *manager) GetEntries(ctx context.Context, opts model.TorrentFilterOptions) (entries []*model.Entry, err error) {
	if entries, err = m.client.List(ctx, opts); err != nil {
		return
	}
	return
}

func (m *manager) Submit(ctx context.Context, e *model.Entry, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error) {
	if buf == nil || buf.Len() == 0 {
		return
	}
	//if err = m.client.Delete(ctx, e.Hash); err != nil {
	//	return
	//}
	err = m.client.AddFromBuffer(ctx, buf, opts)
	return
}

func NewManager(client torrentClient) TorrentManager {
	return &manager{
		client: client,
	}
}
