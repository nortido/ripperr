package runner

import "time"

func scheduleLocation(loc *time.Location) *time.Location {
	if loc != nil {
		return loc
	}
	return time.Local
}

func gridInterval(perDay int) time.Duration {
	// вне 1..24 сетка как при 1 раз в сутки: деление на ноль недопустимо
	if perDay < 1 || perDay > 24 {
		perDay = 1
	}
	return time.Duration(24/perDay) * time.Hour
}

func nextSlot(now time.Time, startHour, perDay int, loc *time.Location) time.Time {
	// слоты StartHour + k*interval, k любое целое; берем ближайший строго после now
	loc = scheduleLocation(loc)
	now = now.In(loc)
	interval := gridInterval(perDay)
	anchor := time.Date(now.Year(), now.Month(), now.Day(), startHour, 0, 0, 0, loc)
	k := now.Sub(anchor) / interval
	if anchor.Add(k * interval).After(now) {
		k--
	}
	return anchor.Add((k + 1) * interval)
}

func NextRunsWithin(now time.Time, startHour, listRefreshPerDay int, within time.Duration, loc *time.Location) []time.Time {
	if listRefreshPerDay <= 0 || within <= 0 {
		return nil
	}
	deadline := now.Add(within)
	runs := make([]time.Time, 0, listRefreshPerDay)
	for t := nextSlot(now, startHour, listRefreshPerDay, loc); !t.After(deadline); t = nextSlot(t, startHour, listRefreshPerDay, loc) {
		runs = append(runs, t)
	}
	return runs
}

func slotFloor(t time.Time, startHour, perDay int, loc *time.Location) time.Time {
	if perDay <= 0 {
		return t
	}
	loc = scheduleLocation(loc)
	t = t.In(loc)
	interval := gridInterval(perDay)
	anchor := time.Date(t.Year(), t.Month(), t.Day(), startHour, 0, 0, 0, loc)
	k := t.Sub(anchor) / interval
	if anchor.Add(k * interval).After(t) {
		k--
	}
	return anchor.Add(k * interval)
}
