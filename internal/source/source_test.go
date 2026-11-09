package source

import (
	"errors"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/model"
)

type fakeSource struct {
	results map[string][]model.Episode
	errs    map[string]error
}

func (f *fakeSource) Name() string { return "fake" }

func (f *fakeSource) SearchByKeyword(keyword string, count int) ([]model.Episode, error) {
	if err := f.errs[keyword]; err != nil {
		return nil, err
	}
	return f.results[keyword], nil
}

func (f *fakeSource) SearchByTag(string, string, int) ([]model.Episode, error) {
	return nil, nil
}

func (f *fakeSource) FetchBangumiCalendar() ([]model.WebsiteBangumi, error) {
	return nil, nil
}

func (f *fakeSource) FetchEpisodeOfBangumi(string, int, []string) ([]model.Episode, error) {
	return nil, nil
}

func (f *fakeSource) FetchSingleBangumi(string, []string, int) (*model.WebsiteBangumi, error) {
	return nil, nil
}

func TestGetManualEpisodesMatchesFirstTitleAndDeduplicates(t *testing.T) {
	src := &fakeSource{results: map[string][]model.Episode{
		"凉宫春日的忧郁": {
			{Title: "[Sub] 凉宫春日的忧郁 - 02 [1080p]", Download: "magnet:2", Episode: 2, Time: 20},
			{Title: "凉宫春日的忧郁 - 02 [720p]", Download: "magnet:2", Episode: 2, Time: 25},
			{Title: "凉宫春日的忧郁 - 01 [1080p]", Download: "magnet:1", Episode: 1, Time: 10},
		},
		"Haruhi": {
			{Title: "Haruhi Suzumiya - 03 [1080p]", Download: "magnet:3", Episode: 3, Time: 30},
		},
	}}
	episodes, err := GetManualEpisodes(src, []string{"凉宫春日的忧郁", "Haruhi"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 2 {
		t.Fatalf("got %d episodes, want 2: %#v", len(episodes), episodes)
	}
	if episodes[0].Episode != 1 || episodes[1].Episode != 2 {
		t.Fatalf("episodes are not sorted: %#v", episodes)
	}
	if episodes[1].Download != "magnet:2" || episodes[1].Time != 20 {
		t.Fatalf("duplicate was not kept from the first result: %#v", episodes[1])
	}
}

func TestGetManualEpisodesFallsBackToAliases(t *testing.T) {
	src := &fakeSource{results: map[string][]model.Episode{
		"Haruhi": {
			{Title: "Haruhi Suzumiya - 03 [1080p]", Download: "magnet:3", Episode: 3, Time: 30},
		},
	}}
	episodes, err := GetManualEpisodes(src, []string{"凉宫春日的忧郁", "Haruhi"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 || episodes[0].Episode != 3 {
		t.Fatalf("alias fallback failed: %#v", episodes)
	}
}

func TestGetManualEpisodesReturnsLastErrorWhenNothingFound(t *testing.T) {
	src := &fakeSource{errs: map[string]error{"Bangumi": errors.New("network down")}}
	if _, err := GetManualEpisodes(src, []string{"Bangumi"}, 1); err == nil {
		t.Fatal("expected the search error to be returned")
	}
}
