package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
)

func TestKazumiDownloadUsesBuiltInHLS(t *testing.T) {
	setupControllersDB(t)
	dir := t.TempDir()
	oldSavePath, oldHome, oldPath := config.Cfg.SavePath, config.UANIHome, os.Getenv("PATH")
	config.Cfg.SavePath = dir
	config.UANIHome = filepath.Join(dir, "uani")
	t.Setenv("PATH", filepath.Join(dir, "empty-bin"))
	defer func() {
		config.Cfg.SavePath = oldSavePath
		config.UANIHome = oldHome
		os.Setenv("PATH", oldPath)
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/play":
			fmt.Fprint(w, `<html><script>var url="`+serverURL(r)+`/video.m3u8";</script></html>`)
		case "/video.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXTINF:1,\nseg0.ts\n#EXTINF:1,\nseg1.ts\n#EXT-X-ENDLIST\n")
		case "/seg0.ts":
			fmt.Fprint(w, "111")
		case "/seg1.ts":
			fmt.Fprint(w, "222")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ruleDir := filepath.Join(config.UANIHome, "kazumi", "rules")
	if err := os.MkdirAll(ruleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rule := map[string]any{"api": "9", "type": "anime", "name": "local", "version": "1.0", "baseURL": server.URL}
	raw, _ := json.Marshal(rule)
	if err := os.WriteFile(filepath.Join(ruleDir, "local.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	task, err := StartKazumiDownload(KazumiDownloadOptions{
		Rule: "local", Name: "测试番剧", Episode: 1, Title: "第01集", Source: server.URL + "/play",
	})
	if err != nil {
		t.Fatal(err)
	}
	var final db.KazumiDownload
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		item, err := db.Conn.GetKazumiDownload(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		final = *item
		if final.Status >= db.KazumiDownloadDone {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if final.Status != db.KazumiDownloadDone {
		t.Fatalf("task status = %d, error = %s", final.Status, final.Error)
	}
	path := filepath.Join(KazumiFilesDir(), final.Path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "111222" {
		t.Fatalf("downloaded data = %q", data)
	}
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
