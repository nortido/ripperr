package tlscapture

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/crypto/cryptobyte"
)

const (
	extSigAlgs    = 0x000d
	extALPN       = 0x0010
	extSupVersion = 0x002b
	emptyHash     = "000000000000"
)

func isGREASE(v uint16) bool {
	return v&0x0f0f == 0x0a0a && v>>8 == v&0xff
}

func JA4(record []byte) (string, error) {
	s := cryptobyte.String(record)
	var recType uint8
	var body cryptobyte.String
	if !s.ReadUint8(&recType) || recType != 0x16 || !s.Skip(2) || !s.ReadUint16LengthPrefixed(&body) {
		return "", errors.New("ja4: not a TLS handshake record")
	}
	var hsType uint8
	var hello cryptobyte.String
	if !body.ReadUint8(&hsType) || hsType != 1 || !body.ReadUint24LengthPrefixed(&hello) {
		return "", errors.New("ja4: not a ClientHello")
	}
	var legacy uint16
	var sessionID, cipherBytes, compression cryptobyte.String
	if !hello.ReadUint16(&legacy) || !hello.Skip(32) || !hello.ReadUint8LengthPrefixed(&sessionID) ||
		!hello.ReadUint16LengthPrefixed(&cipherBytes) || !hello.ReadUint8LengthPrefixed(&compression) {
		return "", errors.New("ja4: truncated ClientHello")
	}

	var ciphers []string
	for !cipherBytes.Empty() {
		var c uint16
		if !cipherBytes.ReadUint16(&c) {
			return "", errors.New("ja4: bad cipher list")
		}
		if !isGREASE(c) {
			ciphers = append(ciphers, fmt.Sprintf("%04x", c))
		}
	}

	var exts, hashExts, sigAlgs []string
	version := legacy
	alpn := "00"
	var extBytes cryptobyte.String
	if !hello.Empty() && !hello.ReadUint16LengthPrefixed(&extBytes) {
		return "", errors.New("ja4: bad extensions")
	}
	for !extBytes.Empty() {
		var id uint16
		var data cryptobyte.String
		if !extBytes.ReadUint16(&id) || !extBytes.ReadUint16LengthPrefixed(&data) {
			return "", errors.New("ja4: bad extension")
		}
		if isGREASE(id) {
			continue
		}
		name := fmt.Sprintf("%04x", id)
		exts = append(exts, name)
		if id != 0x0000 && id != extALPN {
			hashExts = append(hashExts, name)
		}
		switch id {
		case extALPN:
			alpn = firstALPN(data)
		case extSigAlgs:
			var list cryptobyte.String
			if !data.ReadUint16LengthPrefixed(&list) {
				return "", errors.New("ja4: bad signature algorithms")
			}
			for !list.Empty() {
				var sa uint16
				if !list.ReadUint16(&sa) {
					return "", errors.New("ja4: bad signature algorithms")
				}
				if !isGREASE(sa) {
					sigAlgs = append(sigAlgs, fmt.Sprintf("%04x", sa))
				}
			}
		case extSupVersion:
			v, err := highestVersion(data)
			if err != nil {
				return "", err
			}
			if v != 0 {
				version = v
			}
		}
	}

	sni := "i"
	if slices.Contains(exts, "0000") {
		sni = "d"
	}
	a := fmt.Sprintf("t%s%s%02d%02d%s", versionName(version), sni, min(len(ciphers), 99), min(len(exts), 99), alpn)

	slices.Sort(ciphers)
	b := hashList(strings.Join(ciphers, ","), len(ciphers) > 0)

	slices.Sort(hashExts)
	c := strings.Join(hashExts, ",")
	if len(sigAlgs) > 0 {
		c += "_" + strings.Join(sigAlgs, ",")
	}
	return a + "_" + b + "_" + hashList(c, len(hashExts) > 0), nil
}

func hashList(s string, nonEmpty bool) string {
	if !nonEmpty {
		return emptyHash
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func highestVersion(data cryptobyte.String) (uint16, error) {
	var list cryptobyte.String
	if !data.ReadUint8LengthPrefixed(&list) {
		return 0, errors.New("ja4: bad supported_versions")
	}
	var best uint16
	for !list.Empty() {
		var v uint16
		if !list.ReadUint16(&v) {
			return 0, errors.New("ja4: bad supported_versions")
		}
		if !isGREASE(v) && v > best {
			best = v
		}
	}
	return best, nil
}

func versionName(v uint16) string {
	switch v {
	case 0x0304:
		return "13"
	case 0x0303:
		return "12"
	case 0x0302:
		return "11"
	case 0x0301:
		return "10"
	case 0x0300:
		return "s3"
	}
	return "00"
}

func firstALPN(data cryptobyte.String) string {
	var list, proto cryptobyte.String
	if !data.ReadUint16LengthPrefixed(&list) || !list.ReadUint8LengthPrefixed(&proto) || len(proto) == 0 {
		return "00"
	}
	first, last := proto[0], proto[len(proto)-1]
	if isAlnum(first) && isAlnum(last) {
		return string([]byte{first, last})
	}
	h := hex.EncodeToString(proto)
	return string([]byte{h[0], h[len(h)-1]})
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}
