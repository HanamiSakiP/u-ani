package controllers

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/model"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

// DownloadExport is the manifest of a downloaded Kazumi library archive. The
// archive itself also contains every downloaded video and cached cover.
type DownloadExport struct {
	Version    int                  `json:"version"`
	ExportedAt int64                `json:"exported_at"`
	Tasks      []DownloadExportTask `json:"tasks"`
	Subjects   []CacheExportSubject `json:"subjects,omitempty"`
}

// DownloadExportTask is one completed download record.
type DownloadExportTask struct {
	Name        string `json:"name"`
	BangumiID   int    `json:"bangumi_id,omitempty"`
	Episode     int    `json:"episode"`
	Title       string `json:"title,omitempty"`
	Rule        string `json:"rule,omitempty"`
	Source      string `json:"source,omitempty"`
	Path        string `json:"path,omitempty"`
	Status      int    `json:"status"`
	Size        int64  `json:"size,omitempty"`
	CreatedTime int64  `json:"created_time,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Cover       string `json:"cover,omitempty"`
}

// buildDownloadedBangumi returns the manifest and the video/cover files that
// make up a complete download-library export.
func buildDownloadedBangumi() (DownloadExport, []archiveFile, error) {
	if err := ensureDB(); err != nil {
		return DownloadExport{}, nil, err
	}
	tasks, err := db.Conn.AllKazumiDownloads()
	if err != nil {
		return DownloadExport{}, nil, err
	}
	enrichKazumiDownloadTasks(tasks)
	out := DownloadExport{
		Version:    1,
		ExportedAt: time.Now().Unix(),
		Tasks:      []DownloadExportTask{},
	}
	subjectIDs := map[int64]bool{}
	files := []archiveFile{}
	seenFiles := map[string]bool{}
	addFile := func(name, path string, store bool) {
		if name == "" || path == "" || seenFiles[name] {
			return
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return
		}
		seenFiles[name] = true
		files = append(files, archiveFile{Name: name, Path: path, Store: store})
	}
	addCover := func(cover string) {
		rel, ok := coverCacheRel(cover)
		if !ok {
			return
		}
		addFile(archiveEntryName("covers", rel), filepath.Join(utils.CoverCacheDir(), filepath.FromSlash(rel)), false)
	}
	for _, task := range tasks {
		if task.Status != db.KazumiDownloadDone || strings.TrimSpace(task.Path) == "" {
			continue
		}
		out.Tasks = append(out.Tasks, DownloadExportTask{
			Name:        task.Name,
			BangumiID:   task.BangumiID,
			Episode:     task.Episode,
			Title:       task.Title,
			Rule:        task.Rule,
			Source:      task.Source,
			Path:        task.Path,
			Status:      task.Status,
			Size:        task.Size,
			CreatedTime: task.CreatedTime,
			DisplayName: task.DisplayName,
			Cover:       task.Cover,
		})
		if path, err := kazumiFileAbs(task.Path); err == nil {
			if rel, ok := safeArchiveRel(task.Path); ok {
				addFile(archiveEntryName("media", rel), path, true)
			}
		}
		addCover(task.Cover)
		subjectID := int64(task.BangumiID)
		if subjectID <= 0 && strings.TrimSpace(task.Name) != "" {
			if id, err := db.Conn.GetSubjectIDByBangumiName(task.Name); err == nil {
				subjectID = int64(id)
			}
		}
		if subjectID > 0 {
			subjectIDs[subjectID] = true
		}
	}
	// Include orphaned video files that no longer have a task record so the
	// archive is a complete backup of the Kazumi Files directory.
	if mediaFiles, err := ListKazumiFiles(); err == nil {
		for _, media := range mediaFiles {
			path, err := kazumiFileAbs(media.Path)
			if err != nil {
				continue
			}
			if rel, ok := safeArchiveRel(media.Path); ok {
				addFile(archiveEntryName("media", rel), path, true)
			}
		}
	}
	ids := make([]int64, 0, len(subjectIDs))
	for id := range subjectIDs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		cache, err := db.Conn.GetBangumiCache(id)
		if err != nil || cache == nil {
			continue
		}
		if item, ok := cacheExportSubject(*cache); ok {
			out.Subjects = append(out.Subjects, item)
		}
		if cover, ok := cacheOverviewCover(*cache); ok {
			addCover(cover)
		}
	}
	return out, files, nil
}

// ExportDownloadedBangumiArchive streams a ZIP containing the manifest, all
// downloaded videos and their cached covers.
func ExportDownloadedBangumiArchive(w io.Writer) error {
	manifest, files, err := buildDownloadedBangumi()
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	if err := writeArchiveJSON(zw, archiveManifestName, manifest); err != nil {
		_ = zw.Close()
		return err
	}
	for _, file := range files {
		if err := writeArchiveFile(zw, file.Name, file.Path, file.Store); err != nil {
			_ = zw.Close()
			return err
		}
	}
	return zw.Close()
}

// ImportDownloadedBangumiArchive restores a download-library ZIP: videos,
// cached covers, detail rows, task records and missing follow entries.
func ImportDownloadedBangumiArchive(path string) (map[string]any, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var manifest DownloadExport
	if err := readArchiveJSON(&reader.Reader, archiveManifestName, &manifest); err != nil {
		return nil, fmt.Errorf("下载库缺少 manifest.json: %w", err)
	}
	mediaCount, coverCount := 0, 0
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() || entry.Mode()&os.ModeSymlink != 0 {
			continue
		}
		switch {
		case strings.HasPrefix(entry.Name, "media/"):
			if _, err := importArchiveEntry(entry, KazumiFilesDir(), strings.TrimPrefix(entry.Name, "media/")); err == nil {
				mediaCount++
			}
		case strings.HasPrefix(entry.Name, "covers/"):
			if _, err := importArchiveEntry(entry, utils.CoverCacheDir(), strings.TrimPrefix(entry.Name, "covers/")); err == nil {
				coverCount++
			}
		}
	}
	result, err := ImportDownloadedBangumi(manifest)
	if err != nil {
		return nil, err
	}
	result["media"] = mediaCount
	result["covers"] = coverCount
	return result, nil
}

func importArchiveEntry(entry *zip.File, dir, name string) (int64, error) {
	reader, err := entry.Open()
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	return saveArchiveEntry(dir, name, reader, 0o644)
}

func cacheOverviewCover(cache db.BangumiCache) (string, bool) {
	if cache.Overview == "" {
		return "", false
	}
	var payload struct {
		Cover string `json:"cover"`
	}
	if err := json.Unmarshal([]byte(cache.Overview), &payload); err != nil {
		return "", false
	}
	cover := kazumiCoverPath(payload.Cover)
	if cover == "" {
		return "", false
	}
	return cover, true
}

func coverCacheRel(cover string) (string, bool) {
	cover = strings.ReplaceAll(strings.TrimSpace(cover), "\\", "/")
	if !strings.HasPrefix(cover, "/covers/") {
		return "", false
	}
	return safeArchiveRel(strings.TrimPrefix(cover, "/covers/"))
}

// ImportDownloadedBangumi restores task records, cached detail rows and
// minimal follow entries. File contents are handled by the archive importer.
func ImportDownloadedBangumi(snapshot DownloadExport) (map[string]any, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	existing, err := db.Conn.AllKazumiDownloads()
	if err != nil {
		return nil, err
	}
	byPath := map[string]int64{}
	byEpisode := map[string]int64{}
	for _, task := range existing {
		if path := cleanDownloadRelPath(task.Path); path != "" {
			byPath[path] = task.ID
		}
		byEpisode[downloadTaskKey(task.Name, task.Episode)] = task.ID
	}
	imported, updated, failed := 0, 0, 0
	for _, item := range snapshot.Tasks {
		name := strings.TrimSpace(item.Name)
		if name == "" || item.Episode < 0 {
			failed++
			continue
		}
		path := cleanDownloadRelPath(item.Path)
		id := int64(0)
		if path != "" {
			id = byPath[path]
		}
		if id == 0 {
			id = byEpisode[downloadTaskKey(name, item.Episode)]
		}
		record := db.KazumiDownload{
			ID:        id,
			Name:      name,
			BangumiID: item.BangumiID,
			Episode:   item.Episode,
			Title:     item.Title,
			Rule:      item.Rule,
			Source:    item.Source,
			Path:      path,
			// Only completed downloads are exported, and imported running
			// tasks would never resume. Always restore as completed.
			Status:      db.KazumiDownloadDone,
			Size:        item.Size,
			CreatedTime: item.CreatedTime,
		}
		if id != 0 {
			stored, err := db.Conn.GetKazumiDownload(id)
			if err == nil && stored != nil {
				if record.Path == "" {
					record.Path = stored.Path
				}
				if record.CreatedTime == 0 {
					record.CreatedTime = stored.CreatedTime
				}
				if record.Size <= 0 {
					record.Size = stored.Size
				}
			}
			updated++
		} else {
			imported++
		}
		if record.CreatedTime == 0 {
			record.CreatedTime = time.Now().Unix()
		}
		if err := db.Conn.SaveKazumiDownload(&record); err != nil {
			if id != 0 {
				updated--
			} else {
				imported--
			}
			failed++
			continue
		}
		if record.Path != "" {
			byPath[record.Path] = record.ID
		}
		byEpisode[downloadTaskKey(record.Name, record.Episode)] = record.ID
	}
	subjectCount, subjectFailed := importCacheSubjects(snapshot.Subjects)
	// Restore a minimal follow record for downloaded anime that are missing
	// locally, so the 追番 page's 已缓存 filter lists the imported library.
	restoredFollows := 0
	seenFollows := map[string]bool{}
	for _, item := range snapshot.Tasks {
		name := strings.TrimSpace(item.Name)
		if name == "" || seenFollows[name] {
			continue
		}
		seenFollows[name] = true
		if _, err := db.Conn.GetFollowed(name); err == nil {
			continue
		} else if err != db.ErrNotFound {
			continue
		}
		if _, err := db.Conn.GetBangumiByName(name); err == db.ErrNotFound {
			bangumiID := "kazumi:" + name
			if item.BangumiID > 0 {
				bangumiID = "bgmtv:" + strconv.Itoa(item.BangumiID)
			}
			_ = db.Conn.SaveBangumi(model.WebsiteBangumi{
				ID:            bangumiID,
				Name:          name,
				Status:        db.BangumiStatusUpdating,
				UpdateDay:     "Unknown",
				Cover:         downloadCoverField(item.Cover),
				SubtitleGroup: []model.SubtitleGroup{},
			})
		}
		followed := &db.Followed{
			BangumiName: name,
			DisplayName: item.DisplayName,
			Status:      db.FollowedStatusFollowed,
			UpdatedTime: time.Now().Unix(),
		}
		if err := db.Conn.SaveFollowed(followed); err != nil {
			continue
		}
		if item.BangumiID > 0 {
			_ = db.Conn.SaveBangumiLink(name, item.BangumiID)
		}
		restoredFollows++
	}
	return map[string]any{
		"imported": imported, "updated": updated, "failed": failed,
		"subjects": subjectCount, "subject_failed": subjectFailed,
		"follows": restoredFollows, "total": len(snapshot.Tasks),
	}, nil
}

func cleanDownloadRelPath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	value = strings.TrimLeft(value, "/")
	if value == "" {
		return ""
	}
	clean, ok := safeArchiveRel(value)
	if !ok {
		return ""
	}
	return clean
}

func downloadTaskKey(name string, episode int) string {
	return strings.TrimSpace(name) + "\x00" + strconv.Itoa(episode)
}

// downloadCoverField converts a /covers/ URL into the relative cache path
// stored on bangumi rows; localCoverPath resolves it back to /covers/... .
func downloadCoverField(cover string) string {
	cover = strings.TrimSpace(cover)
	if strings.HasPrefix(cover, "/covers/") {
		return strings.TrimPrefix(cover, "/covers/")
	}
	return cover
}

// DownloadExportMessage formats the downloaded-bangumi import result.
func DownloadExportMessage(result map[string]any) string {
	if result == nil {
		return "下载库导入完成"
	}
	message := fmt.Sprintf("下载库导入完成：新增 %v 条，更新 %v 条，失败 %v 条",
		result["imported"], result["updated"], result["failed"])
	if count, ok := result["media"].(int); ok && count > 0 {
		message += fmt.Sprintf("，视频 %d 个", count)
	}
	if count, ok := result["covers"].(int); ok && count > 0 {
		message += fmt.Sprintf("，封面 %d 张", count)
	}
	if count, ok := result["subjects"].(int); ok && count > 0 {
		message += fmt.Sprintf("，番剧信息 %d 条", count)
	}
	if count, ok := result["follows"].(int); ok && count > 0 {
		message += fmt.Sprintf("，补全追番 %d 部", count)
	}
	return message
}
