package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/bgmtv"
	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/db"
)

func commentAvatarURL(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	var images bgmtv.Images
	if err := json.Unmarshal(raw, &images); err == nil {
		return firstCover(images)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, key := range []string{"large", "medium", "small", "common", "grid", "url"} {
			if v, ok := obj[key].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

const (
	detailCacheTTL     = 24 * time.Hour
	detailCacheVersion = 2
)

func handleKazumiLookup(w http.ResponseWriter, r *http.Request) {
	if err := ensureDB(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Fast local-only mode: resolve the subject id from local links and serve
	// cached metadata without any network access.
	localOnly := r.URL.Query().Get("local") == "1"
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	rawID := strings.TrimSpace(r.URL.Query().Get("subject_id"))
	subjectID := 0
	if rawID != "" {
		subjectID, _ = strconv.Atoi(rawID)
	}
	matchedBy := ""
	if subjectID <= 0 && name != "" {
		if id, err := db.Conn.GetBangumiLink(name); err == nil {
			subjectID = id
			matchedBy = "link"
		} else if manual, err := db.Conn.GetManualBangumi(name); err == nil && manual.Provider == "bgmtv" {
			if id, err := strconv.Atoi(manual.ProviderID); err == nil {
				subjectID = id
				matchedBy = "manual"
			}
		} else if id := findTimelineSubject(name); id > 0 {
			subjectID = id
			matchedBy = "timeline"
			_ = db.Conn.SaveBangumiLink(name, id)
		}
	}
	if subjectID <= 0 {
		writeError(w, http.StatusNotFound, "未找到对应的 Bangumi 条目，请手动关联 bgm.tv 条目 ID")
		return
	}
	if localOnly {
		subject, ok := cachedSubjectOverview(subjectID)
		if !ok {
			writeError(w, http.StatusNotFound, "本地暂无该条目缓存")
			return
		}
		subject = enrichSubjectPayload(subjectID, subject)
		writeJSON(w, http.StatusOK, map[string]any{
			"subject_id": subjectID,
			"subject":    subject,
			"matched_by": matchedBy,
			"cached":     true,
		})
		return
	}
	subject, err := loadSubjectOverview(subjectID, false)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	subject = enrichSubjectPayload(subjectID, subject)
	writeJSON(w, http.StatusOK, map[string]any{
		"subject_id": subjectID,
		"subject":    subject,
		"matched_by": matchedBy,
	})
}

func findTimelineSubject(name string) int {
	normalized := normalizeSubjectName(name)
	if normalized == "" {
		return 0
	}
	days, err := cachedKazumiTimeline(false)
	if err != nil {
		return 0
	}
	for _, day := range days {
		for _, item := range day.Items {
			if normalizeSubjectName(item.Name) == normalized || normalizeSubjectName(item.NameCN) == normalized {
				return item.ID
			}
		}
	}
	return 0
}

func normalizeSubjectName(value string) string {
	var builder strings.Builder
	for _, ch := range strings.ToLower(value) {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || (ch >= 0x4e00 && ch <= 0x9fff) {
			builder.WriteRune(ch)
		}
	}
	return builder.String()
}

func loadSubjectOverview(subjectID int, force bool) (map[string]any, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	cache, _ := db.Conn.GetBangumiCache(int64(subjectID))
	if !force && cache != nil && cache.Overview != "" {
		var payload map[string]any
		if json.Unmarshal([]byte(cache.Overview), &payload) == nil {
			// 已缓存时只加载本地缓存的。
			if localized, ok := controllers.LocalizePayloadImages(payload).(map[string]any); ok {
				payload = localized
			}
			return payload, nil
		}
	}
	subject, err := bgmtv.FetchSubject(subjectID)
	if err != nil {
		if cache != nil && cache.Overview != "" {
			var payload map[string]any
			if json.Unmarshal([]byte(cache.Overview), &payload) == nil {
				if localized, ok := controllers.LocalizePayloadImages(payload).(map[string]any); ok {
					payload = localized
				}
				return payload, nil
			}
		}
		return nil, err
	}
	payload := subjectPayload(subject, true)
	if localized, ok := controllers.CachePayloadImages(payload).(map[string]any); ok {
		payload = localized
	}
	payload["cache_version"] = detailCacheVersion
	if data, err := json.Marshal(payload); err == nil {
		if cache == nil {
			cache = &db.BangumiCache{SubjectID: int64(subjectID)}
		}
		cache.Overview = string(data)
		_ = db.Conn.SaveBangumiCache(cache)
	}
	return payload, nil
}

// cachedSubjectOverview returns the locally cached overview without any
// network access. It is used for fast previous/next navigation.
func cachedSubjectOverview(subjectID int) (map[string]any, bool) {
	if err := ensureDB(); err != nil {
		return nil, false
	}
	cache, err := db.Conn.GetBangumiCache(int64(subjectID))
	if err != nil || cache == nil || cache.Overview == "" {
		return nil, false
	}
	var payload map[string]any
	if json.Unmarshal([]byte(cache.Overview), &payload) != nil {
		return nil, false
	}
	if localized, ok := controllers.LocalizePayloadImages(payload).(map[string]any); ok {
		payload = localized
	}
	return payload, true
}

func cachedSectionPayload(subjectID int, section string) (any, bool) {
	if err := ensureDB(); err != nil {
		return nil, false
	}
	cache, err := db.Conn.GetBangumiCache(int64(subjectID))
	if err != nil || cache == nil {
		return nil, false
	}
	raw := cacheSection(cache, section)
	if raw == "" {
		return nil, false
	}
	var payload any
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return nil, false
	}
	payload = controllers.LocalizePayloadImages(payload)
	if section == "comments" {
		payload = normalizeCommentsPayload(payload)
	}
	return payload, true
}

func payloadHasComments(payload any) bool {
	normalized, ok := normalizeCommentsPayload(payload).(map[string]any)
	if !ok {
		return false
	}
	items, _ := normalized["items"].([]any)
	total, _ := normalized["total"].(int)
	return len(items) > 0 || total > 0
}

// normalizeCommentsPayload makes cached comments readable across older cache
// shapes (bare arrays, list/data envelopes) so the 吐槽 tab keeps working
// after upgrades.
func normalizeCommentsPayload(payload any) any {
	items := []any{}
	total := 0
	switch value := payload.(type) {
	case []any:
		items = value
		total = len(value)
	case []map[string]any:
		for _, item := range value {
			items = append(items, item)
		}
		total = len(value)
	case map[string]any:
		for _, key := range []string{"items", "list", "data", "comments"} {
			switch list := value[key].(type) {
			case []any:
				items = list
			case []map[string]any:
				for _, item := range list {
					items = append(items, item)
				}
			}
			if len(items) > 0 {
				break
			}
		}
		if number, ok := value["total"].(float64); ok {
			total = int(number)
		} else if number, ok := value["total"].(int); ok {
			total = number
		}
	}
	if total == 0 {
		total = len(items)
	}
	out := make([]any, 0, len(items))
	for _, entry := range items {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if text, _ := item["comment"].(string); strings.TrimSpace(text) == "" {
			if content, ok := item["content"].(string); ok && content != "" {
				item["comment"] = content
			}
		}
		if number, ok := item["updated_at"].(float64); !ok || number <= 0 {
			for _, key := range []string{"updatedAt", "updated", "createdAt", "created_at"} {
				if number, ok := item[key].(float64); ok && number > 0 {
					item["updated_at"] = number
					break
				}
			}
		}
		if user, ok := item["user"].(map[string]any); ok {
			if cover, _ := user["cover"].(string); strings.TrimSpace(cover) == "" {
				for _, key := range []string{"avatar", "avatar_url"} {
					if value, ok := user[key].(string); ok && value != "" {
						user["cover"] = value
						break
					}
					if avatar, ok := user[key].(map[string]any); ok {
						for _, size := range []string{"large", "medium", "small", "common", "grid"} {
							if value, ok := avatar[size].(string); ok && value != "" {
								user["cover"] = value
								break
							}
						}
					}
				}
			}
		}
		out = append(out, item)
	}
	return map[string]any{"items": out, "total": total}
}

func handleKazumiCharacters(w http.ResponseWriter, r *http.Request, rawID string) {
	subjectID, ok := parseSubjectIDParam(w, rawID)
	if !ok {
		return
	}
	if r.URL.Query().Get("local") == "1" {
		if payload, ok := cachedSectionPayload(subjectID, "characters"); ok {
			writeJSON(w, http.StatusOK, map[string]any{"items": payload, "cached": true})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "cached": false})
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	payload, err := loadCachedSection(subjectID, "characters", refresh, func() (any, error) {
		items, err := bgmtv.FetchCharacters(subjectID)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			actors := make([]map[string]any, 0, len(item.Actors))
			for _, actor := range item.Actors {
				actors = append(actors, map[string]any{
					"id": actor.ID, "name": actor.Name, "cover": firstCover(actor.Images),
				})
			}
			out = append(out, map[string]any{
				"id": item.ID, "name": item.Name, "relation": item.Relation,
				"cover": firstCover(item.Images), "actors": actors,
			})
		}
		return out, nil
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": payload})
}

func handleKazumiStaff(w http.ResponseWriter, r *http.Request, rawID string) {
	subjectID, ok := parseSubjectIDParam(w, rawID)
	if !ok {
		return
	}
	if r.URL.Query().Get("local") == "1" {
		if payload, ok := cachedSectionPayload(subjectID, "staff"); ok {
			writeJSON(w, http.StatusOK, map[string]any{"items": payload, "cached": true})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "cached": false})
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	payload, err := loadCachedSection(subjectID, "staff", refresh, func() (any, error) {
		items, err := bgmtv.FetchStaff(subjectID)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			out = append(out, map[string]any{
				"id": item.ID, "name": item.Name, "relation": item.Relation,
				"eps": item.Eps, "career": nonNilStrings(item.Career), "cover": firstCover(item.Images),
			})
		}
		return out, nil
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": payload})
}

func handleKazumiRelations(w http.ResponseWriter, r *http.Request, rawID string) {
	subjectID, ok := parseSubjectIDParam(w, rawID)
	if !ok {
		return
	}
	if r.URL.Query().Get("local") == "1" {
		if payload, ok := cachedSectionPayload(subjectID, "relations"); ok {
			writeJSON(w, http.StatusOK, map[string]any{"items": payload, "cached": true})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "cached": false})
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	payload, err := loadCachedSection(subjectID, "relations", refresh, func() (any, error) {
		items, err := bgmtv.FetchRelations(subjectID)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			out = append(out, map[string]any{
				"id": item.ID, "name": item.Name, "name_cn": item.NameCN,
				"relation": item.Relation, "type": item.Type, "cover": firstCover(item.Images),
			})
		}
		return out, nil
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": payload})
}

func handleKazumiComments(w http.ResponseWriter, r *http.Request, rawID string) {
	subjectID, ok := parseSubjectIDParam(w, rawID)
	if !ok {
		return
	}
	if r.URL.Query().Get("local") == "1" {
		if payload, ok := cachedSectionPayload(subjectID, "comments"); ok {
			writeJSON(w, http.StatusOK, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0, "cached": false})
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	payload, err := loadCachedSection(subjectID, "comments", refresh, func() (any, error) {
		page, err := bgmtv.FetchComments(subjectID, 0, 20)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(page.List))
		for _, item := range page.List {
			out = append(out, map[string]any{
				"user": map[string]any{
					"id": item.User.ID, "username": item.User.Username,
					"nickname": item.User.Nickname, "cover": commentAvatarURL(item.User.Avatar),
				},
				"rate": item.Rate, "comment": item.Comment, "updated_at": item.UpdatedAt,
			})
		}
		return map[string]any{"items": out, "total": page.Total}, nil
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, normalizeCommentsPayload(payload))
}

func enrichSubjectPayload(subjectID int, payload map[string]any) map[string]any {
	if payload == nil {
		payload = map[string]any{}
	}
	if err := ensureDB(); err != nil {
		return payload
	}
	item, itemErr := db.Conn.FindFollowedBySubjectID(subjectID)
	if itemErr != nil {
		// Old subscriptions added before the link table existed may only match
		// by their exact display name.
		for _, key := range []string{"name_cn", "name"} {
			name, _ := payload[key].(string)
			if name == "" {
				continue
			}
			if followed, err := db.Conn.GetFollowedNotDeleted(name); err == nil {
				if bangumi, err := db.Conn.GetBangumiByName(name); err == nil {
					item = &db.FollowedBangumi{Followed: *followed, Bangumi: *bangumi}
					itemErr = nil
					_ = db.Conn.SaveBangumiLink(name, subjectID)
					break
				}
			}
		}
	}
	if itemErr == nil && item != nil {
		subs, _ := db.Conn.GetSubtitlesByIDs(item.Bangumi.SubtitleGroup)
		names := make([]string, 0, len(subs))
		for _, sub := range subs {
			names = append(names, sub.Name)
		}
		aliases := []string{}
		if manual, err := db.Conn.GetManualBangumi(item.Followed.BangumiName); err == nil {
			aliases = append(aliases, manual.SearchNames...)
		}
		customAliases, _ := db.Conn.ListCustomAliases(item.Followed.BangumiName)
		aliases = append(aliases, customAliases...)
		kazumiEpisodes, _ := kazumiDownloadInfoForName(item.Followed.BangumiName)
		episodeSet := map[int]bool{}
		for ep := range item.Followed.Episodes {
			episodeSet[ep] = true
		}
		for _, ep := range kazumiEpisodes {
			episodeSet[ep] = true
		}
		episodes := make([]int, 0, len(episodeSet))
		for ep := range episodeSet {
			episodes = append(episodes, ep)
		}
		sort.Ints(episodes)
		player := mergedPlayer(item.Followed.BangumiName, episodes, item.Followed.Season, item.Followed.EpisodeOffset, item.Followed.DisplayName)
		payload["subscription"] = map[string]any{
			"bangumi_name":    item.Followed.BangumiName,
			"episodes":        episodes,
			"kazumi_episodes": kazumiEpisodes,
			"downloaded":      len(kazumiEpisodes) > 0,
			"player":          player,
			"display_name":    item.Followed.DisplayName,
			"episode":         item.Followed.Episode(),
			"update_day":      item.Bangumi.UpdateDay,
			"cover":           localCoverPath(item.Bangumi.Cover),
			"status":          item.Followed.Status,
			"subtitle_group":  names,
			"aliases":         aliases,
			"custom_aliases":  customAliases,
		}
	}
	if value, cached, _ := controllers.GetBangumiCollection(subjectID); cached && value > 0 {
		payload["collection_type"] = value
	}
	return payload
}

func parseSubjectIDParam(w http.ResponseWriter, raw string) (int, bool) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid subject id")
		return 0, false
	}
	return id, true
}

func loadCachedSection(subjectID int, section string, force bool, fetch func() (any, error)) (any, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	cache, _ := db.Conn.GetBangumiCache(int64(subjectID))
	cached := cacheSection(cache, section)
	if !force && cached != "" {
		var payload any
		if json.Unmarshal([]byte(cached), &payload) == nil {
			// 已缓存时只加载本地缓存的。空吐槽缓存可能来自不支持评论接口的
			// 镜像，视为未命中并重新获取，避免一直显示“还没有吐槽”。
			if section != "comments" || payloadHasComments(payload) {
				return controllers.LocalizePayloadImages(payload), nil
			}
		}
	}
	payload, err := fetch()
	if err != nil {
		if cached != "" {
			var fallback any
			if json.Unmarshal([]byte(cached), &fallback) == nil {
				return controllers.LocalizePayloadImages(fallback), nil
			}
		}
		return nil, err
	}
	// 更新缓存时包含封面/概述/吐槽/角色/关联/制作人员等全部文字信息；仅封面图片下载到本地，
	// 其他图片仅在本地已存在时复用，避免缓存更新过慢。
	payload = controllers.LocalizePayloadImages(payload)
	if data, err := json.Marshal(payload); err == nil {
		if cache == nil {
			cache = &db.BangumiCache{SubjectID: int64(subjectID)}
		}
		setCacheSection(cache, section, string(data))
		_ = db.Conn.SaveBangumiCache(cache)
	}
	return payload, nil
}

func cacheSubjectDetails(subjectID int) error {
	if _, err := loadSubjectOverview(subjectID, true); err != nil {
		return err
	}
	if _, err := loadCachedSection(subjectID, "characters", true, func() (any, error) {
		items, err := bgmtv.FetchCharacters(subjectID)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			actors := make([]map[string]any, 0, len(item.Actors))
			for _, actor := range item.Actors {
				actors = append(actors, map[string]any{"id": actor.ID, "name": actor.Name, "cover": firstCover(actor.Images)})
			}
			out = append(out, map[string]any{"id": item.ID, "name": item.Name, "relation": item.Relation, "cover": firstCover(item.Images), "actors": actors})
		}
		return out, nil
	}); err != nil {
		return err
	}
	if _, err := loadCachedSection(subjectID, "relations", true, func() (any, error) {
		items, err := bgmtv.FetchRelations(subjectID)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			out = append(out, map[string]any{"id": item.ID, "name": item.Name, "name_cn": item.NameCN, "relation": item.Relation, "type": item.Type, "cover": firstCover(item.Images)})
		}
		return out, nil
	}); err != nil {
		return err
	}
	if _, err := loadCachedSection(subjectID, "staff", true, func() (any, error) {
		items, err := bgmtv.FetchStaff(subjectID)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			out = append(out, map[string]any{"id": item.ID, "name": item.Name, "relation": item.Relation, "eps": item.Eps, "career": nonNilStrings(item.Career), "cover": firstCover(item.Images)})
		}
		return out, nil
	}); err != nil {
		return err
	}
	_, _ = loadCachedSection(subjectID, "comments", true, func() (any, error) {
		page, err := bgmtv.FetchComments(subjectID, 0, 20)
		if err != nil {
			return nil, err
		}
		// Do not overwrite a usable cache with an empty response from a
		// mirror that does not implement the comments endpoint.
		if len(page.List) == 0 && page.Total == 0 {
			return nil, fmt.Errorf("comments endpoint returned an empty page")
		}
		out := make([]map[string]any, 0, len(page.List))
		for _, item := range page.List {
			out = append(out, map[string]any{
				"user":       map[string]any{"id": item.User.ID, "username": item.User.Username, "nickname": item.User.Nickname, "cover": commentAvatarURL(item.User.Avatar)},
				"rate":       item.Rate,
				"comment":    item.Comment,
				"updated_at": item.UpdatedAt,
			})
		}
		return map[string]any{"items": out, "total": page.Total}, nil
	})
	return nil
}

func cacheSection(cache *db.BangumiCache, section string) string {
	if cache == nil {
		return ""
	}
	switch section {
	case "characters":
		return cache.Characters
	case "staff":
		return cache.Staff
	case "relations":
		return cache.Relations
	case "comments":
		return cache.Comments
	default:
		return ""
	}
}

func setCacheSection(cache *db.BangumiCache, section, value string) {
	switch section {
	case "characters":
		cache.Characters = value
	case "staff":
		cache.Staff = value
	case "relations":
		cache.Relations = value
	case "comments":
		cache.Comments = value
	}
}

func adminBangumiLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Subject string `json:"subject"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	subjectID, err := controllers.LinkBangumiSubject(body.Name, body.Subject)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	controllers.RepairKazumiDownloadLinks()
	writeJSON(w, http.StatusOK, map[string]any{"name": body.Name, "subject_id": subjectID})
}

func adminBangumiCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/admin/bangumi/collection/"))
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid subject id")
			return
		}
		value, cached, err := controllers.GetBangumiCollection(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subject_id": id, "type": value, "cached": cached})
		return
	}
	var body struct {
		SubjectID int `json:"subject_id"`
		Type      int `json:"type"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := controllers.SetBangumiCollection(body.SubjectID, body.Type); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subject_id": body.SubjectID, "type": body.Type})
}

func handleKazumiSubjectRoute(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 1 {
		handleKazumiSubject(w, r, parts[0])
		return
	}
	switch parts[1] {
	case "characters":
		handleKazumiCharacters(w, r, parts[0])
	case "staff":
		handleKazumiStaff(w, r, parts[0])
	case "relations":
		handleKazumiRelations(w, r, parts[0])
	case "comments":
		handleKazumiComments(w, r, parts[0])
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}
