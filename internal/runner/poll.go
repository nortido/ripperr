package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/model"
	storageModel "ripperr/internal/model/storage"
	storagePkg "ripperr/internal/storage"
	"ripperr/internal/system/logger"
)

// магнит получает комментарий позже, поэтому торрент без адреса не отбрасываем насовсем
var pollEmptyTTL = 10 * time.Minute

func (h *handler) pollNew(ctx context.Context) (added int, err error) {
	h.pollMu.Lock()
	defer h.pollMu.Unlock()

	knownHashes, knownNames, err := h.storage.KnownReleases(ctx)
	if err != nil {
		return 0, fmt.Errorf("known releases: %w", err)
	}
	// строки читаем до списка: строку, записанную после него, этот список судить не должен
	listedNames, listedFresh, err := h.storage.ListedReleases(ctx)
	if err != nil {
		return 0, fmt.Errorf("listed releases: %w", err)
	}
	ignore, err := h.storage.GetIgnoreSet(ctx)
	if err != nil {
		return 0, fmt.Errorf("ignore set: %w", err)
	}
	if h.pollSkip == nil {
		h.pollSkip = make(map[string]struct{})
	}
	if h.pollEmpty == nil {
		h.pollEmpty = make(map[string]time.Time)
	}
	if h.pollURL == nil {
		h.pollURL = make(map[string]string)
	}
	now := time.Now()
	present := make(map[string]struct{})
	presentNames := make(map[string]struct{})
	catByName := make(map[string]string)
	catByKey := make(map[string]string)
	// дедупликация до фильтров, как в проходе: иначе опрос взял бы торрент, который проход отбросит
	narrow := func(list []*model.Entry) []*model.Entry {
		for _, e := range list {
			present[e.Hash] = struct{}{}
			presentNames[storageModel.FoldName(e.Name)] = struct{}{}
			if e.URL != "" {
				h.pollURL[e.Hash] = storagePkg.URLHash(e.URL)
			}
		}
		var kept []*model.Entry
		for _, e := range helper.DedupeEntriesByName(list) {
			if name := strings.TrimSpace(e.Name); name != "" {
				catByName[name] = e.Category
			}
			if key, ok := h.pollURL[e.Hash]; ok {
				catByKey[key] = e.Category
			}
			if !h.watched(e) {
				continue
			}
			if _, ok := knownNames[storageModel.FoldName(e.Name)]; ok {
				continue
			}
			if _, ok := h.pollSkip[e.Hash]; ok {
				continue
			}
			if seen, ok := h.pollEmpty[e.Hash]; ok && now.Sub(seen) < pollEmptyTTL {
				continue
			}
			kept = append(kept, e)
		}
		return kept
	}
	list, err := h.manager.GetEntries(ctx, model.FilterOptions{Narrow: narrow})
	if errors.Is(err, model.ErrPartialList) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get entries error: %w", err)
	}
	// Transmission и Deluge Narrow не читают, поэтому фильтруем результат еще раз
	list = narrow(list)
	for hash := range h.pollSkip {
		if _, ok := present[hash]; !ok {
			delete(h.pollSkip, hash)
		}
	}
	for hash := range h.pollEmpty {
		if _, ok := present[hash]; !ok {
			delete(h.pollEmpty, hash)
		}
	}
	for hash := range h.pollURL {
		if _, ok := present[hash]; !ok {
			delete(h.pollURL, hash)
		}
	}

	for _, e := range list {
		if e.URL == "" {
			h.pollEmpty[e.Hash] = now
			continue
		}
		delete(h.pollEmpty, e.Hash)
		ttid := h.trackerService.GetID(e.URL)
		key := storagePkg.URLHash(e.URL)
		_, ignored := ignore[key]
		_, known := knownHashes[key]
		if ttid == 0 || ignored || known {
			h.pollSkip[e.Hash] = struct{}{}
			continue
		}
		at := h.slotAtOrAfter(now)
		ok, addErr := h.storage.AddNewRelease(ctx, &storageModel.CheckLogEntry{
			URLHash:       key,
			ItemName:      e.Name,
			ItemURL:       e.URL,
			TrackerTypeID: ttid,
			Category:      e.Category,
		}, at)
		if addErr != nil {
			return added, fmt.Errorf("add new release %s: %w", e.Name, addErr)
		}
		if !ok {
			h.pollSkip[e.Hash] = struct{}{}
			continue
		}
		knownHashes[key] = struct{}{}
		added++
		logger.Info(fmt.Sprintf("new release listed: %s, first check %s", e.Name, at.Format(time.RFC3339)))
	}

	// пустой список скорее сбой клиента, чем удаление всех торрентов: метки не трогаем
	if len(present) == 0 {
		return added, nil
	}

	// ключа нет ни в одной карте: категорию не знаем, строку не трогаем
	batch := make(map[string]string)
	for key, cur := range listedFresh {
		cat, ok := catByKey[key]
		if !ok {
			cat, ok = catByName[strings.TrimSpace(listedNames[key])]
		}
		if ok && cat != cur {
			batch[key] = cat
		}
	}
	updated := 0
	if len(batch) > 0 {
		var updErr error
		if updated, updErr = h.storage.UpdateNewCategories(ctx, batch); updErr != nil {
			logger.Error("update new release categories: " + updErr.Error())
			updated = 0
		}
	}
	presentURL := make(map[string]struct{})
	for hash := range present {
		if key, ok := h.pollURL[hash]; ok {
			presentURL[key] = struct{}{}
		}
	}
	if byURL := h.siblings.Load(); byURL != nil {
		for key, hashes := range *byURL {
			for _, hash := range hashes {
				if _, ok := present[hash]; ok {
					presentURL[key] = struct{}{}
					break
				}
			}
		}
	}
	absent := make(map[string]struct{})
	removed := 0
	for key, name := range listedNames {
		if _, ok := presentURL[key]; ok {
			continue
		}
		if folded := storageModel.FoldName(name); folded != "" {
			if _, ok := presentNames[folded]; ok {
				continue
			}
		}
		if _, ok := listedFresh[key]; ok {
			delErr := h.storage.DeleteNewRelease(ctx, key)
			if delErr == nil {
				removed++
				logger.Info("new release removed, torrent not in client: " + name)
				continue
			}
			logger.Error(fmt.Sprintf("remove new release %s: %v", name, delErr))
		}
		absent[key] = struct{}{}
	}
	return added + removed + updated + h.state.SetAbsent(h.gen, absent), nil
}
