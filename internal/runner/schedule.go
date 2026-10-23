package runner

import "time"

func scheduleLocation(loc *time.Location) *time.Location {
	if loc != nil {
		return loc
	}
	return time.Local
}

func NextRunsWithin(now time.Time, startHour, listRefreshPerDay int, within time.Duration, loc *time.Location) []time.Time {
	if listRefreshPerDay <= 0 || within <= 0 {
		return nil
	}
	loc = scheduleLocation(loc)
	now = now.In(loc)
	interval := time.Duration(24/listRefreshPerDay) * time.Hour
	deadline := now.Add(within)
	anchor := time.Date(now.Year(), now.Month(), now.Day(), startHour, 0, 0, 0, loc)
	t := anchor
	if !t.After(now) {
		k := int64(now.Sub(anchor))/int64(interval) + 1
		t = anchor.Add(time.Duration(k) * interval)
	}
	runs := make([]time.Time, 0, listRefreshPerDay)
	for !t.After(deadline) {
		runs = append(runs, t)
		t = t.Add(interval)
	}
	return runs
}

func slotFloor(t time.Time, startHour, perDay int, loc *time.Location) time.Time {
	if perDay <= 0 {
		return t
	}
	loc = scheduleLocation(loc)
	t = t.In(loc)
	interval := time.Duration(24/perDay) * time.Hour
	anchor := time.Date(t.Year(), t.Month(), t.Day(), startHour, 0, 0, 0, loc)
	k := t.Sub(anchor) / interval
	if anchor.Add(k * interval).After(t) {
		k--
	}
	return anchor.Add(k * interval)
}
