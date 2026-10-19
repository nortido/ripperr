package tracker

import (
	"slices"
	"testing"

	"ripperr/internal/config"
)

func retiredTestApp(confirm bool) config.App {
	return config.App{
		Runner: config.Runner{MirrorConfirm: confirm},
		Tracker: config.Tracker{"kinozal": &config.TrackerItem{
			Name: "kinozal", Domain: "kinozal.me", Mirrors: []string{"kinozal.guru", "x.example"},
			Retired: []string{"kinozal.tv"}, Unconfirmed: []string{"x.example"}, Enabled: true,
		}},
	}
}

func TestRetiredHostKeepsTorrentTracked(t *testing.T) {
	s := &service{handlerRegistry: buildHandlerRegistry(ConfigFromApp(retiredTestApp(true)))}
	if got := s.GetID("https://kinozal.tv/details.php?id=1"); got != KinozalID {
		t.Fatalf("GetID(retired host) = %d, want kinozal %d", got, KinozalID)
	}
}

func TestConfigFromAppUnconfirmedFollowsToggle(t *testing.T) {
	if got := ConfigFromApp(retiredTestApp(true))["kinozal"].Unconfirmed; !slices.Equal(got, []string{"x.example"}) {
		t.Errorf("toggle on: Unconfirmed = %v, want [x.example]", got)
	}
	if got := ConfigFromApp(retiredTestApp(false))["kinozal"].Unconfirmed; got != nil {
		t.Errorf("toggle off: Unconfirmed = %v, want nil", got)
	}
}
