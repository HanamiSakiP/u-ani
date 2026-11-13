package controllers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

// CacheFollowedBangumi downloads complete Bangumi metadata (overview,
// characters, staff, relations, comments) for every followed bangumi. Only
// cover images are downloaded locally; all other images are reused when they
// already exist locally and otherwise stay remote. When refresh is false,
// already cached subjects are skipped. Subjects are fetched concurrently.
func CacheFollowedBangumi(refresh bool) (BangumiCacheSummary, error) {
	summary := BangumiCacheSummary{}
	if err := ensureDB(); err != nil {
		return summary, err
	}
	followed, err := db.Conn.AllFollowed()
	if err != nil {
		return summary, err
	}
	var calendarNames map[string]int
	loadCalendar := func() map[string]int {
		if calendarNames != nil {
			return calendarNames
		}
		calendarNames = map[string]int{}
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
		return calendarNames
	}

	type target struct {
		name      string
		subjectID int
	}
	var targets []target
	for _, item := range followed {
		if item.Status == db.FollowedStatusDeleted {
			continue
		}
		summary.Total++
		if item.IsScript {
			summary.Skipped++
			continue
		}
		subjectID, _ := db.Conn.GetSubjectIDByBangumiName(item.BangumiName)
		if subjectID <= 0 {
			if id := loadCalendar()[normalizeForMatch(item.BangumiName)]; id > 0 {
				subjectID = id
			}
		}
		if subjectID <= 0 {
			summary.Skipped++
			continue
		}
		if !refresh {
			if cache, err := db.Conn.GetBangumiCache(int64(subjectID)); err == nil &&
				cache.Overview != "" && cache.Characters != "" && cache.Staff != "" && cache.Relations != "" && cacheCommentsUsable(cache.Comments) &&
				!cacheHasRemoteImages(cache) {
				summary.Skipped++
				continue
			}
		}
		targets = append(targets, target{name: item.BangumiName, subjectID: subjectID})
	}
	if len(targets) == 0 {
		return summary, nil
	}

	commentsAvailable := &atomic.Bool{}
	commentsAvailable.Store(true)
	var mu sync.Mutex
	workers := 4
	if len(targets) < workers {
		workers = len(targets)
	}
	jobs := make(chan target)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				if cacheOneFollowedSubject(t.name, t.subjectID, commentsAvailable) {
					mu.Lock()
					summary.Updated++
					mu.Unlock()
				} else {
					mu.Lock()
					summary.Failed++
					mu.Unlock()
				}
			}
		}()
	}
	for _, t := range targets {
		jobs <- t
	}
	close(jobs)
	wg.Wait()
	return summary, nil
}

