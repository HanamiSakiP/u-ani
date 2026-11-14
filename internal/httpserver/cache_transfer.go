package httpserver

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/logger"
)

func adminCacheExport(w http.ResponseWriter, r *http.Request) {
	snapshot, err := controllers.ExportCacheData()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func adminCacheImport(w http.ResponseWriter, r *http.Request) {
	var snapshot controllers.CacheExport
	if err := decodeBody(r, &snapshot); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := controllers.ImportCacheData(snapshot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Imported timeline/recommend files must replace the in-memory copies so
	// the next page load sees the restored data immediately.
	resetKazumiBrowseCacheMemory()
	writeJSON(w, http.StatusOK, map[string]any{
		"result":  result,
		"message": controllers.CacheExportMessage(result),
	})
}

func adminBrowseCacheExport(w http.ResponseWriter, r *http.Request) {
	filename := fmt.Sprintf("uani-kazumi-cache-%s.zip", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	if err := controllers.ExportBrowseCacheArchive(w); err != nil {
		logger.Warning("Failed to export Kazumi browse cache: %v", err)
	}
}

func adminBrowseCacheImport(w http.ResponseWriter, r *http.Request) {
	path, err := saveUploadToTemp(r, "uani-kazumi-cache-*.zip")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer os.Remove(path)
	result, err := controllers.ImportBrowseCacheArchive(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resetKazumiBrowseCacheMemory()
	writeJSON(w, http.StatusOK, map[string]any{
		"result":  result,
		"message": controllers.BrowseCacheExportMessage(result),
	})
}
