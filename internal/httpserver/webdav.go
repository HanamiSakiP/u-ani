package httpserver

import (
	"net/http"

	"github.com/HanamiSakiP/u-ani/internal/controllers"
)

func adminWebDAVRouter(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case r.Method == http.MethodGet && path == "":
		writeJSON(w, http.StatusOK, controllers.GetWebDAVStatus())
	case r.Method == http.MethodPatch && path == "":
		var body struct {
			URL              *string `json:"url"`
			Username         *string `json:"username"`
			Password         *string `json:"password"`
			Enabled          *bool   `json:"enabled"`
			SyncCollectibles *bool   `json:"sync_collectibles"`
			SyncDirection    *string `json:"sync_direction"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := controllers.ConfigureWebDAV(body.URL, body.Username, body.Password, body.Enabled, body.SyncCollectibles, body.SyncDirection); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, controllers.GetWebDAVStatus())
	case r.Method == http.MethodPost && path == "sync":
		var body struct {
			Direction string `json:"direction"`
		}
		if r.ContentLength != 0 {
			if err := decodeBody(r, &body); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		result, err := controllers.SyncWebDAV(body.Direction)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": result, "status": controllers.GetWebDAVStatus(),
			"message": controllers.WebDAVSyncSummary(result),
		})
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}
