package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersionAndCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "1.0.0", 0},
		{"1.2.0", "1.1.9", 1},
		{"1.0.0", "1.0.0-rc1", 1},
		{"1.0.0-rc2", "1.0.0-rc1", 1},
		{"1.0.0-rc.10", "1.0.0-rc.9", 1},
		{"1.0.0-rc10", "1.0.0-rc9", -1},
		{"1.0.0-rc1", "1.0.0-beta2", 1},
		{"1.0.0-beta.2", "1.0.0-beta.11", -1},
		{"v2.0.0+build.5", "2.0.0", 0},
		{"0.9.9", "1.0.0", -1},
	}
	for _, tc := range cases {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := Newer(tc.a, tc.b); got != (tc.want > 0) {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want > 0)
		}
	}
}

func TestEscapeModulePath(t *testing.T) {
	got := escapeModulePath("github.com/HanamiSakiP/u-ani")
	want := "github.com/!hanami!saki!p/u-ani"
	if got != want {
		t.Fatalf("escapeModulePath() = %q, want %q", got, want)
	}
}

func TestValidTarget(t *testing.T) {
	for _, target := range []string{"latest", "v1.2.3", "1.2.3", "master", "v1.2.3-rc.1"} {
		if !validTarget(target) {
			t.Errorf("validTarget(%q) = false, want true", target)
		}
	}
	for _, target := range []string{"", "../evil", "v1.2.3; rm -rf /", "a b"} {
		if validTarget(target) {
			t.Errorf("validTarget(%q) = true, want false", target)
		}
	}
}

func TestSourceVersionAndReady(t *testing.T) {
	dir := t.TempDir()
	if SourceReady(dir) {
		t.Fatal("empty directory should not be ready")
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "uani"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/uani\n")
	writeFile(t, filepath.Join(dir, "cmd", "uani", "main.go"), "package main\n")
	writeFile(t, filepath.Join(dir, "internal", "config", "config.go"), "package config\n\nconst Version = \"1.2.3\"\n")
	if !SourceReady(dir) {
		t.Fatal("source tree should be ready")
	}
	if got := SourceVersion(dir); got != "1.2.3" {
		t.Fatalf("SourceVersion() = %q", got)
	}
	writeFile(t, filepath.Join(dir, SourceVersionFile), "v1.4.0\n")
	if got := SourceVersion(dir); got != "v1.4.0" {
		t.Fatalf("marker version = %q", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRejectsInvalidTargetBeforeRunningGo(t *testing.T) {
	_, err := Install(context.Background(), "../evil")
	if err == nil || !strings.Contains(err.Error(), "invalid target") {
		t.Fatalf("Install() error = %v, want invalid target", err)
	}
}
