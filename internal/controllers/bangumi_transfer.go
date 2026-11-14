package controllers

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/model"
)

// FollowExport is the local import/export snapshot for followed bangumi.
type FollowExport struct {
	Version    int                `json:"version"`
	ExportedAt int64              `json:"exported_at"`
	Items      []FollowExportItem `json:"items"`
}

// FollowExportItem is one followed bangumi.
type FollowExportItem struct {
	BangumiID      int          `json:"bangumi_id,omitempty"`
	Name           string       `json:"name"`
	DisplayName    string       `json:"display_name,omitempty"`
	CollectionType int          `json:"collection_type"`
	Episode        int          `json:"episode"`
	Season         int          `json:"season,omitempty"`
	EpisodeOffset  int          `json:"episode_offset,omitempty"`
	Aliases        []string     `json:"aliases,omitempty"`
	Cache          *FollowCache `json:"cache,omitempty"`
}

// FollowCache is the locally cached Bangumi detail text for one subject.
type FollowCache struct {
	Overview   string `json:"overview,omitempty"`
	Characters string `json:"characters,omitempty"`
	Staff      string `json:"staff,omitempty"`
	Relations  string `json:"relations,omitempty"`
	Comments   string `json:"comments,omitempty"`
}

// ExportFollows builds a local follow-list snapshot.
func ExportFollows() (FollowExport, error) {
	if err := ensureDB(); err != nil {
		return FollowExport{}, err
	}
	followed, err := db.Conn.AllFollowed()
	if err != nil {
		return FollowExport{}, err
	}
	out := FollowExport{Version: 1, ExportedAt: time.Now().Unix(), Items: []FollowExportItem{}}
	for _, item := range followed {
		if item.Status == db.FollowedStatusDeleted || item.IsScript {
			continue
		}
		subjectID, _ := db.Conn.GetSubjectIDByBangumiName(item.BangumiName)
		collectionType := 3
		if value, err := db.Conn.GetBangumiCollection(subjectID); err == nil && value > 0 {
			collectionType = value
		}
		aliases := []string{}
		if manual, err := db.Conn.GetManualBangumi(item.BangumiName); err == nil {
			aliases = append(aliases, manual.SearchNames...)
		}
		if custom, err := db.Conn.ListCustomAliases(item.BangumiName); err == nil {
			aliases = append(aliases, custom...)
		}
		entry := FollowExportItem{
			BangumiID: subjectID, Name: item.BangumiName, DisplayName: item.DisplayName,
			CollectionType: collectionType, Episode: item.Episode(), Season: item.Season,
			EpisodeOffset: item.EpisodeOffset, Aliases: aliases,
		}
		if subjectID > 0 {
			if cache, err := db.Conn.GetBangumiCache(int64(subjectID)); err == nil {
				entry.Cache = &FollowCache{
					Overview: cache.Overview, Characters: cache.Characters,
					Staff: cache.Staff, Relations: cache.Relations, Comments: cache.Comments,
				}
			}
		}
		out.Items = append(out.Items, entry)
	}
	return out, nil
}

