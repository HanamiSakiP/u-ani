package mikan

import (
	"encoding/xml"
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

const (
	serverRoot     = "https://mikanani.me/"
	loginURL       = serverRoot + "Account/Login"
	expandAPI      = "https://mikanani.me/Home/ExpandEpisodeTable"
	requestTimeout = 30 * time.Second
)

var (
	backgroundImagePattern = regexp.MustCompile(`url\(["']?(.*?)["']?\)`)
	cnWeek                 = map[string]string{
		"星期日": "Sun", "星期一": "Mon", "星期二": "Tue", "星期三": "Wed",
		"星期四": "Thu", "星期五": "Fri", "星期六": "Sat", "OVA": "Unknown",
	}
	dayOfWeek = map[int]string{
		0: "星期日", 1: "星期一", 2: "星期二", 3: "星期三", 4: "星期四",
		5: "星期五", 6: "星期六", 8: "OVA",
	}
)

type Mikanani struct{}

func New() *Mikanani { return &Mikanani{} }

func (m *Mikanani) Name() string { return "mikan_project" }

func normalizeCoverURL(cover string) string {
	base, _ := url.Parse(serverRoot)
	ref, err := url.Parse(cover)
	if err != nil {
		return cover
	}
	resolved := base.ResolveReference(ref).String()
	return strings.Split(resolved, "?")[0]
}

func mikanLogin() error {
	client := httpx.Client()
	resp, err := client.Get(loginURL)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	doc, err := htmlutil.ParseString(string(body))
	if err != nil {
		return err
	}
	tokenNode := htmlutil.Find(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && htmlutil.Attr(n, "name") == "__RequestVerificationToken"
	})
	if tokenNode == nil {
		return fmt.Errorf("mikan login token not found")
	}
	form := url.Values{
		"UserName":                   {config.Cfg.MikanUsername},
		"Password":                   {config.Cfg.MikanPassword},
		"__RequestVerificationToken": {htmlutil.Attr(tokenNode, "value")},
	}
	req, err := http.NewRequest("POST", loginURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", serverRoot)
	resp2, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	text := string(body2)
	if strings.Contains(text, "&#x767B;&#x5F55;&#x5931;&#x8D25;&#xFF0C;&#x8BF7;&#x91CD;&#x8BD5;") || strings.Contains(text, "登录失败，请重试") {
		return fmt.Errorf("mikan login failed with wrong username or password")
	}
	return nil
}

var getText = func(rawURL string, params url.Values) (string, error) {
	client := httpx.Client()
	u := rawURL
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	if config.Cfg.MikanUsername == "" || config.Cfg.MikanPassword == "" {
		resp, err := client.Get(u)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), nil
	}
	for i := 0; i < 2; i++ {
		resp, err := client.Get(u)
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(resp.Body)
		ct := resp.Header.Get("Content-Type")
		resp.Body.Close()
		if strings.HasPrefix(ct, "text/html") {
			if strings.Contains(string(body), "退出") {
				return string(body), nil
			}
			if err := mikanLogin(); err != nil {
				return "", err
			}
			continue
		}
		return string(body), nil
	}
	return "", fmt.Errorf("mikan login failed")
}

type weeklyDay struct {
	updateDay string
	node      *html.Node
}

func getWeeklyBangumi() ([]weeklyDay, error) {
	text, err := getText(serverRoot, nil)
	if err != nil {
		return nil, err
	}
	doc, err := htmlutil.ParseString(text)
	if err != nil {
		return nil, err
	}
	var out []weeklyDay
	for day := 0; day <= 8; day++ {
		if day == 7 {
			continue
		}
		div := htmlutil.Find(doc, func(n *html.Node) bool {
			return n.Type == html.ElementNode && htmlutil.HasClass(n, "sk-bangumi") && htmlutil.Attr(n, "data-dayofweek") == fmt.Sprint(day)
		})
		if div == nil {
			continue
		}
		update, ok := cnWeek[dayOfWeek[day]]
		if !ok {
			continue
		}
		out = append(out, weeklyDay{updateDay: update, node: div})
	}
	return out, nil
}

