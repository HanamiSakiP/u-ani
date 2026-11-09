package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/bgmtv"
	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/model"
	"github.com/HanamiSakiP/u-ani/internal/source"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

func intPtr(v int) *int { return &v }

func setupControllersDB(t *testing.T) {
	t.Helper()
	oldDBPath, oldSource := config.Cfg.DBPath, config.Cfg.DataSource
	if db.Conn != nil {
		_ = db.Conn.Close()
		db.Conn = nil
	}
	config.Cfg.DBPath = filepath.Join(t.TempDir(), "bangumi.db")
	config.Cfg.DataSource = "does-not-exist"
	conn, err := db.New()
	if err != nil {
		t.Fatal(err)
	}
	db.Conn = conn
	t.Cleanup(func() {
		_ = conn.Close()
		db.Conn = nil
		config.Cfg.DBPath = oldDBPath
		config.Cfg.DataSource = oldSource
	})
}

func addBangumi(t *testing.T, data model.WebsiteBangumi) {
	t.Helper()
	if err := db.Conn.SaveBangumi(data); err != nil {
		t.Fatal(err)
	}
}

func TestAddLifecycle(t *testing.T) {
	setupControllersDB(t)
	addBangumi(t, model.WebsiteBangumi{ID: "one-piece", Name: "海贼王", UpdateDay: "Mon"})

	result := Add("海贼王", intPtr(0), nil, nil, nil)
	if result.Status != "success" {
		t.Fatalf("first Add status = %q (%s)", result.Status, result.Message)
	}
	followed, err := db.Conn.GetFollowed("海贼王")
	if err != nil {
		t.Fatal(err)
	}
	if len(followed.Episodes) != 0 {
		t.Errorf("episode 0 should not mark anything seen, got %v", followed.Episodes)
	}

	result = Add("海贼王", intPtr(0), nil, nil, nil)
	if result.Status != "warning" {
		t.Errorf("second Add status = %q, want warning", result.Status)
	}
}

func TestAddAutoDisplayNameFromSeasonSuffix(t *testing.T) {
	setupControllersDB(t)
	name := "相反的你和我 第二季"
	addBangumi(t, model.WebsiteBangumi{ID: "polar-opposites-s2", Name: name, UpdateDay: "Mon"})

	result := Add(name, intPtr(0), nil, nil, nil)
	if result.Status != "success" {
		t.Fatalf("Add status = %q (%s)", result.Status, result.Message)
	}
	followed, err := db.Conn.GetFollowed(name)
	if err != nil {
		t.Fatal(err)
	}
	if followed.Season != 2 {
		t.Errorf("season = %d, want 2", followed.Season)
	}
	if followed.DisplayName != "相反的你和我" {
		t.Errorf("display_name = %q, want 相反的你和我", followed.DisplayName)
	}
}

func TestAddExplicitDisplayNameSkipsAutoNormalization(t *testing.T) {
	setupControllersDB(t)
	name := "相反的你和我 第二季"
	addBangumi(t, model.WebsiteBangumi{ID: "polar-opposites-s2", Name: name, UpdateDay: "Mon"})

	result := Add(name, intPtr(0), nil, nil, strPtr("You and I Are Polar Opposites"))
	if result.Status != "success" {
		t.Fatalf("Add status = %q (%s)", result.Status, result.Message)
	}
	followed, err := db.Conn.GetFollowed(name)
	if err != nil {
		t.Fatal(err)
	}
	if followed.DisplayName != "You and I Are Polar Opposites" {
		t.Errorf("display_name = %q", followed.DisplayName)
	}
}

func strPtr(v string) *string { return &v }

