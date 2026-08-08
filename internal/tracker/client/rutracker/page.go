package rutracker

import (
	"bytes"

	"golang.org/x/text/encoding/charmap"
)

func pageText(b *bytes.Buffer, encoded bool) string {
	if encoded {
		return b.String()
	}
	bb, _ := charmap.Windows1251.NewDecoder().Bytes(b.Bytes())
	return string(bb)
}
