package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/notify"
)

func applyIntegrationsDoc(t *testing.T, doc string) config.App {
	t.Helper()
	cfg := config.DefaultApp()
	db := &fakeSettingsDB{data: map[string]json.RawMessage{"integrations": json.RawMessage(doc)}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if db.saves != 0 {
		t.Fatalf("read-time migration must not write the DB, saves %d", db.saves)
	}
	return cfg
}

func TestNotifyEventsSharedFromUnion(t *testing.T) {
	cfg := applyIntegrationsDoc(t, `{"notifications":{
		"telegram":{"events":["updated","mirror_suggested"]},
		"webhook":{"events":["check_failed"]},
		"email":{"events":[]},
		"pushover":{"events":[]},
		"browser":{"events":[]}}}`)
	n := cfg.Notify
	if want := []string{"updated", "mirror_switched", "check_failed"}; !slices.Equal(n.Events, want) {
		t.Fatalf("shared = %v, want %v", n.Events, want)
	}

	cfg = applyIntegrationsDoc(t, `{"notifications":{
		"telegram":{"events":["updated"]},
		"webhook":{"events":["check_failed"]},
		"email":{"events":[]},
		"pushover":{"events":[]}}}`)
	for _, k := range []string{"updated", "broken", "tracker_down", "tracker_up", "mirror_switched", "check_failed"} {
		if !slices.Contains(cfg.Notify.Events, k) {
			t.Fatalf("missing browser key must add defaults, %s absent in %v", k, cfg.Notify.Events)
		}
	}
}

func TestNotifyEventsSharedStored(t *testing.T) {
	cfg := applyIntegrationsDoc(t, `{"notifications":{
		"events":["broken","mirror_suggested"],
		"telegram":{"events":["updated"]},
		"webhook":{"events":["check_failed"]}}}`)
	if want := []string{"broken", "mirror_switched"}; !slices.Equal(cfg.Notify.Events, want) {
		t.Fatalf("shared = %v, want %v", cfg.Notify.Events, want)
	}
}

func TestIntegrationsPatchLegacyMirrorKind(t *testing.T) {
	o, err := IntegrationsPatchFromJSON(json.RawMessage(`{"notifications":{"events":["mirror_suggested","mirror_switched"],"telegram":{"events":["mirror_suggested"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if g := o.Notifications.Telegram.Events; g == nil || !slices.Equal(*g, []string{"mirror_switched"}) {
		t.Fatalf("telegram = %v", g)
	}
	if g := o.Notifications.Events; g == nil || !slices.Equal(*g, []string{"mirror_switched"}) {
		t.Fatalf("shared = %v", g)
	}
	for _, doc := range []string{
		`{"notifications":{"telegram":{"events":["bogus"]}}}`,
		`{"notifications":{"events":["bogus"]}}`,
	} {
		if _, err := IntegrationsPatchFromJSON(json.RawMessage(doc)); err == nil {
			t.Fatalf("%s: want error", doc)
		}
	}
}

func TestMergeIntegrationsOldPayloadWithCustomEvents(t *testing.T) {
	db := &fakeSettingsDB{}
	body := `{"notifications":{"events":["updated"],"telegram":{"chat_id":"1","custom_events":true,"events":["mirror_suggested"]}}}`
	if err := MergeIntegrations(context.Background(), db, json.RawMessage(body)); err != nil {
		t.Fatalf("old payload must be accepted: %v", err)
	}
	if strings.Contains(string(db.data["integrations"]), "custom_events") {
		t.Fatalf("custom_events must not be stored: %s", db.data["integrations"])
	}
	cfg := applyIntegrationsDoc(t, string(db.data["integrations"]))
	if !slices.Equal(cfg.Notify.Events, []string{"updated"}) {
		t.Fatalf("shared = %v", cfg.Notify.Events)
	}
}
func TestStoredChannelListsDoNotFilterDelivery(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	cfg := applyIntegrationsDoc(t, `{"notifications":{
		"enabled":true,
		"events":["updated"],
		"webhook":{"url":"`+srv.URL+`","custom_events":true,"events":["broken"]}}}`)
	n := notify.New(notify.Config(cfg.Notify), "")
	n.Notify(notify.BrokenEvent("a", "", 3))
	time.Sleep(300 * time.Millisecond)
	if hits.Load() != 0 {
		t.Fatalf("broken is outside the shared set but was delivered: %d", hits.Load())
	}
	n.Notify(notify.UpdatedEvent("a", "", ""))
	deadline := time.Now().Add(3 * time.Second)
	for hits.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if hits.Load() != 1 {
		t.Fatalf("updated is in the shared set, hits %d", hits.Load())
	}
}

func TestIntegrationsJSONHasOnlySharedEvents(t *testing.T) {
	cfg := applyIntegrationsDoc(t, `{"notifications":{"events":["broken"],"telegram":{"events":["updated"]}}}`)
	var top struct {
		Notifications map[string]json.RawMessage `json:"notifications"`
	}
	if err := json.Unmarshal(IntegrationsJSON(&cfg), &top); err != nil {
		t.Fatal(err)
	}
	notif := top.Notifications
	var shared []string
	if err := json.Unmarshal(notif["events"], &shared); err != nil || !slices.Equal(shared, []string{"broken"}) {
		t.Fatalf("shared = %v %v", shared, err)
	}
	for _, ch := range []string{"telegram", "webhook", "email", "pushover", "browser"} {
		var c map[string]json.RawMessage
		if err := json.Unmarshal(notif[ch], &c); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"events", "custom_events"} {
			if _, ok := c[k]; ok {
				t.Fatalf("%s carries %s", ch, k)
			}
		}
	}
}