func TestAddFromBgmTVMatchesSourceBangumi(t *testing.T) {
	setupControllersDB(t)
	addBangumi(t, model.WebsiteBangumi{ID: "haruhi", Name: "凉宫春日的忧郁", UpdateDay: "Sun"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/subjects/253" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      253,
			"name":    "涼宮ハルヒの憂鬱",
			"name_cn": "凉宫春日的忧郁",
		})
	}))
	defer server.Close()
	oldBase := bgmtv.APIBase
	oldLang := config.Cfg.Lang
	bgmtv.APIBase = server.URL
	config.Cfg.Lang = "zh_cn"
	defer func() {
		bgmtv.APIBase = oldBase
		config.Cfg.Lang = oldLang
	}()

	result := AddFromBgmTV("https://bgm.tv/subject/253", intPtr(0), nil, nil, nil)
	if result.Status != "success" {
		t.Fatalf("AddFromBgmTV status = %q (%s)", result.Status, result.Message)
	}
	if result.Data["bangumi_name"] != "凉宫春日的忧郁" {
		t.Fatalf("matched bangumi name = %#v", result.Data["bangumi_name"])
	}
	if _, err := db.Conn.GetFollowed("凉宫春日的忧郁"); err != nil {
		t.Fatalf("source bangumi was not followed: %v", err)
	}
	if _, err := db.Conn.GetManualBangumi("凉宫春日的忧郁"); err == nil {
		t.Fatal("a source match should not be stored as a manual subscription")
	}
}

func TestAddFromBgmTVCreatesManualSubscriptionAndCover(t *testing.T) {
	setupControllersDB(t)
	addBangumi(t, model.WebsiteBangumi{ID: "other", Name: "另一部番", UpdateDay: "Mon"})

	savePath := t.TempDir()
	oldSavePath := config.Cfg.SavePath
	config.Cfg.SavePath = savePath
	defer func() { config.Cfg.SavePath = oldSavePath }()

	var coverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/subjects/999":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      999,
				"url":     "https://bgm.tv/subject/999",
				"name":    "葬送のフリーレン",
				"name_cn": "葬送的芙莉莲",
				"images":  map[string]string{"large": coverURL},
				"infobox": []map[string]any{{
					"key":   "别名",
					"value": []map[string]string{{"v": "Frieren"}},
				}},
			})
		case "/calendar":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"weekday": map[string]any{"en": "Sun", "cn": "星期日"},
				"items":   []map[string]any{{"id": 999, "name": "葬送のフリーレン"}},
			}})
		case "/cover.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0, 'f', 'a', 'k', 'e'})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	coverURL = server.URL + "/cover.jpg"

	oldBase := bgmtv.APIBase
	oldLang := config.Cfg.Lang
	bgmtv.APIBase = server.URL
	config.Cfg.Lang = "zh_cn"
	defer func() {
		bgmtv.APIBase = oldBase
		config.Cfg.Lang = oldLang
	}()

	result := AddFromBgmTV("999", intPtr(0), nil, nil, nil)
	if result.Status != "success" {
		t.Fatalf("AddFromBgmTV status = %q (%s)", result.Status, result.Message)
	}
	bangumi, err := db.Conn.GetBangumiByName("葬送的芙莉莲")
	if err != nil {
		t.Fatal(err)
	}
	if bangumi.ID != "bgmtv:999" || bangumi.UpdateDay != "Sun" || bangumi.Cover != coverURL {
		t.Fatalf("unexpected manual bangumi: %#v", bangumi)
	}
	if _, err := db.Conn.GetFollowed("葬送的芙莉莲"); err != nil {
		t.Fatalf("manual subscription not followed: %v", err)
	}
	manual, err := db.Conn.GetManualBangumi("葬送的芙莉莲")
	if err != nil {
		t.Fatal(err)
	}
	if manual.Provider != "bgmtv" || manual.ProviderID != "999" {
		t.Fatalf("unexpected manual mapping: %#v", manual)
	}
	foundAlias := false
	for _, name := range manual.SearchNames {
		if name == "Frieren" {
			foundAlias = true
		}
	}
	if !foundAlias {
		t.Fatalf("bgm.tv aliases not stored: %#v", manual.SearchNames)
	}
	_, coverFile := utils.ConvertCoverURLToPath(coverURL)
	if !utils.IsImage(coverFile) {
		t.Fatalf("cover was not downloaded to %s", coverFile)
	}
}

