// Package updatecheck implements uani's once-a-week version check.
package updatecheck

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/selfupdate"
)

func checkOnce() {
	logger.Info("Checking update ...")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	latest, err := selfupdate.LatestVersion(ctx)
	if err != nil {
		logger.Warning("Error occurs when checking update, %v", err)
		return
	}
	_ = os.WriteFile(filepath.Join(config.UANIHome, "latest"), []byte(latest), 0o644)
	if selfupdate.Newer(latest, config.Version) {
		logger.Warning("A new uani version %s is available.\nRun `uani upgrade` or open the Web settings page to install it with go install.", latest)
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
