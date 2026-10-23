package runner

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ripperr/internal/model"
)

var forecastMSK = time.FixedZone("MSK", 3*60*60)

func mskSamples(hm ...string) []time.Time {
	return samplesIn(forecastMSK, hm...)
}

func samplesIn(loc *time.Location, hm ...string) []time.Time {
	out := make([]time.Time, 0, len(hm))
	for i, s := range hm {
		var h, m int
		if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
			panic(err)
		}
		out = append(out, time.Date(2026, 3, 1+i, h, m, 0, 0, loc).UTC())
	}
	return out
}

func TestForecast(t *testing.T) {
	cases := []struct {
		name     string
		samples  []time.Time
		ok       bool
		mean     time.Duration
		meanTol  time.Duration
		late     time.Duration
		checkLat bool
	}{
		{name: "none", samples: nil},
		{name: "two samples", samples: mskSamples("20:00", "20:05")},
		{name: "evening", samples: mskSamples("19:40", "20:10", "19:55", "20:30", "20:05"), ok: true,
			mean: 20*time.Hour + 4*time.Minute, meanTol: time.Minute, late: 26 * time.Minute, checkLat: true},
		{name: "across midnight", samples: mskSamples("23:30", "00:15", "23:50"), ok: true,
			mean: 23*time.Hour + 52*time.Minute, late: 23 * time.Minute, checkLat: true},
		{name: "arc 4 h", samples: mskSamples("18:00", "20:00", "22:00")},
		{name: "arc exactly 3 h", samples: mskSamples("18:00", "19:30", "21:00"), ok: true,
			mean: 19*time.Hour + 30*time.Minute, late: 90 * time.Minute, checkLat: true},
		{name: "arc 3 h 1 min", samples: mskSamples("18:00", "19:30", "21:01")},
		{name: "symmetric hour", samples: mskSamples("19:00", "19:30", "20:00", "20:30", "21:00"), ok: true,
			mean: 20 * time.Hour, late: time.Hour, checkLat: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mean, late, ok := forecast(c.samples, forecastMSK)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (mean %v, late %v)", ok, c.ok, mean, late)
			}
			if !ok {
				return
			}
			if d := mean - c.mean; d < -c.meanTol || d > c.meanTol {
				t.Errorf("mean = %v, want %v +-%v", mean, c.mean, c.meanTol)
			}
			if mean%time.Minute != 0 || late%time.Minute != 0 {
				t.Errorf("mean %v, late %v not whole minutes", mean, late)
			}
			if c.checkLat && late != c.late {
				t.Errorf("late = %v, want %v", late, c.late)
			}
		})
	}
}

func forecastHandler(perDay, startHour int) *handler {
	return &handler{config: model.HandlerConfig{
		ListRefreshPerDay: perDay, StartHour: startHour, Location: forecastMSK,
		ThresholdMonth: 1, ThresholdHalfYear: 7, ThresholdYear: 30, ThresholdOld: 30,
	}}
}

func msk(day, h, m int) time.Time {
	return time.Date(2026, 3, day, h, m, 0, 0, forecastMSK)
}

func TestGetRecheckAtForecast(t *testing.T) {
	evening := mskSamples("19:40", "20:10", "19:55", "20:30", "20:05")
	midnight := mskSamples("23:30", "00:15", "23:50")
	cases := []struct {
		name     string
		perDay   int
		samples  []time.Time
		slot     time.Time
		released bool
		want     time.Time
	}{
		{"no samples", 24, nil, msk(15, 21, 0), false, msk(16, 21, 0)},
		{"two samples", 24, evening[:2], msk(15, 8, 0), false, msk(16, 8, 0)},
		{"released at 21:00", 24, evening, msk(15, 21, 0), true, msk(16, 21, 0)},
		{"transition 08:00", 24, evening, msk(15, 8, 0), false, msk(15, 21, 0)},
		{"23:52 from 22:00", 12, midnight, msk(15, 22, 0), false, msk(16, 0, 0)},
		{"23:52 released at 00:00", 12, midnight, msk(16, 0, 0), true, msk(17, 0, 0)},
		{"23:52 extra after 00:00", 12, midnight, msk(16, 0, 0), false, msk(16, 2, 0)},
		{"23:52 after extra", 12, midnight, msk(16, 2, 0), false, msk(17, 0, 0)},
		{"perDay 0", 0, evening, msk(15, 8, 0), false, msk(16, 8, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := forecastHandler(c.perDay, 0)
			now := c.slot.Add(6 * time.Minute)
			tm := now.Add(-2 * time.Hour)
			got := h.getRecheckAt(&tm, c.slot, now, c.released, c.samples)
			if got == nil || !got.Equal(c.want) {
				t.Fatalf("getRecheckAt(%v) = %v, want %v", c.slot, got, c.want)
			}
		})
	}
}

