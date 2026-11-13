package utils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/kazumi"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/model"
)

var slashPattern = regexp.MustCompile(`/+`)

// NormalizePath turns a URL or title into a safe relative path.
func NormalizePath(u string) string {
	u = slashPattern.ReplaceAllString(u, "/")
	for _, ch := range []string{":", "*", "?", "\"", "<", ">", "|", "'"} {
		u = strings.ReplaceAll(u, ch, "")
	}
	return strings.TrimLeft(u, "/")
}

// BangumiSavePath returns the save directory for a bangumi, honoring
// save_path_map.
func BangumiSavePath(bangumiName string) string {
	name := NormalizePath(bangumiName)
	if r, ok := config.Cfg.SavePathMap[name]; ok {
		if filepath.IsAbs(r) {
			return r
		}
		return filepath.Join(config.Cfg.SavePath, r)
	}
	return filepath.Join(config.Cfg.SavePath, name)
}

// CoverCacheDir returns the dedicated cover cache directory.
func CoverCacheDir() string {
	if config.Cfg.CachePath != "" {
		return filepath.Join(config.Cfg.CachePath, "covers")
	}
	return filepath.Join(config.Cfg.SavePath, ".cover")
}

// ConvertCoverURLToPath returns (dir, file) for a cover URL.
func ConvertCoverURLToPath(coverURL string) (string, string) {
	coverURL = NormalizePath(coverURL)
	filePath := filepath.Join(CoverCacheDir(), coverURL)
	return filepath.Dir(filePath), filePath
}

// IsImage checks image magic bytes.
func IsImage(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 12)
	n, _ := f.Read(head)
	head = head[:n]
	if len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF {
		return true
	}
	if len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
		return true
	}
	if len(head) >= 6 && (bytes.Equal(head[:6], []byte("GIF87a")) || bytes.Equal(head[:6], []byte("GIF89a"))) {
		return true
	}
	if len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")) {
		return true
	}
	if len(head) >= 2 && head[0] == 'B' && head[1] == 'M' {
		return true
	}
	return false
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

// SetHTTPClient allows callers to reuse a proxied client.
func SetHTTPClient(c *http.Client) {
	if c != nil {
		httpClient = c
	}
}

func DownloadFile(url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return nil, fmt.Errorf("unsupported url %s", url)
	}
	logger.Info("Download: %s", url)
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// downloadCoverBytes fetches a cover using the configured Kazumi image
// acceleration mode. The original URL is still used as the local file path.
func downloadCoverBytes(rawURL string) ([]byte, error) {
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return nil, fmt.Errorf("unsupported url %s", rawURL)
	}
	mode, _ := kazumi.NormalizeImageAcceleration(config.Cfg.Kazumi.ImageAcceleration)
	target := kazumi.ApplyImageURL(rawURL, mode)
	logger.Info("Download: %s", rawURL)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := kazumi.Get(ctx, target, map[string]string{
		"User-Agent": "uani/1.0.0 (https://github.com/HanamiSakiP/u-ani)",
	}, mode == kazumi.ImageAccelerationECH && kazumi.IsBangumiImage(rawURL))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// DownloadCover downloads covers to save_path/.cover.
func DownloadCover(coverURLs []string) {
	filtered := coverURLs[:0]
	for _, u := range coverURLs {
		if u != "" {
			filtered = append(filtered, u)
		}
	}
	if len(filtered) == 0 {
		return
	}

	type result struct {
		url  string
		data []byte
		err  error
	}
	sem := make(chan struct{}, 3)
	results := make([]result, len(filtered))
	var wg sync.WaitGroup
	for i, u := range filtered {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			sem <- struct{}{}
			data, err := downloadCoverBytes(u)
			<-sem
			results[i] = result{url: u, data: data, err: err}
		}(i, u)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil || r.data == nil {
			if r.err != nil {
				logger.Warning("Failed to download %s: %v", r.url, r.err)
			}
			continue
		}
		dir, file := ConvertCoverURLToPath(r.url)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Warning("Failed to create cover directory %s: %v", dir, err)
			continue
		}
		if err := os.WriteFile(file, r.data, 0o644); err != nil {
			logger.Warning("Failed to write cover %s: %v", file, err)
		}
	}
}

// EpisodeFilterRegex applies the user regex and the global exclusion list.
func EpisodeFilterRegex(data []model.Episode, regex string) []model.Episode {
	if regex != "" {
		re, err := regexp.Compile(regex)
		if err != nil {
			logger.Warning("can't compile regex %s, skipping filter by regex", regex)
		} else {
			out := data[:0]
			for _, e := range data {
				if re.MatchString(e.Title) {
					out = append(out, e)
				}
			}
			data = out
		}
	}
	if config.Cfg.EnableGlobalFilters {
		keywords := make([]string, 0, len(config.Cfg.GlobalFilters))
		for _, k := range config.Cfg.GlobalFilters {
			keywords = append(keywords, strings.ToLower(strings.TrimSpace(k)))
		}
		out := data[:0]
		for _, e := range data {
			if !e.ContainsAnyWords(keywords) {
				out = append(out, e)
			}
		}
		data = out
	}
	return data
}

// FilterByFollowedFilters applies a followed bangumi's include/exclude/regex.
func FilterByFollowedFilters(data []model.Episode, followed *FollowedFilter) []model.Episode {
	if len(followed.Include) > 0 {
		keywords := lowerTrim(followed.Include)
		out := data[:0]
		for _, e := range data {
			if e.ContainsAnyWords(keywords) {
				out = append(out, e)
			}
		}
		data = out
	}
	if config.Cfg.EnableGlobalIncludeKeywords {
		keywords := lowerTrim(config.Cfg.GlobalIncludeKeywords)
		out := data[:0]
		for _, e := range data {
			if e.ContainsAnyWords(keywords) {
				out = append(out, e)
			}
		}
		data = out
	}
	if len(followed.Exclude) > 0 {
		keywords := lowerTrim(followed.Exclude)
		out := data[:0]
		for _, e := range data {
			if !e.ContainsAnyWords(keywords) {
				out = append(out, e)
			}
		}
		data = out
	}
	return EpisodeFilterRegex(data, followed.Regex)
}

// FollowedFilter is a small interface to avoid an import cycle with db.
type FollowedFilter struct {
	Subtitle []string
	Include  []string
	Exclude  []string
	Regex    string
}

func lowerTrim(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.ToLower(strings.TrimSpace(v)))
	}
	return out
}

// GetTerminalCol returns the terminal width, defaulting to 80.
func GetTerminalCol() int {
	if cols := os.Getenv("COLUMNS"); cols != "" {
		var n int
		if _, err := fmt.Sscanf(cols, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

// Width returns the display width of a string, accounting for wide CJK glyphs.
func Width(s string) int {
	return runewidth.StringWidth(s)
}

func ExpandUser(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