// cacheOneFollowedSubject fetches and stores all detail sections for one
// subject. It returns true when the cache row was saved.
func cacheOneFollowedSubject(name string, subjectID int, commentsAvailable *atomic.Bool) bool {
	subject, err := bgmtv.FetchSubject(subjectID)
	if err != nil {
		return false
	}
	_ = db.Conn.SaveBangumiLink(name, subjectID)
	cache := &db.BangumiCache{SubjectID: int64(subjectID)}
	if existing, err := db.Conn.GetBangumiCache(int64(subjectID)); err == nil && existing != nil {
		*cache = *existing
	}
	// Only the subject cover is downloaded; every other image is reused
	// locally when already present, otherwise left as a remote URL.
	if data, err := json.Marshal(cacheImages(cachedSubjectPayload(subject))); err == nil {
		cache.Overview = string(data)
	}
	if items, err := bgmtv.FetchCharacters(subjectID); err == nil {
		if data, err := json.Marshal(localizeCacheImages(cachedCharacterPayload(items))); err == nil {
			cache.Characters = string(data)
		}
	}
	if items, err := bgmtv.FetchStaff(subjectID); err == nil {
		if data, err := json.Marshal(localizeCacheImages(cachedStaffPayload(items))); err == nil {
			cache.Staff = string(data)
		}
	}
	if items, err := bgmtv.FetchRelations(subjectID); err == nil {
		if data, err := json.Marshal(localizeCacheImages(cachedRelationPayload(items))); err == nil {
			cache.Relations = string(data)
		}
	}
	if commentsAvailable.Load() {
		if page, err := bgmtv.FetchComments(subjectID, 0, 20); err == nil {
			if len(page.List) > 0 || page.Total > 0 {
				if data, err := json.Marshal(localizeCacheImages(cachedCommentPayload(page))); err == nil {
					cache.Comments = string(data)
				}
			}
		} else {
			commentsAvailable.Store(false)
		}
	}
	return db.Conn.SaveBangumiCache(cache) == nil
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

func cacheCommentsUsable(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	var payload struct {
		Items    []any `json:"items"`
		List     []any `json:"list"`
		Data     []any `json:"data"`
		Comments []any `json:"comments"`
		Total    *int  `json:"total"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return false
	}
	if len(payload.Items) > 0 || len(payload.List) > 0 || len(payload.Data) > 0 || len(payload.Comments) > 0 {
		return true
	}
	return payload.Total != nil && *payload.Total > 0
}

func cachedCommentPayload(page *bgmtv.CommentPage) map[string]any {
	out := make([]map[string]any, 0, len(page.List))
	for _, item := range page.List {
		out = append(out, map[string]any{
			"user":       map[string]any{"id": item.User.ID, "username": item.User.Username, "nickname": item.User.Nickname, "cover": commentAvatar(item.User.Avatar)},
			"rate":       item.Rate,
			"comment":    item.Comment,
			"updated_at": item.UpdatedAt,
		})
	}
	return map[string]any{"items": out, "total": page.Total}
}

func commentAvatar(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	var images bgmtv.Images
	if err := json.Unmarshal(raw, &images); err == nil {
		for _, candidate := range []string{images.Large, images.Common, images.Medium, images.Small} {
			if candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

// CacheSummaryText formats a short user-facing summary.
func CacheSummaryText(summary BangumiCacheSummary) string {
	return fmt.Sprintf("追番信息更新完成：成功 %d，跳过 %d，失败 %d（共 %d，%s）",
		summary.Updated, summary.Skipped, summary.Failed, summary.Total, time.Now().Format("15:04:05"))
}

// ClearBangumiDetailCache removes cached Bangumi metadata/comments while
// preserving subscriptions and all followed-anime information.
func ClearBangumiDetailCache() error {
	if err := ensureDB(); err != nil {
		return err
	}
	return db.Conn.ClearBangumiCache()
}

func localizeExistingImages(raw string) string {
	if raw == "" {
		return raw
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}
	data, err := json.Marshal(localizeCacheImages(payload))
	if err != nil {
		return raw
	}
	return string(data)
}

// cacheHasRemoteImages reports whether cached cover metadata still contains
// remote Bangumi image URLs that should be cached locally by "更新缓存".
// Kazumi only caches cover images; other section images stay remote.
func cacheHasRemoteImages(cache *db.BangumiCache) bool {
	for _, raw := range []string{cache.Overview} {
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

// LocalizePayloadImages rewrites images that already exist locally without
// downloading any new ones. It is used for sections whose images are not
// cached locally (staff and comments).
func LocalizePayloadImages(payload any) any {
	return localizeCacheImages(payload)
}

func cacheImages(payload any) any {
	urls := map[string]bool{}
	collectCacheImages(payload, urls)
	if len(urls) > 0 {
		values := make([]string, 0, len(urls))
		for raw := range urls {
			if _, file := utils.ConvertCoverURLToPath(raw); utils.IsImage(file) {
				continue
			}
			values = append(values, raw)
		}
		if len(values) > 0 {
			utils.DownloadCover(values)
		}
	}
	return rewriteCacheImages(payload, urls)
}

func localizeCacheImages(payload any) any {
	urls := map[string]bool{}
	collectCacheImages(payload, urls)
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
				return "/covers/" + utils.NormalizePath(v)
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
