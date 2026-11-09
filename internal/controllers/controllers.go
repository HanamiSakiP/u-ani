// Package controllers implements the CLI command business logic.
package controllers

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/HanamiSakiP/u-ani/internal/bgmtv"
	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/download"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/model"
	"github.com/HanamiSakiP/u-ani/internal/script"
	"github.com/HanamiSakiP/u-ani/internal/season"
	"github.com/HanamiSakiP/u-ani/internal/source"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

// Result is the return value shared by the CLI and web API.
type Result struct {
	Status   string
	Message  string
	Data     map[string]any
	Episodes []int
	Options  map[string]any
}

func ensureDB() error {
	if db.Conn != nil {
		return nil
	}
	_, err := db.New()
	return err
}

func formatIntList(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprint(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func autoDisplayName(name string, displayName *string) (resolved string, auto string) {
	if displayName != nil {
		return *displayName, ""
	}
	stripped := season.StripSeasonSuffix(name)
	if stripped != name {
		return stripped, stripped
	}
	return "", ""
}

func seasonDisplayNote(detected, resolved int, displayName *string, auto string) string {
	if auto == "" || displayName != nil {
		return ""
	}
	if detected != resolved {
		return fmt.Sprintf("detected season %d, using season %d; path display name normalized to %s", detected, resolved, auto)
	}
	return fmt.Sprintf("detected season %d; path display name normalized to %s", detected, auto)
}

// Add subscribes a bangumi.
func Add(name string, episode *int, seasonOverride *int, episodeOffset *int, displayName *string) Result {
	if err := ensureDB(); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	has, err := db.Conn.HasUpdating()
	if err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	if !has {
		if src, err := source.New(); err == nil {
			if _, err := source.Fetch(src, false); err != nil {
				logger.Warning("Failed to fetch bangumi calendar: %v", err)
			}
		}
	}

	bangumi, err := db.Conn.GetBangumiByNameContains(name)
	if err != nil {
		return Result{Status: "error", Message: fmt.Sprintf("%s not found, please check the name", name)}
	}
	name = bangumi.Name

	hasOverrides := seasonOverride != nil || episodeOffset != nil || displayName != nil
	detectedSeason := season.ParseSeason(bangumi.Name)
	resolvedSeason := detectedSeason
	if seasonOverride != nil {
		resolvedSeason = *seasonOverride
	}
	resolvedDisplay, auto := autoDisplayName(bangumi.Name, displayName)
	note := seasonDisplayNote(detectedSeason, resolvedSeason, displayName, auto)
	if note != "" {
		logger.Info("%s: %s", bangumi.Name, note)
	}

	followed, err := db.Conn.GetFollowed(name)
	if err != nil && err != db.ErrNotFound {
		return Result{Status: "error", Message: err.Error()}
	}

	if followed != nil {
		if followed.Status == db.FollowedStatusFollowed {
			shouldSetAuto := auto != "" && followed.DisplayName == ""
			if hasOverrides || shouldSetAuto {
				if seasonOverride != nil {
					followed.Season = *seasonOverride
				}
				if episodeOffset != nil {
					followed.EpisodeOffset = *episodeOffset
				}
				if displayName != nil {
					followed.DisplayName = *displayName
				} else if shouldSetAuto {
					followed.DisplayName = auto
				}
				if err := db.Conn.SaveFollowed(followed); err != nil {
					return Result{Status: "error", Message: err.Error()}
				}
				message := fmt.Sprintf("%s updated", bangumi.Name)
				if shouldSetAuto && note != "" {
					message = message + "; " + note
				}
				return Result{Status: "success", Message: message}
			}
			return Result{Status: "warning", Message: fmt.Sprintf("%s already followed", bangumi.Name)}
		}
		followed.Status = db.FollowedStatusFollowed
		if seasonOverride != nil {
			followed.Season = *seasonOverride
		}
		if episodeOffset != nil {
			followed.EpisodeOffset = *episodeOffset
		}
		if displayName != nil || resolvedDisplay != "" {
			followed.DisplayName = resolvedDisplay
		}
	} else {
		followed = &db.Followed{
			BangumiName:   name,
			Episodes:      map[int]bool{},
			Status:        db.FollowedStatusFollowed,
			UpdatedTime:   0,
			Season:        resolvedSeason,
			EpisodeOffset: 0,
			DisplayName:   resolvedDisplay,
		}
		if episodeOffset != nil {
			followed.EpisodeOffset = *episodeOffset
		}
	}
	if followed.Season == 0 {
		followed.Season = resolvedSeason
	}
	// Save before fetching episodes: get_maximum_episode reads the followed row.
	if err := db.Conn.SaveFollowed(followed); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}

	if episode == nil {
		src, err := source.New()
		if err != nil {
			return Result{Status: "error", Message: err.Error()}
		}
		episodes, err := source.GetMaximumEpisode(src, bangumi, true, config.Cfg.MaxPath)
		if err != nil {
			return Result{Status: "error", Message: err.Error()}
		}
		set := map[int]bool{}
		for _, e := range episodes {
			set[e.Episode] = true
		}
		followed.Episodes = set
	} else {
		set := map[int]bool{}
		for i := 1; i <= *episode; i++ {
			set[i] = true
		}
		followed.Episodes = set
	}
	if err := db.Conn.SaveFollowed(followed); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}

	message := fmt.Sprintf("add %s to subscribing bangumi list", bangumi.Name)
	if note != "" {
		message = message + "; " + note
	}
	return Result{Status: "success", Message: message}
}

