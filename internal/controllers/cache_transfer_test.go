package controllers

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

func TestExportAndImportCacheData(t *testing.T) {
	setupControllersDB(t)
	oldSavePath, oldCachePath := config.Cfg.SavePath, config.Cfg.CachePath
	config.Cfg.SavePath = t.TempDir()
	config.Cfg.CachePath = t.TempDir()
	defer func() {
		config.Cfg.SavePath = oldSavePath
		config.Cfg.CachePath = oldCachePath
	}()

	for _, item := range []*db.BangumiCache{
		{SubjectID: 101, Overview: `{"name":"A"}`, Comments: `{"items":[{"comment":"好看"}]}`},
		{SubjectID: 102, Staff: `[{"name":"导演"}]`},
	} {
		if err := db.Conn.SaveBangumiCache(item); err != nil {
			t.Fatal(err)
		}
	}
	cacheDir := KazumiCacheDir()
	if err := os.WriteFile(filepath.Join(cacheDir, "timeline.json"), []byte(`{"days":[{"weekday":{"id":1}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "recommend_v2_all.json"), []byte(`{"items":[{"id":1}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ExportCacheData()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Subjects) != 2 {
		t.Fatalf("subjects = %#v", snapshot.Subjects)
	}
	if len(snapshot.Timeline) == 0 {
		t.Fatal("timeline was not exported")
	}
	if _, ok := snapshot.Recommend["recommend_v2_all.json"]; !ok {
		t.Fatalf("recommend = %#v", snapshot.Recommend)
	}

	// Importing must overwrite the named rows, add new rows, and keep
	// unrelated cached subjects untouched.
	result, err := ImportCacheData(CacheExport{
		Subjects: []CacheExportSubject{
			{SubjectID: 101, Overview: `{"name":"A2"}`},
			{SubjectID: 103, Relations: `[{"name":"续集"}]`},
		},
		Timeline: json.RawMessage(`{"days":[{"weekday":{"id":2}}]}`),
		Recommend: map[string]json.RawMessage{
			"recommend_v2_new.json": json.RawMessage(`{"items":[{"id":2}]}`),
			"../evil.json":          json.RawMessage(`{"items":[]}`),
			"recommend_bad.txt":     json.RawMessage(`not-json`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["imported"] != 2 || result["timeline"] != true || result["recommend"] != 1 {
		t.Fatalf("result = %#v", result)
	}
	if cache, err := db.Conn.GetBangumiCache(101); err != nil || !strings.Contains(cache.Overview, "A2") {
		t.Fatalf("subject 101 cache = %#v, err = %v", cache, err)
	}
	if _, err := db.Conn.GetBangumiCache(102); err != nil {
		t.Fatalf("unrelated subject 102 was removed: %v", err)
	}
	if cache, err := db.Conn.GetBangumiCache(103); err != nil || cache.Relations == "" {
		t.Fatalf("subject 103 cache = %#v, err = %v", cache, err)
	}
	timeline, err := os.ReadFile(filepath.Join(cacheDir, "timeline.json"))
	if err != nil || !strings.Contains(string(timeline), `"id":2`) {
		t.Fatalf("timeline = %q, err = %v", timeline, err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "recommend_v2_new.json")); err != nil {
		t.Fatalf("new recommend cache missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cacheDir), "evil.json")); !os.IsNotExist(err) {
		t.Fatal("path traversal recommend cache should have been rejected")
	}
}

func TestImportCacheDataFromFollowSnapshotAndMessage(t *testing.T) {
	setupControllersDB(t)
	oldCachePath := config.Cfg.CachePath
	config.Cfg.CachePath = t.TempDir()
	defer func() { config.Cfg.CachePath = oldCachePath }()

	result, err := ImportCacheData(CacheExport{
		Items: []FollowExportItem{{
			BangumiID: 201,
			Cache: &FollowCache{
				Overview: `{"name":"B"}`,
				Comments: `{"items":[{"comment":"不错"}]}`,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["imported"] != 1 {
		t.Fatalf("result = %#v", result)
	}
	cache, err := db.Conn.GetBangumiCache(201)
	if err != nil || !strings.Contains(cache.Comments, "不错") {
		t.Fatalf("cache = %#v, err = %v", cache, err)
	}
	message := CacheExportMessage(result)
	if !strings.Contains(message, "番剧详情 1 条") {
		t.Fatalf("message = %q", message)
	}
}

func TestExportAndImportBrowseCacheArchive(t *testing.T) {
	setupControllersDB(t)
	oldCachePath := config.Cfg.CachePath
	config.Cfg.CachePath = t.TempDir()
	defer func() { config.Cfg.CachePath = oldCachePath }()

	rawCover := "https://lain.bgm.tv/pic/cover/l/x.jpg"
	_, coverFile := utils.ConvertCoverURLToPath(rawCover)
	if err := os.MkdirAll(filepath.Dir(coverFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coverFile, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, 0o644); err != nil {
		t.Fatal(err)
	}
	recommendCover := exportTestCover(t, "test/browse.png")
	if err := db.Conn.SaveBangumiCache(&db.BangumiCache{
		SubjectID: 101,
		Overview:  `{"name":"时间表番剧","cover":"` + rawCover + `"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveBangumiCache(&db.BangumiCache{
		SubjectID: 102,
		Overview:  `{"name":"推荐番剧","cover":"` + recommendCover + `"}`,
	}); err != nil {
		t.Fatal(err)
	}
	dir := KazumiCacheDir()
	if err := os.WriteFile(filepath.Join(dir, "timeline.json"), []byte(`[{"items":[{"id":101,"images":{"large":"`+rawCover+`"}}]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recommend_v2_all.json"), []byte(`[{"id":102,"cover":"`+recommendCover+`"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "browse.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExportBrowseCacheArchive(file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	covers := 0
	for _, entry := range reader.File {
		if strings.HasPrefix(entry.Name, "covers/") {
			covers++
		}
	}
	reader.Close()
	if covers < 2 {
		t.Fatalf("archive covers = %d, want at least 2", covers)
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(coverFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(utils.CoverCacheDir(), "test", "browse.png")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	result, err := ImportBrowseCacheArchive(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if result["timeline"] != true || result["recommend"] != 1 || result["subjects"] != 2 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "timeline.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "recommend_v2_all.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(coverFile); err != nil {
		t.Fatalf("timeline cover was not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(utils.CoverCacheDir(), "test", "browse.png")); err != nil {
		t.Fatalf("recommend cover was not restored: %v", err)
	}
	if cache, err := db.Conn.GetBangumiCache(101); err != nil || cache.Overview == "" {
		t.Fatalf("subject 101 cache missing: %#v, %v", cache, err)
	}
	if message := BrowseCacheExportMessage(result); !strings.Contains(message, "时间表") || !strings.Contains(message, "封面") {
		t.Fatalf("message = %q", message)
	}
}
