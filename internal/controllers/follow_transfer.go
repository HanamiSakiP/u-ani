package controllers

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

// FollowArchiveManifest is the manifest of a follow-list ZIP. It embeds the
// regular JSON export so a bundle can also be imported as follows-only data,
// and adds the referenced subject detail rows.
type FollowArchiveManifest struct {
	FollowExport
	Subjects []CacheExportSubject `json:"subjects,omitempty"`
}

func buildFollowArchive() (FollowArchiveManifest, []archiveFile, error) {
	exported, err := ExportFollows()
	if err != nil {
		return FollowArchiveManifest{}, nil, err
	}
	manifest := FollowArchiveManifest{FollowExport: exported}
	subjectCaches := map[int64]CacheExportSubject{}
	files := []archiveFile{}
	seen := map[string]bool{}
	addCover := func(cover string) {
		rel, ok := coverCacheRel(cover)
		if !ok {
			return
		}
		name := archiveEntryName("covers", rel)
		if seen[name] {
			return
		}
		path := filepath.Join(utils.CoverCacheDir(), filepath.FromSlash(rel))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return
		}
		seen[name] = true
		files = append(files, archiveFile{Name: name, Path: path})
	}
	for _, item := range exported.Items {
		if bangumi, err := db.Conn.GetBangumiByName(item.Name); err == nil {
			addCover(bangumi.Cover)
		}
		subjectID := int64(item.BangumiID)
		if subjectID <= 0 {
			if id, err := db.Conn.GetSubjectIDByBangumiName(item.Name); err == nil {
				subjectID = int64(id)
			}
		}
		if subjectID <= 0 {
			continue
		}
		cache, err := db.Conn.GetBangumiCache(subjectID)
		if err != nil || cache == nil {
			continue
		}
		if subject, ok := cacheExportSubject(*cache); ok {
			subjectCaches[subjectID] = subject
		}
		if cover, ok := cacheOverviewCover(*cache); ok {
			addCover(cover)
		}
	}
	ids := make([]int64, 0, len(subjectCaches))
	for id := range subjectCaches {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		manifest.Subjects = append(manifest.Subjects, subjectCaches[id])
	}
	return manifest, files, nil
}

// ExportFollowsArchive streams a follow-list ZIP with cached covers and
// subject text.
func ExportFollowsArchive(w io.Writer) error {
	manifest, files, err := buildFollowArchive()
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	if err := writeArchiveJSON(zw, archiveManifestName, manifest); err != nil {
		_ = zw.Close()
		return err
	}
	for _, file := range files {
		if err := writeArchiveFile(zw, file.Name, file.Path, false); err != nil {
			_ = zw.Close()
			return err
		}
	}
	return zw.Close()
}

// ImportFollowsArchive restores a follow-list ZIP, including cached covers and
// subject detail text.
func ImportFollowsArchive(path string) (map[string]any, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var manifest FollowArchiveManifest
	if err := readArchiveJSON(&reader.Reader, archiveManifestName, &manifest); err != nil {
		return nil, fmt.Errorf("追番备份缺少 manifest.json: %w", err)
	}
	coverCount := 0
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() || entry.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !strings.HasPrefix(entry.Name, "covers/") {
			continue
		}
		if _, err := importArchiveEntry(entry, utils.CoverCacheDir(), strings.TrimPrefix(entry.Name, "covers/")); err == nil {
			coverCount++
		}
	}
	result, err := ImportFollows(manifest.FollowExport)
	if err != nil {
		return nil, err
	}
	subjectCount, subjectFailed := importCacheSubjects(manifest.Subjects)
	result["covers"] = coverCount
	result["subjects"] = subjectCount
	result["subject_failed"] = subjectFailed
	return result, nil
}

// FollowArchiveMessage formats the follow-bundle import result.
func FollowArchiveMessage(result map[string]any) string {
	if result == nil {
		return "追番备份导入完成"
	}
	message := fmt.Sprintf("追番备份导入完成：新增 %v，更新 %v，失败 %v",
		result["imported"], result["updated"], result["failed"])
	if count, ok := result["covers"].(int); ok && count > 0 {
		message += fmt.Sprintf("，封面 %d 张", count)
	}
	if count, ok := result["subjects"].(int); ok && count > 0 {
		message += fmt.Sprintf("，文字信息 %d 条", count)
	}
	return message
}
