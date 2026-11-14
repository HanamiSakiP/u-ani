package httpserver

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/logger"
)

func adminDownloadExport(w http.ResponseWriter, r *http.Request) {
	filename := fmt.Sprintf("uani-downloads-%s.zip", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	if err := controllers.ExportDownloadedBangumiArchive(w); err != nil {
		logger.Warning("Failed to export download library: %v", err)
	}
}

func adminDownloadImport(w http.ResponseWriter, r *http.Request) {
	path, err := saveUploadToTemp(r, "uani-downloads-*.zip")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer os.Remove(path)
	result, err := controllers.ImportDownloadedBangumiArchive(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result":  result,
		"message": controllers.DownloadExportMessage(result),
	})
}

func saveUploadToTemp(r *http.Request, pattern string) (string, error) {
	tmpDir := config.Cfg.TmpPath
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(tmpDir, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := io.Copy(file, r.Body); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
