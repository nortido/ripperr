package manager

import (
	"bytes"
	"context"

	"trackerr/internal/model"
)

type client interface {
	List(ctx context.Context, opts model.FilterOptions) (entries []*model.Entry, err error)
	GetTorrentDetails(ctx context.Context, hash string) (name, comment string, err error)
	IncomingHashSameAs(buf *bytes.Buffer, magnet string, existing string) (same bool, known bool)
	AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
	AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error)
	Delete(ctx context.Context, opts model.ManagerDeleteOptions) (err error)
	WriteBlackholePair(data []byte, magnet string, opts model.ManagerAddFileOptions) (err error)
	RemoveBlackholePair(hash string)
	ConfirmSettled(ctx context.Context, hash string) (settled bool)
}

type manager struct {
	client client
}

func (m *manager) GetEntries(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error) {
	return m.client.List(ctx, opts)
}

func (m *manager) Submit(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error) {
	if buf == nil || buf.Len() == 0 {
		return
	}
	return m.replaceTorrent(ctx, buf, "", opts, func(opts model.ManagerAddFileOptions) error {
		return m.client.AddFromBuffer(ctx, buf, opts)
	})
}

func (m *manager) SubmitMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error) {
	if magnet == "" {
		return
	}
	return m.replaceTorrent(ctx, nil, magnet, opts, func(opts model.ManagerAddFileOptions) error {
		return m.client.AddFromMagnet(ctx, magnet, opts)
	})
}

// replaceTorrent resolves the current name/comment of an existing torrent (by
// hash) before deleting and re-adding it, so add receives the resolved opts
// rather than the caller's original values.
func (m *manager) replaceTorrent(ctx context.Context, buf *bytes.Buffer, magnet string, opts model.ManagerAddFileOptions, add func(model.ManagerAddFileOptions) error) (err error) {
	if opts.Hash != "" {
		if name, comment, detailErr := m.client.GetTorrentDetails(ctx, opts.Hash); detailErr == nil {
			if name != "" {
				opts.Name = name
			}
			if comment != "" && opts.Comment == "" {
				opts.Comment = comment
			}
		}
	}
	del := func() error {
		return m.client.Delete(ctx, model.ManagerDeleteOptions{Hash: opts.Hash, Category: opts.Category, Name: opts.Name})
	}
	same, known := m.client.IncomingHashSameAs(buf, magnet, opts.Hash)
	if known && same {
		var data []byte
		if buf != nil {
			data = buf.Bytes()
		}
		if err = m.client.WriteBlackholePair(data, magnet, opts); err != nil {
			return
		}
		if err = del(); err != nil {
			return
		}
		if err = add(opts); err != nil {
			return
		}
		// qbit's delete-with-files job from del() above can still be racing to
		// take the freshly re-added torrent back down; only drop the safety
		// net once presence survives the settle wait, otherwise leave it for
		// the sweeper
		if !m.client.ConfirmSettled(ctx, opts.Hash) {
			return
		}
		m.client.RemoveBlackholePair(opts.Hash)
		return
	}
	if err = add(opts); err != nil {
		return
	}
	return del()
}

func NewManager(client client) *manager {
	return &manager{
		client: client,
	}
}
