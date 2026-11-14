// Package selfupdate checks for and installs new uani releases with go install.
package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
)

const (
	// ModulePath is the Go module that contains the uani command.
	ModulePath = "github.com/HanamiSakiP/u-ani"
	// CommandPath is the package installed by go install.
	CommandPath = ModulePath + "/cmd/uani"
	// SourceVersionFile records the version of the local source copy.
	SourceVersionFile = ".uani-source-version"
)

// Version is a parsed semantic version.
type Version struct {
	Major int
	Minor int
	Patch int
	Pre   string
}

// ParseVersion parses versions such as v1.2.3, 1.2.3-rc1 and 1.2.3+build.
func ParseVersion(raw string) Version {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	var v Version
	if i := strings.IndexByte(raw, '+'); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.IndexByte(raw, '-'); i >= 0 {
		v.Pre = raw[i+1:]
		raw = raw[:i]
	}
	parts := strings.Split(raw, ".")
	values := []*int{&v.Major, &v.Minor, &v.Patch}
	for i := 0; i < len(parts) && i < len(values); i++ {
		n, _ := strconv.Atoi(strings.TrimSpace(parts[i]))
		*values[i] = n
	}
	return v
}

// String formats a version without the leading "v".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare returns -1 when a < b, 0 when a == b and 1 when a > b.
func Compare(a, b string) int {
	av, bv := ParseVersion(a), ParseVersion(b)
	if av.Major != bv.Major {
		return compareInt(av.Major, bv.Major)
	}
	if av.Minor != bv.Minor {
		return compareInt(av.Minor, bv.Minor)
	}
	if av.Patch != bv.Patch {
		return compareInt(av.Patch, bv.Patch)
	}
	return comparePrerelease(av.Pre, bv.Pre)
}

// Newer reports whether available is newer than current.
func Newer(available, current string) bool {
	return Compare(available, current) > 0
}

func compareInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}
	// A release without a prerelease suffix has higher precedence.
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	ap, bp := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] == bp[i] {
			continue
		}
		an, aerr := strconv.Atoi(ap[i])
		bn, berr := strconv.Atoi(bp[i])
		switch {
		case aerr == nil && berr == nil:
			return compareInt(an, bn)
		case aerr == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case berr == nil:
			return 1
		default:
			if ap[i] < bp[i] {
				return -1
			}
			return 1
		}
	}
	return compareInt(len(ap), len(bp))
}

// Status describes the current installation and the latest published release.
type Status struct {
	Current       string `json:"current"`
	Latest        string `json:"latest,omitempty"`
	Updatable     bool   `json:"updatable"`
	GoAvailable   bool   `json:"go_available"`
	GoVersion     string `json:"go_version,omitempty"`
	InstallPath   string `json:"install_path,omitempty"`
	Executable    string `json:"executable,omitempty"`
	SourcePath    string `json:"source_path,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
	SourceReady   bool   `json:"source_ready"`
	Error         string `json:"error,omitempty"`
}

// Result describes a completed go install run.
type Result struct {
	Current  string `json:"current"`
	Version  string `json:"version"`
	Path     string `json:"path,omitempty"`
	Replaced bool   `json:"replaced"`
	Output   string `json:"output,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

// SourceResult describes a synchronized source tree.
type SourceResult struct {
	Version string `json:"version"`
	Path    string `json:"path"`
}

// Check inspects the current installation and asks the Go module proxy for the
// newest published version. The check uses `go list` when Go is available so
// that GOPROXY settings and mirrors are respected.
func Check(ctx context.Context) Status {
	status := Status{Current: config.Version}
	if executable, err := os.Executable(); err == nil {
		status.Executable = resolvePath(executable)
	}
	status.SourcePath = SourceDir()
	status.SourceVersion = SourceVersion(status.SourcePath)
	status.SourceReady = SourceReady(status.SourcePath)
	if goBin, err := exec.LookPath("go"); err == nil {
		status.GoAvailable = true
		if out, err := exec.CommandContext(ctx, goBin, "version").Output(); err == nil {
			status.GoVersion = strings.TrimSpace(string(out))
		}
		if path, err := InstallPath(ctx); err == nil {
			status.InstallPath = path
		}
	}
	latest, err := LatestVersion(ctx)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Latest = latest
	status.Updatable = Newer(latest, status.Current)
	return status
}

// LatestVersion returns the newest published uani version.
func LatestVersion(ctx context.Context) (string, error) {
	var goErr error
	if _, err := exec.LookPath("go"); err == nil {
		if version, err := goListLatest(ctx); err == nil {
			return version, nil
		} else {
			goErr = err
		}
	}
	version, err := latestFromProxy(ctx)
	if err == nil {
		return version, nil
	}
	if goErr != nil {
		return "", fmt.Errorf("go list failed: %v; proxy check failed: %v", goErr, err)
	}
	return "", err
}

func goListLatest(ctx context.Context) (string, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, goBin, "list", "-m", "-json", ModulePath+"@latest")
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "GO111MODULE=on", "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var info struct {
		Version string
	}
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return "", err
	}
	if strings.TrimSpace(info.Version) == "" {
		return "", fmt.Errorf("go list returned no version")
	}
	return strings.TrimSpace(info.Version), nil
}