// AddFromBgmTV subscribes to a bgm.tv subject. If the subject matches an entry
// in the active torrent source, the normal source-backed subscription is used.
// Otherwise a manual subscription is created and torrents are matched by the
// subject title and its aliases.
func AddFromBgmTV(value string, episode *int, seasonOverride *int, episodeOffset *int, displayName *string) Result {
	if err := ensureDB(); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	subjectID, err := bgmtv.ParseSubjectID(value)
	if err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	subject, err := bgmtv.FetchSubject(subjectID)
	if err != nil {
		return Result{Status: "error", Message: fmt.Sprintf("failed to fetch bgm.tv subject %d: %v", subjectID, err)}
	}
	return addBgmSubject(subject, nil, episode, seasonOverride, episodeOffset, displayName)
}

// addBgmSubject subscribes a subject that was already fetched from bgm.tv.
// updateDay is nil when the caller wants the weekday looked up from the
// calendar; pass a pointer (possibly to an empty string) to reuse a batch
// lookup and avoid one calendar request per subject.
func addBgmSubject(subject *bgmtv.Subject, updateDay *string, episode *int, seasonOverride *int, episodeOffset *int, displayName *string) Result {
	if subject == nil {
		return Result{Status: "error", Message: "bgm.tv subject is nil"}
	}
	has, err := db.Conn.HasUpdating()
	if err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	if !has {
		if src, err := source.New(); err == nil {
			if _, err := source.Fetch(src, false); err != nil {
				logger.Warning("Failed to fetch bangumi calendar: %v", err)
			}
		}
	}

	if matched := findSourceBangumi(subject.SearchNames()); matched != nil {
		cover := subject.CoverURL()
		if cover != "" && (matched.Cover == "" || isInvalidCover(matched.Cover)) {
			if err := db.Conn.SetBangumiCover(matched.Name, cover); err == nil {
				matched.Cover = cover
			}
		}
		result := Add(matched.Name, episode, seasonOverride, episodeOffset, displayName)
		if result.Status == "success" || result.Status == "warning" {
			_ = db.Conn.SaveBangumiLink(matched.Name, subject.ID)
			result.Message = fmt.Sprintf("%s (matched bgm.tv subject %d)", result.Message, subject.ID)
			if result.Data == nil {
				result.Data = map[string]any{}
			}
			result.Data["bangumi_name"] = matched.Name
			result.Data["bgm_tv_id"] = subject.ID
			if coverNeedsDownload(matched.Cover) {
				utils.DownloadCover([]string{matched.Cover})
			}
		}
		return result
	}

	return addManualBgmTV(subject, updateDay, episode, seasonOverride, episodeOffset, displayName)
}

func findSourceBangumi(names []string) *db.Bangumi {
	all, err := db.Conn.AllBangumi()
	if err != nil {
		return nil
	}
	var normalized []string
	for _, name := range names {
		if value := normalizeForMatch(name); len([]rune(value)) >= 2 {
			normalized = append(normalized, value)
		}
	}
	if len(normalized) == 0 {
		return nil
	}

	var best *db.Bangumi
	bestScore := 0
	for i := range all {
		candidate := &all[i]
		name := normalizeForMatch(candidate.Name)
		if name == "" {
			continue
		}
		for _, alias := range normalized {
			score := 0
			switch {
			case name == alias:
				score = 100
			case strings.Contains(name, alias), strings.Contains(alias, name):
				score = 50
			}
			if score > 0 && candidate.Status == db.BangumiStatusUpdating {
				score += 5
			}
			if score > bestScore {
				bestScore = score
				best = candidate
			}
		}
	}
	return best
}

