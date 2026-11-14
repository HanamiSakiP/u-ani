package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/bgmtv"
	"github.com/HanamiSakiP/u-ani/internal/controllers"
)

var (
	timelineMu    sync.Mutex
	timelineAt    time.Time
	timelineCache []bgmtv.CalendarDay

	recommendMu    sync.Mutex
	recommendCache = map[string]recommendEntry{}

	recommendWarmMu  sync.Mutex
	recommendWarming bool
)

type recommendEntry struct {
	at    time.Time
	items []map[string]any
}

// kazumiRecommendTags mirrors the category chips rendered by the built-in
// web UI so the cache warmer can populate every category.
var kazumiRecommendTags = []string{
	"", "日常", "原创", "校园", "搞笑", "奇幻", "百合", "恋爱",
	"悬疑", "热血", "后宫", "机战", "轻改", "偶像", "治愈", "异世界",
}

func handleKazumiTimeline(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") == "1"
	days, err := cachedKazumiTimeline(refresh)
	if err != nil {
		if cached, ok := loadTimelineDiskCache(); ok {
			days = cached
			err = nil
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": timelinePayload(days), "cached": !refresh})
}

func timelinePayload(days []bgmtv.CalendarDay) []map[string]any {
	out := make([]map[string]any, 0, len(days))
	for _, day := range days {
		items := make([]map[string]any, 0, len(day.Items))
		for _, item := range day.Items {
			items = append(items, calendarItemPayload(item))
		}
		out = append(out, map[string]any{
			"weekday": day.Weekday.En,
			"label":   weekdayLabel(day.Weekday.En),
			"ja":      day.Weekday.Ja,
			"items":   items,
		})
	}
	return out
}

func timelineCachePath() string {
	return filepath.Join(controllers.KazumiCacheDir(), "timeline.json")
}

func saveTimelineDiskCache(days []bgmtv.CalendarDay) {
	data, err := json.Marshal(days)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(timelineCachePath()), 0o755)
	_ = os.WriteFile(timelineCachePath(), data, 0o644)
}

func loadTimelineDiskCache() ([]bgmtv.CalendarDay, bool) {
	data, err := os.ReadFile(timelineCachePath())
	if err != nil {
		return nil, false
	}
	var days []bgmtv.CalendarDay
	if err := json.Unmarshal(data, &days); err != nil || len(days) == 0 {
		return nil, false
	}
	return days, true
}

func cachedKazumiTimeline(refresh bool) ([]bgmtv.CalendarDay, error) {
	timelineMu.Lock()
	defer timelineMu.Unlock()
	if !refresh && len(timelineCache) > 0 && time.Since(timelineAt) < 30*time.Minute {
		return timelineCache, nil
	}
	if !refresh {
		if len(timelineCache) == 0 {
			if disk, ok := loadTimelineDiskCache(); ok {
				timelineCache = disk
				timelineAt = time.Now()
				return disk, nil
			}
		}
	}
	days, err := bgmtv.FetchCalendar()
	if err != nil {
		if len(timelineCache) > 0 {
			return timelineCache, nil
		}
		if disk, ok := loadTimelineDiskCache(); ok {
			return disk, nil
		}
		return nil, err
	}
	timelineCache = days
	timelineAt = time.Now()
	saveTimelineDiskCache(days)
	return days, nil
}

func recommendCachePath(key string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "|", "_", "?", "_", "*", "_", " ", "_").Replace(key)
	if safe == "" {
		safe = "default"
	}
	return filepath.Join(controllers.KazumiCacheDir(), "recommend_v2_"+safe+".json")
}

func saveRecommendDiskCache(key string, items []map[string]any) {
	data, err := json.Marshal(items)
	if err != nil {
		return
	}
	path := recommendCachePath(key)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o644)
}

func loadRecommendDiskCache(key string) ([]map[string]any, bool) {
	data, err := os.ReadFile(recommendCachePath(key))
	if err != nil {
		return nil, false
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, false
	}
	return items, true
}

