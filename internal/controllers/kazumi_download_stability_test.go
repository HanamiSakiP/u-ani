package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/kazumi"
)

func TestRetryKazumiDownloadDoneRequiresConfirmation(t *testing.T) {
	setupControllersDB(t)
	oldSavePath := config.Cfg.SavePath
	config.Cfg.SavePath = t.TempDir()
	defer func() { config.Cfg.SavePath = oldSavePath }()

	task := &db.KazumiDownload{
		Name: "测试番剧", Episode: 1, Title: "第1集", Rule: "local",
		Source: "https://example.com/1.mp4", Path: "测试番剧/1.mp4",
		Status: db.KazumiDownloadDone, Size: 5,
	}
	if err := db.Conn.SaveKazumiDownload(task); err != nil {
		t.Fatal(err)
	}
	if _, err := RetryKazumiDownload(task.ID); !errors.Is(err, ErrKazumiAlreadyDownloaded) {
		t.Fatalf("RetryKazumiDownload error = %v, want ErrKazumiAlreadyDownloaded", err)
	}
	saved, err := db.Conn.GetKazumiDownload(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != db.KazumiDownloadDone || saved.Path != task.Path {
		t.Fatalf("completed task changed without force: %#v", saved)
	}
}

func TestStartKazumiDownloadRejectsCompletedEpisode(t *testing.T) {
	setupControllersDB(t)
	dir := t.TempDir()
	oldSavePath, oldHome := config.Cfg.SavePath, config.UANIHome
	config.Cfg.SavePath = dir
	config.UANIHome = filepath.Join(dir, "uani")
	defer func() {
		config.Cfg.SavePath = oldSavePath
		config.UANIHome = oldHome
	}()

	ruleDir := filepath.Join(config.UANIHome, "kazumi", "rules")
	if err := os.MkdirAll(ruleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rule := []byte(`{"api":"9","type":"anime","name":"local","version":"1.0","baseURL":"https://example.com"}`)
	if err := os.WriteFile(filepath.Join(ruleDir, "local.json"), rule, 0o644); err != nil {
		t.Fatal(err)
	}

	done := &db.KazumiDownload{
		Name: "测试番剧", Episode: 1, Title: "第1集", Rule: "local",
		Source: "https://example.com/1.mp4", Path: "测试番剧/1.mp4",
		Status: db.KazumiDownloadDone, Size: 5,
	}
	if err := db.Conn.SaveKazumiDownload(done); err != nil {
		t.Fatal(err)
	}

	task, err := StartKazumiDownload(KazumiDownloadOptions{
		Rule: "local", Name: "测试番剧", Episode: 1, Title: "第1集",
		Source: "https://example.com/1.mp4",
	})
	if !errors.Is(err, ErrKazumiAlreadyDownloaded) {
		t.Fatalf("StartKazumiDownload error = %v, want ErrKazumiAlreadyDownloaded", err)
	}
	if task != nil {
		t.Fatalf("StartKazumiDownload returned task for completed episode: %#v", task)
	}
	tasks, err := db.Conn.AllKazumiDownloads()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("downloads = %d, want 1 (no duplicate task created)", len(tasks))
	}
}

func TestKazumiDirectDownloadRetriesTransientFailures(t *testing.T) {
	setupControllersDB(t)
	oldSavePath := config.Cfg.SavePath
	config.Cfg.SavePath = t.TempDir()
	defer func() { config.Cfg.SavePath = oldSavePath }()
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty-bin"))

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("video-bytes"))
	}))
	defer server.Close()

	source := server.URL + "/1.mp4"
	task := &db.KazumiDownload{
		Name: "测试番剧", Episode: 1, Title: "第1集", Rule: "local",
		Source: source, Status: db.KazumiDownloadPending,
	}
	if err := db.Conn.SaveKazumiDownload(task); err != nil {
		t.Fatal(err)
	}
	runKazumiDownload(context.Background(), task.ID, &kazumi.Rule{Name: "local"}, KazumiDownloadOptions{
		Rule: "local", Name: task.Name, Episode: task.Episode, Title: task.Title, Source: source,
	})

	saved, err := db.Conn.GetKazumiDownload(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != db.KazumiDownloadDone {
		t.Fatalf("task status = %d, error = %s", saved.Status, saved.Error)
	}
	data, err := os.ReadFile(filepath.Join(KazumiFilesDir(), filepath.FromSlash(saved.Path)))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "video-bytes" {
		t.Fatalf("downloaded data = %q", data)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestKazumiFailedRetryKeepsPreviousFile(t *testing.T) {
	setupControllersDB(t)
	oldSavePath := config.Cfg.SavePath
	config.Cfg.SavePath = t.TempDir()
	defer func() { config.Cfg.SavePath = oldSavePath }()
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty-bin"))

	rel := filepath.FromSlash("测试番剧/1.mp4")
	path := filepath.Join(KazumiFilesDir(), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old-video"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "still broken", http.StatusInternalServerError)
	}))
	defer server.Close()

	task := &db.KazumiDownload{
		Name: "测试番剧", Episode: 1, Title: "第1集", Rule: "local",
		Source: server.URL + "/1.mp4", Path: "测试番剧/1.mp4",
		Status: db.KazumiDownloadDone, Size: int64(len("old-video")),
	}
	if err := db.Conn.SaveKazumiDownload(task); err != nil {
		t.Fatal(err)
	}
	runKazumiDownload(context.Background(), task.ID, &kazumi.Rule{Name: "local"}, KazumiDownloadOptions{
		Rule: "local", Name: task.Name, Episode: task.Episode, Title: task.Title, Source: task.Source,
	})

	saved, err := db.Conn.GetKazumiDownload(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != db.KazumiDownloadFailed {
		t.Fatalf("task status = %d, want failed", saved.Status)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("previous download was removed: %v", err)
	}
	if string(data) != "old-video" {
		t.Fatalf("previous download changed to %q", data)
	}
	if saved.Size != int64(len("old-video")) {
		t.Fatalf("saved size = %d, want %d", saved.Size, len("old-video"))
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(path) {
			t.Fatalf("unexpected leftover file after failed retry: %s", entry.Name())
		}
	}
}

func TestKazumiDownloadReportsProgress(t *testing.T) {
	setupControllersDB(t)
	oldSavePath := config.Cfg.SavePath
	config.Cfg.SavePath = t.TempDir()
	defer func() { config.Cfg.SavePath = oldSavePath }()
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty-bin"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		flusher, _ := w.(http.Flusher)
		chunk := make([]byte, 64*1024)
		for i := 0; i < 20; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(150 * time.Millisecond)
		}
	}))
	defer server.Close()

	source := server.URL + "/1.mp4"
	task := &db.KazumiDownload{
		Name: "测试番剧", Episode: 1, Title: "第1集", Rule: "local",
		Source: source, Status: db.KazumiDownloadPending,
	}
	if err := db.Conn.SaveKazumiDownload(task); err != nil {
		t.Fatal(err)
	}
	go runKazumiDownload(context.Background(), task.ID, &kazumi.Rule{Name: "local"}, KazumiDownloadOptions{
		Rule: "local", Name: task.Name, Episode: task.Episode, Title: task.Title, Source: source,
	})

	distinct := map[int64]bool{}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		saved, err := db.Conn.GetKazumiDownload(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Size > 0 {
			distinct[saved.Size] = true
		}
		if saved.Status == db.KazumiDownloadDone {
			break
		}
		if saved.Status == db.KazumiDownloadFailed {
			t.Fatalf("download failed: %s", saved.Error)
		}
	}
	saved, err := db.Conn.GetKazumiDownload(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != db.KazumiDownloadDone {
		t.Fatalf("task status = %d, error = %s", saved.Status, saved.Error)
	}
	if len(distinct) < 2 {
		t.Fatalf("progress size never changed during download: %v", distinct)
	}
	if saved.TotalSize != saved.Size {
		t.Fatalf("total size = %d, want %d", saved.TotalSize, saved.Size)
	}
}

func TestParseYtDlpHLSProgressUsesFragments(t *testing.T) {
	clearKazumiProgressHint(9001)
	defer clearKazumiProgressHint(9001)
	parseYtDlpProgress(9001, "uani-progress 1024 NA 2048.5 32 320")
	downloaded, total, speed, progress, fresh := getKazumiProgressHint(9001)
	if downloaded != 1024 || total != 0 || speed != 2048 || !fresh {
		t.Fatalf("downloaded=%d total=%d speed=%d fresh=%v", downloaded, total, speed, fresh)
	}
	if progress != 10 {
		t.Fatalf("progress = %d, want 10", progress)
	}
}
