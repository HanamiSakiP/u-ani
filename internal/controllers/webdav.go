package controllers

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/webdav"
)

const webdavCollectPath = "/kazumiSync/uani-collectibles.json"

// WebDAVStatus is the public WebDAV sync configuration view.
type WebDAVStatus struct {
	URL              string `json:"url"`
	Username         string `json:"username"`
	PasswordSet      bool   `json:"password_set"`
	Enabled          bool   `json:"enabled"`
	SyncCollectibles bool   `json:"sync_collectibles"`
	SyncDirection    string `json:"sync_direction"`
	LastSync         int64  `json:"last_sync"`
}

type webdavCollectible struct {
	BangumiID int    `json:"bangumi_id"`
	Name      string `json:"name"`
	Type      int    `json:"type"`
	UpdatedAt int64  `json:"updated_at"`
}

type webdavSnapshot struct {
	Version      int                 `json:"version"`
	UpdatedAt    int64               `json:"updated_at"`
	Collectibles []webdavCollectible `json:"collectibles"`
}

// GetWebDAVStatus returns the current WebDAV settings without the password.
func GetWebDAVStatus() WebDAVStatus {
	return WebDAVStatus{
		URL: config.Cfg.WebDAV.URL, Username: config.Cfg.WebDAV.Username,
		PasswordSet: config.Cfg.WebDAV.Password != "", Enabled: config.Cfg.WebDAV.Enabled,
		SyncCollectibles: config.Cfg.WebDAV.SyncCollectibles, SyncDirection: config.Cfg.WebDAV.SyncDirection,
		LastSync: config.Cfg.WebDAV.LastSync,
	}
}

// ConfigureWebDAV validates and persists WebDAV sync settings.
func ConfigureWebDAV(url, username, password *string, enabled, syncCollectibles *bool, direction *string) error {
	if url != nil {
		config.Cfg.WebDAV.URL = strings.TrimRight(strings.TrimSpace(*url), "/")
	}
	if username != nil {
		config.Cfg.WebDAV.Username = *username
	}
	if password != nil {
		config.Cfg.WebDAV.Password = *password
	}
	if enabled != nil {
		config.Cfg.WebDAV.Enabled = *enabled
	}
	if syncCollectibles != nil {
		config.Cfg.WebDAV.SyncCollectibles = *syncCollectibles
	}
	if direction != nil {
		config.Cfg.WebDAV.SyncDirection = strings.ToLower(strings.TrimSpace(*direction))
	}
	if err := config.Validate(&config.Cfg); err != nil {
		return err
	}
	if config.ConfigFilePath == "" {
		return nil
	}
	return config.Cfg.Save()
}

// SyncWebDAV merges local collection states with the WebDAV snapshot.
func SyncWebDAV(direction string) (map[string]any, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	client, err := webdav.Current()
	if err != nil {
		return nil, err
	}
	local := localCollectibles()
	remote := webdavSnapshot{}
	if data, exists, err := client.Get(webdavCollectPath); err != nil {
		return nil, err
	} else if exists {
		_ = json.Unmarshal(data, &remote)
	}
	direction = strings.ToLower(strings.TrimSpace(direction))
	if direction == "" {
		direction = config.Cfg.WebDAV.SyncDirection
	}
	if direction == "" || direction == "bi" {
		direction = "download"
	}
	merged := local
	switch direction {
	case "upload":
		merged = local
	default:
		direction = "download"
		merged = remote.Collectibles
	}
	applied := 0
	for _, item := range merged {
		if err := db.Conn.SaveBangumiCollection(item.BangumiID, item.Type); err == nil {
			applied++
		}
	}
	if direction != "download" {
		snapshot := webdavSnapshot{Version: 1, UpdatedAt: time.Now().Unix(), Collectibles: merged}
		data, _ := json.MarshalIndent(snapshot, "", "  ")
		if err := client.Put(webdavCollectPath, data); err != nil {
			return nil, err
		}
	}
	config.Cfg.WebDAV.LastSync = time.Now().Unix()
	if config.ConfigFilePath != "" {
		_ = config.Cfg.Save()
	}
	return map[string]any{"local": len(local), "remote": len(remote.Collectibles), "merged": len(merged), "applied": applied, "direction": direction}, nil
}

func localCollectibles() []webdavCollectible {
	followed, err := db.Conn.AllFollowed()
	if err != nil {
		return nil
	}
	out := []webdavCollectible{}
	for _, item := range followed {
		if item.Status == db.FollowedStatusDeleted || item.IsScript {
			continue
		}
		subjectID, err := db.Conn.GetSubjectIDByBangumiName(item.BangumiName)
		if err != nil {
			continue
		}
		collectionType := 3
		if value, err := db.Conn.GetBangumiCollection(subjectID); err == nil && value > 0 {
			collectionType = value
		}
		out = append(out, webdavCollectible{BangumiID: subjectID, Name: item.BangumiName, Type: collectionType, UpdatedAt: item.UpdatedTime})
	}
	return out
}

func mergeCollectibles(local, remote []webdavCollectible) []webdavCollectible {
	byID := map[int]webdavCollectible{}
	for _, item := range local {
		byID[item.BangumiID] = item
	}
	for _, item := range remote {
		if current, ok := byID[item.BangumiID]; !ok || item.UpdatedAt > current.UpdatedAt {
			byID[item.BangumiID] = item
		}
	}
	out := make([]webdavCollectible, 0, len(byID))
	for _, item := range byID {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].BangumiID < out[j].BangumiID })
	return out
}

// WebDAVSyncSummary formats a short summary.
func WebDAVSyncSummary(result map[string]any) string {
	return fmt.Sprintf("WebDAV 同步完成：本地 %v，远端 %v，合并 %v", result["local"], result["remote"], result["merged"])
}