func handleKazumiRecommend(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	refresh := r.URL.Query().Get("refresh") == "1"
	cacheKey := fmt.Sprintf("%s|%s|%d|%d", keyword, tag, limit, offset)
	if keyword == "" && tag == "" {
		// The recommend page is opening; warm the other category chips in the
		// background so the first category switch is quick.
		warmRecommendCategories()
	}
	if !refresh {
		recommendMu.Lock()
		if entry, ok := recommendCache[cacheKey]; ok && time.Since(entry.at) < 30*time.Minute {
			recommendMu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{
				"subjects": entry.items, "tag": tag, "keyword": keyword,
				"limit": limit, "offset": offset, "cached": true,
			})
			return
		}
		recommendMu.Unlock()
		if items, ok := loadRecommendDiskCache(cacheKey); ok {
			recommendMu.Lock()
			recommendCache[cacheKey] = recommendEntry{at: time.Now(), items: items}
			recommendMu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{
				"subjects": items, "tag": tag, "keyword": keyword,
				"limit": limit, "offset": offset, "cached": true,
			})
			return
		}
	}
	var subjects []bgmtv.Subject
	var err error
	if keyword != "" {
		subjects, err = bgmtv.SearchSubjects(keyword, limit)
	} else {
		subjects, err = bgmtv.FetchRecommended(limit, offset, tag)
	}
	fallback := false
	if err != nil && keyword != "" {
		if days, cacheErr := cachedKazumiTimeline(false); cacheErr == nil {
			needle := normalizeSubjectName(keyword)
			for _, day := range days {
				for _, item := range day.Items {
					if strings.Contains(normalizeSubjectName(item.Name), needle) || strings.Contains(normalizeSubjectName(item.NameCN), needle) {
						subjects = append(subjects, bgmtv.Subject{
							ID: item.ID, Name: item.Name, NameCN: item.NameCN,
							Images: item.Images, Rating: item.Rating, MetaTags: item.MetaTags,
							Date: item.Info,
						})
					}
				}
			}
			if len(subjects) > 0 {
				fallback = true
				err = nil
			}
		}
	}
	if err != nil {
		if !refresh {
			if items, ok := loadRecommendDiskCache(cacheKey); ok {
				writeJSON(w, http.StatusOK, map[string]any{
					"subjects": items, "tag": tag, "keyword": keyword,
					"limit": limit, "offset": offset, "cached": true, "fallback": true,
				})
				return
			}
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(subjects))
	for _, subject := range subjects {
		items = append(items, subjectPayload(&subject, false))
	}
	if keyword != "" {
		// Search results are not part of the warmed browse cache. Cache their
		// covers in the background so later searches and the search grid can
		// serve local images.
		covers := make([]string, 0, len(subjects))
		for i := range subjects {
			if cover := cardCover(subjects[i].Images); cover != "" {
				covers = append(covers, cover)
			}
		}
		if len(covers) > 0 {
			go controllers.EnsureCovers(covers)
		}
	}
	recommendMu.Lock()
	recommendCache[cacheKey] = recommendEntry{at: time.Now(), items: items}
	recommendMu.Unlock()
	saveRecommendDiskCache(cacheKey, items)
	writeJSON(w, http.StatusOK, map[string]any{
		"subjects": items,
		"tag":      tag,
		"keyword":  keyword,
		"fallback": fallback,
		"limit":    limit,
		"offset":   offset,
	})
}

// warmRecommendCategories fills category caches in the background. It is
// triggered when the recommend page loads so the first category switch can
// reuse a warmed cache instead of waiting for the network.
func warmRecommendCategories() {
	recommendWarmMu.Lock()
	if recommendWarming {
		recommendWarmMu.Unlock()
		return
	}
	recommendWarming = true
	recommendWarmMu.Unlock()

	go func() {
		defer func() {
			recommendWarmMu.Lock()
			recommendWarming = false
			recommendWarmMu.Unlock()
		}()

		var missing []string
		for _, tag := range kazumiRecommendTags {
			key := fmt.Sprintf("%s|%s|%d|%d", "", tag, 24, 0)
			recommendMu.Lock()
			entry, ok := recommendCache[key]
			recommendMu.Unlock()
			if ok && len(entry.items) > 0 {
				continue
			}
			if items, ok := loadRecommendDiskCache(key); ok && len(items) > 0 {
				recommendMu.Lock()
				recommendCache[key] = recommendEntry{at: time.Now(), items: items}
				recommendMu.Unlock()
				continue
			}
			missing = append(missing, tag)
		}
		if len(missing) == 0 {
			return
		}

		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, tag := range missing {
			tag := tag
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				subjects, err := bgmtv.FetchRecommended(24, 0, tag)
				if err != nil {
					return
				}
				items := make([]map[string]any, 0, len(subjects))
				for i := range subjects {
					items = append(items, subjectPayload(&subjects[i], false))
				}
				key := fmt.Sprintf("%s|%s|%d|%d", "", tag, 24, 0)
				saveRecommendDiskCache(key, items)
				recommendMu.Lock()
				recommendCache[key] = recommendEntry{at: time.Now(), items: items}
				recommendMu.Unlock()
				if len(subjects) > 0 {
					covers := make([]string, 0, len(subjects))
					for i := range subjects {
						if cover := cardCover(subjects[i].Images); cover != "" {
							covers = append(covers, cover)
						}
					}
					if len(covers) > 0 {
						go controllers.EnsureCovers(covers)
					}
				}
			}()
		}
		wg.Wait()
	}()
}

