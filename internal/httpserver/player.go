package httpserver

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/postprocessor"
	"github.com/HanamiSakiP/u-ani/internal/season"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

var videoExtensions = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".webm": true,
	".flv": true, ".rmvb": true, ".mov": true, ".ts": true,
}

// getPlayer returns the playable file path for each downloaded episode.
func getPlayer(name string, episodes []int, seasonNumber, episodeOffset int, displayName string) map[string]map[string]string {
	if config.Cfg.EnablePathFormatter {
		return getFormattedPlayer(name, episodes, seasonNumber, episodeOffset, displayName)
	}
	return getLegacyPlayer(name, episodes)
}

func getLegacyPlayer(name string, episodes []int) map[string]map[string]string {
	base := utils.BangumiSavePath(name)
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return map[string]map[string]string{}
	}

	var dirs []string
	if len(episodes) > 0 {
		for _, episode := range episodes {
			dirs = append(dirs, filepath.Join(base, strconv.Itoa(episode)))
		}
	} else {
		entries, err := os.ReadDir(base)
		if err != nil {
			return map[string]map[string]string{}
		}
		for _, entry := range entries {
			if entry.IsDir() {
				dirs = append(dirs, filepath.Join(base, entry.Name()))
			}
		}
	}

	out := map[string]map[string]string{}
	for _, dir := range dirs {
		episode, err := strconv.Atoi(filepath.Base(dir))
		if err != nil || episode <= 0 {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		video := findLargestVideo(dir)
		if video == "" {
			continue
		}
		if rel, ok := relativeToSavePath(video); ok {
			out[strconv.Itoa(episode)] = map[string]string{"path": "/" + rel}
		}
	}
	return out
}

func getFormattedPlayer(name string, episodes []int, seasonNumber, episodeOffset int, displayName string) map[string]map[string]string {
	display := displayName
	if display == "" {
		display = season.StripSeasonSuffix(name)
	}
	out := map[string]map[string]string{}
	for _, episode := range episodes {
		pattern, err := postprocessor.FormatPath(display, seasonNumber, episode+episodeOffset, "*", "*")
		if err != nil {
			continue
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		video := findLargestVideoFromList(matches)
		if video == "" {
			continue
		}
		if rel, ok := relativeToSavePath(video); ok {
			out[strconv.Itoa(episode)] = map[string]string{"path": "/" + rel}
		}
	}
	return out
}

func relativeToSavePath(path string) (string, bool) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	absSave, err := filepath.Abs(config.Cfg.SavePath)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absSave, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func findLargestVideo(dir string) string {
	var paths []string
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if videoExtensions[strings.ToLower(filepath.Ext(path))] {
			paths = append(paths, path)
		}
		return nil
	})
	return findLargestVideoFromList(paths)
}

func findLargestVideoFromList(paths []string) string {
	type candidate struct {
		path string
		size int64
	}
	var candidates []candidate
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if !videoExtensions[strings.ToLower(filepath.Ext(path))] {
			continue
		}
		candidates = append(candidates, candidate{path: path, size: info.Size()})
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].size > candidates[j].size })
	return candidates[0].path
}
