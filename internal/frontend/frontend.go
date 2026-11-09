// Package frontend installs and updates the uani web frontend.
package frontend

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
	"github.com/HanamiSakiP/u-ani/internal/logger"
)

const (
	npmManifestURL    = "https://registry.npmjs.com/bgmi-frontend/"
	githubReleasesURL = "https://api.github.com/repos/BGmi/BGmi-frontend/releases"
)

type semver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(raw string) (semver, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "v"))
	var v semver
	main := raw
	if i := strings.IndexAny(main, "-+"); i >= 0 {
		if main[i] == '-' {
			v.pre = main[i+1:]
		}
		main = main[:i]
	}
	parts := strings.Split(main, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return v, fmt.Errorf("invalid version %q", raw)
	}
	values := []*int{&v.major, &v.minor, &v.patch}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return v, err
		}
		*values[i] = n
	}
	return v, nil
}

func (v semver) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if v.pre != "" {
		s += "-" + v.pre
	}
	return s
}

func compareSemver(a, b semver) int {
	if a.major != b.major {
		if a.major < b.major {
			return -1
		}
		return 1
	}
	if a.minor != b.minor {
		if a.minor < b.minor {
			return -1
		}
		return 1
	}
	if a.patch != b.patch {
		if a.patch < b.patch {
			return -1
		}
		return 1
	}
	if a.pre == b.pre {
		return 0
	}
	if a.pre == "" {
		return 1
	}
	if b.pre == "" {
		return -1
	}
	if a.pre < b.pre {
		return -1
	}
	return 1
}

func supported(v semver) bool {
	return v.major == 2
}

func getJSON(url string, out any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "uani")
	resp, err := httpx.Client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type npmManifest struct {
	Versions map[string]map[string]any `json:"versions"`
}

func latestNPM() (semver, map[string]any, string, error) {
	var manifest npmManifest
	if err := getJSON(npmManifestURL, &manifest); err != nil {
		return semver{}, nil, "", err
	}
	var best semver
	var bestRaw string
	var bestVersion map[string]any
	for raw, meta := range manifest.Versions {
		v, err := parseSemver(raw)
		if err != nil || !supported(v) {
			continue
		}
		if bestRaw == "" || compareSemver(v, best) > 0 {
			best = v
			bestRaw = raw
			bestVersion = meta
		}
	}
	if bestRaw == "" {
		return semver{}, nil, "", fmt.Errorf("failed to find available web-ui versions")
	}
	dist, _ := bestVersion["dist"].(map[string]any)
	tarURL, _ := dist["tarball"].(string)
	if tarURL == "" {
		return semver{}, nil, "", fmt.Errorf("npm package has no tarball url")
	}
	return best, bestVersion, tarURL, nil
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func latestGitHub() (semver, githubRelease, string, error) {
	var releases []githubRelease
	if err := getJSON(githubReleasesURL, &releases); err != nil {
		return semver{}, githubRelease{}, "", err
	}
	var best semver
	var bestRelease githubRelease
	var bestAsset string
	found := false
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		assetURL := ""
		for _, asset := range release.Assets {
			if strings.HasSuffix(asset.Name, ".tgz") {
				assetURL = asset.BrowserDownloadURL
				break
			}
		}
		if assetURL == "" {
			continue
		}
		v, err := parseSemver(release.TagName)
		if err != nil || !supported(v) {
			continue
		}
		if !found || compareSemver(v, best) > 0 {
			found = true
			best = v
			bestRelease = release
			bestAsset = assetURL
		}
	}
	if !found {
		return semver{}, githubRelease{}, "", fmt.Errorf("failed to find available web-ui GitHub release")
	}
	return best, bestRelease, bestAsset, nil
}

// LatestVersion returns the newest supported frontend version.
func LatestVersion() (semver, error) {
	if v, _, _, err := latestGitHub(); err == nil {
		return v, nil
	} else {
		logger.Warning("failed to check GitHub frontend release, fallback to npm: %v", err)
	}
	v, _, _, err := latestNPM()
	return v, err
}

func download(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "uani")
	resp, err := httpx.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func extractTarball(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(header.Name)
		if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}
		target := filepath.Join(dest, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

func installTarball(method, tarURL string, version map[string]any) error {
	logger.Info("%sing uani frontend", strings.ToUpper(method[:1])+method[1:])
	data, err := download(tarURL)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(config.Cfg.FrontStaticPath); err != nil {
		return err
	}
	if err := os.MkdirAll(config.Cfg.FrontStaticPath, 0o755); err != nil {
		return err
	}
	if err := extractTarball(data, config.Cfg.FrontStaticPath); err != nil {
		return err
	}
	dist := filepath.Join(config.Cfg.FrontStaticPath, "package", "dist")
	entries, err := os.ReadDir(dist)
	if err == nil {
		for _, entry := range entries {
			from := filepath.Join(dist, entry.Name())
			to := filepath.Join(config.Cfg.FrontStaticPath, entry.Name())
			if err := os.Rename(from, to); err != nil {
				if err := copyPath(from, to); err != nil {
					return err
				}
			}
		}
	}
	pkg, err := json.MarshalIndent(version, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(config.Cfg.FrontStaticPath, "package.json"), pkg, 0o644); err != nil {
		return err
	}
	name, _ := version["version"].(string)
	logger.Success("Web admin page %s successfully. version: %s", method, name)
	return nil
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPath(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Install downloads the latest frontend ("install" or "update").
func Install(method string) {
	if v, release, asset, err := latestGitHub(); err == nil {
		version := map[string]any{
			"version":  v.String(),
			"source":   "github",
			"tag_name": release.TagName,
			"html_url": release.HTMLURL,
			"asset":    filepath.Base(asset),
		}
		if err := installTarball(method, asset, version); err == nil {
			return
		} else {
			logger.Warning("failed to install uani frontend from GitHub release, fallback to npm: %v", err)
		}
	} else {
		logger.Warning("failed to find uani frontend GitHub release, fallback to npm: %v", err)
	}

	_, version, tarURL, err := latestNPM()
	if err != nil {
		logger.Warning("failed to download web admin: %v", err)
		return
	}
	if err := installTarball(method, tarURL, version); err != nil {
		logger.Warning("failed to download web admin: %v", err)
	}
}

// LocalVersion reads the installed frontend version.
func LocalVersion() (semver, bool) {
	data, err := os.ReadFile(filepath.Join(config.Cfg.FrontStaticPath, "package.json"))
	if err != nil {
		return semver{}, false
	}
	var pkg map[string]any
	if err := json.Unmarshal(data, &pkg); err != nil {
		return semver{}, false
	}
	raw, _ := pkg["version"].(string)
	v, err := parseSemver(raw)
	if err != nil {
		return semver{}, false
	}
	return v, true
}