func latestFromProxy(ctx context.Context) (string, error) {
	escaped := escapeModulePath(ModulePath)
	var lastErr error
	for _, base := range proxyBases() {
		url := strings.TrimRight(base, "/") + "/" + escaped + "/@latest"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "uani/"+config.Version)
		resp, err := httpx.Client().Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
			continue
		}
		var info struct {
			Version string `json:"Version"`
		}
		err = json.NewDecoder(resp.Body).Decode(&info)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if strings.TrimSpace(info.Version) != "" {
			return strings.TrimSpace(info.Version), nil
		}
		lastErr = fmt.Errorf("%s: empty version", url)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no Go module proxy configured")
	}
	return "", lastErr
}

func proxyBases() []string {
	var bases []string
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || value == "off" || value == "direct" || seen[value] {
			return
		}
		seen[value] = true
		bases = append(bases, value)
	}
	for _, value := range strings.FieldsFunc(os.Getenv("GOPROXY"), func(r rune) bool {
		return r == ',' || r == '|'
	}) {
		add(value)
	}
	// Fallbacks for the common case where Go is not installed.
	add("https://goproxy.cn")
	add("https://proxy.golang.org")
	add("https://mirrors.aliyun.com/goproxy/")
	return bases
}

func escapeModulePath(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// InstallPath returns the path where go install writes the uani binary.
func InstallPath(ctx context.Context) (string, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go is not installed: %w", err)
	}
	out, err := exec.CommandContext(ctx, goBin, "env", "-json", "GOBIN", "GOPATH").Output()
	if err != nil {
		return "", err
	}
	var env struct {
		GOBIN  string
		GOPATH string
	}
	if err := json.Unmarshal(out, &env); err != nil {
		return "", err
	}
	dir := strings.TrimSpace(env.GOBIN)
	if dir == "" {
		gopath := filepath.SplitList(env.GOPATH)
		if len(gopath) == 0 || strings.TrimSpace(gopath[0]) == "" {
			return "", fmt.Errorf("neither GOBIN nor GOPATH is set")
		}
		dir = filepath.Join(gopath[0], "bin")
	}
	return filepath.Join(dir, binaryName()), nil
}

// Install runs go install for the requested version and reports the resulting
// binary path. An empty or "latest" target installs the newest release.
func Install(ctx context.Context, target string) (Result, error) {
	result := Result{Current: config.Version, Mode: "remote"}
	target = strings.TrimSpace(target)
	if target == "" {
		target = "latest"
	}
	if !validTarget(target) {
		return result, fmt.Errorf("invalid target version %q", target)
	}
	target = normalizeModuleVersion(target)
	goBin, err := exec.LookPath("go")
	if err != nil {
		return result, fmt.Errorf("go is required for self-upgrade: %w", err)
	}

	// Compare the running executable with the future install location before
	// go install replaces the file. On Linux /proc/self/exe then points to a
	// deleted inode, which would make the comparison unreliable.
	installPath, pathErr := InstallPath(ctx)
	executable, _ := os.Executable()
	replaced := pathErr == nil && samePath(executable, installPath)

	cmd := exec.CommandContext(ctx, goBin, "install", CommandPath+"@"+target)
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "GO111MODULE=on", "GOWORK=off")
	out, runErr := cmd.CombinedOutput()
	result.Output = strings.TrimSpace(string(out))
	if runErr != nil {
		if result.Output != "" {
			return result, fmt.Errorf("go install failed: %w\n%s", runErr, result.Output)
		}
		return result, fmt.Errorf("go install failed: %w", runErr)
	}

	if target == "latest" {
		result.Version = target
		if latest, err := LatestVersion(ctx); err == nil {
			result.Version = latest
		}
	} else {
		result.Version = target
	}
	if pathErr != nil {
		installPath, pathErr = InstallPath(ctx)
		if pathErr != nil {
			return result, pathErr
		}
	}
	result.Path = installPath
	result.Replaced = replaced
	return result, nil
}

// SourceDir is the default directory used to keep a local copy of the uani
// source tree.
func SourceDir() string {
	return filepath.Join(config.UANIHome, "source")
}

