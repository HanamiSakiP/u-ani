package controllers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/bgmtv"
	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/logger"
)

// BangumiSyncSettings is the public view of the Bangumi 追番 sync settings.
// The access token itself is never exposed.
type BangumiSyncSettings struct {
	Username        string   `json:"username"`
	TokenConfigured bool     `json:"token_configured"`
	SyncEnabled     bool     `json:"sync_enabled"`
	SyncTypes       []string `json:"sync_types"`
	LastSync        int64    `json:"last_sync"`
}

// SyncOptions controls a Bangumi collection sync run.
type SyncOptions struct {
	// Types overrides the configured collection buckets for this run.
	// Accepted values are wish, watched, watching, on_hold, dropped or all.
	Types []string
	// DryRun reports what would be subscribed without changing anything.
	DryRun bool
	// Latest marks all currently available episodes as seen instead of using
	// the Bangumi ep_status for each collection.
	Latest bool
	// Episode overrides the watched episode count (marks 1..N as seen).
	Episode *int
	// Limit caps the number of collections processed in one run.
	Limit int
}

// SyncItem is one collection processed by a sync run.
type SyncItem struct {
	SubjectID int    `json:"subject_id"`
	Name      string `json:"name"`
	Episode   int    `json:"episode"`
	Action    string `json:"action"` // added, exists, would_add, failed
	Message   string `json:"message,omitempty"`
}

// SyncSummary is the result of pulling the Bangumi 追番 list into uani.
type SyncSummary struct {
	Username string     `json:"username"`
	Types    []string   `json:"types"`
	Fetched  int        `json:"fetched"`
	Added    int        `json:"added"`
	Existing int        `json:"existing"`
	Failed   int        `json:"failed"`
	DryRun   bool       `json:"dry_run"`
	Items    []SyncItem `json:"items"`
	LastSync int64      `json:"last_sync"`
}

// BangumiSyncStatus reads the current sync settings.
func BangumiSyncStatus() BangumiSyncSettings {
	types := config.Cfg.Bangumi.SyncTypes
	if len(types) == 0 {
		types = []string{"watching"}
	}
	return BangumiSyncSettings{
		Username:        config.Cfg.Bangumi.Username,
		TokenConfigured: strings.TrimSpace(config.Cfg.Bangumi.AccessToken) != "",
		SyncEnabled:     config.Cfg.Bangumi.SyncEnabled,
		SyncTypes:       append([]string{}, types...),
		LastSync:        config.Cfg.Bangumi.LastSync,
	}
}

// SetBangumiToken validates an Access Token, stores it, and records the
// resolved username.
func SetBangumiToken(token string) (*bgmtv.User, error) {
	user, err := bgmtv.GetCurrentUser(token)
	if err != nil {
		return nil, err
	}
	config.Cfg.Bangumi.AccessToken = strings.TrimSpace(token)
	config.Cfg.Bangumi.Username = user.Username
	if err := saveBangumiConfig(); err != nil {
		return nil, err
	}
	return user, nil
}

// ClearBangumiToken removes the account and disables automatic sync.
func ClearBangumiToken() error {
	config.Cfg.Bangumi.AccessToken = ""
	config.Cfg.Bangumi.Username = ""
	config.Cfg.Bangumi.SyncEnabled = false
	config.Cfg.Bangumi.LastSync = 0
	return saveBangumiConfig()
}

// ConfigureBangumiSync updates the sync switch and collection buckets.
func ConfigureBangumiSync(enabled *bool, types *[]string) error {
	if types != nil {
		normalized, err := bgmtv.NormalizeCollectionTypes(*types)
		if err != nil {
			return err
		}
		if len(normalized) == 0 {
			normalized = []string{"watching"}
		}
		config.Cfg.Bangumi.SyncTypes = normalized
	}
	if enabled != nil {
		config.Cfg.Bangumi.SyncEnabled = *enabled
	}
	return saveBangumiConfig()
}

