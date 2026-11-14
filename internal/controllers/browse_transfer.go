package controllers

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/bgmtv"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

// BrowseArchiveManifest is the manifest for a Kazumi timeline/recommend
// archive. Cached covers and subject detail text travel with it so the browse
// pages work offline after importing on another instance.
type BrowseArchiveManifest struct {
	Version    int                        `json:"version"`
	ExportedAt int64                      `json:"exported_at"`
	Timeline   json.RawMessage            `json:"timeline,omitempty"`
	Recommend  map[string]json.RawMessage `json:"recommend,omitempty"`
	Subjects   []CacheExportSubject       `json:"subjects,omitempty"`
}

type browseCacheItem struct {
	ID     int          `json:"id"`
	Cover  string       `json:"cover"`
	Images bgmtv.Images `json:"images"`
}

func (item browseCacheItem) coverURL() string {
	if strings.TrimSpace(item.Cover) != "" {
		return item.Cover
	}
	for _, candidate := range []string{item.Images.Common, item.Images.Medium, item.Images.Large, item.Images.Small, item.Images.Grid} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

// buildBrowseCacheArchive collects the timeline/recommend JSON plus the
// covers and detail rows referenced by their items.
func buildBrowseCacheArchive() (BrowseArchiveManifest, []archiveFile, error) {
	timeline, recommend := exportKazumiBrowseCaches()
	manifest := BrowseArchiveManifest{
		Version:    1,
		ExportedAt: time.Now().Unix(),
		Timeline:   timeline,
		Recommend:  recommend,
	}
	subjectIDs := map[int64]bool{}
	coverSet := map[string]bool{}
	addCovers := func(items []browseCacheItem) {
		for _, item := range items {
			if item.ID > 0 {
				subjectIDs[int64(item.ID)] = true
			}
			if cover := kazumiCoverPath(item.coverURL()); cover != "" {
				coverSet[cover] = true
			}
		}
	}
	if len(timeline) > 0 {
		var days []struct {
			Items []browseCacheItem `json:"items"`
		}
		if json.Unmarshal(timeline, &days) == nil {
			for _, day := range days {
				addCovers(day.Items)
			}
		}
	}
	for _, data := range recommend {
		var items []browseCacheItem
		if json.Unmarshal(data, &items) == nil {
			addCovers(items)
		}
	}
	if err := ensureDB(); err == nil {
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
				manifest.Subjects = append(manifest.Subjects, item)
			}
			if cover, ok := cacheOverviewCover(*cache); ok {
				coverSet[cover] = true
			}
		}
	}
	files := []archiveFile{}
	seen := map[string]bool{}
	for cover := range coverSet {
		rel, ok := coverCacheRel(cover)
		if !ok {
			continue
		}
		name := archiveEntryName("covers", rel)
		if seen[name] {
			continue
		}
		path := filepath.Join(utils.CoverCacheDir(), filepath.FromSlash(rel))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			continue
		}
		seen[name] = true
		files = append(files, archiveFile{Name: name, Path: path})
	}
	return manifest, files, nil
}

// ExportBrowseCacheArchive streams a ZIP with browse caches, covers and
// cached subject detail text.
func ExportBrowseCacheArchive(w io.Writer) error {
	manifest, files, err := buildBrowseCacheArchive()
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

// ImportBrowseCacheArchive restores browse caches, covers and detail rows.
func ImportBrowseCacheArchive(path string) (map[string]any, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var manifest BrowseArchiveManifest
	if err := readArchiveJSON(&reader.Reader, archiveManifestName, &manifest); err != nil {
		return nil, fmt.Errorf("Kazumi 缓存缺少 manifest.json: %w", err)
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
	timeline, recommend := importKazumiBrowseCaches(manifest.Timeline, manifest.Recommend)
	subjectCount, subjectFailed := importCacheSubjects(manifest.Subjects)
	return map[string]any{
		"timeline": timeline, "recommend": recommend,
		"subjects": subjectCount, "subject_failed": subjectFailed,
		"covers": coverCount,
	}, nil
}