func parserDayBangumi(day *html.Node) []model.WebsiteBangumi {
	var out []model.WebsiteBangumi
	for _, li := range htmlutil.FindAll(day, htmlutil.Element("li")) {
		a := htmlutil.Find(li, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" && strings.HasPrefix(htmlutil.Attr(n, "href"), "/Home/Bangumi/") && htmlutil.Attr(n, "title") != ""
		})
		span := htmlutil.Find(li, htmlutil.Element("span"))
		if a != nil {
			href := htmlutil.Attr(a, "href")
			parts := strings.Split(strings.TrimRight(href, "/"), "/")
			id := parts[len(parts)-1]
			cover := ""
			if span != nil {
				cover = "https://mikanani.me" + htmlutil.Attr(span, "data-src")
			}
			out = append(out, model.WebsiteBangumi{ID: id, Name: htmlutil.Attr(a, "title"), Cover: cover})
			continue
		}
		poster := htmlutil.Find(li, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "span" && htmlutil.HasClass(n, "greyout") && htmlutil.Attr(n, "data-bangumiid") != "" && htmlutil.Attr(n, "data-src") != ""
		})
		title := htmlutil.Find(li, func(n *html.Node) bool {
			return n.Type == html.ElementNode && htmlutil.HasClass(n, "date-text") && htmlutil.Attr(n, "title") != ""
		})
		if poster != nil && title != nil {
			out = append(out, model.WebsiteBangumi{
				ID:    htmlutil.Attr(poster, "data-bangumiid"),
				Name:  htmlutil.Attr(title, "title"),
				Cover: normalizeCoverURL(htmlutil.Attr(poster, "data-src")),
			})
		}
	}
	return out
}

func parseTime(s string) int64 {
	t, err := time.ParseInLocation("2006/01/02 15:04", s, time.Local)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func parseEpisodes(content string, bangumiID string, subtitleList []string) ([]model.Episode, error) {
	doc, err := htmlutil.ParseString(content)
	if err != nil {
		return nil, err
	}
	container := htmlutil.Find(doc, htmlutil.Tag("div", "central-container"))
	if container == nil {
		match := htmlutil.Find(doc, func(n *html.Node) bool {
			return n.Type == html.ElementNode && htmlutil.HasClass(n, "greyout") && htmlutil.Attr(n, "data-bangumiid") == bangumiID
		})
		if match != nil {
			return []model.Episode{}, nil
		}
		return nil, fmt.Errorf("Central container not found or not a Tag")
	}

	expandSubtitleMap := map[string]bool{}
	type epContainer struct {
		id   string
		node *html.Node
	}
	var containers []epContainer
	for _, tag := range htmlutil.DirectChildren(container) {
		classes := strings.Fields(htmlutil.Attr(tag, "class"))
		for _, c := range classes {
			if c == "episode-expand" {
				expandSubtitleMap[htmlutil.Attr(tag, "data-subtitlegroupid")] = true
			}
		}
		subtitleID := htmlutil.Attr(tag, "id")
		if subtitleID == "" {
			continue
		}
		if len(subtitleList) > 0 && !contains(subtitleList, subtitleID) {
			continue
		}
		next := htmlutil.NextSiblingElement(tag)
		if next != nil && htmlutil.HasClass(next, "episode-table") {
			containers = append(containers, epContainer{id: subtitleID, node: next})
		}
	}

	var result []model.Episode
	for _, c := range containers {
		node := c.node
		if expandSubtitleMap[c.id] {
			params := url.Values{
				"bangumiId":       {bangumiID},
				"subtitleGroupId": {c.id},
				"take":            {"200"},
			}
			expandText, err := getText(expandAPI, params)
			if err != nil {
				return nil, err
			}
			expandDoc, err := htmlutil.ParseString(expandText)
			if err != nil {
				return nil, err
			}
			node = htmlutil.Find(expandDoc, htmlutil.Element("table"))
		}
		if node == nil {
			return nil, fmt.Errorf("Failed to parse bangumi %s subtitle %s", bangumiID, c.id)
		}
		trs := htmlutil.FindAll(node, htmlutil.Element("tr"))
		for i, tr := range trs {
			if i == 0 {
				continue
			}
			titleNode := htmlutil.Find(tr, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "a" && htmlutil.HasClass(n, "magnet-link-wrap")
			})
			if titleNode == nil {
				continue
			}
			tds := htmlutil.FindAll(tr, htmlutil.Element("td"))
			if len(tds) < 4 {
				continue
			}
			magnet := htmlutil.Find(tr, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "a" && htmlutil.HasClass(n, "magnet-link")
			})
			if magnet == nil {
				continue
			}
			title := htmlutil.Text(titleNode)
			result = append(result, model.Episode{
				Download:      htmlutil.Attr(magnet, "data-clipboard-text"),
				SubtitleGroup: c.id,
				Title:         title,
				Episode:       episode.Parse(title),
				Time:          parseTime(htmlutil.TextTrim(tds[3])),
			})
		}
	}
	return result, nil
}

