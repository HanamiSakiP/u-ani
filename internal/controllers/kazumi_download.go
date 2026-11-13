package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
	"github.com/HanamiSakiP/u-ani/internal/kazumi"
)

// KazumiDownloadOptions requests one streaming episode download.
type KazumiDownloadOptions struct {
	Rule      string `json:"rule"`
	Name      string `json:"name"`
	BangumiID int    `json:"bangumi_id"`
	Title     string `json:"title"`
	Episode   int    `json:"episode"`
	Source    string `json:"source"`
}

// KazumiFile is one downloaded media file.
type KazumiFile struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

var (
	kazumiTaskMu      sync.Mutex
	kazumiTaskCancels = map[int64]context.CancelFunc{}
)

// KazumiFilesDir is the root directory for streaming downloads.
func KazumiFilesDir() string {
	return filepath.Join(config.Cfg.SavePath, "kazumi")
}

// KazumiCacheDir stores Kazumi browse/detail cache files in a dedicated directory.
func KazumiCacheDir() string {
	base := config.Cfg.CachePath
	if base == "" {
		base = KazumiFilesDir()
	}
	dir := filepath.Join(base, "kazumi")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// StartKazumiDownload creates a task and downloads it in the background.
func StartKazumiDownload(opts KazumiDownloadOptions) (*db.KazumiDownload, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	opts.Rule = strings.TrimSpace(opts.Rule)
	opts.Name = strings.TrimSpace(opts.Name)
	opts.Source = strings.TrimSpace(opts.Source)
	opts.Title = strings.TrimSpace(opts.Title)
	if opts.Rule == "" {
		return nil, fmt.Errorf("rule is required")
	}
	if opts.Name == "" {
		return nil, fmt.Errorf("bangumi name is required")
	}
	if opts.Source == "" {
		return nil, fmt.Errorf("episode source URL is required")
	}
	if opts.Episode <= 0 {
		return nil, fmt.Errorf("episode should be greater than 0")
	}
	rule, err := kazumi.CurrentRule(opts.Rule)
	if err != nil {
		return nil, err
	}
	if opts.Title == "" {
		opts.Title = opts.Name
	}
	if opts.BangumiID <= 0 {
		if subjectID, err := db.Conn.GetSubjectIDByBangumiName(opts.Name); err == nil {
			opts.BangumiID = subjectID
		}
	}
	task := &db.KazumiDownload{
		Name:      opts.Name,
		BangumiID: opts.BangumiID,
		Episode:   opts.Episode,
		Title:     opts.Title,
		Rule:      opts.Rule,
		Source:    opts.Source,
		Status:    db.KazumiDownloadPending,
	}
	if err := db.Conn.SaveKazumiDownload(task); err != nil {
		return nil, err
	}
	enqueueKazumiDownload(task, rule, opts)
	return task, nil
}

func enqueueKazumiDownload(task *db.KazumiDownload, rule *kazumi.Rule, opts KazumiDownloadOptions) {
	ctx, cancel := context.WithCancel(context.Background())
	kazumiTaskMu.Lock()
	kazumiTaskCancels[task.ID] = cancel
	kazumiTaskMu.Unlock()
	go runKazumiDownload(ctx, task.ID, rule, opts)
}

// CancelKazumiDownload stops a running task.
func CancelKazumiDownload(id int64) error {
	kazumiTaskMu.Lock()
	cancel := kazumiTaskCancels[id]
	kazumiTaskMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if err := ensureDB(); err != nil {
		return err
	}
	task, err := db.Conn.GetKazumiDownload(id)
	if err != nil {
		return err
	}
	if task.Status == db.KazumiDownloadPending || task.Status == db.KazumiDownloadRunning {
		task.Status = db.KazumiDownloadCanceled
		task.Error = "canceled"
		task.Speed = 0
		_ = db.Conn.SaveKazumiDownload(task)
	}
	return nil
}

// RetryKazumiDownload re-queues a failed, canceled or finished task.
func RetryKazumiDownload(id int64) (*db.KazumiDownload, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	task, err := db.Conn.GetKazumiDownload(id)
	if err != nil {
		return nil, err
	}
	if task.Status == db.KazumiDownloadPending || task.Status == db.KazumiDownloadRunning {
		return nil, fmt.Errorf("task is still running")
	}
	rule, err := kazumi.CurrentRule(task.Rule)
	if err != nil {
		return nil, err
	}
	task.Status = db.KazumiDownloadPending
	task.Error = ""
	task.Speed = 0
	task.Size = 0
	task.Path = ""
	task.MediaURL = ""
	if err := db.Conn.SaveKazumiDownload(task); err != nil {
		return nil, err
	}
	enqueueKazumiDownload(task, rule, KazumiDownloadOptions{
		Rule:      task.Rule,
		Name:      task.Name,
		BangumiID: task.BangumiID,
		Title:     task.Title,
		Episode:   task.Episode,
		Source:    task.Source,
	})
	return task, nil
}

// DeleteKazumiDownload removes a finished task and its downloaded file.
func DeleteKazumiDownload(id int64) error {
	if err := ensureDB(); err != nil {
		return err
	}
	task, err := db.Conn.GetKazumiDownload(id)
	if err != nil {
		return err
	}
	if task.Status == db.KazumiDownloadPending || task.Status == db.KazumiDownloadRunning {
		return fmt.Errorf("cancel the task before deleting it")
	}
	if task.Path != "" {
		_ = DeleteKazumiFile(task.Path)
	}
	return db.Conn.DeleteKazumiDownload(id)
}

// ListKazumiDownloads returns recent streaming download tasks.
func ListKazumiDownloads() ([]db.KazumiDownload, error) {
	if err := ensureDB(); err != nil {
		return nil, err
	}
	RepairKazumiDownloadLinksOnce()
	tasks, err := db.Conn.ListKazumiDownloads(100)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if tasks[i].Size > 0 || tasks[i].Path == "" {
			continue
		}
		if path, err := kazumiFileAbs(tasks[i].Path); err == nil {
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				tasks[i].Size = info.Size()
				_ = db.Conn.SaveKazumiDownload(&tasks[i])
			}
		}
	}
	return tasks, nil
}

