package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"ripperr/internal/model"
)

func (m *manager) Add(ctx context.Context, data []byte, opts model.ManagerAddFileOptions) error {
	return m.client.AddFromBuffer(ctx, bytes.NewBuffer(data), opts)
}

func (m *manager) Remove(ctx context.Context, targets []model.ManagerDeleteOptions) (accepted []string, err error) {
	for _, t := range targets {
		switch {
		case t.Hash == "":
			return nil, errors.New("remove: empty torrent hash")
		case m.client.ReplacePending(t.Hash):
			return nil, fmt.Errorf("remove %s: %w", t.Hash, model.ErrReplacePending)
		}
	}
	accepted = []string{}
	var unconfirmed []string
	for _, t := range targets {
		// пара уходит до удаления: иначе подметальщик успеет вернуть торрент без пары
		m.client.RemoveBlackholePair(t.Hash)
		var delErr error
		if t.DeleteFiles {
			// ручное удаление идет мимо настроек: они только для замены, общую папку проверил runner
			delErr = m.client.Purge(ctx, t.Hash)
		} else {
			delErr = m.client.Discard(ctx, t.Hash)
		}
		if delErr != nil {
			// ошибка может прийти уже после удаления (таймаут ответа): решает проба
			if present, probeErr := m.client.Exists(ctx, t.Hash); probeErr != nil || present {
				return accepted, fmt.Errorf("remove %s: %w", t.Hash, delErr)
			}
		}
		accepted = append(accepted, t.Hash)
		if absent, waitErr := m.client.WaitAbsent(ctx, t.Hash); waitErr != nil || !absent {
			unconfirmed = append(unconfirmed, t.Hash)
		}
	}
	if len(unconfirmed) > 0 {
		return accepted, fmt.Errorf("%w: %s", model.ErrRemoveUnconfirmed, strings.Join(unconfirmed, ", "))
	}
	return accepted, nil
}