func (m *Mikanani) parseBangumiDetailsPage(content string) (*model.WebsiteBangumi, error) {
	doc, err := htmlutil.ParseString(content)
	if err != nil {
		return nil, err
	}
	left := htmlutil.Find(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "div" && htmlutil.HasClass(n, "pull-left") && htmlutil.HasClass(n, "leftbar-container")
	})
	if left == nil {
		return nil, nil
	}
	title := htmlutil.Find(left, htmlutil.Tag("p", "bangumi-title"))
	if title == nil {
		return nil, nil
	}
	day := htmlutil.NextSiblingElement(title)
	updateDay := ""
	if day != nil {
		text := htmlutil.Text(day)
		if len([]rune(text)) >= 3 {
			updateDay = cnWeek[string([]rune(text)[len([]rune(text))-3:])]
		}
	}
	name := htmlutil.Text(title)
	cover := ""
	poster := htmlutil.Find(left, htmlutil.Tag("div", "bangumi-poster"))
	if poster != nil {
		if mm := backgroundImagePattern.FindStringSubmatch(htmlutil.Attr(poster, "style")); len(mm) > 1 {
			cover = normalizeCoverURL(mm[1])
		}
	}

	container := htmlutil.Find(doc, htmlutil.Tag("div", "central-container"))
	type epContainer struct {
		id   string
		node *html.Node
	}
	var containers []epContainer
	if container != nil {
		for _, tag := range htmlutil.DirectChildren(container) {
			subtitleID := htmlutil.Attr(tag, "id")
			if subtitleID == "" {
				continue
			}
			next := htmlutil.NextSiblingElement(tag)
			if next != nil && htmlutil.HasClass(next, "episode-table") {
				containers = append(containers, epContainer{id: subtitleID, node: next})
			}
		}
	}

	var episodes []model.Episode
	for _, c := range containers {
		trs := htmlutil.FindAll(c.node, htmlutil.Element("tr"))
		for i, tr := range trs {
			if i == 0 {
				continue
			}
			titleNode := htmlutil.Find(tr, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "a" && htmlutil.HasClass(n, "magnet-link-wrap")
			})
			magnet := htmlutil.Find(tr, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "a" && htmlutil.HasClass(n, "magnet-link")
			})
			tds := htmlutil.FindAll(tr, htmlutil.Element("td"))
			if titleNode == nil || magnet == nil || len(tds) < 4 {
				continue
			}
			title := htmlutil.Text(titleNode)
			episodes = append(episodes, model.Episode{
				Download:      htmlutil.Attr(magnet, "data-clipboard-text"),
				SubtitleGroup: c.id,
				Title:         title,
				Episode:       episode.Parse(title),
				Time:          parseTime(htmlutil.TextTrim(tds[3])),
			})
		}
	}

	var groups []model.SubtitleGroup
	nav := htmlutil.Find(doc, htmlutil.Tag("div", "leftbar-nav"))
	if nav != nil {
		for _, li := range htmlutil.FindAll(nav, htmlutil.Element("li")) {
			a := htmlutil.Find(li, htmlutil.Element("a"))
			if a == nil {
				continue
			}
			anchor := htmlutil.Attr(a, "data-anchor")
			id := strings.TrimPrefix(anchor, "#")
			groups = append(groups, model.SubtitleGroup{ID: id, Name: htmlutil.Text(a)})
		}
	}

	return &model.WebsiteBangumi{
		ID:            "",
		UpdateDay:     updateDay,
		Name:          name,
		Status:        0,
		SubtitleGroup: groups,
		Cover:         cover,
		Episodes:      episodes,
	}, nil
}

