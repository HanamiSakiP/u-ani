// Package postprocessor moves completed downloads to formatter paths.
package postprocessor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/download"
	"github.com/HanamiSakiP/u-ani/internal/downloader"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/season"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

var videoExtensions = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".webm": true,
	".flv": true, ".rmvb": true, ".mov": true, ".ts": true,
}

var templateField = regexp.MustCompile(`\{(\w+)(?::([^}]+))?\}`)

// FormatPath applies config.path_formatter and returns an absolute target path.
func FormatPath(bangumiName string, seasonNumber, episode int, suffix, title string) (string, error) {
	values := map[string]any{
		"name":    utils.NormalizePath(bangumiName),
		"season":  seasonNumber,
		"episode": episode,
		"suffix":  suffix,
		"title":   title,
	}
	var formatErr error
	formatted := templateField.ReplaceAllStringFunc(config.Cfg.PathFormatter, func(match string) string {
		parts := templateField.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		key := parts[1]
		value, ok := values[key]
		if !ok {
			formatErr = fmt.Errorf("unknown path formatter field %q", key)
			return match
		}
		spec := parts[2]
		if spec == "" {
			return fmt.Sprint(value)
		}
		switch v := value.(type) {
		case int:
			if strings.HasSuffix(spec, "d") {
				width := strings.TrimSuffix(spec, "d")
				if strings.HasPrefix(width, "0") || width == "" {
					n, err := strconv.Atoi(strings.TrimLeft(width, "0"))
					if err == nil {
						return fmt.Sprintf("%0*d", n, v)
					}
				}
			}
			formatErr = fmt.Errorf("unsupported path formatter format %q", spec)
			return match
		default:
			formatErr = fmt.Errorf("unsupported path formatter format %q", spec)
			return match
		}
	})
	if formatErr != nil {
		return "", formatErr
	}
	return filepath.Join(config.Cfg.SavePath, filepath.FromSlash(formatted)), nil
}

func pickVideoFile(files []string) string {
	for _, f := range files {
		if videoExtensions[strings.ToLower(filepath.Ext(f))] {
			return f
		}
	}
	if len(files) > 0 {
		return files[0]
	}
	return ""
}

func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Cross-device move fallback.
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
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

// MoveToFormattedPath moves the first video file to its formatted location.
func MoveToFormattedPath(dl *db.Download, files []string) (bool, error) {
	seasonNumber := 1
	episodeOffset := 0
	displayName := season.StripSeasonSuffix(dl.BangumiName)
	if followed, err := db.Conn.GetFollowed(dl.BangumiName); err == nil {
		seasonNumber = followed.Season
		episodeOffset = followed.EpisodeOffset
		if followed.DisplayName != "" {
			displayName = followed.DisplayName
		}
	}

	targetFile := pickVideoFile(files)
	if targetFile == "" {
		return false, nil
	}
	if _, err := os.Stat(targetFile); err != nil {
		logger.Warning("File not found, skipping: %s", targetFile)
		return false, nil
	}
	suffix := strings.TrimPrefix(filepath.Ext(targetFile), ".")
	target, err := FormatPath(displayName, seasonNumber, dl.Episode+episodeOffset, suffix, dl.Title)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, err
	}
	logger.Success("Moving %s -> %s", targetFile, target)
	if err := moveFile(targetFile, target); err != nil {
		return false, err
	}
	return true, nil
}

func cleanupDownloadDir(files []string) {
	if len(files) == 0 {
		return
	}
	dir := filepath.Dir(files[0])
	root := filepath.Join(config.Cfg.SavePath, ".downloads")
	absDir, err1 := filepath.Abs(dir)
	absRoot, err2 := filepath.Abs(root)
	if err1 == nil && err2 == nil {
		if absDir != absRoot && !strings.HasPrefix(absDir, absRoot+string(os.PathSeparator)) {
			return
		}
	}
	_ = os.RemoveAll(dir)
}

// ProcessCompletedDownloads checks DOWNLOADING records and moves done tasks.
func ProcessCompletedDownloads() {
	if !config.Cfg.EnablePathFormatter {
		return
	}
	if db.Conn == nil {
		if _, err := db.New(); err != nil {
			logger.Error("%v", err)
			return
		}
	}
	driver, err := download.GetDriver()
	if err != nil {
		return
	}
	status := db.DownloadStatusDownloading
	downloads, err := db.Conn.GetDownloads(&status)
	if err != nil {
		logger.Error("%v", err)
		return
	}
	for i := range downloads {
		dl := &downloads[i]
		if dl.TaskID == nil || *dl.TaskID == "" {
			continue
		}
		st, err := driver.GetStatus(*dl.TaskID)
		if err != nil {
			logger.Warning("Failed to get status for task %s: %v", *dl.TaskID, err)
			continue
		}
		switch st {
		case downloader.Done:
			logger.Info("Download complete: %s", dl.Title)
			files, err := driver.GetFiles(*dl.TaskID)
			if err != nil {
				logger.Warning("Failed to get files for %s: %v", dl.Title, err)
				continue
			}
			if len(files) == 0 {
				logger.Warning("No files found for completed task %s, will retry", *dl.TaskID)
				continue
			}
			ok, err := MoveToFormattedPath(dl, files)
			if err != nil {
				logger.Error("Failed to post-process %s: %v", dl.Title, err)
				continue
			}
			if !ok {
				logger.Warning("Move failed for %s, will retry", dl.Title)
				continue
			}
			cleanupDownloadDir(files)
			if err := driver.RemoveDownload(*dl.TaskID); err != nil {
				logger.Warning("Failed to remove task %s from downloader: %v", *dl.TaskID, err)
			}
			dl.Status = db.DownloadStatusDownloaded
			_ = db.Conn.SaveDownload(dl)
		case downloader.Error:
			logger.Error("Download failed: %s", dl.Title)
			dl.Status = db.DownloadStatusNotDownload
			_ = db.Conn.SaveDownload(dl)
		}
	}
}