func TestFilter(t *testing.T) {
	setupControllersDB(t)
	name := "名侦探柯南"
	addBangumi(t, model.WebsiteBangumi{
		ID:        "conan",
		Name:      name,
		UpdateDay: "Mon",
		SubtitleGroup: []model.SubtitleGroup{
			{ID: "id1", Name: "sg1"},
			{ID: "id2", Name: "sg2"},
		},
	})
	if result := Add(name, intPtr(0), nil, nil, nil); result.Status != "success" {
		t.Fatalf("Add status = %q (%s)", result.Status, result.Message)
	}

	result := Filter(name, strPtr("sg1"), strPtr("include"), strPtr("exclude"), strPtr("regex"))
	if result.Status != "success" {
		t.Fatalf("Filter status = %q (%s)", result.Status, result.Message)
	}
	followed, err := db.Conn.GetFollowed(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(followed.Subtitle) != 1 || followed.Subtitle[0] != "id1" {
		t.Errorf("subtitle = %v, want [id1]", followed.Subtitle)
	}
	if len(followed.Include) != 1 || followed.Include[0] != "include" {
		t.Errorf("include = %v", followed.Include)
	}
	if len(followed.Exclude) != 1 || followed.Exclude[0] != "exclude" {
		t.Errorf("exclude = %v", followed.Exclude)
	}
	if followed.Regex != "regex" {
		t.Errorf("regex = %q", followed.Regex)
	}
}

func TestSeenMarkAndForgetUpdateDownloads(t *testing.T) {
	setupControllersDB(t)
	name := "海贼王"
	addBangumi(t, model.WebsiteBangumi{ID: "one-piece", Name: name, UpdateDay: "Mon"})
	if result := Add(name, intPtr(0), nil, nil, nil); result.Status != "success" {
		t.Fatalf("Add status = %q (%s)", result.Status, result.Message)
	}

	taskID := "task-3"
	if err := db.Conn.SaveDownload(&db.Download{
		BangumiName: name,
		Episode:     3,
		Title:       "episode 3",
		Download:    "magnet:?xt=urn:btih:3",
		Status:      db.DownloadStatusDownloading,
		TaskID:      &taskID,
	}); err != nil {
		t.Fatal(err)
	}

	result := SeenMark(name, 3)
	if result.Status != "success" {
		t.Fatalf("SeenMark status = %q (%s)", result.Status, result.Message)
	}
	followed, err := db.Conn.GetFollowed(name)
	if err != nil {
		t.Fatal(err)
	}
	if !followed.Episodes[3] {
		t.Error("episode 3 should be marked as seen")
	}
	dl, err := db.Conn.GetDownload(name, "magnet:?xt=urn:btih:3", 3, "episode 3")
	if err != nil {
		t.Fatal(err)
	}
	if dl.Status != db.DownloadStatusDownloaded || dl.TaskID != nil {
		t.Errorf("download after mark: status=%d task_id=%v", dl.Status, dl.TaskID)
	}

	result = SeenForget(name, 3)
	if result.Status != "success" {
		t.Fatalf("SeenForget status = %q (%s)", result.Status, result.Message)
	}
	followed, err = db.Conn.GetFollowed(name)
	if err != nil {
		t.Fatal(err)
	}
	if followed.Episodes[3] {
		t.Error("episode 3 should have been forgotten")
	}
	dl, err = db.Conn.GetDownload(name, "magnet:?xt=urn:btih:3", 3, "episode 3")
	if err != nil {
		t.Fatal(err)
	}
	if dl.Status != db.DownloadStatusDownloaded || dl.TaskID != nil {
		t.Errorf("forget must not mark the download failed: status=%d task_id=%v", dl.Status, dl.TaskID)
	}
}

func TestSeenBatchRejectsInvalidEpisodes(t *testing.T) {
	setupControllersDB(t)
	name := "海贼王"
	addBangumi(t, model.WebsiteBangumi{ID: "one-piece", Name: name, UpdateDay: "Mon"})
	if result := Add(name, intPtr(0), nil, nil, nil); result.Status != "success" {
		t.Fatalf("Add status = %q (%s)", result.Status, result.Message)
	}
	for _, episodes := range [][]int{{}, {0}} {
		if got := SeenMarkBatch(name, episodes); got.Status != "error" {
			t.Errorf("SeenMarkBatch(%v) status = %q, want error", episodes, got.Status)
		}
		if got := SeenForgetBatch(name, episodes); got.Status != "error" {
			t.Errorf("SeenForgetBatch(%v) status = %q, want error", episodes, got.Status)
		}
	}
}

func TestDelete(t *testing.T) {
	setupControllersDB(t)
	name := "海贼王"
	addBangumi(t, model.WebsiteBangumi{ID: "one-piece", Name: name, UpdateDay: "Mon"})
	if result := Add(name, intPtr(0), nil, nil, nil); result.Status != "success" {
		t.Fatalf("Add status = %q (%s)", result.Status, result.Message)
	}

	if result := Delete(name, false, false); result.Status != "warning" {
		t.Errorf("Delete status = %q, want warning", result.Status)
	}
	followed, err := db.Conn.GetFollowed(name)
	if err != nil {
		t.Fatal(err)
	}
	if followed.Status != db.FollowedStatusDeleted {
		t.Errorf("followed status = %d, want deleted", followed.Status)
	}
}

func TestDownloadCoversDownloadsMissingImages(t *testing.T) {
	setupControllersDB(t)
	savePath := t.TempDir()
	oldSavePath := config.Cfg.SavePath
	config.Cfg.SavePath = savePath
	defer func() { config.Cfg.SavePath = oldSavePath }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cover.jpg" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0, 'f', 'a', 'k', 'e'})
	}))
	defer server.Close()
	cover := server.URL + "/cover.jpg"
	addBangumi(t, model.WebsiteBangumi{ID: "cover-test", Name: "封面测试", UpdateDay: "Mon", Cover: cover})

	DownloadCovers(false)
	_, file := utils.ConvertCoverURLToPath(cover)
	if !utils.IsImage(file) {
		t.Fatalf("cover was not downloaded to %s", file)
	}
}

