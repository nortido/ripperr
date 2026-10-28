package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"ripperr/internal/system/logger"
)

const telegramMutesKey = "telegram_mutes"

type SettingsStore interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}

type TelegramMutes struct {
	db SettingsStore
}

type mutesDoc struct {
	URLHashes []string `json:"url_hashes"`
}

// чтение-изменение-запись одной строки настроек: без общего замка два нажатия подряд теряют друг друга
var mutesMu sync.Mutex

func NewTelegramMutes(db SettingsStore) *TelegramMutes {
	return &TelegramMutes{db: db}
}

func (m *TelegramMutes) read(ctx context.Context) (mutesDoc, error) {
	var doc mutesDoc
	raw, ok, err := m.db.GetSetting(ctx, telegramMutesKey)
	if err != nil {
		return doc, fmt.Errorf("telegram mutes: read settings: %w", err)
	}
	if !ok {
		return doc, nil
	}
	if json.Unmarshal(raw, &doc) != nil {
		logger.Warn("notify: telegram mutes row is not valid JSON, treated as empty")
		return mutesDoc{}, nil
	}
	return doc, nil
}

func (m *TelegramMutes) Muted(ctx context.Context, urlHash string) (bool, error) {
	mutesMu.Lock()
	defer mutesMu.Unlock()
	doc, err := m.read(ctx)
	if err != nil {
		return false, err
	}
	return slices.Contains(doc.URLHashes, urlHash), nil
}

func (m *TelegramMutes) Set(ctx context.Context, urlHash string, muted bool) error {
	mutesMu.Lock()
	defer mutesMu.Unlock()
	doc, err := m.read(ctx)
	if err != nil {
		return err
	}
	hashes := slices.DeleteFunc(slices.Clone(doc.URLHashes), func(h string) bool { return h == urlHash })
	if muted {
		hashes = append(hashes, urlHash)
	}
	slices.Sort(hashes)
	hashes = slices.Compact(hashes)
	if hashes == nil {
		hashes = []string{}
	}
	raw, err := json.Marshal(mutesDoc{URLHashes: hashes})
	if err != nil {
		return err
	}
	if err := m.db.SaveSetting(ctx, telegramMutesKey, raw); err != nil {
		return fmt.Errorf("telegram mutes: save settings: %w", err)
	}
	return nil
}

func (n *Notifier) muteApplies(ev Event) bool {
	return ev.Kind == KindUpdated && ev.urlHash != "" && n.mutes != nil
}

func (n *Notifier) telegramMuted(ctx context.Context, ev Event) bool {
	if !n.muteApplies(ev) {
		return false
	}
	muted, err := n.mutes.Muted(ctx, ev.urlHash)
	if err != nil {
		// потерянное уведомление хуже лишнего: при сбое чтения шлем как обычно
		logger.Warn("notify: telegram mutes: " + err.Error())
		return false
	}
	return muted
}
