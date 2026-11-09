package download

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/downloader"
	"github.com/HanamiSakiP/u-ani/internal/model"
)

func setupDownloadDB(t *testing.T) string {
	t.Helper()
	oldSave, oldDB, oldDelegate, oldFormatter := config.Cfg.SavePath, config.Cfg.DBPath, config.Cfg.DownloadDelegate, config.Cfg.EnablePathFormatter
	if db.Conn != nil {
		_ = db.Conn.Close()
		db.Conn = nil
	}
	dir := t.TempDir()
	config.Cfg.SavePath = filepath.Join(dir, "bangumi")
	config.Cfg.DBPath = filepath.Join(dir, "bangumi.db")
	config.Cfg.DownloadDelegate = "noop"
	config.Cfg.EnablePathFormatter = false
	driverMu.Lock()
	drivers = map[string]downloader.Service{}
	driverMu.Unlock()
	conn, err := db.New()
	if err != nil {
		t.Fatal(err)
	}
	db.Conn = conn
	t.Cleanup(func() {
		_ = conn.Close()
		db.Conn = nil
		config.Cfg.SavePath = oldSave
		config.Cfg.DBPath = oldDB
		config.Cfg.DownloadDelegate = oldDelegate
		config.Cfg.EnablePathFormatter = oldFormatter
	})
	return dir
}

func TestDownloadEpisodeRecordsTask(t *testing.T) {
	setupDownloadDB(t)
	e := model.Episode{
		Name:     "Test Anime",
		Episode:  3,
		Title:    "Test Anime 03",
		Download: "magnet:?xt=urn:btih:1",
	}
	if !DownloadEpisode(e) {
		t.Fatal("DownloadEpisode failed")
	}
	dl, err := db.Conn.GetDownload(e.Name, e.Download, e.Episode, e.Title)
	if err != nil {
		t.Fatal(err)
	}
	if dl.Status != db.DownloadStatusDownloading {
		t.Errorf("status = %d, want downloading", dl.Status)
	}
	if dl.TaskID == nil {
		t.Error("expected a non-nil task id")
	}
	want := filepath.Join(config.Cfg.SavePath, "Test Anime", "3")
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Errorf("expected save directory %s: %v", want, err)
	}
}

func TestDownloadDownloadsRecordsTasks(t *testing.T) {
	setupDownloadDB(t)
	data := []db.Download{
		{BangumiName: "Show", Episode: 1, Title: "Show 01", Download: "magnet:?xt=urn:btih:a"},
		{BangumiName: "Show", Episode: 2, Title: "Show 02", Download: "magnet:?xt=urn:btih:b"},
	}
	DownloadDownloads(data)

	for _, want := range data {
		dl, err := db.Conn.GetDownload(want.BangumiName, want.Download, want.Episode, want.Title)
		if err != nil {
			t.Fatal(err)
		}
		if dl.Status != db.DownloadStatusDownloading {
			t.Errorf("%s status = %d, want downloading", dl.Title, dl.Status)
		}
		if dl.TaskID == nil {
			t.Errorf("%s expected non-nil task id", dl.Title)
		}
	}
}
