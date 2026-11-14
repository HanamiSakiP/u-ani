package selfupdate

import (
	"archive/tar"
	"bytes"
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

func TestNewestTag(t *testing.T) {
	names := []string{"nightly", "v1.0.0", "v1.2.0-rc1", "v1.1.9", "v1.2.0"}
	if got := newestTag(names); got != "v1.2.0" {
		t.Fatalf("newestTag() = %q, want v1.2.0", got)
	}
	if got := newestTag([]string{"latest", "main"}); got != "" {
		t.Fatalf("newestTag() = %q, want empty", got)
	}
}

func TestGithubRef(t *testing.T) {
	cases := map[string]string{
		"v1.0.1":      "refs/tags/v1.0.1",
		"1.0.1":       "refs/tags/1.0.1",
		"v1.0.1-rc.1": "refs/tags/v1.0.1-rc.1",
		"master":      "master",
		"feature/new": "feature/new",
	}
	for version, want := range cases {
		if got := githubRef(version); got != want {
			t.Errorf("githubRef(%q) = %q, want %q", version, got, want)
		}
	}
}

func TestExtractTarStripsTopLevelAndRejectsEscape(t *testing.T) {
	archive := func(name, content string) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if content == "" {
			if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	dir := t.TempDir()
	if err := extractTar(bytes.NewReader(archive("u-ani-1.0.1/go.mod", "module example.com/uani\n")), dir); err != nil {
		t.Fatalf("extractTar() error = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || string(data) != "module example.com/uani\n" {
		t.Fatalf("extracted go.mod = %q, %v", data, err)
	}

	escape := t.TempDir()
	err := extractTar(bytes.NewReader(archive("u-ani-1.0.1/../../escape", "x")), escape)
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("extractTar() error = %v, want unsafe path", err)
	}
}
