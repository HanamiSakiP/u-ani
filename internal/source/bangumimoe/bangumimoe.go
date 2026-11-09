package bangumimoe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/episode"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/model"
)

const bangumiTag = "549ef207fe682f7549f1ea90"

var availableLang = []string{"zh_cn", "zh_tw", "en", "ja"}

type BangumiMoe struct{}

func New() *BangumiMoe { return &BangumiMoe{} }

func (b *BangumiMoe) Name() string { return "bangumi_moe" }

func baseURL() string {
	return strings.TrimRight(config.Cfg.BangumiMoeURL, "/")
}

var fetchJSON = func(url, method string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpx.Client().Do(req)
	if err != nil {
		logger.Error("error: failed to establish a new connection")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		logger.Error("error: server returned data maybe not be json, please check your network or data source settings")
		return err
	}
	return nil
}

func getResponse(url, method string, body any) (map[string]any, error) {
	var out map[string]any
	if err := fetchJSON(url, method, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type bangumiData struct {
	TagID  string `json:"tag_id"`
	Cover  string `json:"cover"`
	ShowOn int    `json:"showOn"`
	Name   string `json:"name"`
}

type teamEntry struct {
	TagID string `json:"tag_id"`
	Name  string `json:"name"`
}

type tagItem struct {
	ID     string         `json:"_id"`
	Locale map[string]any `json:"locale"`
	Name   string         `json:"name"`
	Type   string         `json:"type"`
}

func processName(data []tagItem) map[string]string {
	lang := config.Cfg.Lang
	found := false
	for _, l := range availableLang {
		if l == lang {
			found = true
			break
		}
	}
	if !found {
		lang = "zh_cn"
	}
	out := map[string]string{}
	for _, item := range data {
		name := ""
		if v, ok := item.Locale[lang].(string); ok {
			name = v
		}
		if name == "" {
			for _, l := range availableLang {
				if v, ok := item.Locale[l].(string); ok && v != "" {
					name = v
					break
				}
			}
		}
		if name == "" {
			name = item.Name
		}
		out[item.ID] = name
	}
	return out
}

func processSubtitle(data []teamEntry) map[string]string {
	out := map[string]string{}
	for _, item := range data {
		out[item.TagID] = item.Name
	}
	return out
}

func parserBangumi(data []bangumiData) ([]model.WebsiteBangumi, error) {
	ids := make([]string, 0, len(data))
	for _, b := range data {
		ids = append(ids, b.TagID)
	}
	var teamMap map[string][]teamEntry
	if err := fetchJSON(baseURL()+"/api/team/working", "POST", map[string]any{"tag_ids": ids}, &teamMap); err != nil {
		return nil, err
	}
	var nameItems []tagItem
	if err := fetchJSON(baseURL()+"/api/tag/fetch", "POST", map[string]any{"_ids": ids}, &nameItems); err != nil {
		return nil, err
	}
	names := processName(nameItems)

	knownDays := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	var out []model.WebsiteBangumi
	for _, item := range data {
		subtitleOfBangumi := processSubtitle(teamMap[item.TagID])
		groups := make([]model.SubtitleGroup, 0, len(subtitleOfBangumi))
		for id, name := range subtitleOfBangumi {
			groups = append(groups, model.SubtitleGroup{ID: id, Name: name})
		}
		name := names[item.TagID]
		if name == "" {
			name = item.Name
		}
		out = append(out, model.WebsiteBangumi{
			ID:            item.TagID,
			Name:          name,
			Status:        0,
			SubtitleGroup: groups,
			UpdateDay:     knownDays[(item.ShowOn+7)%7],
			Cover:         "https://bangumi.moe/" + strings.TrimPrefix(item.Cover, "/"),
		})
	}
	if len(out) == 0 {
		logger.Error("It seems that no bangumi found, if https://bangumi.moe can be opened normally, please check your network or data source settings")
	}
	return out, nil
}

func parsePublishTime(s string) int64 {
	s = strings.Split(s, ".")[0]
	t, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.Local)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func (b *BangumiMoe) FetchBangumiCalendar() ([]model.WebsiteBangumi, error) {
	var data []bangumiData
	if err := fetchJSON(baseURL()+"/api/bangumi/current", "GET", nil, &data); err != nil {
		return nil, err
	}
	return parserBangumi(data)
}

func (b *BangumiMoe) FetchEpisodeOfBangumi(bangumiID string, maxPage int, subtitleList []string) ([]model.Episode, error) {
	var torrents []map[string]any
	if len(subtitleList) > 0 {
		for _, subtitleID := range subtitleList {
			resp, err := getResponse(baseURL()+"/api/torrent/search", "POST", map[string]any{
				"tag_id": []string{bangumiID, subtitleID, bangumiTag},
			})
			if err != nil {
				return nil, err
			}
			items, _ := resp["torrents"].([]any)
			for _, item := range items {
				if obj, ok := item.(map[string]any); ok {
					torrents = append(torrents, obj)
				}
			}
		}
	} else {
		for i := 0; i < maxPage; i++ {
			if maxPage > 1 {
				logger.Info("Fetch page %d ...", i+1)
			}
			resp, err := getResponse(baseURL()+"/api/torrent/search", "POST", map[string]any{
				"tag_id": []string{bangumiID, bangumiTag},
				"p":      i + 1,
			})
			if err != nil {
				return nil, err
			}
			items, _ := resp["torrents"].([]any)
			for _, item := range items {
				if obj, ok := item.(map[string]any); ok {
					torrents = append(torrents, obj)
				}
			}
		}
	}
	return b.torrentsToEpisodes(torrents), nil
}

func (b *BangumiMoe) torrentsToEpisodes(torrents []map[string]any) []model.Episode {
	out := make([]model.Episode, 0, len(torrents))
	for _, t := range torrents {
		id, _ := t["_id"].(string)
		teamID, _ := t["team_id"].(string)
		title, _ := t["title"].(string)
		publish, _ := t["publish_time"].(string)
		out = append(out, model.Episode{
			Download:      baseURL() + "/download/torrent/" + id + "/download.torrent",
			SubtitleGroup: teamID,
			Title:         title,
			Episode:       episode.Parse(title),
			Time:          parsePublishTime(publish),
		})
	}
	return out
}

func (b *BangumiMoe) processSearchResult(keyword string, rows []map[string]any) []model.Episode {
	out := make([]model.Episode, 0, len(rows))
	for _, t := range rows {
		id, _ := t["_id"].(string)
		teamID, _ := t["team_id"].(string)
		title, _ := t["title"].(string)
		publish, _ := t["publish_time"].(string)
		out = append(out, model.Episode{
			Download:      baseURL() + "/download/torrent/" + id + "/download.torrent",
			Name:          keyword,
			SubtitleGroup: teamID,
			Title:         title,
			Episode:       episode.Parse(title),
			Time:          parsePublishTime(publish),
		})
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (b *BangumiMoe) queryTag(query, tagType string) (id, name string, err error) {
	resp, err := getResponse(baseURL()+"/api/tag/search", "POST", map[string]any{
		"name": query, "keywords": true, "multi": true,
	})
	if err != nil {
		return "", "", err
	}
	success, _ := resp["success"].(bool)
	found, _ := resp["found"].(bool)
	if !success || !found {
		return "", "", fmt.Errorf("Search tag failed, keyword: %s", query)
	}
	var tags []map[string]any
	switch v := resp["tag"].(type) {
	case []any:
		for _, item := range v {
			if obj, ok := item.(map[string]any); ok {
				tags = append(tags, obj)
			}
		}
	case map[string]any:
		tags = append(tags, v)
	}
	if tagType != "" {
		filtered := tags[:0]
		for _, t := range tags {
			if typ, _ := t["type"].(string); typ == tagType {
				filtered = append(filtered, t)
			}
		}
		tags = filtered
	}
	if len(tags) == 0 {
		return "", "", fmt.Errorf("No tag of type '%s' found for: %s", tagType, query)
	}
	id, _ = tags[0]["_id"].(string)
	name, _ = tags[0]["name"].(string)
	return id, name, nil
}

func (b *BangumiMoe) SearchByTag(tag, subtitle string, count int) ([]model.Episode, error) {
	if count <= 0 {
		count = 3
	}
	animeID, animeName, err := b.queryTag(tag, "bangumi")
	if err != nil {
		return nil, err
	}
	logger.Info("Matched anime: %s (%s)", animeName, animeID)

	tagIDs := []string{animeID, bangumiTag}
	if subtitle != "" {
		subtitleID, subtitleName, err := b.queryTag(subtitle, "team")
		if err != nil {
			return nil, err
		}
		logger.Info("Matched subtitle: %s (%s)", subtitleName, subtitleID)
		tagIDs = append(tagIDs, subtitleID)
	}

	var rows []map[string]any
	for i := 0; i < count; i++ {
		resp, err := getResponse(baseURL()+"/api/torrent/search", "POST", map[string]any{
			"tag_id": tagIDs, "p": i + 1,
		})
		if err != nil {
			return nil, err
		}
		items, ok := resp["torrents"].([]any)
		if !ok {
			logger.Warning("No torrents in response data, please re-run")
			return nil, nil
		}
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				rows = append(rows, obj)
			}
		}
		if pc, ok := resp["page_count"].(float64); ok && int(pc)-1 == i {
			break
		}
	}
	return b.processSearchResult(animeName, rows), nil
}

func (b *BangumiMoe) SearchByKeyword(keyword string, count int) ([]model.Episode, error) {
	if count <= 0 {
		count = 3
	}
	var rows []map[string]any
	for i := 0; i < count; i++ {
		resp, err := getResponse(baseURL()+"/api/v2/torrent/search", "POST", map[string]any{
			"query": keyword, "p": i + 1,
		})
		if err != nil {
			return nil, err
		}
		items, ok := resp["torrents"].([]any)
		if !ok {
			logger.Warning("No torrents in response data, please re-run")
			return nil, nil
		}
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				rows = append(rows, obj)
			}
		}
	}
	if len(rows) == 0 {
		return b.searchByKeywordViaTag(keyword, count)
	}
	return b.processSearchResult(keyword, rows), nil
}

