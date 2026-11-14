package httpserver

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/postprocessor"
	"github.com/HanamiSakiP/u-ani/internal/script"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

const (
	coverURL       = "/covers"
	scriptIDPrefix = "script:"
)

func jsonify(data any) map[string]any {
	return map[string]any{
		"version":     config.Version,
		"danmaku_api": config.Cfg.HTTP.DanmakuAPIURL,
		"data":        data,
	}
}

func localCoverPath(s string) string {
	if s == "" {
		return ""
	}
	if _, file := utils.ConvertCoverURLToPath(s); utils.IsImage(file) {
		return coverURL + "/" + utils.NormalizePath(s)
	}
	return ""
}

func coverPath(s string) string {
	if p := localCoverPath(s); p != "" {
		return p
	}
	if s == "" {
		return ""
	}
	// Prefer showing cover info immediately: queue local caching in background
	// and return the original URL so the frontend can display it (via image
	// proxy when needed).
	go controllers.EnsureCovers([]string{s})
	return s
}

func scriptID(name string) string {
	sum := sha1.Sum([]byte(name))
	return scriptIDPrefix + hex.EncodeToString(sum[:])
}

func sortedEpisodes(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for ep := range set {
		out = append(out, ep)
	}
	sort.Ints(out)
	return out
}

func followedItem(f *db.Followed, b *db.Bangumi) map[string]any {
	meta := itemMetaForName(b.Name)
	kazumiEpisodes, kazumiPlayer := kazumiDownloadInfoForMeta(meta)
	downloaded := len(kazumiEpisodes) > 0
	return map[string]any{
		"id":              b.ID,
		"name":            b.Name,
		"bangumi_name":    b.Name,
		"subtitle_group":  nonNilStrings(b.SubtitleGroup),
		"update_day":      b.UpdateDay,
		"cover":           localCoverPath(b.Cover),
		"status":          f.Status,
		"episodes":        sortedEpisodes(f.Episodes),
		"episode":         f.Episode(),
		"updated_time":    f.UpdatedTime,
		"subtitle":        nonNilStrings(f.Subtitle),
		"include":         nonNilStrings(f.Include),
		"exclude":         nonNilStrings(f.Exclude),
		"regex":           f.Regex,
		"season":          f.Season,
		"episode_offset":  f.EpisodeOffset,
		"display_name":    f.DisplayName,
		"is_script":       f.IsScript,
		"collection_type": meta.collection,
		"aliases":         meta.aliases,
		"custom_aliases":  meta.customAliases,
		"cached":          downloaded || meta.cached,
		"downloaded":      downloaded,
		"kazumi_episodes": kazumiEpisodes,
		"player":          kazumiPlayer,
	}
}

// itemMetaForName resolves aliases, subject link and collection state once so
// that listing many subscriptions does not repeat the same SQLite queries.
type itemMeta struct {
	names         []string
	subjectID     int
	aliases       []string
	customAliases []string
	collection    int
	cached        bool
}

func itemMetaForName(name string) itemMeta {
	meta := itemMeta{names: []string{name}, collection: 3, customAliases: []string{}, aliases: []string{}}
	if db.Conn == nil {
		return meta
	}
	seenAlias := map[string]bool{"": true}
	addAlias := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seenAlias[value] {
			return
		}
		seenAlias[value] = true
		meta.aliases = append(meta.aliases, value)
	}
	if followed, err := db.Conn.GetFollowed(name); err == nil && followed.DisplayName != "" {
		meta.names = append(meta.names, followed.DisplayName)
	}
	if manual, err := db.Conn.GetManualBangumi(name); err == nil {
		for _, alias := range manual.SearchNames {
			addAlias(alias)
			meta.names = append(meta.names, alias)
		}
	}
	if aliases, err := db.Conn.ListCustomAliases(name); err == nil {
		meta.customAliases = aliases
		for _, alias := range aliases {
			addAlias(alias)
			meta.names = append(meta.names, alias)
		}
	}
	if subjectID, err := db.Conn.GetSubjectIDByBangumiName(name); err == nil && subjectID > 0 {
		meta.subjectID = subjectID
		if value, err := db.Conn.GetBangumiCollection(subjectID); err == nil && value > 0 {
			meta.collection = value
		}
		if cache, err := db.Conn.GetBangumiCache(int64(subjectID)); err == nil && cache.Overview != "" {
			meta.cached = true
		}
	}
	return meta
}