func TestPlaceForecast(t *testing.T) {
	hm := func(h, m int) time.Duration { return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute }
	cases := []struct {
		name       string
		perDay     int
		startHour  int
		mean, late time.Duration
		slot       time.Time
		released   bool
		want       time.Time
	}{
		{"19:10 slot 20:00", 24, 0, hm(19, 10), hm(2, 50), msk(15, 20, 0), false, msk(15, 21, 0)},
		{"19:10 slot 21:00", 24, 0, hm(19, 10), hm(2, 50), msk(15, 21, 0), false, msk(15, 22, 0)},
		{"19:10 slot 22:00", 24, 0, hm(19, 10), hm(2, 50), msk(15, 22, 0), false, msk(16, 20, 0)},
		{"19:10 released at 21:00", 24, 0, hm(19, 10), hm(2, 50), msk(15, 21, 0), true, msk(16, 20, 0)},
		{"cap slot 19:00", 24, 0, hm(19, 0), hm(3, 0), msk(15, 19, 0), false, msk(15, 20, 0)},
		{"cap slot 20:00", 24, 0, hm(19, 0), hm(3, 0), msk(15, 20, 0), false, msk(15, 21, 0)},
		{"cap slot 21:00", 24, 0, hm(19, 0), hm(3, 0), msk(15, 21, 0), false, msk(16, 19, 0)},
		{"night hole 22:00", 8, 7, hm(4, 0), 0, msk(15, 22, 0), false, msk(16, 7, 0)},
		{"night hole released at 07:00", 8, 7, hm(4, 0), 0, msk(15, 7, 0), true, msk(16, 7, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := forecastHandler(c.perDay, c.startHour)
			if got := h.placeForecast(c.slot, 1, c.mean, c.late, c.released); !got.Equal(c.want) {
				t.Fatalf("placeForecast(%v) = %v, want %v", c.slot, got, c.want)
			}
		})
	}
}

func TestPlaceForecastInvariants(t *testing.T) {
	hm := func(h, m int) time.Duration { return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute }
	forecasts := []struct{ mean, late time.Duration }{
		{0, 0}, {hm(4, 0), 0}, {hm(4, 0), hm(2, 0)}, {hm(6, 59), hm(3, 0)}, {hm(12, 30), hm(0, 45)},
		{hm(19, 10), hm(2, 50)}, {hm(20, 4), hm(0, 26)}, {hm(23, 52), hm(0, 23)},
	}
	for _, perDay := range []int{1, 2, 3, 4, 6, 8, 12, 24} {
		for _, startHour := range []int{0, 7} {
			h := forecastHandler(perDay, startHour)
			for _, f := range forecasts {
				for hour := range 24 {
					slot := slotFloor(msk(15, hour, 0), startHour, perDay, forecastMSK)
					for _, days := range []int{1, 7} {
						for _, released := range []bool{false, true} {
							got := h.placeForecast(slot, days, f.mean, f.late, released)
							bound := slot.AddDate(0, 0, days)
							if !got.After(slot) || got.After(bound) || !slotFloor(got, startHour, perDay, forecastMSK).Equal(got) {
								t.Fatalf("perDay %d start %d mean %v late %v slot %v days %d released %v: got %v, bound %v",
									perDay, startHour, f.mean, f.late, slot, days, released, got, bound)
							}
						}
					}
				}
			}
		}
	}
}

func forecastWorkerHandler(pickDate time.Time) (*handler, *loggingStorage) {
	h, store := newReleaseHandler(pickDate)
	h.config.ListRefreshPerDay = 24
	h.config.Location = forecastMSK
	store.recent = mskSamples("19:00", "19:30", "20:00", "20:30", "21:00")
	return h, store
}

