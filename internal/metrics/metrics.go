package metrics

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
)

type Sample struct {
	Values []string
	Value  uint64
}

type Family struct {
	Name       string
	Help       string
	Type       string
	LabelNames []string
	Samples    []Sample
}

type CounterVec struct {
	name   string
	help   string
	labels []string
	mu     sync.Mutex
	vals   map[string]*Sample
}

var (
	Checks        = NewCounterVec("ripperr_checks_total", "Tracker checks by tracker and result", "tracker", "result")
	Notifications = NewCounterVec("ripperr_notifications_total", "Notification deliveries by channel and result", "channel", "result")
)

func NewCounterVec(name, help string, labels ...string) *CounterVec {
	return &CounterVec{name: name, help: help, labels: labels, vals: make(map[string]*Sample)}
}

func (c *CounterVec) Inc(values ...string) {
	key := strings.Join(values, "\x00")
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.vals[key]
	if !ok {
		s = &Sample{Values: slices.Clone(values)}
		c.vals[key] = s
	}
	s.Value++
}

func (c *CounterVec) Value(values ...string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.vals[strings.Join(values, "\x00")]; ok {
		return s.Value
	}
	return 0
}

func (c *CounterVec) Family() Family {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := Family{Name: c.name, Help: c.help, Type: "counter", LabelNames: c.labels, Samples: make([]Sample, 0, len(c.vals))}
	for _, s := range c.vals {
		f.Samples = append(f.Samples, *s)
	}
	return f
}

func WriteText(w io.Writer, families []Family) error {
	for _, f := range families {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", f.Name, helpEscaper.Replace(f.Help), f.Name, f.Type); err != nil {
			return err
		}
		samples := slices.Clone(f.Samples)
		slices.SortFunc(samples, func(a, b Sample) int { return slices.Compare(a.Values, b.Values) })
		for _, s := range samples {
			var b strings.Builder
			b.WriteString(f.Name)
			if len(f.LabelNames) > 0 {
				b.WriteByte('{')
				for i, name := range f.LabelNames {
					if i > 0 {
						b.WriteByte(',')
					}
					fmt.Fprintf(&b, "%s=\"%s\"", name, labelEscaper.Replace(s.Values[i]))
				}
				b.WriteByte('}')
			}
			fmt.Fprintf(&b, " %d\n", s.Value)
			if _, err := io.WriteString(w, b.String()); err != nil {
				return err
			}
		}
	}
	return nil
}

var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
)