func (m *Mikanani) FetchBangumiCalendar() ([]model.WebsiteBangumi, error) {
	days, err := getWeeklyBangumi()
	if err != nil {
		return nil, err
	}
	var out []model.WebsiteBangumi
	for _, d := range days {
		for _, b := range parserDayBangumi(d.node) {
			b.UpdateDay = d.updateDay
			b.Cover = strings.Split(b.Cover, "?")[0]
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *Mikanani) FetchEpisodeOfBangumi(bangumiID string, maxPage int, subtitleList []string) ([]model.Episode, error) {
	text, err := getText(serverRoot+"Home/Bangumi/"+bangumiID, nil)
	if err != nil {
		return nil, err
	}
	return parseEpisodes(text, bangumiID, subtitleList)
}

func (m *Mikanani) FetchSingleBangumi(bangumiID string, subtitleList []string, maxPage int) (*model.WebsiteBangumi, error) {
	text, err := getText(serverRoot+"Home/Bangumi/"+bangumiID, nil)
	if err != nil {
		return nil, err
	}
	info, err := m.parseBangumiDetailsPage(text)
	if err != nil || info == nil {
		return info, err
	}
	info.ID = bangumiID
	eps, err := parseEpisodes(text, bangumiID, subtitleList)
	if err != nil {
		return nil, err
	}
	info.Episodes = eps
	return info, nil
}

func updateQuery(rawURL, key, value string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

func (m *Mikanani) SearchByKeyword(keyword string, count int) ([]model.Episode, error) {
	text, err := getText(serverRoot+"Home/Search", url.Values{"searchstr": {keyword}})
	if err != nil {
		return nil, err
	}
	doc, err := htmlutil.ParseString(text)
	if err != nil {
		return nil, err
	}
	var out []model.Episode
	for _, tr := range htmlutil.FindAll(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "tr" && htmlutil.HasClass(n, "js-search-results-row")
	}) {
		titleNode := htmlutil.Find(tr, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" && htmlutil.HasClass(n, "magnet-link-wrap")
		})
		magnet := htmlutil.Find(tr, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" && htmlutil.HasClass(n, "magnet-link")
		})
		tds := htmlutil.FindAll(tr, htmlutil.Element("td"))
		if titleNode == nil || magnet == nil || len(tds) < 4 {
			continue
		}
		title := htmlutil.Text(titleNode)
		out = append(out, model.Episode{
			Download: updateQuery(htmlutil.Attr(magnet, "data-clipboard-text"), "dn", title),
			Name:     keyword,
			Title:    title,
			Episode:  episode.Parse(title),
			Time:     parseTime(htmlutil.TextTrim(tds[3])),
		})
	}
	return out, nil
}

func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			min := prev[j] + 1
			if cur[j-1]+1 < min {
				min = cur[j-1] + 1
			}
			if prev[j-1]+cost < min {
				min = prev[j-1] + cost
			}
			cur[j] = min
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func normalizedDistance(a, b string) float64 {
	maxLen := len([]rune(a))
	if l := len([]rune(b)); l > maxLen {
		maxLen = l
	}
	if maxLen == 0 {
		return 0
	}
	return float64(levenshtein(a, b)) / float64(maxLen)
}

type rssFeed struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title     string `xml:"title"`
	Enclosure struct {
		URL string `xml:"url,attr"`
	} `xml:"enclosure"`
	Torrent struct {
		PubDate string `xml:"https://mikanani.me/0.1/ pubDate"`
	} `xml:"https://mikanani.me/0.1/ torrent"`
}