func TestWorkerExtraCheckAfterForecastSlot(t *testing.T) {
	d := time.Now().Add(-2 * time.Hour)
	h, store := forecastWorkerHandler(d)
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: "1", Timestamp: &d, Update: true,
		PassSlot: msk(15, 20, 0)}
	before := time.Now()
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "unchanged" {
		t.Fatalf("logs = %+v, want one unchanged", store.logs)
	}
	if len(store.recentCalls) != 1 {
		t.Fatalf("read calls = %+v, want one", store.recentCalls)
	}
	c := store.recentCalls[0]
	if since := before.Add(-90 * 24 * time.Hour); c.hash != "abc" || c.limit != 8 || c.since.Before(since.Add(-time.Minute)) || c.since.After(since.Add(time.Minute)) {
		t.Fatalf("read call = %+v, want abc since %v limit 8", c, since)
	}
	if len(store.saved) != 1 || store.saved[0].RecheckAt == nil || !store.saved[0].RecheckAt.Equal(msk(15, 21, 0)) {
		t.Fatalf("saved = %+v, want RecheckAt %v", store.saved, msk(15, 21, 0))
	}
}

func TestWorkerUpdatedMovesToNextForecastDay(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	h, store := forecastWorkerHandler(time.Now().Add(-2 * time.Hour))
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: "1", Timestamp: &old, Update: true,
		PassSlot: msk(15, 20, 0)}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "updated" {
		t.Fatalf("logs = %+v, want one updated", store.logs)
	}
	if len(store.saved) != 1 || store.saved[0].RecheckAt == nil || !store.saved[0].RecheckAt.Equal(msk(16, 20, 0)) {
		t.Fatalf("saved = %+v, want RecheckAt %v", store.saved, msk(16, 20, 0))
	}
}

func TestWorkerForecastReadErrorFallsBack(t *testing.T) {
	d := time.Now().Add(-2 * time.Hour)
	h, store := forecastWorkerHandler(d)
	store.recentErr = errors.New("boom")
	const msg = "release times of test not read: boom"
	before := countLogged(msg)
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: "1", Timestamp: &d, Update: true,
		PassSlot: msk(15, 20, 0)}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if got := countLogged(msg) - before; got != 1 {
		t.Fatalf("WARN %q logged %d times, want 1", msg, got)
	}
	if len(store.saved) != 1 || store.saved[0].RecheckAt == nil || !store.saved[0].RecheckAt.Equal(msk(16, 20, 0)) {
		t.Fatalf("saved = %+v, want RecheckAt %v", store.saved, msk(16, 20, 0))
	}
}

func TestForecastChainKeepsThreshold(t *testing.T) {
	cases := []struct {
		perDay, startHour int
		loc               *time.Location
		samples           []string
	}{
		{2, 7, forecastMSK, []string{"18:40", "18:50", "19:20"}},
		{1, 0, forecastMSK, []string{"10:40", "11:00", "11:20"}},
		{24, 13, forecastMSK, []string{"00:20", "00:30", "00:40"}},
		{12, 0, forecastMSK, []string{"19:40", "20:10", "19:55", "20:30", "20:05"}},
		{1, 8, forecastMSK, []string{"07:20", "07:30", "07:40", "08:30"}},
		{1, 8, time.UTC, []string{"06:17", "07:30", "08:43"}},
	}
	for _, c := range cases {
		for _, days := range []int{1, 7, 30} {
			h := forecastHandler(c.perDay, c.startHour)
			h.config.Location = c.loc
			h.config.ThresholdMonth = days
			samples := samplesIn(c.loc, c.samples...)
			pass := time.Date(2026, 3, 1, c.startHour, 0, 0, 0, c.loc)
			recheck, end := pass, pass.AddDate(0, 0, 28)
			checks := 0
			for pass.Before(end) {
				if !pass.Before(recheck) {
					checks++
					now := pass.Add(5 * time.Minute)
					tm := now.Add(-2 * time.Hour)
					recheck = *h.getRecheckAt(&tm, pass, now, false, samples)
				}
				pass = h.nextRun(pass)
			}
			hi := (28+days-1)/days*(1+forecastMaxExtra) + 1
			if checks < 28/days || checks > hi {
				t.Errorf("perDay %d start %d loc %v N %d samples %v: %d checks in 28 days, want %d..%d",
					c.perDay, c.startHour, c.loc, days, c.samples, checks, 28/days, hi)
			}
		}
	}
}
