package helper

import (
	"strings"

	"ripperr/internal/model"
)

func DedupeEntriesByName(entries []*model.Entry) []*model.Entry {
	if len(entries) == 0 {
		return entries
	}
	best := make(map[string]*model.Entry, len(entries))
	for _, e := range entries {
		key := strings.TrimSpace(e.Name)
		if cur, ok := best[key]; !ok || entryWins(e, cur) {
			best[key] = e
		}
	}
	out := make([]*model.Entry, 0, len(best))
	seen := make(map[string]struct{}, len(best))
	for _, e := range entries {
		key := strings.TrimSpace(e.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		if best[key] == e {
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
