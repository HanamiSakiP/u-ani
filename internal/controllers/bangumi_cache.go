package controllers

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/bgmtv"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/kazumi"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

// BangumiCacheSummary describes a full follow-list metadata refresh.
type BangumiCacheSummary struct {
	Total   int `json:"total"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

// CacheFollowedBangumi downloads covers and complete Bangumi metadata
// (overview, characters, staff, relations, comments) for every followed
// bangumi. When refresh is false, already cached subjects are skipped.
func CacheFollowedBangumi(refresh bool) (BangumiCacheSummary, error) {
	summary := BangumiCacheSummary{}
	if err := ensureDB(); err != nil {
		return summary, err
	}
	followed, err := db.Conn.AllFollowed()
	if err != nil {
		return summary, err
	}
	commentsAvailable := true
	calendarNames := map[string]int{}
	if days, err := bgmtv.FetchCalendar(); err == nil {
		for _, day := range days {
			for _, item := range day.Items {
				for _, name := range []string{item.Name, item.NameCN} {
					if key := normalizeForMatch(name); key != "" {
						calendarNames[key] = item.ID
					}
				}
			}
		}
	}
	for _, item := range followed {
		if item.Status == db.FollowedStatusDeleted {
			continue
		}
		summary.Total++
		if item.IsScript {
			summary.Skipped++
			continue
		}
		subjectID, err := db.Conn.GetSubjectIDByBangumiName(item.BangumiName)
		if err != nil {
			if id := calendarNames[normalizeForMatch(item.BangumiName)]; id > 0 {
				subjectID = id
			}
		}
		if subjectID <= 0 {
			summary.Skipped++
			continue
		}
		if !refresh {
			if cache, err := db.Conn.GetBangumiCache(int64(subjectID)); err == nil &&
				cache.Overview != "" && cache.Characters != "" && cache.Staff != "" && cache.Relations != "" &&
				!cacheHasRemoteImages(cache) {
				summary.Skipped++
				continue
			}
		}
		subject, err := bgmtv.FetchSubject(subjectID)
		if err != nil {
			summary.Failed++
			continue
		}
		_ = db.Conn.SaveBangumiLink(item.BangumiName, subjectID)
		if cover := subject.CoverURL(); cover != "" {
			utils.DownloadCover([]string{cover})
		}
		cache := &db.BangumiCache{SubjectID: int64(subjectID)}
		if existing, err := db.Conn.GetBangumiCache(int64(subjectID)); err == nil && existing != nil {
			*cache = *existing
		}
		if data, err := json.Marshal(cacheImages(cachedSubjectPayload(subject))); err == nil {
			cache.Overview = string(data)
		}
		if items, err := bgmtv.FetchCharacters(subjectID); err == nil {
			if data, err := json.Marshal(cacheImages(cachedCharacterPayload(items))); err == nil {
				cache.Characters = string(data)
			}
		} else {
			cache.Characters = cacheExistingImages(cache.Characters)
		}
		if items, err := bgmtv.FetchStaff(subjectID); err == nil {
			if data, err := json.Marshal(cacheImages(cachedStaffPayload(items))); err == nil {
				cache.Staff = string(data)
			}
		} else {
			cache.Staff = cacheExistingImages(cache.Staff)
		}
		if items, err := bgmtv.FetchRelations(subjectID); err == nil {
			if data, err := json.Marshal(cacheImages(cachedRelationPayload(items))); err == nil {
				cache.Relations = string(data)
			}
		} else {
			cache.Relations = cacheExistingImages(cache.Relations)
		}
		if commentsAvailable {
			if page, err := bgmtv.FetchComments(subjectID, 0, 20); err == nil {
				if data, err := json.Marshal(cacheImages(cachedCommentPayload(page))); err == nil {
					cache.Comments = string(data)
				}
			} else {
				commentsAvailable = false
				cache.Comments = cacheExistingImages(cache.Comments)
			}
		} else {
			cache.Comments = cacheExistingImages(cache.Comments)
		}
		if err := db.Conn.SaveBangumiCache(cache); err != nil {
			summary.Failed++
			continue
		}
		summary.Updated++
	}
	return summary, nil
}

func cachedSubjectPayload(subject *bgmtv.Subject) map[string]any {
	cover := subject.CoverURL()
	return map[string]any{
		"id": subject.ID, "name": subject.Name, "name_cn": subject.NameCN,
		"cover": cover, "date": subject.Date, "score": subject.Rating.Score,
		"rank": subject.Rating.Rank, "total": subject.Rating.Total,
		"meta_tags": subject.MetaTags, "aliases": subject.SearchNames(),
		"url": subject.URL, "type": subject.Type, "summary": subject.Summary,
		"tags": subject.Tags, "info": subject.Info,
		"info_fields": bgmtv.ParseSubjectInfo(subject.Info),
		"platform":    subject.Platform, "cache_version": 2,
	}
}

func cachedCharacterPayload(items []bgmtv.Character) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		actors := make([]map[string]any, 0, len(item.Actors))
		for _, actor := range item.Actors {
			cover := ""
			for _, candidate := range []string{actor.Images.Large, actor.Images.Common, actor.Images.Medium, actor.Images.Small} {
				if candidate != "" {
					cover = candidate
					break
				}
			}
			actors = append(actors, map[string]any{"id": actor.ID, "name": actor.Name, "cover": cover})
		}
		cover := ""
		for _, candidate := range []string{item.Images.Large, item.Images.Common, item.Images.Medium, item.Images.Small} {
			if candidate != "" {
				cover = candidate
				break
			}
		}
		out = append(out, map[string]any{"id": item.ID, "name": item.Name, "relation": item.Relation, "cover": cover, "actors": actors})
	}
	return out
}

func cachedStaffPayload(items []bgmtv.Person) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		cover := ""
		for _, candidate := range []string{item.Images.Large, item.Images.Common, item.Images.Medium, item.Images.Small} {
			if candidate != "" {
				cover = candidate
				break
			}
		}
		out = append(out, map[string]any{"id": item.ID, "name": item.Name, "relation": item.Relation, "eps": item.Eps, "career": item.Career, "cover": cover})
	}
	return out
}

func cachedRelationPayload(items []bgmtv.Relation) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		cover := ""
		for _, candidate := range []string{item.Images.Large, item.Images.Common, item.Images.Medium, item.Images.Small} {
			if candidate != "" {
				cover = candidate
				break
			}
		}
		out = append(out, map[string]any{"id": item.ID, "name": item.Name, "name_cn": item.NameCN, "relation": item.Relation, "type": item.Type, "cover": cover})
	}
	return out
}

func cachedCommentPayload(page *bgmtv.CommentPage) map[string]any {
	out := make([]map[string]any, 0, len(page.List))
	for _, item := range page.List {
		cover := ""
		for _, candidate := range []string{item.User.Avatar.Large, item.User.Avatar.Common, item.User.Avatar.Medium, item.User.Avatar.Small} {
			if candidate != "" {
				cover = candidate
				break
			}
		}
		out = append(out, map[string]any{
			"user": map[string]any{"id": item.User.ID, "username": item.User.Username, "nickname": item.User.Nickname, "cover": cover},
			"rate": item.Rate, "comment": item.Comment, "updated_at": item.UpdatedAt,
		})
	}
	return map[string]any{"items": out, "total": page.Total}
}

// CacheSummaryText formats a short user-facing summary.
func CacheSummaryText(summary BangumiCacheSummary) string {
	return fmt.Sprintf("追番信息更新完成：成功 %d，跳过 %d，失败 %d（共 %d，%s）",
		summary.Updated, summary.Skipped, summary.Failed, summary.Total, time.Now().Format("15:04:05"))
}

func cacheExistingImages(raw string) string {
	if raw == "" {
		return raw
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}
	data, err := json.Marshal(cacheImages(payload))
	if err != nil {
		return raw
	}
	return string(data)
}

func cacheHasRemoteImages(cache *db.BangumiCache) bool {
	for _, raw := range []string{cache.Overview, cache.Characters, cache.Staff, cache.Relations, cache.Comments} {
		if raw == "" {
			continue
		}
		var payload any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			continue
		}
		if payloadHasRemoteImages(payload) {
			return true
		}
	}
	return false
}

// CachePayloadImages downloads Bangumi images referenced by a detail payload
// and rewrites them to local /bangumi/.cover paths. It is used both by the
// full follow-list cache and by on-demand detail tabs.
func CachePayloadImages(payload any) any {
	return cacheImages(payload)
}

func cacheImages(payload any) any {
	urls := map[string]bool{}
	collectCacheImages(payload, urls)
	if len(urls) > 0 {
		values := make([]string, 0, len(urls))
		for url := range urls {
			values = append(values, url)
		}
		utils.DownloadCover(values)
	}
	return rewriteCacheImages(payload, urls)
}

func payloadHasRemoteImages(value any) bool {
	switch v := value.(type) {
	case string:
		return kazumi.IsBangumiImage(v)
	case []any:
		for _, item := range v {
			if payloadHasRemoteImages(item) {
				return true
			}
		}
	case []map[string]any:
		for _, item := range v {
			if payloadHasRemoteImages(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range v {
			if payloadHasRemoteImages(item) {
				return true
			}
		}
	}
	return false
}

func collectCacheImages(value any, urls map[string]bool) {
	switch v := value.(type) {
	case string:
		if kazumi.IsBangumiImage(v) {
			urls[v] = true
		}
	case []any:
		for _, item := range v {
			collectCacheImages(item, urls)
		}
	case []map[string]any:
		for _, item := range v {
			collectCacheImages(item, urls)
		}
	case map[string]any:
		for _, item := range v {
			collectCacheImages(item, urls)
		}
	}
}

func rewriteCacheImages(value any, urls map[string]bool) any {
	switch v := value.(type) {
	case string:
		if urls[v] {
			_, file := utils.ConvertCoverURLToPath(v)
			if utils.IsImage(file) {
				return "/bangumi/.cover/" + utils.NormalizePath(v)
			}
		}
		return v
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = rewriteCacheImages(item, urls)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, item := range v {
			out[i] = rewriteCacheImages(item, urls).(map[string]any)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			out[key] = rewriteCacheImages(item, urls)
		}
		return out
	default:
		return value
	}
}