func addManualBgmTV(subject *bgmtv.Subject, updateDayOverride *string, episode *int, seasonOverride *int, episodeOffset *int, displayName *string) Result {
	name := subject.PreferredName(config.Cfg.Lang)
	if name == "" {
		return Result{Status: "error", Message: fmt.Sprintf("bgm.tv subject %d has no usable title", subject.ID)}
	}
	providerID := strconv.Itoa(subject.ID)
	bangumiID := "bgmtv:" + providerID
	aliases := subject.SearchNames()
	if len(aliases) == 0 {
		aliases = []string{name}
	}

	updateDay := ""
	if updateDayOverride != nil {
		updateDay = *updateDayOverride
	} else {
		updateDay = bgmtv.WeekdayForSubject(subject.ID)
	}
	if updateDay == "" {
		updateDay = "Unknown"
	}
	cover := subject.CoverURL()
	if existing, err := db.Conn.GetBangumiByName(name); err == nil && existing.ID != bangumiID {
		return Result{Status: "error", Message: fmt.Sprintf("bgm.tv subject %d resolves to %s, which is already used by another bangumi", subject.ID, name)}
	}
	if err := source.SaveBangumi(model.WebsiteBangumi{
		ID:            bangumiID,
		Name:          name,
		Status:        db.BangumiStatusUpdating,
		UpdateDay:     updateDay,
		Cover:         cover,
		SubtitleGroup: []model.SubtitleGroup{},
	}); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}

	detectedSeason := season.ParseSeason(name)
	resolvedSeason := detectedSeason
	if seasonOverride != nil {
		resolvedSeason = *seasonOverride
	}
	resolvedDisplay, auto := autoDisplayName(name, displayName)
	note := seasonDisplayNote(detectedSeason, resolvedSeason, displayName, auto)

	followed, err := db.Conn.GetFollowed(name)
	if err != nil && err != db.ErrNotFound {
		return Result{Status: "error", Message: err.Error()}
	}
	existing := followed != nil
	reactivated := false
	if followed == nil {
		followed = &db.Followed{
			BangumiName: name,
			Episodes:    map[int]bool{},
			Status:      db.FollowedStatusFollowed,
			Season:      resolvedSeason,
			DisplayName: resolvedDisplay,
		}
	} else {
		reactivated = followed.Status != db.FollowedStatusFollowed
		followed.Status = db.FollowedStatusFollowed
		if followed.Season == 0 {
			followed.Season = resolvedSeason
		}
	}
	if seasonOverride != nil {
		followed.Season = *seasonOverride
	}
	if episodeOffset != nil {
		followed.EpisodeOffset = *episodeOffset
	}
	if displayName != nil {
		followed.DisplayName = *displayName
	} else if reactivated || followed.DisplayName == "" {
		followed.DisplayName = resolvedDisplay
	}
	hasOverrides := seasonOverride != nil || episodeOffset != nil || displayName != nil
	if reactivated == false && existing && followed.Status == db.FollowedStatusFollowed && hasOverrides == false {
		_ = db.Conn.SaveFollowed(followed)
		_ = db.Conn.SaveManualBangumi(&db.ManualBangumi{
			BangumiName: name,
			Provider:    "bgmtv",
			ProviderID:  providerID,
			SearchNames: aliases,
			ForeignURL:  subject.URL,
			UpdatedTime: time.Now().Unix(),
		})
		if coverNeedsDownload(cover) {
			utils.DownloadCover([]string{cover})
		}
		return Result{
			Status:  "warning",
			Message: fmt.Sprintf("%s already followed", name),
			Data: map[string]any{
				"bangumi_name": name,
				"bgm_tv_id":    subject.ID,
				"manual":       true,
			},
		}
	}
	if episode == nil {
		episodes := map[int]bool{}
		if src, err := source.New(); err == nil {
			if data, err := source.GetManualEpisodes(src, aliases, config.Cfg.MaxPath); err == nil {
				for _, item := range data {
					episodes[item.Episode] = true
				}
			}
		}
		followed.Episodes = episodes
	} else {
		set := map[int]bool{}
		for i := 1; i <= *episode; i++ {
			set[i] = true
		}
		followed.Episodes = set
	}
	if err := db.Conn.SaveFollowed(followed); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	if err := db.Conn.SaveManualBangumi(&db.ManualBangumi{
		BangumiName: name,
		Provider:    "bgmtv",
		ProviderID:  providerID,
		SearchNames: aliases,
		ForeignURL:  subject.URL,
		UpdatedTime: time.Now().Unix(),
	}); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	_ = db.Conn.SaveBangumiLink(name, subject.ID)
	if coverNeedsDownload(cover) {
		utils.DownloadCover([]string{cover})
	}

	message := fmt.Sprintf("add %s (bgm.tv subject %d) to subscribing bangumi list", name, subject.ID)
	if note != "" {
		message += "; " + note
	}
	return Result{
		Status:  "success",
		Message: message,
		Data: map[string]any{
			"bangumi_name": name,
			"bgm_tv_id":    subject.ID,
			"manual":       true,
		},
	}
}

