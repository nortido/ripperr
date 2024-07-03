package rutor

import (
	"bytes"
	"net/http"
	"time"

	"trackerr/internal/model/tracker"
)

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) Header() (header *http.Header) {
	header = &http.Header{}

	header.Add("Host", t.GetHost())
	header.Add("Referer", "https://rutor.is/browse/")

	return
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	return &time.Time{}
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