// SourceReady reports whether dir looks like a uani source tree.
func SourceReady(dir string) bool {
	if dir == "" {
		dir = SourceDir()
	}
	if info, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil || info.IsDir() {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "cmd", "uani", "main.go"))
	return err == nil && !info.IsDir()
}

var sourceVersionPattern = regexp.MustCompile(`(?m)^const Version = "([^"]+)"`)

// SourceVersion returns the version recorded in a local source tree. It reads
// the marker written by SyncSource first, then falls back to parsing the
// version constant so that a manually edited checkout also works.
func SourceVersion(dir string) string {
	if dir == "" {
		dir = SourceDir()
	}
	if data, err := os.ReadFile(filepath.Join(dir, SourceVersionFile)); err == nil {
		if version := strings.TrimSpace(string(data)); version != "" {
			return version
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "internal", "config", "config.go"))
	if err != nil {
		return ""
	}
	match := sourceVersionPattern.FindSubmatch(data)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(string(match[1]))
}

// SyncSource downloads version's source code and replaces dir with it.
func SyncSource(ctx context.Context, version, dir string) (SourceResult, error) {
	result := SourceResult{}
	if strings.TrimSpace(version) == "" || strings.EqualFold(strings.TrimSpace(version), "latest") {
		latest, err := LatestVersion(ctx)
		if err != nil {
			return result, err
		}
		version = latest
	}
	version = strings.TrimSpace(version)
	version = normalizeModuleVersion(version)
	if dir == "" {
		dir = SourceDir()
	}
	moduleDir, err := moduleSourceDir(ctx, version)
	if err != nil {
		return result, err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return result, err
	}
	tmp, err := os.MkdirTemp(parent, ".uani-source-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(moduleDir, tmp); err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(tmp, SourceVersionFile), []byte(version+"\n"), 0o644); err != nil {
		return result, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return result, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return result, err
	}
	_ = os.Chmod(dir, 0o755)
	result.Version = version
	result.Path = dir
	return result, nil
}

func moduleSourceDir(ctx context.Context, version string) (string, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go is required to synchronize source: %w", err)
	}
	cmd := exec.CommandContext(ctx, goBin, "mod", "download", "-json", ModulePath+"@"+version)
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "GO111MODULE=on", "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go mod download failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var info struct {
		Dir string
	}
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return "", err
	}
	if strings.TrimSpace(info.Dir) == "" {
		return "", fmt.Errorf("go mod download returned no source directory")
	}
	return info.Dir, nil
}

func copyTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		source := filepath.Join(src, entry.Name())
		target := filepath.Join(dst, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(source)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
			continue
		}
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			if err := copyTree(source, target); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// InstallLocal builds and installs uani from a local source tree.
func InstallLocal(ctx context.Context, dir string) (Result, error) {
	result := Result{Current: config.Version, Mode: "local"}
	if dir == "" {
		dir = SourceDir()
	}
	if !SourceReady(dir) {
		return result, fmt.Errorf("%s is not a uani source directory; run `uani upgrade` to synchronize it", dir)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return result, fmt.Errorf("go is required for a local upgrade: %w", err)
	}
	installPath, pathErr := InstallPath(ctx)
	executable, _ := os.Executable()
	replaced := pathErr == nil && samePath(executable, installPath)

	cmd := exec.CommandContext(ctx, goBin, "install", "./cmd/uani")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GO111MODULE=on", "GOWORK=off")
	out, runErr := cmd.CombinedOutput()
	result.Output = strings.TrimSpace(string(out))
	if runErr != nil {
		if result.Output != "" {
			return result, fmt.Errorf("local go install failed: %w\n%s", runErr, result.Output)
		}
		return result, fmt.Errorf("local go install failed: %w", runErr)
	}
	result.Version = SourceVersion(dir)
	if result.Version == "" {
		result.Version = "local"
	}
	if pathErr != nil {
		installPath, pathErr = InstallPath(ctx)
		if pathErr != nil {
			return result, pathErr
		}
	}
	result.Path = installPath
	result.Replaced = replaced
	return result, nil
}

func normalizeModuleVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "latest" {
		return version
	}
	if version[0] >= '0' && version[0] <= '9' {
		return "v" + version
	}
	return version
}

func validTarget(target string) bool {
	if target == "latest" {
		return true
	}
	if target == "" || strings.Contains(target, "..") {
		return false
	}
	for _, r := range target {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '.', '-', '_', '+', '/', '~':
			continue
		}
		return false
	}
	return true
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "uani.exe"
	}
	return "uani"
}

func samePath(a, b string) bool {
	a, b = resolvePath(a), resolvePath(b)
	if a == "" || b == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func resolvePath(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}
