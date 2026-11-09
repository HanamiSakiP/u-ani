package postprocessor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
)

func setupDB(t *testing.T, formatter string) string {
	t.Helper()
	oldSave, oldDB, oldFormatter := config.Cfg.SavePath, config.Cfg.DBPath, config.Cfg.PathFormatter
	if db.Conn != nil {
		_ = db.Conn.Close()
		db.Conn = nil
	}
	dir := t.TempDir()
	config.Cfg.SavePath = filepath.Join(dir, "bangumi")
	config.Cfg.DBPath = filepath.Join(dir, "bangumi.db")
	config.Cfg.PathFormatter = formatter
	conn, err := db.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		db.Conn = nil
		config.Cfg.SavePath = oldSave
		config.Cfg.DBPath = oldDB
		config.Cfg.PathFormatter = oldFormatter
	})
	return dir
}

func withConfig(t *testing.T, savePath, formatter string) {
	t.Helper()
	oldSave, oldFormatter := config.Cfg.SavePath, config.Cfg.PathFormatter
	config.Cfg.SavePath = savePath
	config.Cfg.PathFormatter = formatter
	t.Cleanup(func() {
		config.Cfg.SavePath = oldSave
		config.Cfg.PathFormatter = oldFormatter
	})
}

func TestFormatPath(t *testing.T) {
	withConfig(t, "/bangumi", "{name}/S{season:02d}/E{episode:02d}.{suffix}")
	got, err := FormatPath("名侦探柯南", 1, 5, "mp4", "test title")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.FromSlash("/bangumi/名侦探柯南/S01/E05.mp4")
	if got != want {
		t.Errorf("FormatPath = %q, want %q", got, want)
	}
}

func TestFormatPathCustom(t *testing.T) {
	withConfig(t, "/data/anime", "{name}/Season {season}/{title}.{suffix}")
	got, err := FormatPath("进击的巨人", 3, 1, "mkv", "Episode 01")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.FromSlash("/data/anime/进击的巨人/Season 3/Episode 01.mkv")
	if got != want {
		t.Errorf("FormatPath = %q, want %q", got, want)
	}
}

func TestPickVideoFile(t *testing.T) {
	cases := []struct {
		files []string
		want  string
	}{
		{[]string{"/tmp/sub.ass", "/tmp/ep01.mp4", "/tmp/ep02.mkv"}, "/tmp/ep01.mp4"},
		{[]string{"/tmp/readme.txt", "/tmp/data.nfo"}, "/tmp/readme.txt"},
		{[]string{}, ""},
	}
	for _, tc := range cases {
		if got := pickVideoFile(tc.files); got != tc.want {
			t.Errorf("pickVideoFile(%v) = %q, want %q", tc.files, got, tc.want)
		}
	}
}

func TestCleanupDownloadDirOnlyInsideDownloads(t *testing.T) {
	root := t.TempDir()
	withConfig(t, root, "{name}")

	inside := filepath.Join(root, ".downloads", "uuid-123")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(inside, "file.mp4")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanupDownloadDir([]string{file})
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed", inside)
	}

	outside := filepath.Join(root, "keep")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "keep.mkv")
	cleanupDownloadDir([]string{outsideFile})
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("expected %s to be kept: %v", outside, err)
	}
}

func TestMoveToFormattedPathUsesFollowedMetadata(t *testing.T) {
	dir := setupDB(t, "{name}/S{season:02d}/E{episode:02d}.{suffix}")

	followed := &db.Followed{
		BangumiName:   "Test Anime [Sub]",
		Episodes:      map[int]bool{},
		Status:        db.FollowedStatusFollowed,
		Season:        2,
		EpisodeOffset: 10,
		DisplayName:   "Test Anime",
	}
	if err := db.Conn.SaveFollowed(followed); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(dir, "download", "episode.mp4")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}

	dl := &db.Download{
		BangumiName: "Test Anime [Sub]",
		Title:       "Test Anime - 05",
		Episode:     5,
	}
	ok, err := MoveToFormattedPath(dl, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected move to succeed")
	}

	target := filepath.Join(config.Cfg.SavePath, "Test Anime", "S02", "E15.mp4")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected target file %s: %v", target, err)
	}
	if string(data) != "video" {
		t.Errorf("target content = %q, want %q", data, "video")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("expected source %s to be moved away", src)
	}
}

func TestMoveToFormattedPathSkipsMissingFile(t *testing.T) {
	setupDB(t, "{name}.{suffix}")
	dl := &db.Download{BangumiName: "Test Anime", Episode: 1}
	ok, err := MoveToFormattedPath(dl, []string{filepath.Join(t.TempDir(), "missing.mp4")})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected move to be skipped for missing file")
	}
}
