package source

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/model"
	"github.com/HanamiSakiP/u-ani/internal/source/bangumimoe"
	"github.com/HanamiSakiP/u-ani/internal/source/dmhy"
	"github.com/HanamiSakiP/u-ani/internal/source/mikan"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

// Source is the data source interface shared by all scrapers.
type Source interface {
	Name() string
	SearchByKeyword(keyword string, count int) ([]model.Episode, error)
	SearchByTag(tag string, subtitle string, count int) ([]model.Episode, error)
	FetchBangumiCalendar() ([]model.WebsiteBangumi, error)
	FetchEpisodeOfBangumi(bangumiID string, maxPage int, subtitleList []string) ([]model.Episode, error)
	FetchSingleBangumi(bangumiID string, subtitleList []string, maxPage int) (*model.WebsiteBangumi, error)
}

// New returns the configured data source.
func New() (Source, error) {
	return NewByName(string(config.Cfg.DataSource))
}

// NewByName returns a data source by its config identifier.
func NewByName(name string) (Source, error) {
	switch config.Source(name) {
	case config.SourceMikan:
		return mikan.New(), nil
	case config.SourceBangumiMoe:
		return bangumimoe.New(), nil
	case config.SourceDmhy:
		return dmhy.New(), nil
	default:
		return nil, fmt.Errorf("date source %q in config is wrong, please edit it manually", name)
	}
}

// SaveBangumi stores calendar/detail data, updating changed IDs by name.
func SaveBangumi(data model.WebsiteBangumi) error {
	if db.Conn == nil {
		if _, err := db.New(); err != nil {
			return err
		}
	}
	return db.Conn.SaveBangumi(data)
}

// Fetch downloads the calendar and stores it. When groupByWeekday is true the
// result is grouped by lower-case weekday.
func Fetch(src Source, groupByWeekday bool) (map[string][]model.WebsiteBangumi, error) {
	data, err := src.FetchBangumiCalendar()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		fmt.Println("can't fetch anything from website")
		return map[string][]model.WebsiteBangumi{}, nil
	}
	if db.Conn == nil {
		if _, err := db.New(); err != nil {
			return nil, err
		}
	}
	if err := db.Conn.MarkAllEnd(time.Now().Unix()); err != nil {
		return nil, err
	}
	for _, b := range data {
		if err := db.Conn.SaveBangumi(b); err != nil {
			logger.Warning("Failed to save bangumi %s: %v", b.Name, err)
		}
	}
	if !groupByWeekday {
		return nil, nil
	}
	grouped := map[string][]model.WebsiteBangumi{}
	for _, b := range data {
		key := strings.ToLower(b.UpdateDay)
		grouped[key] = append(grouped[key], b)
	}
	return grouped, nil
}

// ApplyFilters applies a followed bangumi's filters to episodes.
func ApplyFilters(f *db.Followed, data []model.Episode) []model.Episode {
	return utils.FilterByFollowedFilters(data, &utils.FollowedFilter{
		Subtitle: f.Subtitle,
		Include:  f.Include,
		Exclude:  f.Exclude,
		Regex:    f.Regex,
	})
}

// GetMaximumEpisode fetches the latest episodes for a subscribed bangumi.
func GetMaximumEpisode(src Source, bangumi *db.Bangumi, ignoreOld bool, maxPage int) ([]model.Episode, error) {
	if maxPage <= 0 {
		maxPage = config.Cfg.MaxPath
	}
	followed, err := db.Conn.GetFollowed(bangumi.Name)
	if err != nil {
		return nil, err
	}

	if manual, err := db.Conn.GetManualBangumi(bangumi.Name); err == nil {
		data, err := GetManualEpisodes(src, manual.SearchNames, maxPage)
		if err != nil {
			return nil, err
		}
		data = ApplyFilters(followed, data)
		for i := range data {
			data[i].Name = bangumi.Name
		}
		if ignoreOld {
			cutoff := time.Now().Unix() - 3600*24*30*3
			out := data[:0]
			for _, e := range data {
				if e.Time > cutoff {
					out = append(out, e)
				}
			}
			data = out
		}
		return data, nil
	}

	info, err := src.FetchSingleBangumi(bangumi.ID, followed.Subtitle, maxPage)
	if err != nil {
		return nil, err
	}
	var data []model.Episode
	if info != nil {
		if err := db.Conn.SaveBangumi(*info); err != nil {
			logger.Warning("Failed to save bangumi %s: %v", info.Name, err)
		}
		data = ApplyFilters(followed, info.Episodes)
	} else {
		data, err = src.FetchEpisodeOfBangumi(bangumi.ID, maxPage, followed.Subtitle)
		if err != nil {
			return nil, err
		}
		data = ApplyFilters(followed, data)
	}

	for i := range data {
		data[i].Name = bangumi.Name
	}
	if ignoreOld {
		cutoff := time.Now().Unix() - 3600*24*30*3
		out := data[:0]
		for _, e := range data {
			if e.Time > cutoff {
				out = append(out, e)
			}
		}
		data = out
	}
	return data, nil
}

// GetManualEpisodes searches the active torrent source for a subscription that
// was created from an external metadata provider such as bgm.tv.
func GetManualEpisodes(src Source, searchNames []string, maxPage int) ([]model.Episode, error) {
	if maxPage <= 0 {
		maxPage = config.Cfg.MaxPath
	}
	seenDownloads := map[string]bool{}
	var out []model.Episode
	var lastErr error
	for _, name := range searchNames {
		normalized := normalizeSearchText(name)
		if normalized == "" {
			continue
		}
		episodes, err := src.SearchByKeyword(name, maxPage)
		if err != nil {
			lastErr = err
			continue
		}
		for _, episode := range episodes {
			if episode.Episode <= 0 {
				continue
			}
			if !strings.Contains(normalizeSearchText(episode.Title), normalized) {
				continue
			}
			key := episode.Download
			if key == "" {
				key = episode.Title
			}
			if seenDownloads[key] {
				continue
			}
			seenDownloads[key] = true
			out = append(out, episode)
		}
		// The first title that produces results is normally the best match.
		// Stop before trying generic aliases such as "Fate", which would match
		// unrelated entries from the same franchise.
		if len(out) > 0 {
			break
		}
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Episode == out[j].Episode {
			return out[i].Time > out[j].Time
		}
		return out[i].Episode < out[j].Episode
	})
	return out, nil
}

func normalizeSearchText(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
