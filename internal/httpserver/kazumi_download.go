package httpserver

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/kazumi"
)

type kazumiSearchRequest struct {
	Rule    string `json:"rule"`
	Keyword string `json:"keyword"`
}

func adminKazumiSearch(w http.ResponseWriter, r *http.Request) {
	var body kazumiSearchRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := kazumi.CurrentRule(strings.TrimSpace(body.Rule))
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	results, err := kazumi.Search(rule, body.Keyword)
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "rule": rule.Name})
}

type kazumiChaptersRequest struct {
	Rule   string `json:"rule"`
	Source string `json:"source"`
}

func adminKazumiChapters(w http.ResponseWriter, r *http.Request) {
	var body kazumiChaptersRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := kazumi.CurrentRule(strings.TrimSpace(body.Rule))
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	roads, err := kazumi.Chapters(rule, body.Source)
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roads": roads})
}

func adminKazumiStartDownload(w http.ResponseWriter, r *http.Request) {
	var body controllers.KazumiDownloadOptions
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := controllers.StartKazumiDownload(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func adminKazumiDownloads(w http.ResponseWriter, r *http.Request) {
	tasks, err := controllers.ListKazumiDownloads()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func adminKazumiFiles(w http.ResponseWriter, r *http.Request) {
	files, err := controllers.ListKazumiFiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "root": controllers.KazumiFilesDir()})
}

func adminKazumiDeleteFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if err := controllers.DeleteKazumiFile(path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": path})
}

func adminKazumiRuleFile(w http.ResponseWriter, r *http.Request) {
	name, err := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/kazumi/rules/"), "/file"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method == http.MethodGet {
		raw, err := kazumi.ReadCurrentRule(name)
		if err != nil {
			writeKazumiError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": name, "content": string(raw)})
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	if err := repo.SaveRule(name, []byte(body.Content)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "saved": true})
}

func adminKazumiDeleteDownload(w http.ResponseWriter, r *http.Request, rawID string) {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	if r.URL.Query().Get("delete") == "1" {
		if err := controllers.DeleteKazumiDownload(id); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
		return
	}
	if err := controllers.CancelKazumiDownload(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"canceled": id})
}

func adminKazumiRetryDownload(w http.ResponseWriter, r *http.Request, rawID string) {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid task id")
		return
	}
	task, err := controllers.RetryKazumiDownload(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func adminKazumiResolve(w http.ResponseWriter, r *http.Request) {
	var body kazumiChaptersRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := kazumi.CurrentRule(strings.TrimSpace(body.Rule))
	if err != nil {
		writeKazumiError(w, err)
		return
	}
	input, media, referer, err := kazumi.ResolvePlayback(rule, body.Source)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if input == "" {
		writeError(w, http.StatusNotFound, "没有解析到可播放地址")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url": input, "media": media, "referer": referer,
	})
}