func normalizeForMatch(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// Filter sets subtitle/include/exclude/regex filters for a followed bangumi.
func Filter(name string, subtitle, include, exclude, regex *string) Result {
	if err := ensureDB(); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	bangumi, err := db.Conn.GetBangumiByNameContains(name)
	if err != nil {
		return Result{Status: "error", Message: fmt.Sprintf("Bangumi %s does not exist.", name)}
	}
	followed, err := db.Conn.GetFollowed(bangumi.Name)
	if err != nil {
		return Result{Status: "error", Message: fmt.Sprintf("Bangumi %s has not subscribed, try 'uani add \"%s\"'.", bangumi.Name, bangumi.Name)}
	}

	if subtitle != nil {
		names := splitTrim(*subtitle)
		subs, err := db.Conn.GetSubtitlesByNames(names)
		if err != nil {
			return Result{Status: "error", Message: err.Error()}
		}
		allowed := map[string]bool{}
		for _, id := range bangumi.SubtitleGroup {
			allowed[id] = true
		}
		var ids []string
		for _, s := range subs {
			if allowed[s.ID] {
				ids = append(ids, s.ID)
			}
		}
		followed.Subtitle = ids
	}
	if include != nil {
		followed.Include = splitNonEmpty(*include)
	}
	if exclude != nil {
		followed.Exclude = splitNonEmpty(*exclude)
	}
	if regex != nil {
		followed.Regex = *regex
	}
	if err := db.Conn.SaveFollowed(followed); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}

	subtitleList, _ := db.Conn.GetSubtitlesByIDs(bangumi.SubtitleGroup)
	names := make([]string, 0, len(subtitleList))
	for _, s := range subtitleList {
		names = append(names, s.Name)
	}
	followedNames := []string{}
	if len(followed.Subtitle) > 0 {
		fs, _ := db.Conn.GetSubtitlesByIDs(followed.Subtitle)
		for _, s := range fs {
			followedNames = append(followedNames, s.Name)
		}
	}
	return Result{
		Status:  "success",
		Message: "",
		Data: map[string]any{
			"name":           bangumi.Name,
			"subtitle_group": names,
			"followed":       followedNames,
			"include":        followed.Include,
			"exclude":        followed.Exclude,
			"regex":          followed.Regex,
		},
	}
}