// ListKazumiFiles walks the streaming download directory.
func ListKazumiFiles() ([]KazumiFile, error) {
	root := KazumiFilesDir()
	out := []KazumiFile{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !kazumiVideoExtension(ext) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, KazumiFile{
			Name:     entry.Name(),
			Path:     filepath.ToSlash(rel),
			Size:     info.Size(),
			Modified: info.ModTime().Unix(),
		})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	return out, nil
}

// DeleteKazumiFile removes a downloaded file and empty parent directories.
func DeleteKazumiFile(rel string) error {
	path, err := kazumiFileAbs(rel)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	for dir := filepath.Dir(path); strings.HasPrefix(dir, KazumiFilesDir()) && dir != KazumiFilesDir(); dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			break
		}
	}
	return nil
}

// KazumiFileAbs resolves a relative streaming download path safely.
func KazumiFileAbs(rel string) (string, error) {
	return kazumiFileAbs(rel)
}

func kazumiFileAbs(rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(rel, "/")))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid file path")
	}
	root, err := filepath.Abs(KazumiFilesDir())
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, clean)
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid file path")
	}
	return abs, nil
}

func runKazumiDownload(ctx context.Context, id int64, rule *kazumi.Rule, opts KazumiDownloadOptions) {
	defer func() {
		kazumiTaskMu.Lock()
		delete(kazumiTaskCancels, id)
		kazumiTaskMu.Unlock()
	}()
	task, err := db.Conn.GetKazumiDownload(id)
	if err != nil {
		return
	}
	task.Status = db.KazumiDownloadRunning
	_ = db.Conn.SaveKazumiDownload(task)

	dir := filepath.Join(KazumiFilesDir(), safeKazumiName(opts.Name))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		failKazumiTask(task, err)
		return
	}
	cleanupKazumiEpisodeFiles(dir, opts.Episode, "")

	progressCtx, stopProgress := context.WithCancel(ctx)
	defer stopProgress()
	go trackKazumiProgress(progressCtx, id, dir, opts.Episode)

	input, media, mediaReferer, _ := kazumi.ResolvePlayback(rule, opts.Source)
	if input == "" {
		input = opts.Source
	}
	if media || input != opts.Source {
		task.MediaURL = input
		_ = db.Conn.SaveKazumiDownload(task)
	}

	referer := strings.TrimSpace(rule.Referer)
	if referer == "" {
		referer = opts.Source
	}
	if mediaReferer == "-" {
		referer = ""
	} else if mediaReferer != "" {
		referer = mediaReferer
	}
	ua := strings.TrimSpace(rule.UserAgent)
	if ua == "" {
		ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
	}

	origin := ""
	if referer != "" {
		origin = originOf(opts.Source)
	}
	runErr := downloadKazumiInput(ctx, dir, opts.Episode, opts.Source, input, media, referer, ua, origin)
	if runErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			task.Status = db.KazumiDownloadCanceled
			task.Error = "canceled"
		} else {
			task.Status = db.KazumiDownloadFailed
			task.Error = runErr.Error()
		}
		task.Speed = 0
		_ = db.Conn.SaveKazumiDownload(task)
		return
	}

	path := findKazumiEpisodeFile(dir, opts.Episode)
	if path == "" {
		task.Status = db.KazumiDownloadFailed
		task.Error = "download finished but no output file was found"
		task.Speed = 0
		_ = db.Conn.SaveKazumiDownload(task)
		return
	}
	cleanupKazumiEpisodeFiles(dir, opts.Episode, path)
	info, err := os.Stat(path)
	if err == nil {
		task.Size = info.Size()
	}
	rel, err := filepath.Rel(KazumiFilesDir(), path)
	if err == nil {
		task.Path = filepath.ToSlash(rel)
	} else {
		task.Path = path
	}
	task.Status = db.KazumiDownloadDone
	task.Error = ""
	task.Speed = 0
	_ = db.Conn.SaveKazumiDownload(task)
}