func (b *BangumiMoe) searchByKeywordViaTag(keyword string, count int) ([]model.Episode, error) {
	resp, err := getResponse(baseURL()+"/api/tag/search", "POST", map[string]any{
		"name": keyword, "keywords": true, "multi": true,
	})
	if err != nil {
		return nil, err
	}
	success, _ := resp["success"].(bool)
	found, _ := resp["found"].(bool)
	if !success || !found {
		return nil, nil
	}
	var tags []map[string]any
	switch v := resp["tag"].(type) {
	case []any:
		for _, item := range v {
			if obj, ok := item.(map[string]any); ok {
				tags = append(tags, obj)
			}
		}
	case map[string]any:
		tags = append(tags, v)
	}
	var animeTags []map[string]any
	for _, t := range tags {
		if typ, _ := t["type"].(string); typ == "bangumi" {
			animeTags = append(animeTags, t)
		}
	}
	if len(animeTags) == 0 {
		return nil, nil
	}
	tagID, _ := animeTags[0]["_id"].(string)
	tagName, _ := animeTags[0]["name"].(string)
	var rows []map[string]any
	for i := 0; i < count; i++ {
		resp, err := getResponse(baseURL()+"/api/torrent/search", "POST", map[string]any{
			"tag_id": []string{tagID, bangumiTag}, "p": i + 1,
		})
		if err != nil {
			return nil, err
		}
		items, ok := resp["torrents"].([]any)
		if !ok {
			return nil, nil
		}
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				rows = append(rows, obj)
			}
		}
		if pc, ok := resp["page_count"].(float64); ok && int(pc)-1 == i {
			break
		}
	}
	return b.processSearchResult(tagName, rows), nil
}

func (b *BangumiMoe) FetchSingleBangumi(bangumiID string, subtitleList []string, maxPage int) (*model.WebsiteBangumi, error) {
	return nil, nil
}
