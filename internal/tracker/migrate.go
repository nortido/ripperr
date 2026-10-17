package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"ripperr/internal/config"
	"ripperr/internal/helper/formcreds"
	"ripperr/internal/model/tracker"
	"ripperr/internal/settings"
	"ripperr/internal/storage/client/sqlite"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker/registry"
)

type migrationStore interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
	GetTracker(ctx context.Context, trackerID int) (*tracker.Tracker, error)
}

func MigrateSettings(ctx context.Context, db migrationStore, base config.App) error {
	raw, ok, err := db.GetSetting(ctx, sqlite.SettingTrackers)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if ok {
		if err = json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s: %w", sqlite.SettingTrackers, err)
		}
	}

	converted, err := templatizeCredentials(doc)
	if err != nil {
		return err
	}
	if len(converted) > 0 {
		out, err := marshalRaw(doc)
		if err != nil {
			return err
		}
		if err = db.SaveSetting(ctx, sqlite.SettingTrackers, out); err != nil {
			return err
		}
		logger.Info("trackers: credentials converted to a template: " + strings.Join(converted, ", "))
	}

	return bootstrapAdded(ctx, db, base, doc)
}

func templatizeCredentials(doc map[string]json.RawMessage) ([]string, error) {
	var converted []string
	for _, def := range registry.All() {
		entryRaw, ok := doc[def.Key]
		if def.Login == nil || !ok {
			continue
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(entryRaw, &entry); err != nil {
			return nil, fmt.Errorf("%s %s: %w", sqlite.SettingTrackers, def.Key, err)
		}
		body, err := stringField(entry, "credentials")
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", sqlite.SettingTrackers, def.Key, err)
		}
		if body == "" {
			continue
		}
		tmpl, login, password, hasLogin, hasPassword := formcreds.Templatize(body, def.Login.UserField, def.Login.PassField)
		if !hasLogin && !hasPassword {
			continue
		}
		if !hasLogin {
			if login, err = stringField(entry, "login"); err != nil {
				return nil, fmt.Errorf("%s %s: %w", sqlite.SettingTrackers, def.Key, err)
			}
		}
		if !hasPassword {
			if password, err = stringField(entry, "password"); err != nil {
				return nil, fmt.Errorf("%s %s: %w", sqlite.SettingTrackers, def.Key, err)
			}
		}
		// другие байты тела после подстановки трекер примет за смену логина и войдет заново,
		// а rutracker на новом входе может спросить капчу
		// не UTF-8 (например, cp1251 вручную) JSON сохранит как U+FFFD, и логин испортится
		if formcreds.Expand(tmpl, login, password) != body || !utf8.ValidString(login) || !utf8.ValidString(password) {
			logger.Warn("trackers: " + def.Key + " credentials kept as is, save the tracker once to convert")
			continue
		}
		for field, value := range map[string]string{"credentials": tmpl, "login": login, "password": password} {
			if entry[field], err = marshalRaw(value); err != nil {
				return nil, err
			}
		}
		if doc[def.Key], err = marshalRaw(entry); err != nil {
			return nil, err
		}
		converted = append(converted, def.Key)
	}
	return converted, nil
}

func bootstrapAdded(ctx context.Context, db migrationStore, base config.App, doc map[string]json.RawMessage) error {
	if _, ok, err := settings.TrackersAdded(ctx, db); err != nil || ok {
		return err
	}
	defaults := registry.ConfigDefaults()
	added := []string{}
	for _, def := range registry.All() {
		_, inOverlay := doc[def.Key]
		item, defItem := base.Tracker[def.Key], defaults[def.Key]
		if inOverlay || (item != nil && defItem != nil && !reflect.DeepEqual(*item, *defItem)) {
			added = append(added, def.Key)
			continue
		}
		row, err := db.GetTracker(ctx, def.ID)
		if err != nil {
			return fmt.Errorf("tracker %s: %w", def.Key, err)
		}
		if row != nil && row.Exists {
			added = append(added, def.Key)
		}
	}
	if err := settings.SaveTrackersAdded(ctx, db, added); err != nil {
		return err
	}
	list := strings.Join(added, ", ")
	if list == "" {
		list = "none"
	}
	logger.Info("trackers: added list created: " + list)
	return nil
}

func stringField(entry map[string]json.RawMessage, name string) (string, error) {
	var s string
	if v, ok := entry[name]; ok && string(v) != "null" {
		if err := json.Unmarshal(v, &s); err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
	}
	return s, nil
}

func marshalRaw(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// иначе & в нетронутых записях станет escape-последовательностью: значения те же, но правка лишняя
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