func downloadKazumiInput(ctx context.Context, dir string, episode int, pageURL, input string, media bool, referer, ua, origin string) error {
	var attempts int
	var failures []string
	attempt := func(label string, fn func() error) bool {
		if err := ctx.Err(); err != nil {
			return false
		}
		attempts++
		if err := fn(); err != nil {
			failures = append(failures, label+": "+strings.TrimSpace(err.Error()))
			return false
		}
		return true
	}

	if hasExecutable("yt-dlp") {
		if attempt("yt-dlp", func() error {
			return downloadWithYtDlp(ctx, dir, episode, input, referer, ua, origin)
		}) {
			return nil
		}
	}
	if media && hasExecutable("ffmpeg") {
		if attempt("ffmpeg", func() error {
			return downloadWithFFmpeg(ctx, dir, episode, input, referer, ua, origin)
		}) {
			return nil
		}
	}
	if media && strings.Contains(strings.ToLower(input), ".m3u8") {
		if attempt("hls", func() error {
			_, err := kazumi.DownloadHLS(ctx, input, kazumi.HLSOutputPath(dir, episode), map[string]string{
				"User-Agent": ua,
				"Referer":    referer,
				"Origin":     origin,
			})
			return err
		}) {
			return nil
		}
	}
	if media && !strings.Contains(strings.ToLower(input), ".m3u8") {
		if attempt("http", func() error {
			return downloadDirect(ctx, dir, episode, input, referer, ua)
		}) {
			return nil
		}
	}
	if hasExecutable("yt-dlp") && input != pageURL {
		if attempt("yt-dlp-page", func() error {
			return downloadWithYtDlp(ctx, dir, episode, pageURL, referer, ua, origin)
		}) {
			return nil
		}
	}
	if attempts == 0 {
		return fmt.Errorf("no supported downloader found; install yt-dlp or ffmpeg, or use a direct media URL")
	}
	message := strings.Join(failures, "; ")
	if len(message) > 1500 {
		message = message[:1500] + "..."
	}
	return fmt.Errorf("%s", message)
}

