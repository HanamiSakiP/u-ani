package httpserver

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/logger"
)

func adminFollowExportBundle(w http.ResponseWriter, r *http.Request) {
	filename := fmt.Sprintf("uani-follows-%s.zip", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	if err := controllers.ExportFollowsArchive(w); err != nil {
		logger.Warning("Failed to export follow bundle: %v", err)
	}
}

func adminFollowImportBundle(w http.ResponseWriter, r *http.Request) {
	path, err := saveUploadToTemp(r, "uani-follows-*.zip")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer os.Remove(path)
	result, err := controllers.ImportFollowsArchive(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result":  result,
		"message": controllers.FollowArchiveMessage(result),
	})
}
