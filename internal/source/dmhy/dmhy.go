package dmhy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/episode"
	"github.com/HanamiSakiP/u-ani/internal/htmlutil"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/model"
)

var (
	encodedPlusPattern = regexp.MustCompile(`'\+encodeURIComponent\('(.*?)'\)\+'`)
	encodedPattern     = regexp.MustCompile(`encodeURIComponent\('(.*?)'\)`)
	coverPattern       = regexp.MustCompile(`(/images/.*)$`)
	teamIDEncoded      = regexp.MustCompile(`team_id%3A(.+)$`)
	teamIDPlain        = regexp.MustCompile(`team_id/(.+)$`)
	pushTemplate       = `(?s)%s\.push\(\['(.*?)','(.*?)','(.*?)','(.*?)','(.*?)'\]\)`
)

type Dmhy struct{}

func New() *Dmhy { return &Dmhy{} }

func (d *Dmhy) Name() string { return "dmhy" }

func baseURL() string {
	return strings.TrimRight(config.Cfg.ShareDmhyURL, "/")
}

func fetchURL(rawURL string, params url.Values) (string, error) {
	u := rawURL
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "uani-cli")
	resp, err := httpx.Client().Do(req)
	if err != nil {
		logger.Error("Create connection to %s... failed", baseURL())
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(body), nil
}

func quoteKeyword(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func parseBangumiWithWeekDays(content, updateTime, arrayName string) []model.WebsiteBangumi {
	content = encodedPlusPattern.ReplaceAllString(content, "$1")
	content = encodedPattern.ReplaceAllString(content, "'URLE#$1'")

	re := regexp.MustCompile(fmt.Sprintf(pushTemplate, regexp.QuoteMeta(arrayName)))
	rows := re.FindAllStringSubmatch(content, -1)
	out := make([]model.WebsiteBangumi, 0, len(rows))
	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		coverURL, name, keyword, subtitleRaw := row[1], row[2], row[3], row[4]
		if strings.HasPrefix(keyword, "URLE#") {
			keyword = quoteKeyword(keyword[5:])
		}
		cover := ""
		if m := coverPattern.FindStringSubmatch(coverURL); len(m) > 1 {
			cover = m[1]
		}

		var groups []model.SubtitleGroup
		doc, err := htmlutil.ParseString(subtitleRaw)
		if err == nil {
			for _, a := range htmlutil.FindAll(doc, htmlutil.Element("a")) {
				groupName := strings.TrimSpace(htmlutil.Text(a))
				m := teamIDEncoded.FindStringSubmatch(htmlutil.Attr(a, "href"))
				if len(m) == 0 || groupName == "" {
					continue
				}
				groups = append(groups, model.SubtitleGroup{ID: m[1], Name: groupName})
			}
		}

		out = append(out, model.WebsiteBangumi{
			ID:            keyword,
			Name:          name,
			UpdateDay:     updateTime,
			Cover:         baseURL() + cover,
			SubtitleGroup: groups,
		})
	}
	return out
}

func parseListTime(s string) int64 {
	t, err := time.ParseInLocation("2006/01/02 15:04", strings.TrimSpace(s), time.Local)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func hasClassAttribute(n *html.Node) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == "class" {
			return a.Val, true
		}
	}
	return "", false
}

func firstAnchorClass(n *html.Node) string {
	a := htmlutil.Find(n, htmlutil.Element("a"))
	if a == nil {
		return ""
	}
	classes := strings.Fields(htmlutil.Attr(a, "class"))
	if len(classes) == 0 {
		return ""
	}
	return classes[0]
}

// topicRows returns the torrent rows of dmhy's topic list table.
func topicRows(content string) []*html.Node {
	doc, err := htmlutil.ParseString(content)
	if err != nil {
		return nil
	}
	table := htmlutil.Find(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "table" && htmlutil.Attr(n, "id") == "topic_list"
	})
	if table == nil {
		return nil
	}
	var out []*html.Node
	for _, tr := range htmlutil.FindAll(table, htmlutil.Element("tr")) {
		class, has := hasClassAttribute(tr)
		if !has || class != "" {
			continue
		}
		out = append(out, tr)
	}
	return out
}

func parseTopicRow(tr *html.Node, name string) (model.Episode, bool) {
	tds := htmlutil.FindAll(tr, htmlutil.Element("td"))
	if len(tds) < 4 {
		return model.Episode{}, false
	}
	if firstAnchorClass(tds[1]) != "sort-2" {
		return model.Episode{}, false
	}
	var timeString string
	if span := htmlutil.Find(tds[0], htmlutil.Element("span")); span != nil {
		timeString = strings.TrimSpace(htmlutil.Text(span))
	}
	titleAnchor := htmlutil.Find(tds[2], func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "a" && htmlutil.Attr(n, "target") == "_blank"
	})
	downloadAnchor := htmlutil.Find(tds[3], htmlutil.Element("a"))
	if titleAnchor == nil || downloadAnchor == nil {
		return model.Episode{}, false
	}
	title := strings.TrimSpace(htmlutil.Text(titleAnchor))
	return model.Episode{
		Title:    title,
		Download: htmlutil.Attr(downloadAnchor, "href"),
		Episode:  episode.Parse(title),
		Time:     parseListTime(timeString),
		Name:     name,
	}, true
}

