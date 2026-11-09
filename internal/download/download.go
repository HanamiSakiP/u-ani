// Package download converts episodes into downloader tasks and records them.
package download

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/downloader"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/model"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

var (
	driverMu sync.Mutex
	drivers  = map[string]downloader.Service{}
)

// GetDriver returns the configured download delegate, caching by name.
func GetDriver() (downloader.Service, error) {
	name := config.Cfg.DownloadDelegate
	driverMu.Lock()
	defer driverMu.Unlock()
	if d, ok := drivers[name]; ok {
		return d, nil
	}
	d, err := downloader.New(name)
	if err != nil {
		logger.Error("%v", err)
		return nil, err
	}
	drivers[name] = d
	return d, nil
}

func newUUID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func ensureIgnoreFiles(dir string) {
	for _, name := range []string{".tmmignore", ".ignore"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			_ = os.WriteFile(p, nil, 0o644)
		}
	}
}

func savePathFor(name string, episode int) string {
	if config.Cfg.EnablePathFormatter {
		dir := filepath.Join(config.Cfg.SavePath, ".downloads")
		_ = os.MkdirAll(dir, 0o755)
		ensureIgnoreFiles(dir)
		return filepath.Join(dir, newUUID())
	}
	return filepath.Join(utils.BangumiSavePath(name), strconv.Itoa(episode))
}

func ensureDB() error {
	if db.Conn != nil {
		return nil
	}
	_, err := db.New()
	return err
}

// DownloadEpisode adds one episode to the downloader and records it.
func DownloadEpisode(e model.Episode) bool {
	driver, err := GetDriver()
	if err != nil {
		return false
	}
	if err := ensureDB(); err != nil {
		logger.Error("Error when downloading %s: %v", e.Title, err)
		return false
	}
	savePath := savePathFor(e.Name, e.Episode)
	if err := os.MkdirAll(savePath, 0o755); err != nil {
		logger.Error("Error when downloading %s: %v", e.Title, err)
		return false
	}

	dl, err := db.Conn.GetDownload(e.Name, e.Download, e.Episode, e.Title)
	if err != nil {
		if err != db.ErrNotFound {
			logger.Error("Error when downloading %s: %v", e.Title, err)
			return false
		}
		dl = &db.Download{
			BangumiName: e.Name,
			Download:    e.Download,
			Episode:     e.Episode,
			Title:       e.Title,
		}
	}
	dl.Status = db.DownloadStatusDownloading

	taskID, err := driver.AddDownload(downloader.AddTracker(dl.Download), savePath)
	if err != nil {
		if os.Getenv("DEBUG") != "" {
			logger.Error("%v", err)
			return false
		}
		logger.Error("Error when downloading %s: %v", dl.Title, err)
		dl.Status = db.DownloadStatusNotDownload
		_ = db.Conn.SaveDownload(dl)
		return false
	}
	dl.TaskID = &taskID
	if err := db.Conn.SaveDownload(dl); err != nil {
		logger.Error("Error when downloading %s: %v", dl.Title, err)
		return false
	}
	logger.Info("Add torrent into the download queue, the file will be saved at %s", savePath)
	return true
}

// DownloadDownloads queues explicit download records (used by search --download).
func DownloadDownloads(data []db.Download) {
	driver, err := GetDriver()
	if err != nil {
		return
	}
	if err := ensureDB(); err != nil {
		logger.Error("%v", err)
		return
	}
	for i := range data {
		dl := &data[i]
		savePath := savePathFor(dl.BangumiName, dl.Episode)
		if err := os.MkdirAll(savePath, 0o755); err != nil {
			logger.Error("Error when downloading %s: %v", dl.Title, err)
			continue
		}
		dl.Status = db.DownloadStatusDownloading
		_ = db.Conn.SaveDownload(dl)
		taskID, err := driver.AddDownload(dl.Download, savePath)
		if err != nil {
			if os.Getenv("DEBUG") != "" {
				logger.Error("%v", err)
				continue
			}
			logger.Error("Error when downloading %s: %v", dl.Title, err)
			dl.Status = db.DownloadStatusNotDownload
			_ = db.Conn.SaveDownload(dl)
			continue
		}
		dl.TaskID = &taskID
		_ = db.Conn.SaveDownload(dl)
		logger.Info("Add torrent into the download queue, the file will be saved at %s", savePath)
	}
}
