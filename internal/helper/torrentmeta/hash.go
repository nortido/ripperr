package torrentmeta

import (
	"bytes"

	"github.com/anacrolix/torrent/metainfo"
)

func InfoHash(data []byte) (string, error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	return mi.HashInfoBytes().HexString(), nil
}