func (m *Mikanani) SearchByTag(tag string, subtitle string, count int) ([]model.Episode, error) {
	text, err := getText(serverRoot+"Home/Search", url.Values{"searchstr": {tag}})
	if err != nil {
		return nil, err
	}
	doc, err := htmlutil.ParseString(text)
	if err != nil {
		return nil, err
	}
	animate := htmlutil.Find(doc, htmlutil.Tag("div", "an-info-group"))
	if animate == nil {
		return nil, fmt.Errorf("Animate [div.an-info-group] not found or not a Tag")
	}
	animateName := strings.TrimSpace(htmlutil.Text(animate))
	linkElement := climbToLink(animate)
	if linkElement == nil {
		return nil, fmt.Errorf("Animate link not found or not a Tag")
	}
	animateLink := htmlutil.Attr(linkElement, "href")
	parts := strings.Split(strings.TrimRight(animateLink, "/"), "/")
	animateID := parts[len(parts)-1]
	logger.Info("Matched animate: %s (%s)", animateName, animateID)

	detailText, err := getText(serverRoot+strings.TrimPrefix(animateLink, "/"), nil)
	if err != nil {
		return nil, err
	}
	detailDoc, err := htmlutil.ParseString(detailText)
	if err != nil {
		return nil, err
	}

	lowestDistance := 1.0
	var bestNode *html.Node
	var bestNames, bestLinks []string
	var bestInfo map[string]string

	for _, subgroup := range htmlutil.FindAll(detailDoc, htmlutil.Tag("div", "subgroup-text")) {
		var names, links []string
		info := map[string]string{}
		for _, a := range htmlutil.FindAll(subgroup, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a" && htmlutil.Attr(n, "href") != ""
		}) {
			link := htmlutil.Attr(a, "href")
			if strings.HasPrefix(link, "/Home/PublishGroup/") {
				names = append(names, htmlutil.Text(a))
				links = append(links, strings.TrimPrefix(link, "/Home/PublishGroup/"))
			}
			if strings.HasPrefix(link, "/RSS/Bangumi") {
				req := link[len("/RSS/Bangumi")+1:]
				for _, pair := range strings.Split(req, "&") {
					kv := strings.SplitN(pair, "=", 2)
					if len(kv) == 2 {
						info[kv[0]] = kv[1]
					}
				}
			}
		}
		if len(names) == 0 {
			continue
		}
		if subtitle != "" {
			cmp := strings.Join(names, " ")
			distance := normalizedDistance(cmp, subtitle)
			if distance < lowestDistance {
				lowestDistance = distance
				bestNode, bestNames, bestLinks, bestInfo = subgroup, names, links, info
			}
		} else {
			bestNode, bestNames, bestLinks, bestInfo = subgroup, names, links, info
			break
		}
	}
	_ = bestNode
	_ = bestLinks
	if bestInfo == nil {
		return []model.Episode{}, nil
	}
	bangumiID, subgroupID := bestInfo["bangumiId"], bestInfo["subgroupid"]
	if bangumiID == "" || subgroupID == "" {
		return []model.Episode{}, nil
	}
	rssURL := fmt.Sprintf("%sRSS/Bangumi?bangumiId=%s&subgroupid=%s", serverRoot, bangumiID, subgroupID)
	subtitleGroup := strings.Join(bestNames, " ")
	if subtitle != "" {
		logger.Info("Matched subtitle: %s (%s)", subtitleGroup, subgroupID)
	} else {
		logger.Info("Use first subtitle: %s (%s)", subtitleGroup, subgroupID)
	}

	rssText, err := getText(rssURL, nil)
	if err != nil {
		return nil, err
	}
	var feed rssFeed
	if err := xml.Unmarshal([]byte(rssText), &feed); err != nil {
		return nil, err
	}
	var out []model.Episode
	for _, item := range feed.Channel.Items {
		link := item.Enclosure.URL
		title := item.Title
		pubDate := strings.Split(item.Torrent.PubDate, ".")[0]
		if link == "" || title == "" || pubDate == "" {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02T15:04:05", pubDate, time.Local)
		if err != nil {
			continue
		}
		out = append(out, model.Episode{
			Download:      updateQuery(link, "dn", title),
			Name:          animateName,
			Title:         title,
			SubtitleGroup: subtitleGroup,
			Episode:       episode.Parse(title),
			Time:          t.Unix(),
		})
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func climbToLink(n *html.Node) *html.Node {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Type == html.ElementNode && cur.Data == "a" && htmlutil.Attr(cur, "href") != "" {
			return cur
		}
	}
	return nil
}

var _ = requestTimeout
