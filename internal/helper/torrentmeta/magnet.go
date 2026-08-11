package torrentmeta

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
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

func BtihFromMagnet(magnet string) (hash string, ok bool) {
	u, err := url.Parse(magnet)
	if err != nil {
		return "", false
	}
	xt := u.Query().Get("xt")
	const prefix = "urn:btih:"
	if len(xt) < len(prefix) || !strings.EqualFold(xt[:len(prefix)], prefix) {
		return "", false
	}
	btih := xt[len(prefix):]
	if i := strings.IndexAny(btih, "&?"); i >= 0 {
		btih = btih[:i]
	}
	btih = strings.ToLower(btih)
	switch len(btih) {
	case 40:
		if _, err := hex.DecodeString(btih); err != nil {
			return "", false
		}
		return btih, true
	case 32:
		decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(btih))
		if err != nil || len(decoded) != 20 {
			return "", false
		}
		return hex.EncodeToString(decoded), true
	default:
		return "", false
	}
}
