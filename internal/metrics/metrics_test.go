package metrics

import (
	"bytes"
	"sync"
	"testing"
)

func TestWriteText(t *testing.T) {
	c := NewCounterVec("ripperr_checks_total", "Checks by result", "tracker", "result")
	c.Inc("rutracker", "updated")
	c.Inc("kinozal", "error")
	c.Inc("rutracker", "updated")
	c.Inc("a\\b\"c\nd", "unknown")

	gauge := Family{
		Name:       "ripperr_tracker_circuit_open",
		Help:       "Circuit open",
		Type:       "gauge",
		LabelNames: []string{"tracker"},
		Samples: []Sample{
			{Values: []string{"rutracker"}, Value: 1},
			{Values: []string{"kinozal"}, Value: 0},
		},
	}
	empty := NewCounterVec("ripperr_notifications_total", "Notifications by result", "channel", "result")

	var buf bytes.Buffer
	if err := WriteText(&buf, []Family{c.Family(), empty.Family(), gauge}); err != nil {
		t.Fatal(err)
	}
	want := "# HELP ripperr_checks_total Checks by result\n" +
		"# TYPE ripperr_checks_total counter\n" +
		"ripperr_checks_total{tracker=\"a\\\\b\\\"c\\nd\",result=\"unknown\"} 1\n" +
		"ripperr_checks_total{tracker=\"kinozal\",result=\"error\"} 1\n" +
		"ripperr_checks_total{tracker=\"rutracker\",result=\"updated\"} 2\n" +
		"# HELP ripperr_notifications_total Notifications by result\n" +
		"# TYPE ripperr_notifications_total counter\n" +
		"# HELP ripperr_tracker_circuit_open Circuit open\n" +
		"# TYPE ripperr_tracker_circuit_open gauge\n" +
		"ripperr_tracker_circuit_open{tracker=\"kinozal\"} 0\n" +
		"ripperr_tracker_circuit_open{tracker=\"rutracker\"} 1\n"
	if got := buf.String(); got != want {
		t.Fatalf("output mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestCounterVecConcurrentInc(t *testing.T) {
	c := NewCounterVec("x_total", "x", "k")
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			for range 20 {
				c.Inc("a")
			}
		})
	}
	wg.Wait()
	if got := c.Value("a"); got != 1000 {
		t.Fatalf("value = %d, want 1000", got)
	}
	if got := c.Value("b"); got != 0 {
		t.Fatalf("unseen value = %d, want 0", got)
	}
}
