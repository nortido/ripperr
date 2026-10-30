package client

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
)

var challengeTitle = []byte("<title>just a moment...</title>")

func cloudflareChallenge(res *http.Response) (bool, error) {
	if strings.EqualFold(res.Header.Get("Cf-Mitigated"), "challenge") {
		return true, nil
	}
	if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusServiceUnavailable {
		return false, nil
	}
	switch strings.ToLower(res.Header.Get("Server")) {
	case "cloudflare", "cloudflare-nginx":
	default:
		return false, nil
	}
	buf := make([]byte, maxErrorResponse)
	n, err := io.ReadFull(res.Body, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	buf = buf[:n]
	// тело нужно дальше: checkResponse читает его для лога ошибки
	res.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(buf), res.Body), res.Body}
	return bytes.Contains(bytes.ToLower(buf), challengeTitle), nil
}
