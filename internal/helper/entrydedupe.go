package helper

import (
	"strings"

	"trackerr/internal/model"
)

func NormalizeReleaseName(name string) string {
	return strings.TrimSpace(name)
}

func DedupeEntriesByName(entries []*model.Entry) []*model.Entry {
	if len(entries) == 0 {
		return entries
	}
	type pick struct {
		entry *model.Entry
	}
	best := make(map[string]pick, len(entries))
	for _, e := range entries {
		key := NormalizeReleaseName(e.Name)
		cur, ok := best[key]
		if !ok || entryWins(e, cur.entry) {
			best[key] = pick{entry: e}
		}
	}
	out := make([]*model.Entry, 0, len(best))
	seen := make(map[string]struct{}, len(best))
	for _, e := range entries {
		key := NormalizeReleaseName(e.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		if best[key].entry == e {
			out = append(out, e)
			seen[key] = struct{}{}
		}
	}
	return out
}

func entryWins(a, b *model.Entry) bool {
	ta, tb := a.Timestamp, b.Timestamp
	if ta == nil {
		return false
	}
	if tb == nil {
		return true
	}
	return ta.Before(*tb)
}
