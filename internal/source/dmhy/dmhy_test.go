package dmhy

import (
	"testing"
)

func TestParseBangumiWithWeekDays(t *testing.T) {
	content := `monarray.push(['/images/cover.jpg?x=1','Anime Name','keyword',` +
		`'<a href="/topics/list/team_id%3A123">Group</a>','extra'])`
	got := parseBangumiWithWeekDays(content, "Mon", "monarray")
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	entry := got[0]
	if entry.Name != "Anime Name" || entry.UpdateDay != "Mon" || entry.ID != "keyword" {
		t.Errorf("unexpected entry: %+v", entry)
	}
	if entry.Cover != baseURL()+"/images/cover.jpg?x=1" {
		t.Errorf("cover = %q", entry.Cover)
	}
	if len(entry.SubtitleGroup) != 1 || entry.SubtitleGroup[0].ID != "123" || entry.SubtitleGroup[0].Name != "Group" {
		t.Errorf("subtitle groups = %+v", entry.SubtitleGroup)
	}
}

const topicListHTML = `<table id="topic_list"><tbody>
<tr class="">
  <td><span>2026/07/04 12:00</span></td>
  <td><a class="sort-2" href="/topics/list/sort_id/2"></a></td>
  <td>
    <a target="_blank" href="/topics/view/1.html">Test Show 01</a>
    <span class="tag"><a href="/topics/list/team_id/456">Group Name</a></span>
  </td>
  <td><a href="magnet:?xt=urn:btih:1">magnet</a></td>
</tr>
<tr><td colspan="4">header or empty row</td></tr>
</tbody></table>`

func TestTopicRowsAndParseTopicRow(t *testing.T) {
	rows := topicRows(topicListHTML)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	episode, ok := parseTopicRow(rows[0], "Test Show")
	if !ok {
		t.Fatal("parseTopicRow failed")
	}
	if episode.Title != "Test Show 01" || episode.Episode != 1 {
		t.Errorf("unexpected episode: %+v", episode)
	}
	if episode.Download != "magnet:?xt=urn:btih:1" {
		t.Errorf("download = %q", episode.Download)
	}
	if episode.Time == 0 {
		t.Error("expected parsed time")
	}
	if got := subtitleGroupID(rows[0]); got != "456" {
		t.Errorf("subtitleGroupID = %q, want 456", got)
	}
	if got := subtitleGroupName(rows[0]); got != "Group Name" {
		t.Errorf("subtitleGroupName = %q, want Group Name", got)
	}
}
