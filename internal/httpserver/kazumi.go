package httpserver

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/kazumi"
	"github.com/HanamiSakiP/u-ani/internal/logger"
)

func adminKazumiRouter(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case r.Method == http.MethodGet && path == "catalog":
		adminKazumiCatalog(w, r)
	case r.Method == http.MethodGet && path == "rules":
		adminKazumiList(w, r)
	case r.Method == http.MethodPost && path == "rules":
		adminKazumiInstall(w, r)
	case r.Method == http.MethodPost && path == "rules/update":
		adminKazumiUpdate(w, r)
	case (r.Method == http.MethodGet || r.Method == http.MethodPut) && strings.HasPrefix(path, "rules/") && strings.HasSuffix(path, "/file"):
		adminKazumiRuleFile(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "rules/"):
		name, err := url.PathUnescape(strings.TrimPrefix(path, "rules/"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		adminKazumiRemove(w, r, name)
	case r.Method == http.MethodPost && path == "browse/refresh":
		if r.URL.Query().Get("async") == "0" {
			if err := refreshKazumiBrowseCache(); err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已更新 Kazumi 时间表/推荐缓存"})
			return
		}
		go func() {
			if err := refreshKazumiBrowseCache(); err != nil {
				logger.Warning("refresh kazumi browse cache failed: %v", err)
			}
		}()
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "started": true, "message": "已开始更新 Kazumi 时间表/推荐缓存，后台进行中"})
	case r.Method == http.MethodDelete && path == "browse/cache":
		clearKazumiBrowseCache()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "已清除 Kazumi 时间表/推荐缓存"})
	case r.Method == http.MethodPost && path == "search":
		adminKazumiSearch(w, r)
	case r.Method == http.MethodGet && path == "cache/export":
		adminCacheExport(w, r)
	case r.Method == http.MethodPost && path == "cache/import":
		adminCacheImport(w, r)
	case r.Method == http.MethodPost && path == "sources":
		handleKazumiSources(w, r)
	case r.Method == http.MethodPost && path == "test-mirror":
		adminKazumiTestMirror(w, r)
	case r.Method == http.MethodPost && path == "chapters":
		adminKazumiChapters(w, r)
	case r.Method == http.MethodPost && path == "resolve":
		adminKazumiResolve(w, r)
	case r.Method == http.MethodPost && path == "download":
		adminKazumiStartDownload(w, r)
	case r.Method == http.MethodGet && path == "downloads":
		adminKazumiDownloads(w, r)
	case r.Method == http.MethodGet && path == "downloads/export":
		adminDownloadExport(w, r)
	case r.Method == http.MethodPost && path == "downloads/import":
		adminDownloadImport(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "downloads/") && strings.HasSuffix(path, "/retry"):
		adminKazumiRetryDownload(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "downloads/"), "/retry"))
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "downloads/"):
		adminKazumiDeleteDownload(w, r, strings.TrimPrefix(path, "downloads/"))
	case r.Method == http.MethodGet && path == "browse/export":
		adminBrowseCacheExport(w, r)
	case r.Method == http.MethodPost && path == "browse/import":
		adminBrowseCacheImport(w, r)
	case r.Method == http.MethodGet && path == "files":
		adminKazumiFiles(w, r)
	case r.Method == http.MethodDelete && path == "files":
		adminKazumiDeleteFile(w, r)
	case (r.Method == http.MethodGet || r.Method == http.MethodPatch || r.Method == http.MethodPost || r.Method == http.MethodPut) && path == "settings":
		adminKazumiSettings(w, r)
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}

func adminKazumiCatalog(w http.ResponseWriter, r *http.Request) {
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	entries, err := repo.Catalog(r.URL.Query().Get("keyword"))
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"repository": repo.RepositoryURL(),
		"rules":      entries,
	})
}

func adminKazumiList(w http.ResponseWriter, r *http.Request) {
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	rules, err := repo.ListInstalled()
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"repository": repo.RepositoryURL(),
		"rules":      rules,
	})
}

type kazumiInstallRequest struct {
	Names []string `json:"names"`
	Name  string   `json:"name"`
	All   bool     `json:"all"`
}