func splitTrim(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Delete unsubscribes a bangumi or clears all subscriptions.
func Delete(name string, clearAll bool, batch bool) Result {
	if err := ensureDB(); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	if clearAll {
		if !batch {
			fmt.Print("[+] are you sure want to CLEAR ALL THE BANGUMI? (y/N): ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if strings.TrimSpace(line) != "y" {
				return Result{Status: "error", Message: "user canceled"}
			}
		}
		if err := db.Conn.DeleteAllFollowed(); err != nil {
			return Result{Status: "error", Message: err.Error()}
		}
		_ = db.Conn.DeleteAllManualBangumi()
		return Result{Status: "warning", Message: "all subscriptions have been deleted"}
	}
	if name != "" {
		followed, err := db.Conn.GetFollowed(name)
		if err != nil {
			return Result{Status: "error", Message: fmt.Sprintf("Bangumi %s does not exist", name)}
		}
		followed.Status = db.FollowedStatusDeleted
		if err := db.Conn.SaveFollowed(followed); err != nil {
			return Result{Status: "error", Message: err.Error()}
		}
		return Result{Status: "warning", Message: fmt.Sprintf("Bangumi %s has been deleted", name)}
	}
	return Result{Status: "warning", Message: "Nothing has been done."}
}

func seenPayload(followed *db.Followed, episode *int) (int, []int) {
	episodes := make([]int, 0, len(followed.Episodes))
	for ep := range followed.Episodes {
		episodes = append(episodes, ep)
	}
	sort.Ints(episodes)
	candidates := append([]int{}, episodes...)
	if episode != nil {
		candidates = append(candidates, *episode)
	}
	if maxDownload, err := db.Conn.MaxDownloadEpisode(followed.BangumiName); err == nil && maxDownload > 0 {
		candidates = append(candidates, maxDownload)
	}
	total := 0
	if len(candidates) > 0 {
		total = candidates[0]
		for _, v := range candidates[1:] {
			if v > total {
				total = v
			}
		}
	}
	return total, episodes
}

func seenResult(status, message string, followed *db.Followed, episode *int) Result {
	total, episodes := seenPayload(followed, episode)
	return Result{
		Status:  status,
		Message: message,
		Data: map[string]any{
			"bangumi":       followed.BangumiName,
			"total_episode": total,
			"seen":          episodes,
		},
	}
}

// Seen returns downloaded episode records for a followed bangumi.
func Seen(name string) Result {
	if err := ensureDB(); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	followed, err := db.Conn.GetFollowedNotDeleted(name)
	if err != nil {
		return Result{Status: "error", Message: fmt.Sprintf("%s is not followed", name)}
	}
	return seenResult("success", fmt.Sprintf("Got seen episodes of %s", name), followed, nil)
}

func normalizeEpisodes(episodes []int) []int {
	set := map[int]bool{}
	for _, ep := range episodes {
		set[ep] = true
	}
	out := make([]int, 0, len(set))
	for ep := range set {
		out = append(out, ep)
	}
	sort.Ints(out)
	return out
}

// SeenForgetBatch removes episodes from downloaded records.
func SeenForgetBatch(name string, episodes []int) Result {
	if err := ensureDB(); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	episodes = normalizeEpisodes(episodes)
	if len(episodes) == 0 {
		return Result{Status: "error", Message: "episodes should not be empty"}
	}
	for _, ep := range episodes {
		if ep <= 0 {
			return Result{Status: "error", Message: "episode should be greater than 0"}
		}
	}
	followed, err := db.Conn.GetFollowedNotDeleted(name)
	if err != nil {
		return Result{Status: "error", Message: fmt.Sprintf("%s is not followed", name)}
	}
	for _, ep := range episodes {
		if !followed.Episodes[ep] {
			return Result{Status: "error", Message: fmt.Sprintf("episode %d is not in download records", ep)}
		}
	}
	for _, ep := range episodes {
		delete(followed.Episodes, ep)
	}
	if err := db.Conn.SaveFollowed(followed); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	if err := db.Conn.MarkDownloadsDownloaded(name, episodes); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	maxEp := episodes[len(episodes)-1]
	result := seenResult("success", fmt.Sprintf("Forgot episodes %s of %s; they will be downloaded on next update", formatIntList(episodes), name), followed, &maxEp)
	result.Episodes = episodes
	return result
}

// SeenForget removes one episode from downloaded records.
func SeenForget(name string, episode int) Result {
	result := SeenForgetBatch(name, []int{episode})
	if result.Status == "success" {
		result.Data["episode"] = episode
	}
	return result
}

// SeenMarkBatch marks episodes as seen.
func SeenMarkBatch(name string, episodes []int) Result {
	if err := ensureDB(); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	episodes = normalizeEpisodes(episodes)
	if len(episodes) == 0 {
		return Result{Status: "error", Message: "episodes should not be empty"}
	}
	for _, ep := range episodes {
		if ep <= 0 {
			return Result{Status: "error", Message: "episode should be greater than 0"}
		}
	}
	followed, err := db.Conn.GetFollowedNotDeleted(name)
	if err != nil {
		return Result{Status: "error", Message: fmt.Sprintf("%s is not followed", name)}
	}
	for _, ep := range episodes {
		followed.Episodes[ep] = true
	}
	if err := db.Conn.SaveFollowed(followed); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	if err := db.Conn.MarkDownloadsDownloaded(name, episodes); err != nil {
		return Result{Status: "error", Message: err.Error()}
	}
	maxEp := episodes[len(episodes)-1]
	result := seenResult("success", fmt.Sprintf("Marked episodes %s of %s as seen", formatIntList(episodes), name), followed, &maxEp)
	result.Episodes = episodes
	return result
}

// SeenMark marks one episode as seen.
func SeenMark(name string, episode int) Result {
	result := SeenMarkBatch(name, []int{episode})
	if result.Status == "success" {
		result.Data["episode"] = episode
	}
	return result
}

func isInvalidCover(cover string) bool {
	return strings.HasSuffix(cover, "/subscribed-badge.svg") || strings.HasSuffix(cover, "subscribed-badge.svg")
}

func coverNeedsDownload(cover string) bool {
	if cover == "" || isInvalidCover(cover) {
		return false
	}
	_, file := utils.ConvertCoverURLToPath(cover)
	if info, err := os.Stat(file); err == nil && !info.IsDir() && utils.IsImage(file) {
		return false
	}
	return true
}

// EnsureCovers downloads any missing local cover files synchronously. It is
// used by the web API before returning local cover paths.
func EnsureCovers(urls []string) {
	seen := map[string]bool{}
	var need []string
	for _, url := range urls {
		if url == "" || seen[url] || !coverNeedsDownload(url) {
			continue
		}
		seen[url] = true
		need = append(need, url)
	}
	if len(need) > 0 {
		utils.DownloadCover(need)
	}
}

// DownloadCovers downloads all local cover images used by the web frontend.
// When refresh is true, missing covers of followed bangumi are first fetched
// from the active data source.
func DownloadCovers(refresh bool) {
	if err := ensureDB(); err != nil {
		logger.Warning("%v", err)
		return
	}
	if refresh {
		if src, err := source.New(); err == nil {
			refreshMissingCovers(src)
		}
	}

	seen := map[string]bool{}
	var covers []string
	add := func(url string) {
		if url == "" || seen[url] || !coverNeedsDownload(url) {
			return
		}
		seen[url] = true
		covers = append(covers, url)
	}

	if all, err := db.Conn.AllBangumi(); err == nil {
		for _, bangumi := range all {
			add(bangumi.Cover)
		}
	}
	for _, m := range script.GetModels() {
		add(m.Cover)
	}

	if len(covers) == 0 {
		logger.Info("Cover is up to date.")
		return
	}
	logger.Info("Updating cover (%d files) ...", len(covers))
	utils.DownloadCover(covers)
}

// CalItem is one entry in the weekly calendar.
type CalItem struct {
	ID            string
	Name          string
	Status        *int
	Episode       *int
	SubtitleGroup []db.Subtitle
	UpdateDay     string
	Cover         string
	UpdatedTime   int64
	IsScript      bool
}

// Cal returns the weekly calendar, including scripts.
func Cal(forceUpdate bool, cover []string) (map[string][]CalItem, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	weekly, err := db.Conn.GetUpdatingBangumi(nil)
	if err != nil {
		return nil, err
	}
	if len(weekly) == 0 {
		logger.Warning("Warning: no bangumi schedule, fetching ...")
		forceUpdate = true
	}
	src, srcErr := source.New()

	if forceUpdate {
		logger.Info("Fetching bangumi info ...")
		if srcErr != nil {
			return nil, srcErr
		}
		if _, err := source.Fetch(src, true); err != nil {
			return nil, err
		}
		weekly, err = db.Conn.GetUpdatingBangumi(nil)
		if err != nil {
			return nil, err
		}
	}

	if cover != nil && srcErr == nil {
		refreshMissingCovers(src)
		weekly, err = db.Conn.GetUpdatingBangumi(nil)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		var toDownload []string
		for _, url := range cover {
			if url != "" && !seen[url] {
				seen[url] = true
				toDownload = append(toDownload, url)
			}
		}
		for _, items := range weekly {
			for _, item := range items {
				if coverNeedsDownload(item.Cover) && !seen[item.Cover] {
					seen[item.Cover] = true
					toDownload = append(toDownload, item.Cover)
				}
			}
		}
		if len(toDownload) > 0 {
			logger.Info("Updating cover (%d files) ...", len(toDownload))
			utils.DownloadCover(toDownload)
		} else {
			logger.Info("Cover is up to date.")
		}
	}

	result := map[string][]CalItem{}
	for day, items := range weekly {
		for _, item := range items {
			subs, _ := db.Conn.GetSubtitlesByIDs(item.SubtitleGroup)
			result[day] = append(result[day], CalItem{
				ID:            item.ID,
				Name:          item.Name,
				Status:        item.FollowedStatus,
				Episode:       item.Episode,
				SubtitleGroup: subs,
				UpdateDay:     item.UpdateDay,
				Cover:         utils.NormalizePath(item.Cover),
			})
		}
	}

	for _, m := range script.GetModels() {
		status := m.Status
		ep := m.Episode
		day := strings.ToLower(m.UpdateDay)
		result[day] = append(result[day], CalItem{
			Name:          m.Name,
			Status:        &status,
			Episode:       &ep,
			SubtitleGroup: nil,
			UpdateDay:     m.UpdateDay,
			Cover:         utils.NormalizePath(m.Cover),
			UpdatedTime:   m.UpdatedTime,
			IsScript:      true,
		})
	}
	return result, nil
}

func refreshMissingCovers(src source.Source) {
	list, err := db.Conn.AllFollowedBangumi(db.BangumiStatusUpdating)
	if err != nil {
		return
	}
	var missing []db.FollowedBangumi
	for _, item := range list {
		if _, err := db.Conn.GetManualBangumi(item.Bangumi.Name); err == nil {
			continue
		}
		if item.Bangumi.Cover == "" || isInvalidCover(item.Bangumi.Cover) {
			missing = append(missing, item)
		}
	}
	if len(missing) == 0 {
		return
	}
	logger.Info("Refreshing missing covers (%d bangumi) ...", len(missing))
	for i, item := range missing {
		logger.Info("Refreshing cover %d/%d: %s", i+1, len(missing), item.Bangumi.Name)
		info, err := src.FetchSingleBangumi(item.Bangumi.ID, item.Followed.Subtitle, config.Cfg.MaxPath)
		if err != nil {
			logger.Warning("Failed to refresh cover for %s: %v", item.Bangumi.Name, err)
			continue
		}
		if info != nil && info.Cover != "" {
			_ = db.Conn.SaveBangumi(*info)
		}
	}
}

// Search searches torrents from the data source.
func Search(keyword string, count int, regex *string, dupe bool, minEpisode, maxEpisode *int, tag bool, subtitle *string) Result {
	options := map[string]any{
		"keyword":     keyword,
		"count":       count,
		"regex":       regex,
		"dupe":        dupe,
		"min_episode": minEpisode,
		"max_episode": maxEpisode,
	}
	src, err := source.New()
	if err != nil {
		return Result{Status: "error", Message: err.Error(), Data: map[string]any{"data": []model.Episode{}}}
	}
	var data []model.Episode
	if tag {
		sub := ""
		if subtitle != nil {
			sub = *subtitle
		}
		data, err = src.SearchByTag(keyword, sub, count)
	} else {
		data, err = src.SearchByKeyword(keyword, count)
	}
	if err != nil {
		return Result{Status: "error", Message: err.Error(), Options: options}
	}
	if regex != nil {
		data = utils.EpisodeFilterRegex(data, *regex)
	} else {
		data = utils.EpisodeFilterRegex(data, "")
	}
	if minEpisode != nil {
		out := data[:0]
		for _, e := range data {
			if e.Episode >= *minEpisode {
				out = append(out, e)
			}
		}
		data = out
	}
	if maxEpisode != nil {
		out := data[:0]
		for _, e := range data {
			if e.Episode <= *maxEpisode {
				out = append(out, e)
			}
		}
		data = out
	}
	if !dupe {
		data = model.RemoveDuplicatedEpisode(data)
	}
	sort.SliceStable(data, func(i, j int) bool { return data[i].Episode < data[j].Episode })
	return Result{Status: "success", Message: "", Data: map[string]any{"data": data}, Options: options}
}

func downloadPreviousFailedDownloads() {
	if err := ensureDB(); err != nil {
		return
	}
	status := db.DownloadStatusNotDownload
	failures, err := db.Conn.GetDownloads(&status)
	if err != nil || len(failures) == 0 {
		return
	}
	followed, err := db.Conn.AllFollowed()
	if err != nil {
		return
	}
	followings := map[string]*db.Followed{}
	for i := range followed {
		followings[followed[i].BangumiName] = &followed[i]
	}
	var need []db.Download
	for _, failure := range failures {
		f, ok := followings[failure.BangumiName]
		if !ok {
			continue
		}
		if f.Episodes[failure.Episode] {
			continue
		}
		need = append(need, failure)
	}
	if len(need) == 0 {
		return
	}
	logger.Info("try to re-downloading previous failed torrents ...")
	for _, d := range need {
		download.DownloadEpisode(model.Episode{
			Title:    d.Title,
			Episode:  d.Episode,
			Download: d.Download,
			Name:     d.BangumiName,
		})
	}
}

func groupEpisodes(all []model.Episode) []int {
	seen := map[int]bool{}
	var order []int
	for _, e := range all {
		if !seen[e.Episode] {
			seen[e.Episode] = true
			order = append(order, e.Episode)
		}
	}
	sort.Ints(order)
	return order
}

func episodesByNumber(all []model.Episode) map[int][]model.Episode {
	groups := map[int][]model.Episode{}
	for _, e := range all {
		groups[e.Episode] = append(groups[e.Episode], e)
	}
	return groups
}

func downloadEpisodes(following *db.Followed, all []model.Episode) {
	groups := episodesByNumber(all)
	updated := false
	for _, ep := range groupEpisodes(all) {
		if ep <= 0 || following.Episodes[ep] {
			continue
		}
		logger.Success("%s updated, episode: %d", following.BangumiName, ep)
		for _, e := range groups[ep] {
			if download.DownloadEpisode(e) {
				updated = true
				following.Episodes[ep] = true
				break
			}
		}
	}
	if updated {
		following.Status = db.FollowedStatusUpdated
		following.UpdatedTime = time.Now().Unix()
		_ = db.Conn.SaveFollowed(following)
	}
}

func downloadScriptEpisodes(following *db.Scripts, all []model.Episode) {
	groups := episodesByNumber(all)
	updated := false
	for _, ep := range groupEpisodes(all) {
		if ep <= 0 || following.Episodes[ep] {
			continue
		}
		logger.Success("%s updated, episode: %d", following.BangumiName, ep)
		for _, e := range groups[ep] {
			if download.DownloadEpisode(e) {
				updated = true
				following.Episodes[ep] = true
				break
			}
		}
	}
	if updated {
		following.Status = db.FollowedStatusUpdated
		following.UpdatedTime = time.Now().Unix()
		_ = db.Conn.SaveScript(following)
	}
}

// Update checks subscriptions and downloads new episodes.
func Update(names []string, withDownload bool, notIgnore bool) error {
	if err := ensureDB(); err != nil {
		return err
	}
	RunConfiguredBangumiSync()
	logger.Info("updating subscriptions ...")
	if withDownload {
		downloadPreviousFailedDownloads()
	}

	var subscriptions []db.Followed
	if len(names) == 0 {
		list, err := db.Conn.AllFollowedBangumi(db.BangumiStatusUpdating)
		if err != nil {
			return err
		}
		for _, item := range list {
			subscriptions = append(subscriptions, item.Followed)
		}
		sort.SliceStable(subscriptions, func(i, j int) bool { return subscriptions[i].BangumiName < subscriptions[j].BangumiName })
	} else {
		for _, name := range names {
			f, err := db.Conn.GetFollowed(name)
			if err != nil {
				logger.Warning("missing followed bangumi '%s'", name)
				continue
			}
			subscriptions = append(subscriptions, *f)
		}
	}

	now := time.Now().Unix()
	for _, result := range script.Run() {
		if !withDownload {
			for _, e := range result.Episodes {
				result.Script.Episodes[e.Episode] = true
			}
			result.Script.UpdatedTime = now
			_ = db.Conn.SaveScript(result.Script)
		} else {
			downloadScriptEpisodes(result.Script, result.Episodes)
		}
	}

	src, err := source.New()
	if err != nil {
		return err
	}
	ignoreOld := !notIgnore
	for _, subscribe := range subscriptions {
		logger.Info("fetching %s ...", subscribe.BangumiName)
		bangumi, err := db.Conn.GetBangumiByName(subscribe.BangumiName)
		if err != nil {
			logger.Error("Bangumi<%s> does not exists.", subscribe.BangumiName)
			continue
		}
		following, err := db.Conn.GetFollowed(subscribe.BangumiName)
		if err != nil {
			logger.Error("Followed<%s> is not followed.", subscribe.BangumiName)
			continue
		}
		all, err := source.GetMaximumEpisode(src, bangumi, ignoreOld, config.Cfg.MaxPath)
		if err != nil {
			logger.Warning("error %v to fetch %s, skip", err, bangumi.Name)
			continue
		}
		if len(all) == 0 {
			continue
		}
		if !withDownload {
			for _, e := range all {
				following.Episodes[e.Episode] = true
			}
			_ = db.Conn.SaveFollowed(following)
		} else {
			downloadEpisodes(following, all)
		}
	}
	return nil
}
