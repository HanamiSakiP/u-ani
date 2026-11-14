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
	"github.com/HanamiSakiP/u-ani/internal/model"
)

func TestExportAndImportFollowsArchive(t *testing.T) {
	setupControllersDB(t)
	oldSavePath, oldCachePath := config.Cfg.SavePath, config.Cfg.CachePath
	config.Cfg.SavePath = t.TempDir()
	config.Cfg.CachePath = t.TempDir()
	defer func() {
		config.Cfg.SavePath = oldSavePath
		config.Cfg.CachePath = oldCachePath
	}()

	cover := exportTestCover(t, "test/follow.png")
	if err := db.Conn.SaveBangumi(model.WebsiteBangumi{
		ID: "b1", Name: "测试番剧", Status: db.BangumiStatusUpdating,
		Cover: strings.TrimPrefix(cover, "/covers/"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveBangumiLink("测试番剧", 101); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveFollowed(&db.Followed{
		BangumiName: "测试番剧", DisplayName: "测试番剧 第二季",
		Status: db.FollowedStatusFollowed, UpdatedTime: 1700000000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveBangumiCache(&db.BangumiCache{
		SubjectID: 101,
		Overview:  `{"name":"测试番剧","cover":"` + cover + `"}`,
		Staff:     `[{"name":"导演"}]`,
	}); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(t.TempDir(), "follows.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExportFollowsArchive(file); err != nil {
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
	foundManifest, foundCover := false, false
	for _, entry := range reader.File {
		if entry.Name == archiveManifestName {
			foundManifest = true
			opened, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			var manifest FollowArchiveManifest
			if err := json.NewDecoder(opened).Decode(&manifest); err != nil {
				opened.Close()
				t.Fatal(err)
			}
			opened.Close()
			if len(manifest.Items) != 1 || manifest.Items[0].Name != "测试番剧" || len(manifest.Subjects) != 1 {
				t.Fatalf("manifest = %#v", manifest)
			}
		}
		if entry.Name == "covers/test/follow.png" {
			foundCover = true
		}
	}
	reader.Close()
	if !foundManifest || !foundCover {
		t.Fatalf("archive manifest=%v cover=%v", foundManifest, foundCover)
	}

	// Target instance has no follow entry, cached cover or subject text.
	setupControllersDB(t)
	if err := os.Remove(filepath.Join(config.Cfg.CachePath, "covers", "test", "follow.png")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	result, err := ImportFollowsArchive(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if result["imported"] != 1 || result["covers"] != 1 || result["subjects"] != 1 {
		t.Fatalf("result = %#v", result)
	}
	followed, err := db.Conn.GetFollowed("测试番剧")
	if err != nil || followed.DisplayName != "测试番剧 第二季" {
		t.Fatalf("followed = %#v, err = %v", followed, err)
	}
	cache, err := db.Conn.GetBangumiCache(101)
	if err != nil || !strings.Contains(cache.Overview, "测试番剧") || cache.Staff == "" {
		t.Fatalf("cache = %#v, err = %v", cache, err)
	}
	if _, err := os.Stat(filepath.Join(config.Cfg.CachePath, "covers", "test", "follow.png")); err != nil {
		t.Fatalf("cover was not restored: %v", err)
	}
	if message := FollowArchiveMessage(result); !strings.Contains(message, "封面") || !strings.Contains(message, "文字信息") {
		t.Fatalf("message = %q", message)
	}
}