func handleKazumiRecommendCategories(w http.ResponseWriter, r *http.Request) {
	categories := map[string][]map[string]any{}
	for _, tag := range kazumiRecommendTags {
		key := fmt.Sprintf("%s|%s|%d|%d", "", tag, 24, 0)
		recommendMu.Lock()
		entry, ok := recommendCache[key]
		recommendMu.Unlock()
		if ok && len(entry.items) > 0 {
			categories[tag] = entry.items
			continue
		}
		if items, ok := loadRecommendDiskCache(key); ok && len(items) > 0 {
			categories[tag] = items
			recommendMu.Lock()
			recommendCache[key] = recommendEntry{at: time.Now(), items: items}
			recommendMu.Unlock()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
}

func clearKazumiBrowseCache() {
	resetKazumiBrowseCacheMemory()
	_ = os.Remove(timelineCachePath())
	matches, _ := filepath.Glob(filepath.Join(controllers.KazumiCacheDir(), "recommend_*.json"))
	for _, match := range matches {
		_ = os.Remove(match)
	}
}

// resetKazumiBrowseCacheMemory drops the in-process timeline/recommend
// caches. Disk caches are kept so they can be reloaded (for example after an
// imported cache snapshot replaces the files).
func resetKazumiBrowseCacheMemory() {
	timelineMu.Lock()
	timelineCache = nil
	timelineAt = time.Time{}
	timelineMu.Unlock()
	recommendMu.Lock()
	recommendCache = map[string]recommendEntry{}
	recommendMu.Unlock()
}

// refreshKazumiBrowseCache updates the timeline/recommend caches and stores
// complete text metadata for timeline subjects. Only cover images are
// downloaded; all other images stay remote unless already cached locally.
func refreshKazumiBrowseCache() error {
	days, err := cachedKazumiTimeline(true)
	if err != nil {
		return err
	}
	// Clear recommend caches so next visits fetch fresh network data and re-cache.
	recommendMu.Lock()
	recommendCache = map[string]recommendEntry{}
	recommendMu.Unlock()
	matches, _ := filepath.Glob(filepath.Join(controllers.KazumiCacheDir(), "recommend_*.json"))
	for _, match := range matches {
		_ = os.Remove(match)
	}

	// Cache covers first so timeline/recommend cards render from local disk.
	seen := map[int]bool{}
	var ids []int
	var covers []string
	for _, day := range days {
		for _, item := range day.Items {
			if item.ID <= 0 || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			ids = append(ids, item.ID)
			if cover := cardCover(item.Images); cover != "" {
				covers = append(covers, cover)
			}
		}
	}

	// Prefetch every recommendation category so switching category chips is
	// instant after the cache has been refreshed. Fetch a few categories at a
	// time to avoid hammering the mirrors.
	type categoryResult struct {
		tag      string
		subjects []bgmtv.Subject
	}
	results := make(chan categoryResult, len(kazumiRecommendTags))
	sem := make(chan struct{}, 4)
	var tagWG sync.WaitGroup
	for _, tag := range kazumiRecommendTags {
		tag := tag
		tagWG.Add(1)
		go func() {
			defer tagWG.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			subjects, err := bgmtv.FetchRecommended(24, 0, tag)
			if err != nil {
				return
			}
			results <- categoryResult{tag: tag, subjects: subjects}
		}()
	}
	tagWG.Wait()
	close(results)

	for result := range results {
		items := make([]map[string]any, 0, len(result.subjects))
		for i := range result.subjects {
			subject := &result.subjects[i]
			if subject.ID > 0 && !seen[subject.ID] {
				seen[subject.ID] = true
				ids = append(ids, subject.ID)
			}
			if cover := cardCover(subject.Images); cover != "" {
				covers = append(covers, cover)
			}
			items = append(items, subjectPayload(subject, false))
		}
		key := fmt.Sprintf("%s|%s|%d|%d", "", result.tag, 24, 0)
		saveRecommendDiskCache(key, items)
		recommendMu.Lock()
		recommendCache[key] = recommendEntry{at: time.Now(), items: items}
		recommendMu.Unlock()
	}
	if len(covers) > 0 {
		controllers.EnsureCovers(covers)
	}

	// Fetch text metadata concurrently. Cover downloads happen inside each
	// subject fetch as well, so warm the cache in parallel.
	workers := 6
	if len(ids) < workers {
		workers = len(ids)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				_ = cacheSubjectDetails(id)
			}
		}()
	}
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	return nil
}