// ImportFollows restores a local follow-list snapshot.
func ImportFollows(snapshot FollowExport) (map[string]any, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	imported := 0
	updated := 0
	failed := 0
	for _, item := range snapshot.Items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			failed++
			continue
		}
		if item.BangumiID > 0 {
			_ = db.Conn.SaveBangumiLink(name, item.BangumiID)
		}
		if item.CollectionType >= 1 && item.CollectionType <= 5 && item.BangumiID > 0 {
			_ = db.Conn.SaveBangumiCollection(item.BangumiID, item.CollectionType)
		}
		for _, alias := range item.Aliases {
			if alias = strings.TrimSpace(alias); alias != "" {
				_ = db.Conn.SaveCustomAlias(name, alias)
			}
		}
		if item.BangumiID > 0 && item.Cache != nil {
			_ = db.Conn.SaveBangumiCache(&db.BangumiCache{
				SubjectID: int64(item.BangumiID), Overview: item.Cache.Overview,
				Characters: item.Cache.Characters, Staff: item.Cache.Staff,
				Relations: item.Cache.Relations, Comments: item.Cache.Comments,
			})
		}
		existing, err := db.Conn.GetFollowed(name)
		if err == nil {
			if item.DisplayName != "" {
				existing.DisplayName = item.DisplayName
			}
			if item.Season > 0 {
				existing.Season = item.Season
			}
			if item.EpisodeOffset != 0 {
				existing.EpisodeOffset = item.EpisodeOffset
			}
			if existing.Status == db.FollowedStatusDeleted {
				existing.Status = db.FollowedStatusFollowed
			}
			if err := db.Conn.SaveFollowed(existing); err == nil {
				updated++
			} else {
				failed++
			}
			continue
		}
		if item.Cache != nil {
			if importedFollow, err := importFollowFromCache(item); err == nil && importedFollow {
				imported++
				continue
			}
		}
		episode := item.Episode
		var result Result
		if item.BangumiID > 0 {
			result = AddFromBgmTV(strconv.Itoa(item.BangumiID), &episode, nil, nil, nil)
		} else {
			result = Add(name, &episode, nil, nil, nil)
		}
		if result.Status == "success" || result.Status == "warning" {
			imported++
		} else {
			failed++
		}
	}
	return map[string]any{"imported": imported, "updated": updated, "failed": failed, "total": len(snapshot.Items)}, nil
}

// FollowExportMessage formats the import result.
func FollowExportMessage(result map[string]any) string {
	return fmt.Sprintf("追番导入完成：新增 %v，更新 %v，失败 %v", result["imported"], result["updated"], result["failed"])
}

// importFollowFromCache recreates a subscription entirely from the exported
// follow item and its cached subject overview. This keeps JSON/ZIP imports
// working offline instead of requiring another bgm.tv lookup.
func importFollowFromCache(item FollowExportItem) (bool, error) {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		return false, nil
	}
	bangumiID := "follow:" + name
	if item.BangumiID > 0 {
		bangumiID = "bgmtv:" + strconv.Itoa(item.BangumiID)
	}
	if _, err := db.Conn.GetBangumiByName(name); err == db.ErrNotFound {
		cover := ""
		if item.Cache != nil && item.Cache.Overview != "" {
			var payload struct {
				Cover string `json:"cover"`
			}
			if json.Unmarshal([]byte(item.Cache.Overview), &payload) == nil {
				cover = downloadCoverField(kazumiCoverPath(payload.Cover))
			}
		}
		if err := db.Conn.SaveBangumi(model.WebsiteBangumi{
			ID: bangumiID, Name: name, Status: db.BangumiStatusUpdating,
			UpdateDay: "Unknown", Cover: cover,
			SubtitleGroup: []model.SubtitleGroup{},
		}); err != nil {
			return false, err
		}
	} else if err != nil {
		return false, err
	}
	if item.BangumiID > 0 {
		_ = db.Conn.SaveBangumiLink(name, item.BangumiID)
		if item.CollectionType >= 1 && item.CollectionType <= 5 {
			_ = db.Conn.SaveBangumiCollection(item.BangumiID, item.CollectionType)
		}
	}
	for _, alias := range item.Aliases {
		if alias = strings.TrimSpace(alias); alias != "" {
			_ = db.Conn.SaveCustomAlias(name, alias)
		}
	}
	season := item.Season
	if season <= 0 {
		season = 1
	}
	followed := &db.Followed{
		BangumiName:   name,
		Episodes:      map[int]bool{},
		Status:        db.FollowedStatusFollowed,
		UpdatedTime:   time.Now().Unix(),
		Season:        season,
		EpisodeOffset: item.EpisodeOffset,
		DisplayName:   item.DisplayName,
	}
	for episode := 1; episode <= item.Episode; episode++ {
		followed.Episodes[episode] = true
	}
	if err := db.Conn.SaveFollowed(followed); err != nil {
		return false, err
	}
	return true, nil
}
