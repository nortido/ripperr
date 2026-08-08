package torrentmeta

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/anacrolix/log"
	"github.com/anacrolix/torrent"
)

const magnetMetadataTimeout = 5 * time.Minute

func TorrentFromMagnet(ctx context.Context, magnetURI, comment string) ([]byte, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.ListenPort = 0
	cfg.Logger = log.Default.WithNames("trackerr")
	cfg.Logger.SetHandlers(log.DiscardHandler)

	tmp, err := os.MkdirTemp("", "trackerr-metainfo-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cfg.DataDir = tmp

	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	t, err := client.AddMagnet(magnetURI)
	if err != nil {
		return nil, err
	}
	defer t.Drop()

	ctx, cancel := context.WithTimeout(ctx, magnetMetadataTimeout)
	defer cancel()

	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return nil, fmt.Errorf("metadata fetch: %w", ctx.Err())
	}

	mi := t.Metainfo()
	mi.Comment = comment

	var buf bytes.Buffer
	if err = mi.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