func handleKazumiSubject(w http.ResponseWriter, r *http.Request, rawID string) {
	id, err := strconv.Atoi(rawID)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid subject id")
		return
	}
	if r.URL.Query().Get("local") == "1" {
		subject, ok := cachedSubjectOverview(id)
		if !ok {
			subject = map[string]any{"id": id}
		}
		subject = enrichSubjectPayload(id, subject)
		writeJSON(w, http.StatusOK, map[string]any{"subject": subject, "cached": ok})
		return
	}
	subject, err := loadSubjectOverview(id, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	subject = enrichSubjectPayload(id, subject)
	writeJSON(w, http.StatusOK, map[string]any{"subject": subject})
}

func subjectPayload(subject *bgmtv.Subject, detail bool) map[string]any {
	if subject == nil {
		return map[string]any{}
	}
	cover := subject.CoverURL()
	if !detail {
		cover = cardCover(subject.Images)
	}
	if local := localCoverPath(cover); local != "" {
		cover = local
	} else if !detail {
		// Reuse an already cached larger cover instead of downloading the
		// card-sized one again.
		if local := localCoverPath(subject.CoverURL()); local != "" {
			cover = local
		}
	}
	out := map[string]any{
		"id":        subject.ID,
		"name":      subject.Name,
		"name_cn":   subject.NameCN,
		"cover":     cover,
		"date":      subject.Date,
		"score":     subject.Rating.Score,
		"rank":      subject.Rating.Rank,
		"total":     subject.Rating.Total,
		"meta_tags": nonNilStrings(subject.MetaTags),
		"aliases":   subject.SearchNames(),
		"url":       subject.URL,
		"type":      subject.Type,
	}
	if detail {
		out["summary"] = subject.Summary
		out["tags"] = subject.Tags
		out["info"] = subject.Info
		out["info_fields"] = bgmtv.ParseSubjectInfo(subject.Info)
		out["platform"] = subject.Platform
	}
	return out
}

func calendarItemPayload(item bgmtv.CalendarItem) map[string]any {
	cover := cardCover(item.Images)
	if local := localCoverPath(cover); local != "" {
		cover = local
	} else {
		if local := localCoverPath(firstCover(item.Images)); local != "" {
			cover = local
		}
	}
	return map[string]any{
		"id":        item.ID,
		"name":      item.Name,
		"name_cn":   item.NameCN,
		"cover":     cover,
		"info":      item.Info,
		"score":     item.Rating.Score,
		"rank":      item.Rating.Rank,
		"total":     item.Rating.Total,
		"meta_tags": nonNilStrings(item.MetaTags),
		"watchers":  item.Watchers,
		"url":       "https://bgm.tv/subject/" + strconv.Itoa(item.ID),
	}
}

// cardCover prefers a smaller thumbnail for list cards. Large covers make the
// recommendation/search grid noticeably slower to paint on mobile networks.
func cardCover(images bgmtv.Images) string {
	for _, value := range []string{images.Common, images.Large, images.Medium, images.Small, images.Grid} {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstCover(images bgmtv.Images) string {
	for _, value := range []string{images.Large, images.Common, images.Medium, images.Small, images.Grid} {
		if value != "" {
			return value
		}
	}
	return ""
}

func weekdayLabel(day string) string {
	switch day {
	case "Mon":
		return "周一"
	case "Tue":
		return "周二"
	case "Wed":
		return "周三"
	case "Thu":
		return "周四"
	case "Fri":
		return "周五"
	case "Sat":
		return "周六"
	case "Sun":
		return "周日"
	default:
		return day
	}
}
