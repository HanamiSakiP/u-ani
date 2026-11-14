package config

import (
	"os"
	"path/filepath"
	"testing"
)

func restoreGlobals(t *testing.T) {
	t.Helper()
	oldHome, oldPath, oldCfg := UANIHome, ConfigFilePath, Cfg
	t.Cleanup(func() {
		UANIHome = oldHome
		ConfigFilePath = oldPath
		Cfg = oldCfg
	})
}

func TestLoadCreatesHomeAndConfig(t *testing.T) {
	restoreGlobals(t)
	home := filepath.Join(t.TempDir(), "uani-data")
	t.Setenv("UANI_PATH", home)
	t.Setenv("BGMI_PATH", "")

	if err := Load(); err != nil {
		t.Fatal(err)
	}
	if UANIHome != home {
		t.Errorf("UANIHome = %q, want %q", UANIHome, home)
	}

	configPath := filepath.Join(home, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected default config file to be created: %v", err)
	}
	if len(data) == 0 {
		t.Error("default config file is empty")
	}
	if Cfg.Kazumi.BangumiAcceleration != "mirror" || Cfg.Kazumi.RulesMirror != "ghProxy" || Cfg.Kazumi.ImageAcceleration != "ech" {
		t.Errorf("unexpected Kazumi defaults: %#v", Cfg.Kazumi)
	}
	if len(Cfg.Bangumi.SyncTypes) != 5 {
		t.Errorf("unexpected Bangumi sync defaults: %#v", Cfg.Bangumi)
	}

	dirs := []string{
		home,
		Cfg.SavePath,
		Cfg.TmpPath,
		Cfg.LogPath,
		Cfg.ScriptPath,
		Cfg.ToolsPath,
		Cfg.FrontStaticPath,
		filepath.Dir(Cfg.DBPath),
		filepath.Join(home, "kazumi", "rules"),
	}
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("expected directory %s to be created: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

func TestLoadDefaultsToDotUANI(t *testing.T) {
	restoreGlobals(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("UANI_PATH", "")
	t.Setenv("BGMI_PATH", "")

	if err := Load(); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".uani")
	if UANIHome != want {
		t.Errorf("UANIHome = %q, want %q", UANIHome, want)
	}
	if _, err := os.Stat(filepath.Join(want, "config.toml")); err != nil {
		t.Errorf("expected config file in default home: %v", err)
	}
}

func TestKazumiConfigSetValue(t *testing.T) {
	restoreGlobals(t)
	t.Setenv("UANI_PATH", filepath.Join(t.TempDir(), "uani"))
	t.Setenv("BGMI_PATH", "")
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	if err := SetValue([]string{"kazumi", "image_acceleration"}, "mirror"); err != nil {
		t.Fatal(err)
	}
	if Cfg.Kazumi.ImageAcceleration != "mirror" {
		t.Fatalf("image acceleration = %q", Cfg.Kazumi.ImageAcceleration)
	}
	if err := SetValue([]string{"kazumi", "rules_mirror"}, "invalid"); err == nil {
		t.Fatal("expected invalid rules mirror to fail")
	}
	if Cfg.Kazumi.RulesMirror == "invalid" {
		t.Fatal("invalid value was applied")
	}
}

func TestLoadOldConfigKeepsBangumiDefaults(t *testing.T) {
	restoreGlobals(t)
	home := filepath.Join(t.TempDir(), "uani")
	t.Setenv("UANI_PATH", home)
	t.Setenv("BGMI_PATH", "")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("data_source = \"bangumi_moe\"\ndownload_delegate = \"noop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	if len(Cfg.Bangumi.SyncTypes) != 5 {
		t.Fatalf("old config should keep Bangumi defaults: %#v", Cfg.Bangumi)
	}
}

func TestMigrateLegacyWatchingSyncTypes(t *testing.T) {
	restoreGlobals(t)
	home := filepath.Join(t.TempDir(), "uani")
	t.Setenv("UANI_PATH", home)
	t.Setenv("BGMI_PATH", "")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "data_source = \"bangumi_moe\"\ndownload_delegate = \"noop\"\n\n[bangumi]\nsync_types = [\"watching\"]\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Load(); err != nil {
		t.Fatal(err)
	}
	if len(Cfg.Bangumi.SyncTypes) != 5 {
		t.Fatalf("legacy sync types were not migrated: %#v", Cfg.Bangumi.SyncTypes)
	}
}

func TestMigrateBangumiMirrorsAddsNextDefault(t *testing.T) {
	old := Cfg.Kazumi
	defer func() { Cfg.Kazumi = old }()

	Cfg.Kazumi = KazumiConfig{
		BangumiMirrors: []string{"https://api.kazumi.fyi", "https://api.bgmapi.com"},
		BangumiMirror:  "https://api.kazumi.fyi",
	}
	if !migrateBangumiMirrors() {
		t.Fatal("legacy default mirrors should be migrated")
	}
	if len(Cfg.Kazumi.BangumiMirrors) != 3 || Cfg.Kazumi.BangumiMirrors[0] != "https://next.bangumi.lat" {
		t.Fatalf("mirrors = %#v", Cfg.Kazumi.BangumiMirrors)
	}
	if Cfg.Kazumi.BangumiMirror != "https://next.bangumi.lat" {
		t.Fatalf("active mirror = %q", Cfg.Kazumi.BangumiMirror)
	}

	Cfg.Kazumi = KazumiConfig{
		BangumiMirrors: []string{"https://example.com"},
		BangumiMirror:  "https://example.com",
	}
	if migrateBangumiMirrors() {
		t.Fatal("custom mirror list should not be migrated")
	}
	if len(Cfg.Kazumi.BangumiMirrors) != 1 || Cfg.Kazumi.BangumiMirrors[0] != "https://example.com" {
		t.Fatalf("custom mirrors = %#v", Cfg.Kazumi.BangumiMirrors)
	}
}
