package web

import (
	"context"
	"encoding/json"
	"strings"

	"ripperr/internal/model/storage"
	"ripperr/internal/storage/client/sqlite"
	"ripperr/internal/system/logger"
)

func (s *Server) categoryColors(ctx context.Context, items []storage.CheckLogEntry) map[string]int {
	s.categoryColorMu.Lock()
	defer s.categoryColorMu.Unlock()

	colors := map[string]int{}
	raw, ok, err := s.db.GetSetting(ctx, sqlite.SettingCategoryColors)
	if err != nil {
		logger.Warn("read category colors: " + err.Error())
		// при ошибке чтения не пишем: иначе затрем документ, который просто не прочитался
		return nil
	}
	if ok {
		if err := json.Unmarshal(raw, &colors); err != nil {
			logger.Warn("category colors: " + err.Error())
			colors = map[string]int{}
		}
		// null обнуляет карту, а запись в nil-карту паникует
		if colors == nil {
			colors = map[string]int{}
		}
	}

	used := make(map[int]struct{}, len(colors))
	for _, idx := range colors {
		used[idx] = struct{}{}
	}
	changed := false
	for _, e := range items {
		name := strings.TrimSpace(e.Category)
		if name == "" {
			continue
		}
		if _, has := colors[name]; has {
			continue
		}
		idx := 0
		for {
			if _, taken := used[idx]; !taken {
				break
			}
			idx++
		}
		colors[name] = idx
		used[idx] = struct{}{}
		changed = true
	}
	if !changed {
		return colors
	}
	doc, err := json.Marshal(colors)
	if err == nil {
		err = s.db.SaveSetting(ctx, sqlite.SettingCategoryColors, doc)
	}
	if err != nil {
		logger.Warn("save category colors: " + err.Error())
	}
	return colors
}
