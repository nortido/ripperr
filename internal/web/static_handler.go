package web

import (
	"embed"
	"fmt"
	"hash/fnv"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

type cachedStaticHandler struct {
	fs    fs.FS
	etags map[string]string
}

func newCachedStaticHandler(staticFS embed.FS) (http.Handler, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	etags := make(map[string]string)
	err = fs.WalkDir(sub, ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(sub, rel)
		if err != nil {
			return err
		}
		h := fnv.New64a()
		_, _ = h.Write(data)
		etags["/"+rel] = fmt.Sprintf(`"%x"`, h.Sum64())
		return nil
	})
	if err != nil {
		return nil, err
	}
	if etag, ok := etags["/index.html"]; ok {
		etags["/"] = etag
	}
	return &cachedStaticHandler{fs: sub, etags: etags}, nil
}

func (h *cachedStaticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Cache-Control", "no-cache")

	urlPath := path.Clean(r.URL.Path)
	if urlPath == "/" {
		urlPath = "/index.html"
	}
	if strings.Contains(urlPath, "..") {
		http.NotFound(w, r)
		return
	}

	etag, ok := h.etags[urlPath]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("ETag", etag)
	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	data, err := fs.ReadFile(h.fs, strings.TrimPrefix(urlPath, "/"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if ct := mime.TypeByExtension(path.Ext(urlPath)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(data)
}

func etagMatch(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == etag || part == "*" {
			return true
		}
	}
	return false
}
