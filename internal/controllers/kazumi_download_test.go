package controllers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/model"
)

func TestKazumiFilesListAndDelete(t *testing.T) {
	setupControllersDB(t)
	oldSavePath := config.Cfg.SavePath
	config.Cfg.SavePath = t.TempDir()
	defer func() { config.Cfg.SavePath = oldSavePath }()

	path := filepath.Join(KazumiFilesDir(), "测试番剧", "1.mp4")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := ListKazumiFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "测试番剧/1.mp4" || files[0].Size != 5 {
		t.Fatalf("unexpected files: %#v", files)
	}
	if _, err := KazumiFileAbs("../escape.mp4"); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
	if err := DeleteKazumiFile(files[0].Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file was not deleted")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("empty episode directory was not removed")
	}
}

func TestSafeKazumiName(t *testing.T) {
	if got := safeKazumiName("  a/b:c*?  "); got != "a_b_c__" {
		t.Fatalf("safeKazumiName = %q", got)
	}
	if got := safeKazumiName("   "); got == "" {
		t.Fatal("empty name should fall back to kazumi-<timestamp>")
	}
}

func TestDeleteKazumiDownloadRemovesTaskAndFile(t *testing.T) {
	setupControllersDB(t)
	oldSavePath := config.Cfg.SavePath
	config.Cfg.SavePath = t.TempDir()
	defer func() { config.Cfg.SavePath = oldSavePath }()

	path := filepath.Join(KazumiFilesDir(), "番剧", "1.mp4")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := &db.KazumiDownload{
		Name: "番剧", Episode: 1, Title: "第1集", Rule: "local", Source: "https://example.com/1",
		Path: "番剧/1.mp4", Status: db.KazumiDownloadFailed,
	}
	if err := db.Conn.SaveKazumiDownload(task); err != nil {
		t.Fatal(err)
	}
	if err := DeleteKazumiDownload(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn.GetKazumiDownload(task.ID); err != db.ErrNotFound {
		t.Fatalf("task still exists: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("downloaded file was not removed")
	}
}

func TestCleanupKazumiEpisodeFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"1.mp4", "1.part", "1.ts", "2.mp4", "2.part"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cleanupKazumiEpisodeFiles(dir, 1, filepath.Join(dir, "1.mp4"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	if !names["1.mp4"] || !names["2.mp4"] || !names["2.part"] || names["1.part"] || names["1.ts"] {
		t.Fatalf("unexpected files after cleanup: %v", names)
	}
}

func TestMergeWebDAVCollectibles(t *testing.T) {
	local := []webdavCollectible{{BangumiID: 1, Type: 3, UpdatedAt: 100}, {BangumiID: 2, Type: 1, UpdatedAt: 300}}
	remote := []webdavCollectible{{BangumiID: 1, Type: 2, UpdatedAt: 200}, {BangumiID: 2, Type: 4, UpdatedAt: 100}, {BangumiID: 3, Type: 5, UpdatedAt: 50}}
	merged := mergeCollectibles(local, remote)
	if len(merged) != 3 {
		t.Fatalf("merged len = %d", len(merged))
	}
	byID := map[int]int{}
	for _, item := range merged {
		byID[item.BangumiID] = item.Type
	}
	if byID[1] != 2 || byID[2] != 1 || byID[3] != 5 {
		t.Fatalf("merged = %#v", merged)
	}
}

func TestRepairKazumiDownloadLinksByName(t *testing.T) {
	setupControllersDB(t)
	addBangumi(t, model.WebsiteBangumi{ID: "repair-test", Name: "测试番剧", UpdateDay: "Mon"})
	if result := Add("测试番剧", intPtr(0), nil, nil, nil); result.Status != "success" {
		t.Fatalf("Add status = %q (%s)", result.Status, result.Message)
	}
	task := &db.KazumiDownload{
		Name: "[字幕组] 测试番剧 - 01", Episode: 1, Title: "第1集",
		Rule: "local", Source: "https://example.com/1",
		Path: "测试番剧/1.mp4", Status: db.KazumiDownloadDone,
	}
	if err := db.Conn.SaveKazumiDownload(task); err != nil {
		t.Fatal(err)
	}
	repairKazumiDownloadLinks([]db.KazumiDownload{*task})
	saved, err := db.Conn.GetKazumiDownload(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != "测试番剧" {
		t.Fatalf("download name = %q, want subscription name", saved.Name)
	}
}

func TestKazumiVideosForNames(t *testing.T) {
	setupControllersDB(t)
	for _, item := range []db.KazumiDownload{
		{Name: "测试番剧", Episode: 1, Title: "第1集", Rule: "local", Source: "x", Path: "测试番剧/1.mp4", Status: db.KazumiDownloadDone, BangumiID: 42},
		{Name: "别名番剧", Episode: 2, Title: "第2集", Rule: "local", Source: "y", Path: "别名番剧/2.mp4", Status: db.KazumiDownloadDone},
		{Name: "测试番剧", Episode: 3, Title: "第3集", Rule: "local", Source: "z", Path: "测试番剧/3.mp4", Status: db.KazumiDownloadFailed, BangumiID: 42},
	} {
		task := item
		if err := db.Conn.SaveKazumiDownload(&task); err != nil {
			t.Fatal(err)
		}
	}
	videos, err := db.Conn.KazumiVideosForNames([]string{"测试番剧", "别名番剧"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) != 2 || videos[1] != "测试番剧/1.mp4" || videos[2] != "别名番剧/2.mp4" {
		t.Fatalf("videos = %#v", videos)
	}
	if _, ok := videos[3]; ok {
		t.Fatal("failed task must not be returned as a downloaded video")
	}
}
