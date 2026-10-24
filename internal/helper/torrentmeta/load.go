package torrentmeta

import (
	"bytes"
	"fmt"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func load(data []byte) (*metainfo.MetaInfo, error) {
	var mi metainfo.MetaInfo
	d := bencode.NewDecoder(bytes.NewReader(data))
	// Строка не может быть длиннее самих данных: без лимита заявленная длина выделяет до 128 МиБ.
	d.MaxStrLen = int64(len(data))
	if err := d.Decode(&mi); err != nil {
		return nil, err
	}
	if err := d.ReadEOF(); err != nil {
		return nil, fmt.Errorf("error after decoding metainfo: %w", err)
	}
	return &mi, nil
}

func HasV2(data []byte) (bool, error) {
	mi, err := load(data)
	if err != nil {
		return false, err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return false, err
	}
	return info.HasV2(), nil
}