func adminKazumiInstall(w http.ResponseWriter, r *http.Request) {
	var body kazumiInstallRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	names := body.Names
	if body.Name != "" {
		names = append(names, body.Name)
	}
	if body.All {
		entries, err := repo.Catalog("")
		if err != nil {
			writeKazumiError(w, err)
			return
		}
		names = make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name)
		}
	}
	if len(names) == 0 {
		writeError(w, http.StatusBadRequest, "missing rule names")
		return
	}
	results, err := repo.Install(names)
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": results})
}

func adminKazumiUpdate(w http.ResponseWriter, r *http.Request) {
	var body kazumiInstallRequest
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	names := body.Names
	if body.Name != "" {
		names = append(names, body.Name)
	}
	results, err := repo.Update(names)
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": results})
}

func adminKazumiRemove(w http.ResponseWriter, r *http.Request, name string) {
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	removed, err := repo.Remove([]string{name})
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

type kazumiSettingsPatch struct {
	RuleRepository      *string   `json:"rule_repository"`
	BangumiAcceleration *string   `json:"bangumi_acceleration"`
	BangumiMirror       *string   `json:"bangumi_mirror"`
	BangumiMirrors      *[]string `json:"bangumi_mirrors"`
	RulesMirror         *string   `json:"rules_mirror"`
	ImageAcceleration   *string   `json:"image_acceleration"`
}

func adminKazumiSettings(w http.ResponseWriter, r *http.Request) {
	settings := kazumi.CurrentSettings()
	if r.Method != http.MethodGet {
		var body kazumiSettingsPatch
		if r.ContentLength != 0 {
			if err := decodeBody(r, &body); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if body.RuleRepository != nil {
			settings.RuleRepository = *body.RuleRepository
		}
		if body.BangumiAcceleration != nil {
			settings.BangumiAcceleration = *body.BangumiAcceleration
		}
		if body.BangumiMirror != nil {
			settings.BangumiMirror = *body.BangumiMirror
		}
		if body.BangumiMirrors != nil {
			settings.BangumiMirrors = *body.BangumiMirrors
		}
		if body.RulesMirror != nil {
			settings.RulesMirror = *body.RulesMirror
		}
		if body.ImageAcceleration != nil {
			settings.ImageAcceleration = *body.ImageAcceleration
		}
		if err := kazumi.SaveSettings(settings); err != nil {
			writeKazumiError(w, err)
			return
		}
		settings = kazumi.CurrentSettings()
	}
	writeJSON(w, http.StatusOK, kazumiSettingsResponse(settings))
}

func kazumiSettingsResponse(settings kazumi.Settings) map[string]any {
	return map[string]any{
		"rule_repository":      settings.RuleRepository,
		"bangumi_acceleration": settings.BangumiAcceleration,
		"bangumi_mirror":       settings.BangumiMirror,
		"bangumi_mirrors":      settings.BangumiMirrors,
		"rules_mirror":         settings.RulesMirror,
		"image_acceleration":   settings.ImageAcceleration,
		"bangumi_modes":        kazumi.BangumiModes(),
		"rules_modes":          kazumi.RulesModes(),
		"image_modes":          kazumi.ImageModes(),
	}
}

func writeKazumiError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, kazumi.ErrRuleNotFound) || errors.Is(err, kazumi.ErrNotInstalled) {
		status = http.StatusNotFound
	}
	var fetchErr *kazumi.FetchError
	if errors.As(err, &fetchErr) {
		status = http.StatusBadGateway
	}
	writeError(w, status, err.Error())
}

func adminKazumiTestMirror(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, latency, err := kazumi.TestBangumiMirror(body.URL)
	if err != nil {
		message := err.Error()
		if strings.HasPrefix(message, "镜像可用，但") {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "status": status, "latency_ms": latency.Milliseconds(),
				"warning": message, "message": message,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "status": status, "latency_ms": latency.Milliseconds(), "message": message,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "status": status, "latency_ms": latency.Milliseconds(), "message": "镜像可用",
	})
}
