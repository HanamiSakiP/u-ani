package controllers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/db"
)

// CacheExport is a portable snapshot of locally cached Bangumi metadata.
// Unlike the follow-list export it does not touch subscriptions, downloads or
// images on disk; it only carries the JSON/text cache used by detail pages and
// the Kazumi timeline/recommend pages.
type CacheExport struct {
	Version    int                        `json:"version"`
	ExportedAt int64                      `json:"exported_at"`
	Subjects   []CacheExportSubject       `json:"subjects"`
	Timeline   json.RawMessage            `json:"timeline,omitempty"`
	Recommend  map[string]json.RawMessage `json:"recommend,omitempty"`
	// Items lets users import a follow-list export into the cache importer
	// without having to extract the nested cache by hand.
	Items []FollowExportItem `json:"items,omitempty"`
}

// CacheExportSubject is the cached text for one bgm.tv subject.
type CacheExportSubject struct {
	SubjectID  int64  `json:"subject_id"`
	Overview   string `json:"overview,omitempty"`
	Characters string `json:"characters,omitempty"`
	Staff      string `json:"staff,omitempty"`
	Relations  string `json:"relations,omitempty"`
	Comments   string `json:"comments,omitempty"`
}

// ExportCacheData collects Bangumi detail caches plus the Kazumi browse cache
// files (timeline and recommendation categories) into one JSON snapshot.
func ExportCacheData() (CacheExport, error) {
	if err := ensureDB(); err != nil {
		return CacheExport{}, err
	}
	out := CacheExport{
		Version:    1,
		ExportedAt: time.Now().Unix(),
		Subjects:   []CacheExportSubject{},
	}
	rows, err := db.Conn.ListBangumiCaches()
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		if item, ok := cacheExportSubject(row); ok {
			out.Subjects = append(out.Subjects, item)
		}
	}
	out.Timeline, out.Recommend = exportKazumiBrowseCaches()
	return out, nil
}

func cacheExportSubject(row db.BangumiCache) (CacheExportSubject, bool) {
	if row.SubjectID <= 0 {
		return CacheExportSubject{}, false
	}
	return CacheExportSubject{
		SubjectID:  row.SubjectID,
		Overview:   row.Overview,
		Characters: row.Characters,
		Staff:      row.Staff,
		Relations:  row.Relations,
		Comments:   row.Comments,
	}, true
}

func exportKazumiBrowseCaches() (json.RawMessage, map[string]json.RawMessage) {
	var timeline json.RawMessage
	recommend := map[string]json.RawMessage{}
	dir := KazumiCacheDir()
	if data, err := os.ReadFile(filepath.Join(dir, "timeline.json")); err == nil && json.Valid(data) {
		timeline = append(json.RawMessage(nil), data...)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "recommend_*.json"))
	sort.Strings(matches)
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil || !json.Valid(data) {
			continue
		}
		recommend[filepath.Base(match)] = append(json.RawMessage(nil), data...)
	}
	if len(recommend) == 0 {
		recommend = nil
	}
	return timeline, recommend
}

// ImportCacheData restores a cache snapshot. Existing subject rows are
// overwritten, while unrelated subjects remain untouched.
func ImportCacheData(snapshot CacheExport) (map[string]any, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	subjects := snapshot.Subjects
	if len(subjects) == 0 {
		// Accept a follow-list snapshot as well: its per-item Cache field has
		// the same section layout.
		for _, item := range snapshot.Items {
			if item.BangumiID <= 0 || item.Cache == nil {
				continue
			}
			subjects = append(subjects, CacheExportSubject{
				SubjectID:  int64(item.BangumiID),
				Overview:   item.Cache.Overview,
				Characters: item.Cache.Characters,
				Staff:      item.Cache.Staff,
				Relations:  item.Cache.Relations,
				Comments:   item.Cache.Comments,
			})
		}
	}
	imported, failed := importCacheSubjects(subjects)
	timeline, recommend := importKazumiBrowseCaches(snapshot.Timeline, snapshot.Recommend)
	return map[string]any{
		"imported": imported, "failed": failed, "total": len(subjects),
		"timeline": timeline, "recommend": recommend,
	}, nil
}

func importCacheSubjects(subjects []CacheExportSubject) (int, int) {
	imported := 0
	failed := 0
	for _, item := range subjects {
		if item.SubjectID <= 0 {
			failed++
			continue
		}
		cache := &db.BangumiCache{
			SubjectID:  item.SubjectID,
			Overview:   item.Overview,
			Characters: item.Characters,
			Staff:      item.Staff,
			Relations:  item.Relations,
			Comments:   item.Comments,
		}
		if err := db.Conn.SaveBangumiCache(cache); err != nil {
			failed++
			continue
		}
		imported++
	}
	return imported, failed
}

func importKazumiBrowseCaches(timelineRaw json.RawMessage, recommendRaw map[string]json.RawMessage) (bool, int) {
	timeline := false
	if len(timelineRaw) > 0 && json.Valid(timelineRaw) {
		if err := os.MkdirAll(KazumiCacheDir(), 0o755); err == nil {
			if err := os.WriteFile(filepath.Join(KazumiCacheDir(), "timeline.json"), timelineRaw, 0o644); err == nil {
				timeline = true
			}
		}
	}
	recommend := 0
	for name, data := range recommendRaw {
		name = filepath.Base(strings.TrimSpace(name))
		if !strings.HasPrefix(name, "recommend_") || !strings.HasSuffix(name, ".json") || strings.ContainsAny(name, `/\`) {
			continue
		}
		if len(data) == 0 || !json.Valid(data) {
			continue
		}
		if err := os.MkdirAll(KazumiCacheDir(), 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(KazumiCacheDir(), name), data, 0o644); err != nil {
			continue
		}
		recommend++
	}
	return timeline, recommend
}

// CacheExportMessage formats the cache import result.
func CacheExportMessage(result map[string]any) string {
	if result == nil {
		return "缓存导入完成"
	}
	message := fmt.Sprintf("缓存导入完成：番剧详情 %v 条，失败 %v 条", result["imported"], result["failed"])
	if timeline, _ := result["timeline"].(bool); timeline {
		message += "，已恢复时间表"
	}
	if count, ok := result["recommend"].(int); ok && count > 0 {
		message += fmt.Sprintf("，已恢复 %d 个推荐分类", count)
	}
	return message
}

// BrowseCacheExportMessage formats the timeline/recommend import result.
func BrowseCacheExportMessage(result map[string]any) string {
	if result == nil {
		return "Kazumi 缓存导入完成"
	}
	message := "Kazumi 缓存导入完成"
	if timeline, _ := result["timeline"].(bool); timeline {
		message += "，已恢复时间表"
	}
	if count, ok := result["recommend"].(int); ok && count > 0 {
		message += fmt.Sprintf("，已恢复 %d 个推荐分类", count)
	}
	if count, ok := result["covers"].(int); ok && count > 0 {
		message += fmt.Sprintf("，封面 %d 张", count)
	}
	if count, ok := result["subjects"].(int); ok && count > 0 {
		message += fmt.Sprintf("，番剧信息 %d 条", count)
	}
	return message
}
