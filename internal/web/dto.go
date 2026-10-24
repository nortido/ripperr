package web

import (
	"ripperr/internal/config"
	"ripperr/internal/settings"
)

type trackerResp struct {
	ID  int    `json:"id"`
	Key string `json:"key"`
	// закрывают одноименные поля TrackerItem: пароль уходит маской, а credentials
	// у трекера с формой входа только шаблон с плейсхолдерами
	Login         string `json:"login"`
	Password      string `json:"password"`
	SupportsLogin bool   `json:"supports_login"`
	config.TrackerItem
}

type trackerListResp struct {
	trackerResp
	Suggestions []settings.MirrorSuggestion `json:"suggestions,omitempty"`
	Paused      string                      `json:"paused,omitempty"`
	Auto        []settings.MirrorAuto       `json:"auto,omitempty"`
	ConfirmOn   bool                        `json:"confirm_on"`
}

type trackerTypeResp struct {
	trackerResp
	Added bool `json:"added"`
}

type healthResp struct {
	Status string `json:"status"`
}

type errorResp struct {
	Error string `json:"error"`
}

type loginErrorResp struct {
	Error        string `json:"error"`
	AttemptsLeft *int   `json:"attempts_left,omitempty"`
	RetryAfter   int    `json:"retry_after,omitempty"`
}

type alertResp struct {
	Tracker string `json:"tracker"`
	Since   string `json:"since"`
	Error   string `json:"error"`
}

type statusResp struct {
	Now         string      `json:"now"`
	NextRuns    []string    `json:"next_runs"`
	Running     bool        `json:"running"`
	DryRun      bool        `json:"dry_run"`
	Stopping    bool        `json:"stopping"`
	Checking    []string    `json:"checking"`
	Queued      []string    `json:"queued"`
	QueuedTotal int         `json:"queued_total"`
	Alerts      []alertResp `json:"alerts"`
}

type logEntryResp struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type logsResp struct {
	Items []logEntryResp `json:"items"`
}

type runResp struct {
	Started   bool `json:"started,omitempty"`
	Stopped   bool `json:"stopped,omitempty"`
	Restarted bool `json:"restarted,omitempty"`
	DryRun    bool `json:"dry_run,omitempty"`
}

type historyItemResp struct {
	ID          int64  `json:"id"`
	CheckedAt   string `json:"checked_at"`
	URLHash     string `json:"url_hash"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	TrackerID   int    `json:"tracker_id"`
	TrackerName string `json:"tracker_name"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	ErrorStreak int    `json:"error_streak"`
	Category    string `json:"category"`
	Broken      bool   `json:"broken"`
	RecheckAt   string `json:"recheck_at"`
	ForceCheck  bool   `json:"force_check"`
}

type historyResp struct {
	Items []historyItemResp `json:"items"`
	Total int               `json:"total"`
}

type checkLogSummaryResp struct {
	Updated int `json:"updated"`
	Errors  int `json:"errors"`
}

type ignoreItemResp struct {
	URLHash string `json:"url_hash"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	AddedAt string `json:"added_at"`
}

type testResultResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Hosts int    `json:"hosts,omitempty"`
}

type categoriesResp struct {
	Categories []string `json:"categories"`
}

type pushDeviceResp struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"created_at"`
}

type pushStateResp struct {
	PublicKey string           `json:"public_key"`
	Devices   []pushDeviceResp `json:"devices"`
}
