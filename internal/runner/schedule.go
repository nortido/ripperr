package runner

import "time"

func scheduleLocation(loc *time.Location) *time.Location {
	if loc != nil {
		return loc
	}
	return time.Local
}

// ScheduleLocation returns loc or time.Local when loc is nil.
func ScheduleLocation(loc *time.Location) *time.Location {
	return scheduleLocation(loc)
}

// NextRunsWithin returns scheduled run slots from the first upcoming slot while t <= now+within.
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
