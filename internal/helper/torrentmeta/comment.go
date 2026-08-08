package torrentmeta

import (
	"bytes"

	"github.com/anacrolix/torrent/metainfo"
)

func SetComment(data []byte, comment string) ([]byte, error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	mi.Comment = comment
	var buf bytes.Buffer
	if err = mi.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