// SyncSubscriptions pulls the configured Bangumi collection buckets and turns
// new subjects into uani subscriptions. The Bangumi ep_status is used as the
// starting episode so already watched episodes are not downloaded again.
func SyncSubscriptions(opts SyncOptions) (SyncSummary, error) {
	summary := SyncSummary{DryRun: opts.DryRun, Items: []SyncItem{}}
	if err := ensureDB(); err != nil {
		return summary, err
	}
	if opts.Latest && opts.Episode != nil {
		return summary, fmt.Errorf("latest and episode cannot be used together")
	}
	if opts.Episode != nil && *opts.Episode < 0 {
		return summary, fmt.Errorf("episode should not be negative")
	}
	if opts.Limit < 0 {
		return summary, fmt.Errorf("limit should not be negative")
	}

	token := strings.TrimSpace(config.Cfg.Bangumi.AccessToken)
	if token == "" {
		return summary, fmt.Errorf("Bangumi Access Token is not configured; run `uani sync login <token>` first")
	}

	typeValues := opts.Types
	if len(typeValues) == 0 {
		typeValues = config.Cfg.Bangumi.SyncTypes
	}
	if len(typeValues) == 0 {
		typeValues = []string{"watching"}
	}
	typeIDs, err := bgmtv.ParseCollectionTypes(typeValues)
	if err != nil {
		return summary, err
	}
	if len(typeIDs) == 0 {
		return summary, fmt.Errorf("no Bangumi collection types selected")
	}
	selected := map[int]bool{}
	for _, id := range typeIDs {
		selected[id] = true
	}

	username := strings.TrimSpace(config.Cfg.Bangumi.Username)
	if username == "" {
		user, err := bgmtv.GetCurrentUser(token)
		if err != nil {
			return summary, err
		}
		username = user.Username
		config.Cfg.Bangumi.Username = username
		if !opts.DryRun {
			if err := saveBangumiConfig(); err != nil {
				return summary, err
			}
		}
	}
	summary.Username = username
	for _, id := range typeIDs {
		summary.Types = append(summary.Types, bgmtv.CollectionTypeName(id))
	}

	collections, err := bgmtv.GetCollections(username, token, 2, 50)
	if err != nil {
		return summary, err
	}

	weekdays := bgmtv.WeekdayMap()
	now := time.Now().Unix()
	processed := 0
	for _, collection := range collections {
		if !selected[collection.Type] {
			continue
		}
		if opts.Limit > 0 && processed >= opts.Limit {
			break
		}
		processed++

		subject := collection.Subject
		if subject.ID == 0 {
			subject.ID = collection.SubjectID
		}
		if subject.URL == "" && subject.ID > 0 {
			subject.URL = fmt.Sprintf("https://bgm.tv/subject/%d", subject.ID)
		}
		if collection.Type >= bgmtv.CollectionWish && collection.Type <= bgmtv.CollectionDropped {
			_ = db.Conn.SaveBangumiCollection(subject.ID, collection.Type)
		}
		name := subject.PreferredName(config.Cfg.Lang)
		if name == "" {
			summary.Failed++
			summary.Items = append(summary.Items, SyncItem{
				SubjectID: subject.ID,
				Action:    "failed",
				Message:   "bgm.tv subject has no usable title",
			})
			continue
		}

		episode := collection.EpStatus
		if episode < 0 {
			episode = 0
		}
		var episodePtr *int
		switch {
		case opts.Episode != nil:
			episode = *opts.Episode
			episodePtr = opts.Episode
		case opts.Latest:
			episodePtr = nil
		default:
			episodePtr = &episode
		}

		if existing, ok := existingSubscription(&subject); ok {
			summary.Existing++
			summary.Items = append(summary.Items, SyncItem{
				SubjectID: subject.ID,
				Name:      existing,
				Episode:   episode,
				Action:    "exists",
			})
			continue
		}

		if opts.DryRun {
			summary.Items = append(summary.Items, SyncItem{
				SubjectID: subject.ID,
				Name:      name,
				Episode:   episode,
				Action:    "would_add",
			})
			continue
		}

		// Reuse one calendar lookup for the whole batch. An empty string is
		// still a valid value: it prevents one calendar request per subject.
		day := weekdays[subject.ID]
		result := addBgmSubject(&subject, &day, episodePtr, nil, nil, nil)
		item := SyncItem{SubjectID: subject.ID, Name: name, Episode: episode}
		switch result.Status {
		case "success":
			summary.Added++
			item.Action = "added"
			if result.Data != nil {
				if actual, ok := result.Data["bangumi_name"].(string); ok && actual != "" {
					item.Name = actual
				}
			}
		case "warning":
			summary.Existing++
			item.Action = "exists"
			if result.Data != nil {
				if actual, ok := result.Data["bangumi_name"].(string); ok && actual != "" {
					item.Name = actual
				}
			}
		default:
			summary.Failed++
			item.Action = "failed"
			item.Message = result.Message
		}
		summary.Items = append(summary.Items, item)
	}
	summary.Fetched = processed

	if !opts.DryRun {
		config.Cfg.Bangumi.LastSync = now
		if err := saveBangumiConfig(); err != nil {
			return summary, err
		}
		summary.LastSync = now
	}
	return summary, nil
}

func existingSubscription(subject *bgmtv.Subject) (string, bool) {
	if subject == nil {
		return "", false
	}
	if mapping, err := db.Conn.GetManualBangumiByProviderID("bgmtv", strconv.Itoa(subject.ID)); err == nil {
		if _, err := db.Conn.GetFollowedNotDeleted(mapping.BangumiName); err == nil {
			return mapping.BangumiName, true
		}
	}
	if matched := findSourceBangumi(subject.SearchNames()); matched != nil {
		if _, err := db.Conn.GetFollowedNotDeleted(matched.Name); err == nil {
			return matched.Name, true
		}
	}
	if name := subject.PreferredName(config.Cfg.Lang); name != "" {
		if _, err := db.Conn.GetFollowedNotDeleted(name); err == nil {
			return name, true
		}
	}
	return "", false
}

func saveBangumiConfig() error {
	// Tests and library users may not have loaded a config file.
	if config.ConfigFilePath == "" {
		return nil
	}
	return config.Cfg.Save()
}

// RunConfiguredBangumiSync is called before update when automatic sync is
// enabled. It never aborts an update; failures are logged.
func RunConfiguredBangumiSync() {
	if !config.Cfg.Bangumi.SyncEnabled {
		return
	}
	if strings.TrimSpace(config.Cfg.Bangumi.AccessToken) == "" {
		return
	}
	summary, err := SyncSubscriptions(SyncOptions{})
	if err != nil {
		logger.Warning("Bangumi subscription sync failed: %v", err)
		return
	}
	if summary.Added > 0 {
		logger.Info("Bangumi sync: %d new subscription(s) imported", summary.Added)
	}
}
