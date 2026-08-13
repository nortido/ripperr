package torrentmeta

import (
	"encoding/base32"
	"encoding/hex"
	"net/url"
	"strings"
)

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
