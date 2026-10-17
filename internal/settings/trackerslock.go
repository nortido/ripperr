package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"ripperr/internal/config"
	"ripperr/internal/storage/client/sqlite"
)

var trackersMu sync.Mutex

func LockTrackers() (unlock func()) {
	trackersMu.Lock()
	return trackersMu.Unlock
}

func loadTrackerEntry(ctx context.Context, db settingsDB, key string) (doc, entry map[string]json.RawMessage, err error) {
	// записи читаются как RawMessage: чужие трекеры и секреты переписываются байт в байт
	raw, ok, err := db.GetSetting(ctx, sqlite.SettingTrackers)
	if err != nil {
		return nil, nil, err
	}
	if ok {
		if err = json.Unmarshal(raw, &doc); err != nil {
			return nil, nil, err
		}
	}
	if doc == nil {
		doc = make(map[string]json.RawMessage)
	}
	if r, found := doc[key]; found {
		if err = json.Unmarshal(r, &entry); err != nil {
			return nil, nil, err
		}
	}
	if entry == nil {
		entry = make(map[string]json.RawMessage)
	}
	return doc, entry, nil
}

func overlayField[T any](entry map[string]json.RawMessage, field string, dst *T) error {
	raw, ok := entry[field]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*dst = v
	return nil
}

func encodeRaw(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// без экранирования HTML: иначе & и < в паролях сменят байты
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func saveTrackerEntry(ctx context.Context, db settingsStore, doc, entry map[string]json.RawMessage, key string, set map[string]any) error {
	for field, v := range set {
		b, err := encodeRaw(v)
		if err != nil {
			return err
		}
		entry[field] = b
	}
	b, err := encodeRaw(entry)
	if err != nil {
		return err
	}
	doc[key] = b
	out, err := encodeRaw(doc)
	if err != nil {
		return err
	}
	return db.SaveSetting(ctx, sqlite.SettingTrackers, out)
}

func sameHost(host string) func(string) bool {
	want := MirrorHostKey(host)
	return func(h string) bool { return MirrorHostKey(h) == want }
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func MaintainTrackerMirrors(ctx context.Context, db settingsStore, key string, base config.TrackerItem, add, retire, prune []string) (added, retired, pruned []string, err error) {
	keys, ok, err := TrackersAdded(ctx, db)
	if err != nil {
		return nil, nil, nil, err
	}
	if ok && !slices.Contains(keys, key) {
		return nil, nil, nil, nil
	}
	doc, entry, err := loadTrackerEntry(ctx, db, key)
	if err != nil {
		return nil, nil, nil, err
	}
	domain, active := base.Domain, base.ActiveDomain
	mirrors, unconfirmed, retiredList := slices.Clone(base.Mirrors), slices.Clone(base.Unconfirmed), slices.Clone(base.Retired)
	if err = errors.Join(
		overlayField(entry, "domain", &domain),
		overlayField(entry, "active_domain", &active),
		overlayField(entry, "mirrors", &mirrors),
		overlayField(entry, "unconfirmed", &unconfirmed),
		overlayField(entry, "retired", &retiredList),
	); err != nil {
		return nil, nil, nil, err
	}
	origMirrors, origUnconfirmed, origRetired, origActive := slices.Clone(mirrors), slices.Clone(unconfirmed), slices.Clone(retiredList), active
	isDomain := sameHost(domain)

	for _, h := range add {
		h = strings.ToLower(strings.TrimSpace(h))
		if !ValidMirrorHost(h) || isDomain(h) || slices.ContainsFunc(mirrors, sameHost(h)) || slices.ContainsFunc(retiredList, sameHost(h)) {
			continue
		}
		mirrors = append(mirrors, h)
		if !slices.ContainsFunc(unconfirmed, sameHost(h)) {
			unconfirmed = append(unconfirmed, h)
		}
		added = append(added, h)
	}
	for _, h := range retire {
		i := slices.IndexFunc(mirrors, sameHost(h))
		if MirrorHostKey(h) == "" || isDomain(h) || i < 0 {
			continue
		}
		host := mirrors[i]
		mirrors = slices.DeleteFunc(mirrors, sameHost(host))
		unconfirmed = slices.DeleteFunc(unconfirmed, sameHost(host))
		if !slices.ContainsFunc(retiredList, sameHost(host)) {
			retiredList = append(retiredList, host)
		}
		if sameHost(host)(active) {
			active = ""
		}
		retired = append(retired, host)
	}
	for _, h := range prune {
		i := slices.IndexFunc(retiredList, sameHost(h))
		if MirrorHostKey(h) == "" || isDomain(h) || slices.ContainsFunc(mirrors, sameHost(h)) || i < 0 {
			continue
		}
		pruned = append(pruned, retiredList[i])
		retiredList = slices.DeleteFunc(retiredList, sameHost(h))
	}

	set := make(map[string]any)
	if !slices.Equal(mirrors, origMirrors) {
		set["mirrors"] = nonNil(mirrors)
	}
	if !slices.Equal(unconfirmed, origUnconfirmed) {
		set["unconfirmed"] = nonNil(unconfirmed)
	}
	if !slices.Equal(retiredList, origRetired) {
		set["retired"] = nonNil(retiredList)
	}
	if active != origActive {
		set["active_domain"] = active
	}
	if len(set) == 0 {
		return added, retired, pruned, nil
	}
	if err = saveTrackerEntry(ctx, db, doc, entry, key, set); err != nil {
		return nil, nil, nil, err
	}
	return added, retired, pruned, nil
}

func ConfirmTrackerMirror(ctx context.Context, db settingsStore, key, host string) (bool, error) {
	doc, entry, err := loadTrackerEntry(ctx, db, key)
	if err != nil {
		return false, err
	}
	var unconfirmed []string
	if err = overlayField(entry, "unconfirmed", &unconfirmed); err != nil {
		return false, err
	}
	if MirrorHostKey(host) == "" || !slices.ContainsFunc(unconfirmed, sameHost(host)) {
		return false, nil
	}
	unconfirmed = slices.DeleteFunc(unconfirmed, sameHost(host))
	if err = saveTrackerEntry(ctx, db, doc, entry, key, map[string]any{"unconfirmed": nonNil(unconfirmed)}); err != nil {
		return false, err
	}
	return true, nil
}
