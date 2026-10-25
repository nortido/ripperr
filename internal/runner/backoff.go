package runner

import (
	"time"

	"ripperr/internal/model"
	storageModel "ripperr/internal/model/storage"
)

const backoffCap = 24 * time.Hour

func topicBackoff(streak int, slot time.Duration) time.Duration {
	// первая ошибка повторяется в следующем слоте: сбой чаще разовый, задержка только съест проверку
	if streak < 2 || slot <= 0 {
		return 0
	}
	k := int64(1)<<min(streak-1, 62) - 1
	if k > int64(backoffCap)/int64(slot) {
		return backoffCap
	}
	return min(time.Duration(k)*slot, backoffCap)
}

func inBackoff(now time.Time, m storageModel.ErrorMark, slot time.Duration) bool {
	d := topicBackoff(m.Streak, slot)
	return d > 0 && !m.CheckedAt.After(now) && now.Before(m.CheckedAt.Add(d))
}

func (h *handler) slotInterval() time.Duration {
	if h.config.ListRefreshPerDay <= 0 {
		return 0
	}
	return time.Duration(24/h.config.ListRefreshPerDay) * time.Hour
}

func (h *handler) backedOff(p *pushPass, e *model.Entry) bool {
	if it := p.items[e.URLHash]; it != nil && it.ForceCheck {
		return false
	}
	// как отметки ошибок: пробный и форсированный проходы паузу не применяют
	// у релиза только с 5xx нет отметки ошибки, поэтому проверка раньше возврата без отметки
	if !p.dry && p.forced == nil {
		if sm, ok := h.state.ServerErrorMark(e.URLHash); ok && inBackoff(time.Now(), sm, h.slotInterval()) {
			return true
		}
	}
	m, ok := p.marks[e.URLHash]
	if !ok {
		return false
	}
	return inBackoff(time.Now(), m, h.slotInterval())
}
