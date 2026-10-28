package config

const (
	WatchModeCategories = "categories"
	WatchModeTags       = "tags"
)

func ClientSupportsTags(kind string) bool {
	return NormalizeClient(kind) != "deluge"
}

func (r Runner) EffectiveWatchMode() string {
	switch r.WatchMode {
	case WatchModeCategories, WatchModeTags:
		return r.WatchMode
	}
	// режим не сохранен: один заполненный список тегов без категорий значит режим тегов
	if len(r.AllowedCategories) == 0 && len(r.WatchTags) > 0 {
		return WatchModeTags
	}
	return WatchModeCategories
}

func (r Runner) WatchSelection(tagsSupported bool) (categories, tags []string) {
	if r.EffectiveWatchMode() == WatchModeTags && tagsSupported {
		return nil, r.WatchTags
	}
	return r.AllowedCategories, nil
}
