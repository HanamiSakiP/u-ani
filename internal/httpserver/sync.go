package httpserver

import (
	"net/http"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/db"
)

func adminBangumiRouter(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case r.Method == http.MethodGet && path == "sync":
		writeJSON(w, http.StatusOK, controllers.BangumiSyncStatus())
	case r.Method == http.MethodPost && path == "sync":
		adminBangumiSyncRun(w, r)
	case r.Method == http.MethodPatch && path == "sync":
		adminBangumiSyncConfigure(w, r)
	case r.Method == http.MethodPost && path == "token":
		adminBangumiToken(w, r)
	case r.Method == http.MethodDelete && path == "token":
		if err := controllers.ClearBangumiToken(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, controllers.BangumiSyncStatus())
	case r.Method == http.MethodGet && path == "export":
		adminBangumiExport(w, r)
	case r.Method == http.MethodPost && path == "import":
		adminBangumiImport(w, r)
	case r.Method == http.MethodGet && path == "export/bundle":
		adminFollowExportBundle(w, r)
	case r.Method == http.MethodPost && path == "import/bundle":
		adminFollowImportBundle(w, r)
	case r.Method == http.MethodPost && path == "cache":
		adminBangumiCache(w, r)
	case r.Method == http.MethodGet && path == "cache/export":
		adminCacheExport(w, r)
	case r.Method == http.MethodPost && path == "cache/import":
		adminCacheImport(w, r)
	case r.Method == http.MethodPost && path == "cache/clear":
		if err := controllers.ClearBangumiDetailCache(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已清除番剧详情缓存（保留追番与下载信息）"})
	case r.Method == http.MethodPost && path == "alias":
		adminBangumiAlias(w, r)
	case r.Method == http.MethodDelete && path == "alias":
		adminBangumiAliasDelete(w, r)
	case r.Method == http.MethodPost && path == "link":
		adminBangumiLink(w, r)
	case (r.Method == http.MethodGet || r.Method == http.MethodPost) && (path == "collection" || strings.HasPrefix(path, "collection/")):
		adminBangumiCollection(w, r)
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}

type bangumiSyncPatch struct {
	AccessToken *string   `json:"access_token"`
	SyncEnabled *bool     `json:"sync_enabled"`
	SyncTypes   *[]string `json:"sync_types"`
}

func adminBangumiSyncConfigure(w http.ResponseWriter, r *http.Request) {
	var body bangumiSyncPatch
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if body.AccessToken != nil {
		if _, err := controllers.SetBangumiToken(*body.AccessToken); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := controllers.ConfigureBangumiSync(body.SyncEnabled, body.SyncTypes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, controllers.BangumiSyncStatus())
}

type bangumiTokenRequest struct {
	AccessToken string `json:"access_token"`
	Token       string `json:"token"`
}

func adminBangumiToken(w http.ResponseWriter, r *http.Request) {
	var body bangumiTokenRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	token := body.AccessToken
	if token == "" {
		token = body.Token
	}
	user, err := controllers.SetBangumiToken(token)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username": user.Username,
		"status":   controllers.BangumiSyncStatus(),
	})
}

type bangumiSyncRequest struct {
	Types   []string `json:"types"`
	Episode *int     `json:"episode"`
	Latest  bool     `json:"latest"`
	DryRun  bool     `json:"dry_run"`
	Limit   int      `json:"limit"`
}

func adminBangumiSyncRun(w http.ResponseWriter, r *http.Request) {
	var body bangumiSyncRequest
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	summary, err := controllers.SyncSubscriptions(controllers.SyncOptions{
		Types:   body.Types,
		Episode: body.Episode,
		Latest:  body.Latest,
		DryRun:  body.DryRun,
		Limit:   body.Limit,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": summary,
		"status":  controllers.BangumiSyncStatus(),
	})
}

func adminBangumiCache(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Refresh *bool `json:"refresh"`
	}
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	refresh := true
	if body.Refresh != nil {
		refresh = *body.Refresh
	}
	summary, err := controllers.CacheFollowedBangumi(refresh)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": summary, "message": controllers.CacheSummaryText(summary)})
}

func adminBangumiAlias(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Alias string `json:"alias"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(body.Name)
	alias := strings.TrimSpace(body.Alias)
	if name == "" || alias == "" {
		writeError(w, http.StatusBadRequest, "name and alias are required")
		return
	}
	if err := db.Conn.SaveCustomAlias(name, alias); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	controllers.RepairKazumiDownloadLinks()
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "alias": alias})
}

func adminBangumiAliasDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	alias := strings.TrimSpace(r.URL.Query().Get("alias"))
	if name == "" || alias == "" {
		writeError(w, http.StatusBadRequest, "name and alias are required")
		return
	}
	if err := db.Conn.DeleteCustomAlias(name, alias); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func adminBangumiExport(w http.ResponseWriter, r *http.Request) {
	snapshot, err := controllers.ExportFollows()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func adminBangumiImport(w http.ResponseWriter, r *http.Request) {
	var snapshot controllers.FollowExport
	if err := decodeBody(r, &snapshot); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := controllers.ImportFollows(snapshot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	controllers.RepairKazumiDownloadLinks()
	writeJSON(w, http.StatusOK, map[string]any{"result": result, "message": controllers.FollowExportMessage(result)})
}