type fakeTorrentSource struct {
	results map[string][]model.Episode
}

func (f *fakeTorrentSource) Name() string { return "fake" }

func (f *fakeTorrentSource) SearchByKeyword(keyword string, count int) ([]model.Episode, error) {
	return f.results[keyword], nil
}

func (f *fakeTorrentSource) SearchByTag(string, string, int) ([]model.Episode, error) {
	return nil, nil
}

func (f *fakeTorrentSource) FetchBangumiCalendar() ([]model.WebsiteBangumi, error) {
	return nil, nil
}

func (f *fakeTorrentSource) FetchEpisodeOfBangumi(string, int, []string) ([]model.Episode, error) {
	return nil, nil
}

func (f *fakeTorrentSource) FetchSingleBangumi(string, []string, int) (*model.WebsiteBangumi, error) {
	return nil, nil
}

func TestManualSubscriptionUsesAliasSearchWhenUpdating(t *testing.T) {
	setupControllersDB(t)
	name := "葬送的芙莉莲"
	if err := db.Conn.SaveBangumi(model.WebsiteBangumi{ID: "bgmtv:999", Name: name, UpdateDay: "Sun"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveFollowed(&db.Followed{
		BangumiName: name,
		Episodes:    map[int]bool{},
		Status:      db.FollowedStatusFollowed,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.SaveManualBangumi(&db.ManualBangumi{
		BangumiName: name,
		Provider:    "bgmtv",
		ProviderID:  "999",
		SearchNames: []string{name, "Frieren"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Conn.MarkAllEnd(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	bangumi, err := db.Conn.GetBangumiByName(name)
	if err != nil {
		t.Fatal(err)
	}
	if bangumi.Status != db.BangumiStatusUpdating {
		t.Fatalf("manual bangumi was marked ended by MarkAllEnd: %#v", bangumi)
	}
	fake := &fakeTorrentSource{results: map[string][]model.Episode{
		name: {
			{Title: name + " - 03 [1080p]", Download: "magnet:3", Episode: 3, Time: time.Now().Unix()},
		},
		"Frieren": {
			{Title: "Frieren - 99 [1080p]", Download: "magnet:99", Episode: 99, Time: time.Now().Unix()},
		},
	}}
	oldMaxPath := config.Cfg.MaxPath
	config.Cfg.MaxPath = 2
	defer func() { config.Cfg.MaxPath = oldMaxPath }()

	episodes, err := source.GetMaximumEpisode(fake, bangumi, true, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 1 || episodes[0].Episode != 3 || episodes[0].Name != name {
		t.Fatalf("unexpected manual episodes: %#v", episodes)
	}
}

func TestSyncSubscriptionsImportsWatchingCollections(t *testing.T) {
	setupControllersDB(t)
	addBangumi(t, model.WebsiteBangumi{ID: "haruhi", Name: "凉宫春日的忧郁", UpdateDay: "Sun"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/users/tester/collections":
			if got := r.URL.Query().Get("subject_type"); got != "2" {
				t.Errorf("subject_type = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"total": 3,
				"limit": 50,
				"data": []map[string]any{
					{
						"subject_id": 253,
						"type":       3,
						"ep_status":  2,
						"subject": map[string]any{
							"id":      253,
							"name":    "涼宮ハルヒの憂鬱",
							"name_cn": "凉宫春日的忧郁",
						},
					},
					{
						"subject_id": 999,
						"type":       3,
						"ep_status":  1,
						"subject": map[string]any{
							"id":      999,
							"name":    "葬送のフリーレン",
							"name_cn": "葬送的芙莉莲",
						},
					},
					{
						"subject_id": 300,
						"type":       2,
						"subject": map[string]any{
							"id":      300,
							"name":    "Already Watched",
							"name_cn": "已经看过",
						},
					},
				},
			})
		case "/calendar":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"weekday": map[string]any{"en": "Sun", "cn": "星期日"},
				"items":   []map[string]any{{"id": 999, "name": "葬送のフリーレン"}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldBase := bgmtv.APIBase
	oldLang := config.Cfg.Lang
	oldBangumi := config.Cfg.Bangumi
	bgmtv.APIBase = server.URL
	config.Cfg.Lang = "zh_cn"
	config.Cfg.Bangumi = config.BangumiConfig{
		AccessToken: "secret",
		Username:    "tester",
		SyncTypes:   []string{"watching"},
	}
	defer func() {
		bgmtv.APIBase = oldBase
		config.Cfg.Lang = oldLang
		config.Cfg.Bangumi = oldBangumi
	}()

	summary, err := SyncSubscriptions(SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Added != 2 || summary.Fetched != 2 || summary.Failed != 0 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	followed, err := db.Conn.GetFollowed("凉宫春日的忧郁")
	if err != nil {
		t.Fatal(err)
	}
	if !followed.Episodes[1] || !followed.Episodes[2] || followed.Episodes[3] {
		t.Fatalf("ep_status was not applied: %v", followed.Episodes)
	}
	manual, err := db.Conn.GetManualBangumi("葬送的芙莉莲")
	if err != nil {
		t.Fatal(err)
	}
	if manual.Provider != "bgmtv" || manual.ProviderID != "999" {
		t.Fatalf("unexpected manual mapping: %#v", manual)
	}
	bangumi, err := db.Conn.GetBangumiByName("葬送的芙莉莲")
	if err != nil {
		t.Fatal(err)
	}
	if bangumi.UpdateDay != "Sun" {
		t.Fatalf("update day = %q, want Sun", bangumi.UpdateDay)
	}

	summary, err = SyncSubscriptions(SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Added != 0 || summary.Existing != 2 {
		t.Fatalf("second sync should be idempotent: %#v", summary)
	}

	summary, err = SyncSubscriptions(SyncOptions{Types: []string{"watched"}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Added != 0 || summary.Fetched != 1 || len(summary.Items) != 1 || summary.Items[0].Action != "would_add" {
		t.Fatalf("unexpected dry-run summary: %#v", summary)
	}
	if config.Cfg.Bangumi.LastSync == 0 {
		t.Fatal("last_sync was not updated in memory")
	}
}
