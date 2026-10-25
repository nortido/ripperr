package fileselect

import (
	"strings"

	"ripperr/internal/model"
)

func Unwanted(old []model.TorrentFile, cur []string) []int {
	if len(cur) == 0 || !hasUnwanted(old) {
		return nil
	}
	oldPaths := make([]string, len(old))
	for i, f := range old {
		oldPaths[i] = f.Path
	}
	o, n := stripRoots(oldPaths, cur)

	wanted := make(map[string]bool, len(old))
	full := make(map[string]bool)
	for i, f := range old {
		wanted[o[i]] = f.Wanted
		for _, d := range ancestors(o[i]) {
			prev, ok := full[d]
			if !ok {
				prev = true
			}
			full[d] = prev && f.Wanted
		}
	}

	var res []int
	for i, p := range n {
		if w, ok := wanted[p]; ok {
			if !w {
				res = append(res, i)
			}
			continue
		}
		// файл новый: качаем только внутри папки, где были отмечены все прежние файлы
		keep := false
		for _, d := range ancestors(p) {
			if full[d] {
				keep = true
				break
			}
		}
		if !keep {
			res = append(res, i)
		}
	}
	if len(res) == len(cur) {
		return nil
	}
	return res
}

func hasUnwanted(old []model.TorrentFile) bool {
	for _, f := range old {
		if !f.Wanted {
			return true
		}
	}
	return false
}

func ancestors(p string) []string {
	var out []string
	for i := 0; i < len(p); i++ {
		if p[i] == '/' && i > 0 {
			out = append(out, p[:i])
		}
	}
	return out
}

func stripRoots(a, b []string) ([]string, []string) {
	ra, okA := CommonRoot(a)
	rb, okB := CommonRoot(b)
	if !okA || !okB {
		return a, b
	}
	return trimAll(a, ra), trimAll(b, rb)
}

func CommonRoot(paths []string) (string, bool) {
	if len(paths) == 0 {
		return "", false
	}
	root := ""
	for i, p := range paths {
		idx := strings.IndexByte(p, '/')
		if idx <= 0 {
			return "", false
		}
		if i == 0 {
			root = p[:idx]
		} else if p[:idx] != root {
			return "", false
		}
	}
	return root, true
}

func trimAll(paths []string, root string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = strings.TrimPrefix(p, root+"/")
	}
	return out
}
