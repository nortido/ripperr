package settings

import (
	"context"
	"encoding/json"
	"testing"

	"ripperr/internal/config"
)

func TestWatchChosen(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		cats []string
		tags []string
		want bool
	}{
		{"no doc, default tag only", "", nil, nil, false},
		{"doc without watch fields", `{"start_hour":7}`, nil, nil, false},
		{"watch_mode", `{"watch_mode":"categories"}`, nil, nil, true},
		{"empty monitored_categories", `{"monitored_categories":[]}`, nil, nil, true},
		{"watch_tags", `{"watch_tags":["x"]}`, nil, nil, true},
		{"watch_tag", `{"watch_tag":"x"}`, nil, nil, true},
		{"allowed categories from cfg", "", []string{"tv"}, nil, true},
		{"env tag differs from default", "", nil, []string{"tv"}, true},
		{"env tag is default", "", nil, []string{"watch"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeSettingsDB{}
			if tc.doc != "" {
				db.data = map[string]json.RawMessage{"schedule": json.RawMessage(tc.doc)}
			}
			cfg := &config.App{Runner: config.Runner{WatchTags: tc.tags, AllowedCategories: tc.cats}}
			got, err := WatchChosen(context.Background(), db, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("WatchChosen = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOnboardingMerge(t *testing.T) {
	ctx := context.Background()
	yes, no := true, false

	t.Run("absent doc is zero", func(t *testing.T) {
		got, err := GetOnboarding(ctx, &fakeSettingsDB{})
		if err != nil || got != (Onboarding{}) {
			t.Fatalf("got %+v, %v", got, err)
		}
	})

	t.Run("patches keep the other field", func(t *testing.T) {
		db := &fakeSettingsDB{}
		if err := MergeOnboarding(ctx, db, OnboardingPatch{TourDone: &yes}); err != nil {
			t.Fatal(err)
		}
		if err := MergeOnboarding(ctx, db, OnboardingPatch{ChecklistHidden: &yes}); err != nil {
			t.Fatal(err)
		}
		got, err := GetOnboarding(ctx, db)
		if err != nil || got != (Onboarding{ChecklistHidden: true, TourDone: true}) {
			t.Fatalf("got %+v, %v", got, err)
		}
		if err = MergeOnboarding(ctx, db, OnboardingPatch{ChecklistHidden: &no, TourDone: &no}); err != nil {
			t.Fatal(err)
		}
		got, err = GetOnboarding(ctx, db)
		if err != nil || got != (Onboarding{}) {
			t.Fatalf("after reset got %+v, %v", got, err)
		}
	})

	t.Run("invalid stored json", func(t *testing.T) {
		db := &fakeSettingsDB{data: map[string]json.RawMessage{"onboarding": json.RawMessage(`{broken`)}}
		got, err := GetOnboarding(ctx, db)
		if err != nil || got != (Onboarding{}) {
			t.Fatalf("got %+v, %v", got, err)
		}
		if err = MergeOnboarding(ctx, db, OnboardingPatch{TourDone: &yes}); err != nil {
			t.Fatal(err)
		}
		if !json.Valid(db.data["onboarding"]) {
			t.Fatalf("stored doc still invalid: %s", db.data["onboarding"])
		}
		got, err = GetOnboarding(ctx, db)
		if err != nil || got != (Onboarding{TourDone: true}) {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
}
