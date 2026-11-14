package controllers

import (
	"fmt"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/bgmtv"
	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
)

// SetBangumiCollection changes the user's Bangumi collection type for a
// subject and caches it locally. Types match Bangumi: 1 wish, 2 watched,
// 3 watching, 4 on hold, 5 dropped.
func SetBangumiCollection(subjectID, collectionType int) error {
	if subjectID <= 0 {
		return fmt.Errorf("invalid Bangumi subject id %d", subjectID)
	}
	if collectionType < bgmtv.CollectionWish || collectionType > bgmtv.CollectionDropped {
		return fmt.Errorf("invalid collection type %d", collectionType)
	}
	token := strings.TrimSpace(config.Cfg.Bangumi.AccessToken)
	if token == "" {
		return fmt.Errorf("Bangumi Access Token is not configured; run `uani sync login <token>` first")
	}
	if err := bgmtv.UpdateCollection(subjectID, token, collectionType); err != nil {
		return err
	}
	if err := ensureDB(); err != nil {
		return err
	}
	return db.Conn.SaveBangumiCollection(subjectID, collectionType)
}

// GetBangumiCollection returns the cached collection type, falling back to the
// Bangumi API when a token and username are configured.
func GetBangumiCollection(subjectID int) (int, bool, error) {
	if subjectID <= 0 {
		return 0, false, fmt.Errorf("invalid Bangumi subject id %d", subjectID)
	}
	if err := ensureDB(); err != nil {
		return 0, false, err
	}
	if value, err := db.Conn.GetBangumiCollection(subjectID); err == nil {
		return value, true, nil
	}
	token := strings.TrimSpace(config.Cfg.Bangumi.AccessToken)
	username := strings.TrimSpace(config.Cfg.Bangumi.Username)
	if token == "" || username == "" {
		return 0, false, nil
	}
	value, err := bgmtv.GetCollection(username, subjectID, token)
	if err != nil {
		return 0, false, nil
	}
	_ = db.Conn.SaveBangumiCollection(subjectID, value)
	return value, true, nil
}

// ClearBangumiCollection removes a subject from the user's Bangumi collection
// and clears the local cache. Missing tokens or unlinked subscriptions are
// treated as success because there is no remote state to change.
func ClearBangumiCollection(subjectID int) error {
	if subjectID <= 0 {
		return nil
	}
	if err := ensureDB(); err != nil {
		return err
	}
	value, cacheErr := db.Conn.GetBangumiCollection(subjectID)
	hadStatus := cacheErr == nil && value > 0
	// Keep a zero-value tombstone so a failed remote delete cannot resurrect
	// the cached status through the Bangumi API fallback.
	_ = db.Conn.SaveBangumiCollection(subjectID, 0)
	token := strings.TrimSpace(config.Cfg.Bangumi.AccessToken)
	if token == "" {
		if hadStatus {
			return fmt.Errorf("Bangumi Access Token is not configured; local collection cache was cleared")
		}
		return nil
	}
	return bgmtv.DeleteCollection(subjectID, token)
}

// ClearSubscriptionCollection finds the bgm.tv subject linked to a local
// subscription and clears its collection status. It returns the subject id so
// callers can report what happened.
func ClearSubscriptionCollection(name string) (int, error) {
	if err := ensureDB(); err != nil {
		return 0, err
	}
	subjectID, err := db.Conn.GetSubjectIDByBangumiName(name)
	if err != nil || subjectID <= 0 {
		return 0, nil
	}
	if err := ClearBangumiCollection(subjectID); err != nil {
		return subjectID, err
	}
	return subjectID, nil
}

// LinkBangumiSubject stores a manual subscription -> bgm.tv subject mapping.
func LinkBangumiSubject(name, value string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("bangumi name is required")
	}
	subjectID, err := bgmtv.ParseSubjectID(value)
	if err != nil {
		return 0, err
	}
	if err := ensureDB(); err != nil {
		return 0, err
	}
	if err := db.Conn.SaveBangumiLink(name, subjectID); err != nil {
		return 0, err
	}
	return subjectID, nil
}