func downloadWithYtDlp(ctx context.Context, dir string, episode int, input, referer, ua, origin string) error {
	args := []string{
		"--no-warnings", "--no-playlist", "--no-progress",
		"--retries", "3", "--fragment-retries", "3",
		"--concurrent-fragments", "4",
		"--user-agent", ua,
		"-o", filepath.Join(dir, fmt.Sprintf("%d.%%(ext)s", episode)),
	}
	if referer != "" {
		args = append(args, "--referer", referer)
	}
	if origin != "" {
		args = append(args, "--add-header", "Origin: "+origin)
	}
	args = append(args, input)
	return runCommand(ctx, "yt-dlp", args...)
}

func downloadWithFFmpeg(ctx context.Context, dir string, episode int, input, referer, ua, origin string) error {
	output := filepath.Join(dir, fmt.Sprintf("%d.mp4", episode))
	headerValue := "User-Agent: " + ua + "\r\n"
	if referer != "" {
		headerValue += "Referer: " + referer + "\r\n"
	}
	if origin != "" {
		headerValue += "Origin: " + origin + "\r\n"
	}
	return runCommand(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error", "-y",
		"-user_agent", ua,
		"-referer", referer,
		"-headers", headerValue,
		"-i", input, "-c", "copy", output,
	)
}

func originOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func downloadDirect(ctx context.Context, dir string, episode int, input, referer, ua string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, input, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", ua)
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	resp, err := httpx.Client().Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	ext := strings.ToLower(filepath.Ext(input))
	if ext == "" || len(ext) > 6 {
		ext = ".mp4"
	}
	output := filepath.Join(dir, fmt.Sprintf("%d%s", episode, ext))
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		if len(message) > 800 {
			message = message[:800]
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func hasExecutable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func failKazumiTask(task *db.KazumiDownload, err error) {
	task.Status = db.KazumiDownloadFailed
	task.Error = err.Error()
	task.Speed = 0
	_ = db.Conn.SaveKazumiDownload(task)
}

func trackKazumiProgress(ctx context.Context, id int64, dir string, episode int) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	prefix := strconv.Itoa(episode) + "."
	var last int64 = -1
	lastTime := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			var total int64
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
					continue
				}
				if info, err := entry.Info(); err == nil {
					total += info.Size()
				}
			}
			now := time.Now()
			if total == last {
				if now.Sub(lastTime) > 3*time.Second {
					if item, err := db.Conn.GetKazumiDownload(id); err == nil && item.Status == db.KazumiDownloadRunning && item.Speed != 0 {
						item.Speed = 0
						_ = db.Conn.SaveKazumiDownload(item)
					}
				}
				continue
			}
			speed := int64(0)
			if elapsed := now.Sub(lastTime).Seconds(); elapsed > 0 && total > last {
				speed = int64(float64(total-last) / elapsed)
			}
			last = total
			lastTime = now
			item, err := db.Conn.GetKazumiDownload(id)
			if err != nil || item.Status != db.KazumiDownloadRunning {
				continue
			}
			item.Size = total
			item.Speed = speed
			_ = db.Conn.SaveKazumiDownload(item)
		}
	}
}

func cleanupKazumiEpisodeFiles(dir string, episode int, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := strconv.Itoa(episode) + "."
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(dir, name)
		if path == keep {
			continue
		}
		if strings.HasPrefix(name, prefix) {
			_ = os.Remove(path)
		}
	}
}

