package httpserver

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/bgmtv"
)

var (
	timelineMu    sync.Mutex
	timelineAt    time.Time
	timelineCache []bgmtv.CalendarDay

	recommendMu    sync.Mutex
	recommendCache = map[string]recommendEntry{}
)

type recommendEntry struct {
	at    time.Time
	items []map[string]any
}

func handleKazumiTimeline(w http.ResponseWriter, r *http.Request) {
	days, err := cachedKazumiTimeline()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
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
	writeJSON(w, http.StatusOK, map[string]any{"days": out})
}

func cachedKazumiTimeline() ([]bgmtv.CalendarDay, error) {
	timelineMu.Lock()
	defer timelineMu.Unlock()
	if len(timelineCache) > 0 && time.Since(timelineAt) < 10*time.Minute {
		return timelineCache, nil
	}
	days, err := bgmtv.FetchCalendar()
	if err != nil {
		if len(timelineCache) > 0 {
			return timelineCache, nil
		}
		return nil, err
	}
	timelineCache = days
	timelineAt = time.Now()
	return days, nil
}

func handleKazumiRecommend(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	cacheKey := fmt.Sprintf("%s|%s|%d|%d", keyword, tag, limit, offset)
	recommendMu.Lock()
	if entry, ok := recommendCache[cacheKey]; ok && time.Since(entry.at) < 5*time.Minute {
		recommendMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"subjects": entry.items, "tag": tag, "keyword": keyword,
			"limit": limit, "offset": offset,
		})
		return
	}
	recommendMu.Unlock()
	var subjects []bgmtv.Subject
	var err error
	if keyword != "" {
		subjects, err = bgmtv.SearchSubjects(keyword, limit)
	} else {
		subjects, err = bgmtv.FetchRecommended(limit, offset, tag)
	}
	fallback := false
	if err != nil && keyword != "" {
		if days, cacheErr := cachedKazumiTimeline(); cacheErr == nil {
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
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(subjects))
	for _, subject := range subjects {
		items = append(items, subjectPayload(&subject, false))
	}
	recommendMu.Lock()
	recommendCache[cacheKey] = recommendEntry{at: time.Now(), items: items}
	recommendMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"subjects": items,
		"tag":      tag,
		"keyword":  keyword,
		"fallback": fallback,
		"limit":    limit,
		"offset":   offset,
	})
}

func handleKazumiSubject(w http.ResponseWriter, r *http.Request, rawID string) {
	id, err := strconv.Atoi(rawID)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid subject id")
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
	out := map[string]any{
		"id":        subject.ID,
		"name":      subject.Name,
		"name_cn":   subject.NameCN,
		"cover":     subject.CoverURL(),
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
	return map[string]any{
		"id":        item.ID,
		"name":      item.Name,
		"name_cn":   item.NameCN,
		"cover":     firstCover(item.Images),
		"info":      item.Info,
		"score":     item.Rating.Score,
		"rank":      item.Rating.Rank,
		"total":     item.Rating.Total,
		"meta_tags": nonNilStrings(item.MetaTags),
		"watchers":  item.Watchers,
		"url":       "https://bgm.tv/subject/" + strconv.Itoa(item.ID),
	}
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
