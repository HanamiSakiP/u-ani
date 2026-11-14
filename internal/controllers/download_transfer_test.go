package controllers

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

func exportTestCover(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join(utils.CoverCacheDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatal(err)
	}
	return "/covers/" + rel
}

func writeTestDownloadArchive(t *testing.T) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "downloads-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	if err := ExportDownloadedBangumiArchive(file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}

func TestExportAndImportDownloadedBangumiArchive(t *testing.T) {
	setupControllersDB(t)
	oldSavePath, oldCachePath := config.Cfg.SavePath, config.Cfg.CachePath
	config.Cfg.SavePath = t.TempDir()
	config.Cfg.CachePath = t.TempDir()
	defer func() {
		config.Cfg.SavePath = oldSavePath
		config.Cfg.CachePath = oldCachePath
	}()

	cover := exportTestCover(t, "test/cover.png")
	if err := db.Conn.SaveBangumiCache(&db.BangumiCache{
		SubjectID: 101,
		Overview:  `{"name":"测试番剧","cover":"` + cover + `"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveKazumiDownload(&db.KazumiDownload{
		Name: "测试番剧", BangumiID: 101, Episode: 1, Title: "第一话",
		Path: "测试番剧/1.mp4", Status: db.KazumiDownloadDone, Size: 6,
	}); err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(KazumiFilesDir(), "测试番剧", "1.mp4")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("videos"), 0o644); err != nil {
		t.Fatal(err)
	}

	archivePath := writeTestDownloadArchive(t)
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, entry := range reader.File {
		names[entry.Name] = true
	}
	reader.Close()
	for _, name := range []string{archiveManifestName, "media/测试番剧/1.mp4", "covers/test/cover.png"} {
		if !names[name] {
			t.Fatalf("archive is missing %s: %#v", name, names)
		}
	}

	// Simulate importing into another instance: fresh database and no files.
	setupControllersDB(t)
	if err := os.RemoveAll(KazumiFilesDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(utils.CoverCacheDir()); err != nil {
		t.Fatal(err)
	}
	result, err := ImportDownloadedBangumiArchive(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if result["imported"] != 1 || result["media"] != 1 || result["covers"] != 1 || result["subjects"] != 1 {
		t.Fatalf("result = %#v", result)
	}
	restored, err := os.ReadFile(filepath.Join(KazumiFilesDir(), "测试番剧", "1.mp4"))
	if err != nil || string(restored) != "videos" {
		t.Fatalf("restored media = %q, err = %v", restored, err)
	}
	if _, err := os.Stat(filepath.Join(utils.CoverCacheDir(), "test", "cover.png")); err != nil {
		t.Fatalf("cover was not restored: %v", err)
	}
	tasks, err := db.Conn.AllKazumiDownloads()
	if err != nil || len(tasks) != 1 || tasks[0].Path != "测试番剧/1.mp4" {
		t.Fatalf("tasks = %#v, err = %v", tasks, err)
	}
	if cache, err := db.Conn.GetBangumiCache(101); err != nil || cache.Overview == "" {
		t.Fatalf("cache = %#v, err = %v", cache, err)
	}
	if _, err := db.Conn.GetFollowedNotDeleted("测试番剧"); err != nil {
		t.Fatalf("downloaded anime was not restored to the follow list: %v", err)
	}
	if b, err := db.Conn.GetBangumiByName("测试番剧"); err != nil || b.Cover == "" {
		t.Fatalf("bangumi row = %#v, err = %v", b, err)
	}

	// Importing the same archive again updates instead of duplicating.
	result, err = ImportDownloadedBangumiArchive(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if result["imported"] != 0 || result["updated"] != 1 || result["media"] != 1 {
		t.Fatalf("second result = %#v", result)
	}
	tasks, _ = db.Conn.AllKazumiDownloads()
	if len(tasks) != 1 {
		t.Fatalf("tasks after re-import = %#v", tasks)
	}
}

func TestImportDownloadedBangumiRejectsUnsafePaths(t *testing.T) {
	setupControllersDB(t)
	oldSavePath := config.Cfg.SavePath
	config.Cfg.SavePath = t.TempDir()
	defer func() { config.Cfg.SavePath = oldSavePath }()

	result, err := ImportDownloadedBangumi(DownloadExport{
		Tasks: []DownloadExportTask{{
			Name: "测试", Episode: 1, Path: "../../escape.mp4",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["imported"] != 1 {
		t.Fatalf("result = %#v", result)
	}
	tasks, _ := db.Conn.AllKazumiDownloads()
	if len(tasks) != 1 || tasks[0].Path != "" {
		t.Fatalf("tasks = %#v", tasks)
	}
	if _, ok := safeArchiveRel("../../escape.png"); ok {
		t.Fatal("unsafe archive path was accepted")
	}
}

func writeControllerZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestImportDownloadedBangumiArchiveRejectsTraversal(t *testing.T) {
	setupControllersDB(t)
	oldSavePath, oldCachePath := config.Cfg.SavePath, config.Cfg.CachePath
	config.Cfg.SavePath = t.TempDir()
	config.Cfg.CachePath = t.TempDir()
	defer func() {
		config.Cfg.SavePath = oldSavePath
		config.Cfg.CachePath = oldCachePath
	}()

	archive := filepath.Join(t.TempDir(), "unsafe.zip")
	writeControllerZip(t, archive, map[string]string{
		"manifest.json":           `{"version":1,"tasks":[{"name":"测试","episode":1,"path":"ok/1.mp4","status":2}]}`,
		"media/../../escape.mp4":  "bad",
		"covers/../../escape.png": "bad",
		"media/ok/1.mp4":          "good",
		"covers/ok/cover.png":     "good",
	})
	result, err := ImportDownloadedBangumiArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	if result["media"] != 1 || result["covers"] != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(KazumiFilesDir()), "escape.mp4")); !os.IsNotExist(err) {
		t.Fatal("unsafe media path was extracted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(utils.CoverCacheDir()), "escape.png")); !os.IsNotExist(err) {
		t.Fatal("unsafe cover path was extracted")
	}
	if _, err := os.Stat(filepath.Join(KazumiFilesDir(), "ok", "1.mp4")); err != nil {
		t.Fatalf("safe media path was not extracted: %v", err)
	}
}