func subtitleGroupID(tr *html.Node) string {
	tds := htmlutil.FindAll(tr, htmlutil.Element("td"))
	if len(tds) < 3 {
		return ""
	}
	group := ""
	for _, span := range htmlutil.FindAll(tds[2], func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "span" && htmlutil.HasClass(n, "tag")
	}) {
		a := htmlutil.Find(span, htmlutil.Element("a"))
		if a == nil {
			continue
		}
		if m := teamIDPlain.FindStringSubmatch(htmlutil.Attr(a, "href")); len(m) > 0 {
			group = m[1]
		}
	}
	return group
}

func subtitleGroupName(tr *html.Node) string {
	tds := htmlutil.FindAll(tr, htmlutil.Element("td"))
	if len(tds) < 3 {
		return ""
	}
	for _, span := range htmlutil.FindAll(tds[2], func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "span" && htmlutil.HasClass(n, "tag")
	}) {
		if a := htmlutil.Find(span, htmlutil.Element("a")); a != nil {
			return strings.TrimSpace(htmlutil.Text(a))
		}
	}
	return ""
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func (d *Dmhy) SearchByKeyword(keyword string, count int) ([]model.Episode, error) {
	if count <= 0 {
		count = 3
	}
	searchURL := baseURL() + "/topics/list/"
	var out []model.Episode
	for i := 0; i < count; i++ {
		content, err := fetchURL(searchURL, url.Values{"keyword": {keyword}, "page": {fmt.Sprint(i + 1)}})
		if err != nil {
			return nil, err
		}
		for _, tr := range topicRows(content) {
			if e, ok := parseTopicRow(tr, keyword); ok {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

func (d *Dmhy) resolveTeamID(subtitleName string) string {
	list, err := d.FetchBangumiCalendar()
	if err != nil {
		return ""
	}
	for _, b := range list {
		for _, sg := range b.SubtitleGroup {
			if sg.Name == subtitleName {
				return sg.ID
			}
		}
	}
	return ""
}

func (d *Dmhy) SearchByTag(tag string, subtitle string, count int) ([]model.Episode, error) {
	if count <= 0 {
		count = 3
	}
	teamID := ""
	if subtitle != "" {
		teamID = d.resolveTeamID(subtitle)
	}
	searchURL := baseURL() + "/topics/list/"
	var out []model.Episode
	for i := 0; i < count; i++ {
		params := url.Values{"keyword": {tag}, "page": {fmt.Sprint(i + 1)}}
		if teamID != "" {
			params.Set("team_id", teamID)
		}
		content, err := fetchURL(searchURL, params)
		if err != nil {
			return nil, err
		}
		if content == "" {
			break
		}
		for _, tr := range topicRows(content) {
			e, ok := parseTopicRow(tr, tag)
			if !ok {
				continue
			}
			e.SubtitleGroup = subtitleGroupName(tr)
			out = append(out, e)
		}
	}
	return out, nil
}

func (d *Dmhy) FetchBangumiCalendar() ([]model.WebsiteBangumi, error) {
	weekDays := []struct {
		day  string
		name string
	}{
		{"Sun", "sunarray"}, {"Mon", "monarray"}, {"Tue", "tuearray"},
		{"Wed", "wedarray"}, {"Thu", "thuarray"}, {"Fri", "friarray"},
		{"Sat", "satarray"},
	}
	content, err := fetchURL(baseURL()+"/cms/page/name/programme.html", nil)
	if err != nil {
		return nil, err
	}
	var out []model.WebsiteBangumi
	for _, w := range weekDays {
		out = append(out, parseBangumiWithWeekDays(content, w.day, w.name)...)
	}
	return out, nil
}

func (d *Dmhy) FetchEpisodeOfBangumi(bangumiID string, maxPage int, subtitleList []string) ([]model.Episode, error) {
	searchURL := baseURL() + "/topics/list/"
	var out []model.Episode
	for i := 0; i < maxPage; i++ {
		u := searchURL + "?keyword=" + url.QueryEscape(bangumiID) + "&page=" + fmt.Sprint(i+1)
		content, err := fetchURL(u, nil)
		if err != nil {
			return nil, err
		}
		for _, tr := range topicRows(content) {
			e, ok := parseTopicRow(tr, bangumiID)
			if !ok {
				continue
			}
			group := subtitleGroupID(tr)
			if len(subtitleList) > 0 && !contains(subtitleList, group) {
				continue
			}
			e.SubtitleGroup = group
			e.Name = ""
			out = append(out, e)
		}
	}
	return out, nil
}

func (d *Dmhy) FetchSingleBangumi(bangumiID string, subtitleList []string, maxPage int) (*model.WebsiteBangumi, error) {
	return nil, nil
}