func kazumiDownloadInfoForMeta(meta itemMeta) ([]int, map[string]map[string]string) {
	player := map[string]map[string]string{}
	if db.Conn == nil {
		return []int{}, player
	}
	videos, _ := db.Conn.KazumiVideosForNames(meta.names, meta.subjectID)
	if len(videos) == 0 {
		controllers.RepairKazumiDownloadLinksOnce()
		videos, _ = db.Conn.KazumiVideosForNames(meta.names, meta.subjectID)
	}
	episodes := make([]int, 0, len(videos))
	for episode, relative := range videos {
		episodes = append(episodes, episode)
		player[strconv.Itoa(episode)] = map[string]string{
			"path": kazumiPlayerPath(relative),
			"kind": "kazumi",
		}
	}
	sort.Ints(episodes)
	return episodes, player
}

func kazumiNamesForName(name string) ([]string, int) {
	names := []string{name}
	if followed, err := db.Conn.GetFollowed(name); err == nil && followed.DisplayName != "" {
		names = append(names, followed.DisplayName)
	}
	if manual, err := db.Conn.GetManualBangumi(name); err == nil {
		names = append(names, manual.SearchNames...)
	}
	if aliases, err := db.Conn.ListCustomAliases(name); err == nil {
		names = append(names, aliases...)
	}
	subjectID, _ := db.Conn.GetSubjectIDByBangumiName(name)
	return names, subjectID
}

func kazumiDownloadInfoForName(name string) ([]int, map[string]map[string]string) {
	player := map[string]map[string]string{}
	if db.Conn == nil {
		return []int{}, player
	}
	names, subjectID := kazumiNamesForName(name)
	videos, _ := db.Conn.KazumiVideosForNames(names, subjectID)
	if len(videos) == 0 {
		controllers.RepairKazumiDownloadLinksOnce()
		videos, _ = db.Conn.KazumiVideosForNames(names, subjectID)
	}
	episodes := make([]int, 0, len(videos))
	for episode, relative := range videos {
		episodes = append(episodes, episode)
		player[strconv.Itoa(episode)] = map[string]string{
			"path": kazumiPlayerPath(relative),
			"kind": "kazumi",
		}
	}
	sort.Ints(episodes)
	return episodes, player
}

func kazumiEpisodesForName(name string) []int {
	episodes, _ := kazumiDownloadInfoForName(name)
	return episodes
}

func kazumiPlayerForName(name string) map[string]map[string]string {
	_, player := kazumiDownloadInfoForName(name)
	return player
}

func kazumiPlayerPath(relative string) string {
	return "/kazumi-files/" + filepath.ToSlash(relative)
}

func mergedPlayer(name string, episodes []int, season, offset int, display string) map[string]map[string]string {
	player := getPlayer(name, episodes, season, offset, display)
	for episode, info := range kazumiPlayerForName(name) {
		player[episode] = info
	}
	return player
}

func bangumiCachedForName(name string, downloaded bool) bool {
	if db.Conn == nil {
		return false
	}
	if downloaded {
		return true
	}
	subjectID, err := db.Conn.GetSubjectIDByBangumiName(name)
	if err != nil {
		return false
	}
	cache, err := db.Conn.GetBangumiCache(int64(subjectID))
	return err == nil && cache.Overview != ""
}

func aliasesForBangumiName(name string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, value)
	}
	if db.Conn != nil {
		if manual, err := db.Conn.GetManualBangumi(name); err == nil {
			for _, alias := range manual.SearchNames {
				add(alias)
			}
		}
		if values, err := db.Conn.ListCustomAliases(name); err == nil {
			for _, alias := range values {
				add(alias)
			}
		}
	}
	return out
}

