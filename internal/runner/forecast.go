package runner

import (
	"math"
	"time"
)

const (
	forecastSamples    = 8
	forecastWindow     = 90 * 24 * time.Hour
	forecastMinSamples = 3
	forecastMaxArc     = 3 * time.Hour
	forecastMaxExtra   = 2
)

func forecast(samples []time.Time, loc *time.Location) (mean, late time.Duration, ok bool) {
	n := len(samples)
	if n < forecastMinSamples {
		return 0, 0, false
	}
	angles := make([]float64, n)
	var sumSin, sumCos float64
	for i, s := range samples {
		s = s.In(loc)
		sec := s.Hour()*3600 + s.Minute()*60 + s.Second()
		angles[i] = 2 * math.Pi * float64(sec) / 86400
		sumSin += math.Sin(angles[i])
		sumCos += math.Cos(angles[i])
	}
	if math.Hypot(sumSin, sumCos)/float64(n) < 1e-9 {
		return 0, 0, false
	}
	meanAngle := math.Atan2(sumSin, sumCos)
	minOff, maxOff := math.Inf(1), math.Inf(-1)
	for _, a := range angles {
		off := math.Remainder(a-meanAngle, 2*math.Pi)
		if off <= -math.Pi {
			off += 2 * math.Pi
		}
		minOff = min(minOff, off)
		maxOff = max(maxOff, off)
	}
	toMinutes := func(a float64) float64 { return a * 1440 / (2 * math.Pi) }
	if math.Round(toMinutes(maxOff-minOff)) > forecastMaxArc.Minutes() {
		return 0, 0, false
	}
	meanMin := math.Mod(math.Round(toMinutes(meanAngle))+1440, 1440)
	lateMin := max(0, math.Round(toMinutes(maxOff)))
	return time.Duration(meanMin) * time.Minute, time.Duration(lateMin) * time.Minute, true
}

func (h *handler) slotAtOrAfter(t time.Time) time.Time {
	return h.nextRun(t.Add(-time.Nanosecond))
}

func (h *handler) lastWindow(t time.Time, mean time.Duration) (occ, first time.Time) {
	loc := scheduleLocation(h.config.Location)
	t = t.In(loc)
	for day := 1; ; day-- {
		occ = time.Date(t.Year(), t.Month(), t.Day()+day, 0, int(mean/time.Minute), 0, 0, loc)
		if first = h.slotAtOrAfter(occ); !first.After(t) {
			return occ, first
		}
	}
}

func (h *handler) placeForecast(slot time.Time, days int, mean, late time.Duration, released bool) time.Time {
	if !released {
		occ, first := h.lastWindow(slot, mean)
		next := h.nextRun(slot)
		interval := time.Duration(24/h.config.ListRefreshPerDay) * time.Hour
		// не больше двух лишних проверок после первой, даже когда окно выхода шире, и только
		// до окна следующего дня: иначе единственный слот в сутки каждый день был бы лишним
		if slot.Before(h.slotAtOrAfter(occ.Add(late))) && next.Sub(first) <= forecastMaxExtra*interval &&
			next.Before(first.AddDate(0, 0, 1)) {
			return next
		}
	}
	// последнее окно не позже slot+N: иначе непроверенный релиз шел бы в ближайшее окно
	// каждый день и порог N терялся
	_, first := h.lastWindow(slot.AddDate(0, 0, days), mean)
	return first
}