func findKazumiEpisodeFile(dir string, episode int) string {
	matches, _ := filepath.Glob(filepath.Join(dir, strconv.Itoa(episode)+".*"))
	best := ""
	var bestSize int64
	for _, match := range matches {
		if !kazumiVideoExtension(strings.ToLower(filepath.Ext(match))) {
			continue
		}
		info, err := os.Stat(match)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() > bestSize {
			best = match
			bestSize = info.Size()
		}
	}
	return best
}

func kazumiVideoExtension(ext string) bool {
	switch ext {
	case ".mp4", ".mkv", ".avi", ".webm", ".flv", ".rmvb", ".mov", ".ts", ".m4v":
		return true
	default:
		return false
	}
}

func safeKazumiName(name string) string {
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", "\x00", "_",
	)
	name = replacer.Replace(name)
	name = strings.Trim(name, ". ")
	if name == "" {
		name = "kazumi-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	return name
}

var repairLinksOnce sync.Once

// RepairKazumiDownloadLinksOnce links legacy completed downloads to their
// subscriptions. New downloads carry bangumi_id from the start; this runs once
// so older files also show up in 已缓存 and 已安装集数.
func RepairKazumiDownloadLinksOnce() {
	repairLinksOnce.Do(RepairKazumiDownloadLinks)
}

// RepairKazumiDownloadLinks re-links completed downloads immediately. It is
// called after aliases or Bangumi links are changed.
func RepairKazumiDownloadLinks() {
	if ensureDB() != nil {
		return
	}
	tasks, err := db.Conn.ListKazumiDownloads(500)
	if err != nil {
		return
	}
	repairKazumiDownloadLinks(tasks)
}

func repairKazumiDownloadLinks(tasks []db.KazumiDownload) {
	followed, err := db.Conn.AllFollowed()
	if err != nil {
		return
	}
	type candidate struct {
		name      string
		subjectID int
		norm      []string
	}
	var candidates []candidate
	for _, item := range followed {
		if item.Status == db.FollowedStatusDeleted || item.IsScript {
			continue
		}
		names := []string{item.BangumiName, item.DisplayName}
		if manual, err := db.Conn.GetManualBangumi(item.BangumiName); err == nil {
			names = append(names, manual.SearchNames...)
		}
		if aliases, err := db.Conn.ListCustomAliases(item.BangumiName); err == nil {
			names = append(names, aliases...)
		}
		subjectID, _ := db.Conn.GetSubjectIDByBangumiName(item.BangumiName)
		entry := candidate{name: item.BangumiName, subjectID: subjectID}
		for _, name := range names {
			if value := normalizeForMatch(name); value != "" {
				entry.norm = append(entry.norm, value)
			}
		}
		candidates = append(candidates, entry)
	}
	for i := range tasks {
		if tasks[i].Status != db.KazumiDownloadDone || (tasks[i].BangumiID > 0 && tasks[i].Name != "") {
			continue
		}
		value := normalizeForMatch(tasks[i].Name)
		if value == "" {
			continue
		}
		best := -1
		bestExact := false
		bestLength := 0
		for ci, candidate := range candidates {
			for _, name := range candidate.norm {
				if value == name {
					best = ci
					bestExact = true
					break
				}
				if len([]rune(name)) >= 4 && (strings.Contains(value, name) || strings.Contains(name, value)) {
					if length := len([]rune(name)); length > bestLength {
						best = ci
						bestLength = length
					}
				}
			}
			if bestExact {
				break
			}
		}
		if best < 0 {
			continue
		}
		candidate := candidates[best]
		changed := false
		if tasks[i].Name != candidate.name {
			tasks[i].Name = candidate.name
			changed = true
		}
		if candidate.subjectID > 0 && tasks[i].BangumiID != candidate.subjectID {
			tasks[i].BangumiID = candidate.subjectID
			changed = true
		}
		if changed {
			_ = db.Conn.SaveKazumiDownload(&tasks[i])
		}
	}
}