func customAliasesForBangumiName(name string) []string {
	if db.Conn == nil {
		return nil
	}
	values, _ := db.Conn.ListCustomAliases(name)
	return values
}

func collectionTypeForBangumi(name string) int {
	if db.Conn == nil {
		return 3
	}
	if id, err := db.Conn.GetSubjectIDByBangumiName(name); err == nil {
		if value, err := db.Conn.GetBangumiCollection(id); err == nil && value > 0 {
			return value
		}
	}
	return 3
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func apiRouter(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(path, "index/"):
		handleIndex(w, r, strings.TrimPrefix(path, "index/"))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "player/"):
		handlePlayer(w, r, strings.TrimPrefix(path, "player/"))
	case r.Method == http.MethodGet && path == "calendar":
		handleCalendar(w, r)
	case r.Method == http.MethodGet && path == "image":
		handleImage(w, r)
	case r.Method == http.MethodGet && path == "kazumi/timeline":
		handleKazumiTimeline(w, r)
	case r.Method == http.MethodGet && path == "kazumi/recommend/all":
		handleKazumiRecommendCategories(w, r)
	case r.Method == http.MethodGet && path == "kazumi/recommend":
		handleKazumiRecommend(w, r)
	case r.Method == http.MethodGet && path == "kazumi/lookup":
		handleKazumiLookup(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "kazumi/subject/"):
		handleKazumiSubjectRoute(w, r, strings.TrimPrefix(path, "kazumi/subject/"))
	case strings.HasPrefix(path, "admin/"):
		adminRouter(w, r, strings.TrimPrefix(path, "admin/"))
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}

// bangumiSummary returns the upstream Bangumi response fields plus the
// extra fields used by the embedded 追番 UI (collection filter, cached badge
// and downloaded episode numbers).
func bangumiSummary(item map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{
		"id", "status", "episode", "cover", "bangumi_name", "updated_time",
		"display_name", "update_day", "episodes", "subtitle", "include",
		"exclude", "regex", "season", "episode_offset", "is_script",
		"collection_type", "aliases", "custom_aliases", "cached", "downloaded", "kazumi_episodes",
	} {
		if value, ok := item[key]; ok {
			out[key] = value
		}
	}
	if _, ok := out["display_name"]; !ok {
		out["display_name"] = ""
	}
	return out
}

