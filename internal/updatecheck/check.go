// Package updatecheck implements uani's once-a-week version check.
package updatecheck

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
	"github.com/HanamiSakiP/u-ani/internal/logger"
)

type pypiResponse struct {
	Info struct {
		Version string `json:"version"`
	} `json:"info"`
}

type version struct {
	major, minor, patch int
	pre                 string
}

func parseVersion(raw string) version {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "v"))
	var v version
	main := raw
	if i := strings.IndexAny(main, "-+"); i >= 0 {
		if main[i] == '-' {
			v.pre = main[i+1:]
		}
		main = main[:i]
	}
	parts := strings.Split(main, ".")
	values := []*int{&v.major, &v.minor, &v.patch}
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		*values[i] = n
	}
	return v
}

func newer(available, current string) bool {
	a := parseVersion(available)
	c := parseVersion(current)
	if a.major != c.major {
		return a.major > c.major
	}
	if a.minor != c.minor {
		return a.minor > c.minor
	}
	if a.patch != c.patch {
		return a.patch > c.patch
	}
	if a.pre == c.pre {
		return false
	}
	if a.pre == "" {
		return true
	}
	if c.pre == "" {
		return false
	}
	return a.pre > c.pre
}

func checkOnce() {
	logger.Info("Checking update ...")
	req, err := http.NewRequest("GET", "https://pypi.org/pypi/bgmi/json", nil)
	if err != nil {
		logger.Warning("Error occurs when checking update, %v", err)
		return
	}
	req.Header.Set("User-Agent", "uani")
	resp, err := httpx.Client().Do(req)
	if err != nil {
		logger.Warning("Error occurs when checking update, %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		logger.Warning("Error occurs when checking update, HTTP %d", resp.StatusCode)
		return
	}
	var data pypiResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		logger.Warning("Error occurs when checking update, %v", err)
		return
	}
	_ = os.WriteFile(filepath.Join(config.UANIHome, "latest"), []byte(data.Info.Version), 0o644)
	// PyPI publishes the upstream BGmi version. uani has its own 1.x version
	// line, so only compare releases with the same major version.
	if parseVersion(data.Info.Version).major == parseVersion(config.Version).major && newer(data.Info.Version, config.Version) {
		logger.Warning("Please update uani to the latest version %s.\nThen execute `uani upgrade` to migrate database", data.Info.Version)
	} else {
		logger.Success("Your uani is the latest version.")
	}
}

// CheckUpdate runs at most once every seven days.
func CheckUpdate() {
	versionFile := filepath.Join(config.UANIHome, "version")
	data, err := os.ReadFile(versionFile)
	if err != nil {
		_ = os.WriteFile(versionFile, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o644)
		checkOnce()
		return
	}
	timestamp, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return
	}
	if float64(time.Now().Unix())-7*24*3600 > float64(timestamp) {
		_ = os.WriteFile(versionFile, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o644)
		checkOnce()
	}
}
