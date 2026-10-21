package transmission

import (
	"context"

	"ripperr/internal/model"
)

func (c *Client) SetComment(context.Context, string, string) error {
	// в torrent-set нет поля comment ни в одной версии RPC
	return model.ErrCommentUnsupported
}

func (c *Client) ExportTorrent(context.Context, string) ([]byte, error) {
	return nil, model.ErrExportUnsupported
}

func (c *Client) DeleteEnabled() bool {
	return c.config.DeleteEnabled
}
