package bangumimoe

import (
	"strings"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/config"
)

func withBangumiMoeURL(t *testing.T) {
	t.Helper()
	old := config.Cfg.BangumiMoeURL
	config.Cfg.BangumiMoeURL = "https://bangumi.moe"
	t.Cleanup(func() { config.Cfg.BangumiMoeURL = old })
}

func stubFetchJSON(t *testing.T, fn func(url string, out any) error) {
	t.Helper()
	old := fetchJSON
	fetchJSON = func(url, method string, body any, out any) error {
		return fn(url, out)
	}
	t.Cleanup(func() { fetchJSON = old })
}

func TestParserBangumi(t *testing.T) {
	withBangumiMoeURL(t)
	stubFetchJSON(t, func(url string, out any) error {
		switch {
		case strings.HasSuffix(url, "/api/team/working"):
			m := out.(*map[string][]teamEntry)
			*m = map[string][]teamEntry{
				"tag-1": {{TagID: "team-1", Name: "Group 1"}},
			}
		case strings.HasSuffix(url, "/api/tag/fetch"):
			items := out.(*[]tagItem)
			*items = []tagItem{
				{ID: "tag-1", Locale: map[string]any{"zh_cn": "中文名"}, Name: "fallback"},
			}
		default:
			t.Fatalf("unexpected url %s", url)
		}
		return nil
	})

	got, err := parserBangumi([]bangumiData{
		{TagID: "tag-1", Cover: "/images/cover.jpg", ShowOn: 1, Name: "fallback"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	entry := got[0]
	if entry.Name != "中文名" || entry.UpdateDay != "Mon" || entry.ID != "tag-1" {
		t.Errorf("unexpected entry: %+v", entry)
	}
	if entry.Cover != "https://bangumi.moe/images/cover.jpg" {
		t.Errorf("cover = %q", entry.Cover)
	}
	if len(entry.SubtitleGroup) != 1 || entry.SubtitleGroup[0].ID != "team-1" {
		t.Errorf("subtitle group = %+v", entry.SubtitleGroup)
	}
}

func TestTorrentsToEpisodes(t *testing.T) {
	withBangumiMoeURL(t)
	episodes := (&BangumiMoe{}).torrentsToEpisodes([]map[string]any{
		{
			"_id":          "torrent-1",
			"team_id":      "team-1",
			"title":        "[Group] Test Show 01 [1080p]",
			"publish_time": "2026-07-04T12:00:00.000Z",
		},
	})
	if len(episodes) != 1 {
		t.Fatalf("got %d episodes, want 1", len(episodes))
	}
	e := episodes[0]
	if e.Episode != 1 || e.SubtitleGroup != "team-1" {
		t.Errorf("unexpected episode: %+v", e)
	}
	if e.Download != "https://bangumi.moe/download/torrent/torrent-1/download.torrent" {
		t.Errorf("download = %q", e.Download)
	}
}

func TestProcessSearchResultReversesOrder(t *testing.T) {
	withBangumiMoeURL(t)
	got := (&BangumiMoe{}).processSearchResult("Test Show", []map[string]any{
		{"_id": "a", "team_id": "t", "title": "Test Show 01", "publish_time": "2026-07-04T12:00:00"},
		{"_id": "b", "team_id": "t", "title": "Test Show 02", "publish_time": "2026-07-04T12:00:00"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d episodes, want 2", len(got))
	}
	if got[0].Episode != 2 || got[1].Episode != 1 {
		t.Errorf("expected reversed order, got %d then %d", got[0].Episode, got[1].Episode)
	}
	if got[0].Name != "Test Show" {
		t.Errorf("name = %q", got[0].Name)
	}
}
