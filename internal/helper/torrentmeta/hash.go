package torrentmeta

import (
	"bytes"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

func InfoHash(data []byte) (string, error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	return mi.HashInfoBytes().HexString(), nil
}

func SameHash(data []byte, magnet, existing string) (same, known bool) {
	if existing == "" {
		return false, false
	}
	if len(data) > 0 {
		hash, err := InfoHash(data)
		if err != nil {
			return false, false
		}
		return strings.EqualFold(hash, existing), true
	}
	if magnet != "" {
		hash, ok := BtihFromMagnet(magnet)
		if !ok {
			return false, false
		}
		return strings.EqualFold(hash, existing), true
	}
	return false, false
}
