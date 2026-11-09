package mikan

import (
	"net/url"
	"strings"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/model"
)

const mikanBangumiHTML = `
<div class="pull-left leftbar-container">
  <img src="/images/subscribed-badge.svg" class="subscribed-badge" />
  <div class="bangumi-poster" style="background-image: url('/images/Bangumi/202604/c68609a0.jpg?width=400&height=560&format=webp');"></div>
  <p class="bangumi-title">大欺诈师</p>
  <p class="bangumi-info">更新时间：星期一</p>
</div>
<div class="leftbar-nav">
  <ul><li><a data-anchor="#34">极影字幕社</a></li></ul>
</div>
<div class="central-container">
  <div id="34"></div>
  <div class="episode-table">
    <table>
      <tr><th>title</th><th>download</th><th>size</th><th>time</th></tr>
      <tr>
        <td></td>
        <td>
          <a class="magnet-link-wrap">大欺诈师 01</a>
          <a class="magnet-link" data-clipboard-text="magnet:?xt=urn:btih:1"></a>
        </td>
        <td></td>
        <td>2026/07/04 12:00</td>
      </tr>
    </table>
  </div>
</div>
`

const mikanGreyCalendar = `
<div class="sk-bangumi" data-dayofweek="1"><ul>
<li><span class="b-lazy greyout" data-bangumiid="123" data-src="/images/upcoming.jpg?width=400"></span>
<div class="an-info"><div class="date-text">此番组下暂无作品</div>
<div class="date-text" title="待播番剧">待播番剧</div></div></li>
<li><span data-src="/images/current.jpg"></span>
<a href="/Home/Bangumi/456" title="已上线番剧">已上线番剧</a></li>
</ul></div>
<div class="sk-bangumi" data-dayofweek="7"><ul>
<li><span class="greyout" data-bangumiid="789" data-src="/images/movie.jpg"></span>
<div class="an-info"><div class="date-text" title="剧场版">剧场版</div></div></li>
</ul></div>
`

func withGetText(t *testing.T, pages ...string) {
	t.Helper()
	old := getText
	index := 0
	getText = func(rawURL string, params url.Values) (string, error) {
		page := pages[index%len(pages)]
		index++
		return page, nil
	}
	t.Cleanup(func() { getText = old })
}

func TestSearchPreservesDownloadQueryParameters(t *testing.T) {
	links := []string{
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=https://tracker.example/1" +
			"&tr=https://tracker.example/2&dn=old",
		"https://example.com/episode.torrent?token=test&dn=old",
	}
	for _, link := range links {
		t.Run("keyword/"+link, func(t *testing.T) {
			title := "Test Show 01"
			withGetText(t, `
			<table><tr class="js-search-results-row">
			  <td><a class="magnet-link-wrap">`+title+`</a>
			  <a class="magnet-link" data-clipboard-text="`+escapeAttr(link)+`"></a></td>
			  <td></td><td></td><td>2026/07/04 12:00</td>
			</tr></table>`)
			episodes, err := New().SearchByKeyword("keyword", 0)
			if err != nil {
				t.Fatal(err)
			}
			assertEpisodeLink(t, episodes, link, title)
		})
		t.Run("tag/"+link, func(t *testing.T) {
			title := "Test Show 01"
			withGetText(t,
				`<a href="/Home/Bangumi/123"><div class="an-info-group">Test Show</div></a>`,
				`<div class="subgroup-text"><a href="/Home/PublishGroup/34">Group</a>`+
					`<a href="/RSS/Bangumi?bangumiId=123&amp;subgroupid=34">RSS</a></div>`,
				`<rss><channel><item><title>`+title+`</title>`+
					`<enclosure url="`+escapeAttr(link)+`"/>`+
					`<torrent xmlns="https://mikanani.me/0.1/"><pubDate>2026-07-04T12:00:00</pubDate></torrent>`+
					`</item></channel></rss>`,
			)
			episodes, err := New().SearchByTag("keyword", "", 0)
			if err != nil {
				t.Fatal(err)
			}
			assertEpisodeLink(t, episodes, link, title)
		})
	}
}

func escapeAttr(s string) string {
	return strings.ReplaceAll(s, "&", "&amp;")
}

func splitQuery(raw string) (string, url.Values) {
	base, query, _ := strings.Cut(raw, "?")
	values, _ := url.ParseQuery(query)
	return base, values
}

func assertEpisodeLink(t *testing.T, episodes []model.Episode, link, title string) {
	t.Helper()
	if len(episodes) != 1 {
		t.Fatalf("got %d episodes, want 1", len(episodes))
	}
	actualBase, actualQuery := splitQuery(episodes[0].Download)
	originalBase, originalQuery := splitQuery(link)
	if actualBase != originalBase {
		t.Errorf("download base = %q, want %q", actualBase, originalBase)
	}
	for key, values := range originalQuery {
		if key == "dn" {
			continue
		}
		got := actualQuery[key]
		if strings.Join(got, "\x00") != strings.Join(values, "\x00") {
			t.Errorf("query %q = %v, want %v", key, got, values)
		}
	}
	if got := actualQuery["dn"]; len(got) != 1 || got[0] != title {
		t.Errorf("dn = %v, want [%q]", got, title)
	}
}

func TestFetchSingleBangumiIncludesCover(t *testing.T) {
	withGetText(t, mikanBangumiHTML)
	bangumi, err := New().FetchSingleBangumi("2242", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bangumi == nil {
		t.Fatal("expected bangumi, got nil")
	}
	want := "https://mikanani.me/images/Bangumi/202604/c68609a0.jpg"
	if bangumi.Cover != want {
		t.Errorf("cover = %q, want %q", bangumi.Cover, want)
	}
}

func TestCalendarIncludesGreyEntriesButSkipsMovies(t *testing.T) {
	withGetText(t, mikanGreyCalendar)
	entries, err := New().FetchBangumiCalendar()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries: %+v", len(entries), entries)
	}
	got := []struct{ id, name, day string }{
		{entries[0].ID, entries[0].Name, entries[0].UpdateDay},
		{entries[1].ID, entries[1].Name, entries[1].UpdateDay},
	}
	want := []struct{ id, name, day string }{
		{"123", "待播番剧", "Mon"},
		{"456", "已上线番剧", "Mon"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if entries[0].Cover != "https://mikanani.me/images/upcoming.jpg" {
		t.Errorf("grey cover = %q", entries[0].Cover)
	}
}

func TestGreyDetailRedirectHasNoEpisodes(t *testing.T) {
	withGetText(t, mikanGreyCalendar)
	website := New()
	bangumi, err := website.FetchSingleBangumi("123", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bangumi != nil {
		t.Errorf("expected nil bangumi, got %+v", bangumi)
	}
	episodes, err := website.FetchEpisodeOfBangumi("123", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 0 {
		t.Errorf("expected no episodes, got %+v", episodes)
	}
}

func TestMissingDetailIsNotSilentlyIgnored(t *testing.T) {
	withGetText(t, mikanGreyCalendar)
	_, err := New().FetchEpisodeOfBangumi("999", 0, nil)
	if err == nil || !strings.Contains(err.Error(), "Central container") {
		t.Errorf("expected Central container error, got %v", err)
	}
}
