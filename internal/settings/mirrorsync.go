package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/system/logger"
)

const settingMirrorSync = "mirror_sync"

const (
	MirrorKindRemove  = "remove"
	MirrorKindMain    = "main"
	MirrorKindConfirm = "confirm"

	MirrorAutoAdded   = "added"
	MirrorAutoRemoved = "removed"

	MirrorReasonJackett          = "jackett"
	MirrorReasonDead             = "dead"
	MirrorReasonRedirect         = "redirect"
	MirrorReasonRedirectUnlisted = "redirect_unlisted"
)

type MirrorSuggestion struct {
	Kind   string `json:"kind"`
	Host   string `json:"host"`
	From   string `json:"from,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type MirrorSource struct {
	Links   []string  `json:"links"`
	Legacy  []string  `json:"legacy"`
	Fetched time.Time `json:"fetched"`
}

type MirrorAuto struct {
	Kind   string    `json:"kind"`
	Host   string    `json:"host"`
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

type MirrorPause struct {
	Host  string    `json:"host"`
	Since time.Time `json:"since"`
}

type MirrorPruned struct {
	Host string    `json:"host"`
	At   time.Time `json:"at"`
}

type MirrorSync struct {
	LastRun     time.Time                     `json:"last_run"`
	Sources     map[string]MirrorSource       `json:"sources,omitempty"`
	Fails       map[string]map[string]int     `json:"fails,omitempty"`
	Suggestions map[string][]MirrorSuggestion `json:"suggestions,omitempty"`
	Dismissed   []string                      `json:"dismissed,omitempty"`
	Auto        map[string][]MirrorAuto       `json:"auto,omitempty"`
	Paused      map[string]MirrorPause        `json:"paused,omitempty"`
	Pruned      map[string][]MirrorPruned     `json:"pruned,omitempty"`
}

type MirrorPending struct {
	Suggestions []MirrorSuggestion `json:"suggestions,omitempty"`
	Paused      string             `json:"paused,omitempty"`
	Auto        []MirrorAuto       `json:"auto,omitempty"`
}

type settingsStore interface {
	settingsDB
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}

func MirrorHostKey(h string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(h)), "www.")
}

func ValidMirrorHost(h string) bool {
	if validateTrackerHostname(h, "mirror domain") != nil {
		return false
	}
	if strings.Contains(h, ":") || !strings.Contains(h, ".") || strings.Contains(h, "..") || strings.HasSuffix(h, ".") {
		return false
	}
	for _, c := range h {
		if c > 127 {
			return false
		}
	}
	return net.ParseIP(h) == nil
}

func GetMirrorSync(ctx context.Context, db settingsDB) (MirrorSync, error) {
	var d MirrorSync
	raw, ok, err := db.GetSetting(ctx, settingMirrorSync)
	if err != nil || !ok {
		return d, err
	}
	if err = json.Unmarshal(raw, &d); err != nil {
		logger.Warn(fmt.Sprintf("settings %s: invalid JSON: %v", settingMirrorSync, err))
		return MirrorSync{}, nil
	}
	return d, nil
}

var mirrorSyncMu sync.Mutex

func UpdateMirrorSync(ctx context.Context, db settingsStore, fn func(*MirrorSync)) error {
	mirrorSyncMu.Lock()
	defer mirrorSyncMu.Unlock()
	d, err := GetMirrorSync(ctx, db)
	if err != nil {
		return err
	}
	before, err := json.Marshal(d)
	if err != nil {
		return err
	}
	fn(&d)
	after, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		return nil
	}
	return db.SaveSetting(ctx, settingMirrorSync, after)
}

func dismissID(key, kind, host string) string {
	return key + "|" + kind + "|" + MirrorHostKey(host)
}

func (d *MirrorSync) SetSuggestions(key string, list []MirrorSuggestion) {
	var kept []MirrorSuggestion
	for _, s := range list {
		if !slices.Contains(d.Dismissed, dismissID(key, s.Kind, s.Host)) {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		delete(d.Suggestions, key)
		return
	}
	if d.Suggestions == nil {
		d.Suggestions = make(map[string][]MirrorSuggestion)
	}
	d.Suggestions[key] = kept
}

func (d *MirrorSync) Dismiss(key, kind, host string) bool {
	if kind != MirrorKindRemove && kind != MirrorKindMain {
		return false
	}
	list := d.Suggestions[key]
	want := MirrorHostKey(host)
	i := slices.IndexFunc(list, func(s MirrorSuggestion) bool { return s.Kind == kind && MirrorHostKey(s.Host) == want })
	if i < 0 {
		return false
	}
	if id := dismissID(key, kind, host); !slices.Contains(d.Dismissed, id) {
		d.Dismissed = append(d.Dismissed, id)
	}
	d.SetSuggestions(key, slices.Delete(slices.Clone(list), i, i+1))
	return true
}

func (d MirrorSync) IsDismissed(key, kind, host string) bool {
	return slices.Contains(d.Dismissed, dismissID(key, kind, host))
}

func hasHostKey(list []string, host string) bool {
	want := MirrorHostKey(host)
	return slices.ContainsFunc(list, func(h string) bool { return MirrorHostKey(h) == want })
}

func (d MirrorSync) Pending(key string, item config.TrackerItem, confirmOn bool) MirrorPending {
	var p MirrorPending
	domain := MirrorHostKey(item.Domain)
	if confirmOn {
		for _, h := range item.Unconfirmed {
			if hasHostKey(item.Mirrors, h) {
				p.Suggestions = append(p.Suggestions, MirrorSuggestion{Kind: MirrorKindConfirm, Host: h})
			}
		}
	}
	for _, s := range d.Suggestions[key] {
		host := MirrorHostKey(s.Host)
		switch s.Kind {
		case MirrorKindRemove:
			if confirmOn && host != domain && hasHostKey(item.Mirrors, s.Host) {
				p.Suggestions = append(p.Suggestions, s)
			}
		case MirrorKindMain:
			if MirrorHostKey(s.From) == domain && host != domain {
				p.Suggestions = append(p.Suggestions, s)
			}
		}
	}
	if pause, ok := d.Paused[key]; ok && confirmOn && hasHostKey(item.Unconfirmed, pause.Host) {
		p.Paused = pause.Host
	}
	since := time.Now().Add(-30 * 24 * time.Hour)
	for _, a := range d.Auto[key] {
		if a.At.After(since) {
			p.Auto = append(p.Auto, a)
		}
	}
	return p
}
