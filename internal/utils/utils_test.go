package utils

import (
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/model"
)

func TestEpisodeFilterRegex(t *testing.T) {
	old := config.Cfg.EnableGlobalFilters
	config.Cfg.EnableGlobalFilters = false
	t.Cleanup(func() { config.Cfg.EnableGlobalFilters = old })

	data := []model.Episode{
		{Name: "1", Title: "720", Download: "1", Episode: 1},
		{Name: "2", Title: "1080", Download: "1", Episode: 1},
		{Name: "2", Title: "23", Download: "2", Episode: 2},
		{Name: "1", Title: "17202", Download: "2", Episode: 2},
		{Name: "3", Title: "..71..", Download: "3", Episode: 3},
		{Name: "5", Title: "no", Download: "5", Episode: 5},
	}
	got := EpisodeFilterRegex(data, ".*720.*")
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Name != "1" {
			t.Errorf("unexpected name %q", e.Name)
		}
	}
}

func TestFilterByFollowedFiltersEmptyListsKeepEverything(t *testing.T) {
	oldFilters, oldInclude := config.Cfg.EnableGlobalFilters, config.Cfg.EnableGlobalIncludeKeywords
	config.Cfg.EnableGlobalFilters = false
	config.Cfg.EnableGlobalIncludeKeywords = false
	t.Cleanup(func() {
		config.Cfg.EnableGlobalFilters = oldFilters
		config.Cfg.EnableGlobalIncludeKeywords = oldInclude
	})

	data := []model.Episode{
		{Title: "Episode 1", Episode: 1},
		{Title: "Episode 2", Episode: 2},
	}
	// JSON decoding of "[]" produces a non-nil empty slice.
	followed := &FollowedFilter{Include: []string{}, Exclude: []string{}}
	got := FilterByFollowedFilters(data, followed)
	if len(got) != len(data) {
		t.Fatalf("empty filters dropped %d of %d episodes", len(data)-len(got), len(data))
	}
}