func handleIndex(w http.ResponseWriter, r *http.Request, kind string) {
	if kind != "index" && kind != "old" {
		writeError(w, http.StatusBadRequest, "type should be `index` or `old`")
		return
	}
	old := kind == "old"
	if r.URL.Query().Get("refresh") != "1" {
		indexMu.Lock()
		entry, ok := indexCache[kind]
		indexMu.Unlock()
		if ok && time.Since(entry.at) < 10*time.Second {
			writeJSON(w, http.StatusOK, entry.payload)
			return
		}
	}
	if err := ensureDB(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := db.BangumiStatusUpdating
	if old {
		status = db.BangumiStatusEnd
	}
	list, err := db.Conn.AllFollowedBangumi(status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := make([]map[string]any, 0, len(list))
	for i := range list {
		data = append(data, bangumiSummary(followedItem(&list[i].Followed, &list[i].Bangumi)))
	}

	if !old {
		// Append locally installed script entries.
		models := script.GetModels()
		for _, m := range models {
			row, err := db.Conn.GetScript(m.Name)
			if err != nil || row.Status == db.FollowedStatusDeleted {
				continue
			}
			data = append(data, bangumiSummary(map[string]any{
				"id":           scriptID(m.Name),
				"bangumi_name": m.Name,
				"updated_time": row.UpdatedTime,
				"status":       row.Status,
				"cover":        localCoverPath(m.Cover),
				"episodes":     sortedEpisodes(row.Episodes),
				"episode":      row.Episode(),
			}))
		}
		sortByUpdated(data, false)
	} else {
		sortByUpdated(data, true)
	}

	// 追番优先显示已经下载视频的封面信息并排在前面（仅本地信息）。
	sort.SliceStable(data, func(i, j int) bool {
		di, _ := data[i]["downloaded"].(bool)
		dj, _ := data[j]["downloaded"].(bool)
		if di != dj {
			return di
		}
		ci, _ := data[i]["cover"].(string)
		cj, _ := data[j]["cover"].(string)
		if (ci != "") != (cj != "") {
			return ci != ""
		}
		return false
	})

	payload := jsonify(data)
	indexMu.Lock()
	indexCache[kind] = indexCacheEntry{at: time.Now(), payload: payload}
	indexMu.Unlock()
	writeJSON(w, http.StatusOK, payload)
}

func sortByUpdated(items []map[string]any, ascending bool) {
	sort.SliceStable(items, func(i, j int) bool {
		left, _ := items[i]["updated_time"].(int64)
		right, _ := items[j]["updated_time"].(int64)
		if left == 0 {
			left = 1
		}
		if right == 0 {
			right = 1
		}
		if ascending {
			return left < right
		}
		return left > right
	})
}

func handlePlayer(w http.ResponseWriter, r *http.Request, bangumiID string) {
	if err := ensureDB(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var item map[string]any
	if b, err := db.Conn.GetBangumiByID(bangumiID); err == nil {
		if f, err := db.Conn.GetFollowedNotDeleted(b.Name); err == nil {
			item = followedItem(f, b)
		}
	}
	if item == nil && strings.HasPrefix(bangumiID, scriptIDPrefix) {
		for _, m := range script.GetModels() {
			if scriptID(m.Name) != bangumiID {
				continue
			}
			row, err := db.Conn.GetScript(m.Name)
			if err != nil || row.Status == db.FollowedStatusDeleted {
				continue
			}
			item = map[string]any{
				"id":           bangumiID,
				"bangumi_name": m.Name,
				"updated_time": row.UpdatedTime,
				"status":       row.Status,
				"cover":        localCoverPath(m.Cover),
				"episodes":     sortedEpisodes(row.Episodes),
				"episode":      row.Episode(),
			}
			break
		}
	}
	if item == nil {
		writeError(w, http.StatusNotFound, "bangumi not found")
		return
	}

	episodes := intSlice(item["episodes"])
	delete(item, "episodes")
	season := 1
	if value, ok := item["season"].(int); ok {
		season = value
	}
	offset, _ := item["episode_offset"].(int)
	display, _ := item["display_name"].(string)
	name := fmt.Sprint(item["bangumi_name"])
	player := mergedPlayer(name, episodes, season, offset, display)
	summary := bangumiSummary(item)
	summary["player"] = player

	writeJSON(w, http.StatusOK, jsonify(summary))
}

func intSlice(value any) []int {
	switch v := value.(type) {
	case []int:
		return v
	case []any:
		out := make([]int, 0, len(v))
		for _, item := range v {
			if n, ok := item.(int); ok {
				out = append(out, n)
			}
		}
		return out
	default:
		return nil
	}
}

func handleCalendar(w http.ResponseWriter, r *http.Request) {
	if err := ensureDB(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	weekly, err := db.Conn.GetUpdatingBangumi(nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := map[string]any{
		"sat": nil,
		"sun": nil,
		"mon": nil,
		"tue": nil,
		"wed": nil,
		"thu": nil,
		"fri": nil,
	}
	for day, items := range weekly {
		list := make([]map[string]any, 0, len(items))
		for _, item := range items {
			subs, _ := db.Conn.GetSubtitlesByIDs(item.SubtitleGroup)
			subtitleNames := make([]string, 0, len(subs))
			for _, sub := range subs {
				subtitleNames = append(subtitleNames, sub.Name)
			}
			entry := map[string]any{
				"id":             item.ID,
				"cover":          localCoverPath(item.Cover),
				"name":           item.Name,
				"update_day":     item.UpdateDay,
				"status":         nullableInt(item.FollowedStatus),
				"episode":        nullableInt(item.Episode),
				"subtitle_group": subtitleNames,
			}
			if item.FollowedStatus != nil {
				if followed, err := db.Conn.GetFollowed(item.Name); err == nil {
					installedSet := map[int]bool{}
					for ep := range followed.Episodes {
						installedSet[ep] = true
					}
					kazumi := kazumiEpisodesForName(item.Name)
					for _, ep := range kazumi {
						installedSet[ep] = true
					}
					installed := make([]int, 0, len(installedSet))
					for ep := range installedSet {
						installed = append(installed, ep)
					}
					sort.Ints(installed)
					entry["followed"] = true
					entry["installed_episodes"] = installed
					entry["installed_count"] = len(installed)
					entry["bangumi_name"] = item.Name
					entry["season"] = followed.Season
					entry["episode_offset"] = followed.EpisodeOffset
					entry["display_name"] = followed.DisplayName
					if len(kazumi) > 0 {
						entry["downloaded"] = true
						entry["kazumi_episodes"] = kazumi
					}
				}
			} else {
				entry["followed"] = false
			}
			list = append(list, entry)
		}
		out[day] = list
	}
	writeJSON(w, http.StatusOK, out)
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func ensureDB() error {
	if db.Conn != nil {
		return nil
	}
	_, err := db.New()
	return err
}

var (
	coverSyncMu   sync.Mutex
	coverSyncLast time.Time

	indexMu    sync.Mutex
	indexCache = map[string]indexCacheEntry{}
)

type indexCacheEntry struct {
	at      time.Time
	payload any
}

// ensureCoversAsync downloads missing local covers for subscriptions and the
// source calendar, at most once every 30 seconds.
func ensureCoversAsync() {
	coverSyncMu.Lock()
	if time.Since(coverSyncLast) < 30*time.Second {
		coverSyncMu.Unlock()
		return
	}
	coverSyncLast = time.Now()
	coverSyncMu.Unlock()
	go controllers.DownloadCovers(false)
}

func adminRouter(w http.ResponseWriter, r *http.Request, path string) {
	if !adminAuthorized(r) {
		writeError(w, http.StatusForbidden, "wrong api token")
		return
	}
	switch {
	case r.Method == http.MethodPost && path == "auth":
		writeJSON(w, http.StatusOK, map[string]any{})
	case r.Method == http.MethodPost && path == "add":
		adminAdd(w, r)
	case r.Method == http.MethodPost && path == "delete":
		adminDelete(w, r)
	case r.Method == http.MethodPost && path == "update":
		adminUpdate(w, r)
	case r.Method == http.MethodPost && path == "search":
		adminSearch(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "filter/"):
		adminGetFilter(w, r, strings.TrimPrefix(path, "filter/"))
	case r.Method == http.MethodPatch && strings.HasPrefix(path, "filter/"):
		adminPatchFilter(w, r, strings.TrimPrefix(path, "filter/"))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "seen/"):
		adminSeen(w, r, strings.TrimPrefix(path, "seen/"))
	case r.Method == http.MethodPost && path == "seen_forget":
		adminSeenChange(w, r, true)
	case r.Method == http.MethodPost && path == "seen_mark":
		adminSeenChange(w, r, false)
	case r.Method == http.MethodGet && path == "cache/export":
		adminCacheExport(w, r)
	case r.Method == http.MethodPost && path == "cache/import":
		adminCacheImport(w, r)
	case strings.HasPrefix(path, "bangumi/"):
		adminBangumiRouter(w, r, strings.TrimPrefix(path, "bangumi/"))
	case strings.HasPrefix(path, "webdav"):
		adminWebDAVRouter(w, r, strings.TrimPrefix(strings.TrimPrefix(path, "webdav"), "/"))
	case strings.HasPrefix(path, "kazumi/"):
		adminKazumiRouter(w, r, strings.TrimPrefix(path, "kazumi/"))
	default:
		writeError(w, http.StatusNotFound, "Not Found")
	}
}

func adminAuthorized(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	return strings.TrimPrefix(header, "Bearer ") == config.Cfg.HTTP.AdminToken
}

type addRequest struct {
	Bangumi       *string `json:"bangumi"`
	Name          *string `json:"name"`
	BgmID         any     `json:"bgm_id"`
	BgmURL        *string `json:"bgm_url"`
	Season        *int    `json:"season"`
	EpisodeOffset *int    `json:"episode_offset"`
	DisplayName   *string `json:"display_name"`
}

func adminAdd(w http.ResponseWriter, r *http.Request) {
	var body addRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	episode := 0

	var bgm string
	if body.BgmURL != nil && *body.BgmURL != "" {
		bgm = *body.BgmURL
	} else if body.BgmID != nil {
		bgm = fmt.Sprint(body.BgmID)
	}
	if bgm != "" {
		result := controllers.AddFromBgmTV(bgm, &episode, body.Season, body.EpisodeOffset, body.DisplayName)
		if result.Status == "error" {
			writeError(w, http.StatusNotFound, result.Message)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}

	name := ""
	if body.Bangumi != nil {
		name = *body.Bangumi
	} else if body.Name != nil {
		name = *body.Name
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing bangumi name")
		return
	}
	result := controllers.Add(name, &episode, body.Season, body.EpisodeOffset, body.DisplayName)
	if result.Status == "error" {
		writeError(w, http.StatusNotFound, result.Message)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

type deleteRequest struct {
	Bangumi *string `json:"bangumi"`
	Name    *string `json:"name"`
}

func adminDelete(w http.ResponseWriter, r *http.Request) {
	var body deleteRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := ""
	if body.Bangumi != nil {
		name = *body.Bangumi
	} else if body.Name != nil {
		name = *body.Name
	}
	if err := ensureDB(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	followed, err := db.Conn.GetFollowedNotDeleted(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "bangumi not exists or not followed")
		return
	}
	followed.Status = db.FollowedStatusDeleted
	if err := db.Conn.SaveFollowed(followed); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

type updateRequest struct {
	Names     []string `json:"names"`
	NotIgnore bool     `json:"not_ignore"`
}

func adminUpdate(w http.ResponseWriter, r *http.Request) {
	var body updateRequest
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	async := r.URL.Query().Get("async") != "0"
	if async {
		go func(names []string, notIgnore bool) {
			if err := controllers.Update(names, true, notIgnore); err != nil {
				logger.Warning("update failed: %v", err)
				return
			}
			if config.Cfg.EnablePathFormatter {
				postprocessor.ProcessCompletedDownloads()
			}
		}(append([]string(nil), body.Names...), body.NotIgnore)
		writeJSON(w, http.StatusAccepted, map[string]any{"updated": "started", "async": true})
		return
	}
	if err := controllers.Update(body.Names, true, body.NotIgnore); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if config.Cfg.EnablePathFormatter {
		postprocessor.ProcessCompletedDownloads()
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true})
}

type searchRequest struct {
	Keyword    string  `json:"keyword"`
	Count      int     `json:"count"`
	Regex      *string `json:"regex"`
	Dupe       bool    `json:"dupe"`
	MinEpisode *int    `json:"min_episode"`
	MaxEpisode *int    `json:"max_episode"`
	Tag        bool    `json:"tag"`
	Subtitle   *string `json:"subtitle"`
}

func adminSearch(w http.ResponseWriter, r *http.Request) {
	var body searchRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Keyword == "" {
		writeError(w, http.StatusBadRequest, "missing keyword")
		return
	}
	result := controllers.Search(body.Keyword, body.Count, body.Regex, body.Dupe, body.MinEpisode, body.MaxEpisode, body.Tag, body.Subtitle)
	if result.Status != "success" {
		writeError(w, http.StatusBadGateway, result.Message)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":    result.Data["data"],
		"options": result.Options,
	})
}

func adminGetFilter(w http.ResponseWriter, r *http.Request, name string) {
	if err := ensureDB(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	followed, err := db.Conn.GetFollowedNotDeleted(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "bangumi not followed")
		return
	}
	bangumi, err := db.Conn.GetBangumiByName(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "bangumi not exists")
		return
	}
	available, _ := db.Conn.GetSubtitlesByIDs(bangumi.SubtitleGroup)
	selected, _ := db.Conn.GetSubtitlesByIDs(followed.Subtitle)
	writeJSON(w, http.StatusOK, map[string]any{
		"available_subtitle": subtitleNames(available),
		"selected_subtitle":  subtitleNames(selected),
		"include":            nonNilStrings(followed.Include),
		"exclude":            nonNilStrings(followed.Exclude),
		"regex":              followed.Regex,
		"season":             followed.Season,
		"episode_offset":     followed.EpisodeOffset,
		"display_name":       followed.DisplayName,
	})
}

type filterPatchRequest struct {
	SelectedSubtitle *[]string `json:"selected_subtitle"`
	Include          *[]string `json:"include"`
	Exclude          *[]string `json:"exclude"`
	Regex            *string   `json:"regex"`
}

func adminPatchFilter(w http.ResponseWriter, r *http.Request, name string) {
	var body filterPatchRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ensureDB(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	followed, err := db.Conn.GetFollowedNotDeleted(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "bangumi not followed")
		return
	}
	bangumi, err := db.Conn.GetBangumiByName(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "bangumi not exists")
		return
	}

	if body.SelectedSubtitle != nil {
		available, _ := db.Conn.GetSubtitlesByIDs(bangumi.SubtitleGroup)
		byName := map[string]string{}
		for _, s := range available {
			byName[s.Name] = s.ID
		}
		ids := make([]string, 0, len(*body.SelectedSubtitle))
		for _, selected := range *body.SelectedSubtitle {
			id, ok := byName[selected]
			if !ok {
				writeError(w, http.StatusNotFound, fmt.Sprintf("字幕组 %s 不可用", selected))
				return
			}
			ids = append(ids, id)
		}
		followed.Subtitle = ids
	}
	if body.Include != nil {
		followed.Include = *body.Include
	}
	if body.Exclude != nil {
		followed.Exclude = *body.Exclude
	}
	if body.Regex != nil {
		followed.Regex = *body.Regex
	}
	if err := db.Conn.SaveFollowed(followed); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func subtitleNames(items []db.Subtitle) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Name)
	}
	return out
}

func adminSeen(w http.ResponseWriter, r *http.Request, name string) {
	result := controllers.Seen(name)
	if result.Status != "success" {
		writeError(w, http.StatusNotFound, result.Message)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bangumi":       result.Data["bangumi"],
		"total_episode": result.Data["total_episode"],
		"seen":          result.Data["seen"],
	})
}

type seenRequest struct {
	Bangumi  *string `json:"bangumi"`
	Name     *string `json:"name"`
	Episode  *int    `json:"episode"`
	Episodes *[]int  `json:"episodes"`
}

func adminSeenChange(w http.ResponseWriter, r *http.Request, forget bool) {
	var body seenRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := ""
	if body.Bangumi != nil {
		name = *body.Bangumi
	} else if body.Name != nil {
		name = *body.Name
	}
	var episodes []int
	if body.Episodes != nil {
		episodes = *body.Episodes
	} else if body.Episode != nil {
		episodes = []int{*body.Episode}
	}

	var result controllers.Result
	if forget {
		result = controllers.SeenForgetBatch(name, episodes)
	} else {
		result = controllers.SeenMarkBatch(name, episodes)
	}
	if result.Status != "success" {
		writeError(w, http.StatusNotFound, result.Message)
		return
	}
	data := map[string]any{
		"bangumi": result.Data["bangumi"],
		"seen":    result.Data["seen"],
	}
	if body.Episodes == nil {
		if body.Episode != nil {
			data["episode"] = *body.Episode
		} else {
			data["episode"] = nil
		}
	} else {
		data["episodes"] = result.Episodes
	}
	writeJSON(w, http.StatusOK, data)
}
