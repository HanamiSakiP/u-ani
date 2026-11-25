package controllers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	"sync/atomic"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/httpx"
	"github.com/HanamiSakiP/u-ani/internal/kazumi"
	"github.com/HanamiSakiP/u-ani/internal/utils"
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
	Name        string `json:"name"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	Modified    int64  `json:"modified"`
	Title       string `json:"title,omitempty"`
	BangumiName string `json:"bangumi_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	BangumiID   int    `json:"bangumi_id,omitempty"`
	Episode     int    `json:"episode,omitempty"`
	Cover       string `json:"cover,omitempty"`
}

var (
	kazumiTaskMu      sync.Mutex
	kazumiTaskCancels = map[int64]context.CancelFunc{}
	// kazumiStartMu serializes task creation so two clicks (or a batch download
	// and a manual retry) cannot start the same episode at the same time and
	// delete each other's in-progress files.
	kazumiStartMu sync.Mutex
	// kazumiDownloadSlots limits concurrent downloads. A 12-episode batch used
	// to spawn 12 yt-dlp/ffmpeg processes at once, which frequently hit site
	// rate limits and left half of the batch failed.
	kazumiDownloadSlots = make(chan struct{}, 3)
)

// ErrKazumiAlreadyDownloaded is returned when a completed download is retried
// without explicit confirmation. The Web UI asks the user before passing force.
var ErrKazumiAlreadyDownloaded = errors.New("该视频已下载完成；重新下载会覆盖现有文件，请确认后再试")

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
	kazumiStartMu.Lock()
	defer kazumiStartMu.Unlock()
	// Reuse an in-flight task for the same episode instead of starting a second
	// downloader that would fight over the same output file.
	if active := findActiveKazumiDownload(opts.Name, opts.Episode); active != nil {
		return active, nil
	}
	// Never download an episode twice. The watch page hides the download
	// button for completed episodes, but this also protects stale pages and
	// direct API calls. Re-downloading a completed task still works through
	// RetryKazumiDownload with force.
	if completed := findCompletedKazumiDownload(opts.Name, opts.Episode); completed != nil {
		return nil, ErrKazumiAlreadyDownloaded
	}
	if err := db.Conn.SaveKazumiDownload(task); err != nil {
		return nil, err
	}
	enqueueKazumiDownload(task, rule, opts)
	return task, nil
}

func findCompletedKazumiDownload(name string, episode int) *db.KazumiDownload {
	if db.Conn == nil {
		return nil
	}
	tasks, err := db.Conn.AllKazumiDownloads()
	if err != nil {
		return nil
	}
	name = strings.TrimSpace(name)
	for i := range tasks {
		task := &tasks[i]
		if strings.TrimSpace(task.Name) != name || task.Episode != episode {
			continue
		}
		if task.Status == db.KazumiDownloadDone && strings.TrimSpace(task.Path) != "" {
			return task
		}
	}
	return nil
}

func findActiveKazumiDownload(name string, episode int) *db.KazumiDownload {
	if db.Conn == nil {
		return nil
	}
	tasks, err := db.Conn.AllKazumiDownloads()
	if err != nil {
		return nil
	}
	name = strings.TrimSpace(name)
	for i := range tasks {
		task := &tasks[i]
		if strings.TrimSpace(task.Name) != name || task.Episode != episode {
			continue
		}
		if task.Status == db.KazumiDownloadPending || task.Status == db.KazumiDownloadRunning {
			return task
		}
	}
	return nil
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

// RetryKazumiDownload re-queues a failed or canceled task. A completed task is
// only retried when force is true, because re-downloading overwrites the file
// that is currently available for playback.
func RetryKazumiDownload(id int64, force ...bool) (*db.KazumiDownload, error) {
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
	allowOverwrite := len(force) > 0 && force[0]
	if task.Status == db.KazumiDownloadDone && !allowOverwrite {
		return nil, ErrKazumiAlreadyDownloaded
	}
	rule, err := kazumi.CurrentRule(task.Rule)
	if err != nil {
		return nil, err
	}
	kazumiStartMu.Lock()
	defer kazumiStartMu.Unlock()
	if active := findActiveKazumiDownload(task.Name, task.Episode); active != nil && active.ID != task.ID {
		return nil, fmt.Errorf("该集已有下载任务正在进行")
	}
	task.Status = db.KazumiDownloadPending
	task.Error = ""
	task.Speed = 0
	task.TotalSize = 0
	task.Progress = 0
	task.MediaURL = ""
	// Keep Path and Size for a completed task. The downloader snapshots the
	// existing file and only replaces it after a new file is fully downloaded,
	// so a failed retry no longer destroys a playable copy.
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
	enrichKazumiDownloadTasks(tasks)
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
		// Hidden staging/backup files produced by an interrupted download are
		// not playable episodes and may still carry a video extension.
		if strings.HasPrefix(entry.Name(), ".uani-") {
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
		// A zero-byte file is a leftover from an interrupted old download and
		// cannot be played; showing it as an episode only confuses the user.
		if info.Size() <= 0 {
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
	enrichKazumiFiles(out)
	return out, nil
}

// enrichKazumiFiles adds the bangumi title, episode number and cover from
// completed download tasks so the Kazumi Files page can render a useful card
// instead of only showing an opaque file path. Legacy files without a task
// still get a best-effort title from their directory name.
func enrichKazumiFiles(files []KazumiFile) {
	if len(files) == 0 || db.Conn == nil {
		return
	}
	tasks, err := db.Conn.AllKazumiDownloads()
	if err != nil {
		return
	}
	byPath := map[string]db.KazumiDownload{}
	byEpisode := map[string]db.KazumiDownload{}
	for _, task := range tasks {
		if task.Path == "" {
			continue
		}
		key := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(filepath.FromSlash(task.Path)), "/"))
		byPath[key] = task
		byEpisode[strings.TrimSpace(task.Name)+"\x00"+strconv.Itoa(task.Episode)] = task
	}
	resolver := newKazumiMetaResolver()
	for i := range files {
		file := &files[i]
		rel := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(filepath.FromSlash(file.Path)), "/"))
		task, ok := byPath[rel]
		if !ok {
			folder := firstPathSegment(rel)
			episode := episodeFromFileName(file.Name)
			task = byEpisode[strings.TrimSpace(folder)+"\x00"+strconv.Itoa(episode)]
			if task.Name == "" {
				task = db.KazumiDownload{Name: folder, Episode: episode}
			}
		}
		if task.Name != "" {
			file.BangumiName = task.Name
		}
		if file.BangumiName == "" {
			file.BangumiName = firstPathSegment(rel)
		}
		if task.Episode > 0 {
			file.Episode = task.Episode
		} else if file.Episode == 0 {
			file.Episode = episodeFromFileName(file.Name)
		}
		if task.Title != "" {
			file.Title = task.Title
		}
		if task.BangumiID > 0 {
			file.BangumiID = task.BangumiID
		}
		file.DisplayName = resolver.displayName(file.BangumiName)
		file.Cover = resolver.cover(task, file.BangumiName)
	}
}

// enrichKazumiDownloadTasks adds the display name and cover used by the task
// page. The fields are derived at read time and never persisted.
func enrichKazumiDownloadTasks(tasks []db.KazumiDownload) {
	if len(tasks) == 0 || db.Conn == nil {
		return
	}
	resolver := newKazumiMetaResolver()
	for i := range tasks {
		tasks[i].DisplayName = resolver.displayName(tasks[i].Name)
		tasks[i].Cover = resolver.cover(tasks[i], tasks[i].Name)
	}
}

// kazumiMetaResolver resolves the display name and cover for download tasks.
// Repeated lookups for the same subject are cached.
type kazumiMetaResolver struct {
	covers map[string]string
}

func newKazumiMetaResolver() *kazumiMetaResolver {
	return &kazumiMetaResolver{covers: map[string]string{}}
}

func (r *kazumiMetaResolver) displayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || db.Conn == nil {
		return ""
	}
	if followed, err := db.Conn.GetFollowed(name); err == nil {
		return followed.DisplayName
	}
	return ""
}

func (r *kazumiMetaResolver) cover(task db.KazumiDownload, name string) string {
	key := fmt.Sprintf("%d\x00%s", task.BangumiID, name)
	if cover, ok := r.covers[key]; ok {
		return cover
	}
	cover := ""
	if task.BangumiID > 0 {
		cover = cachedOverviewCover(int64(task.BangumiID))
	}
	if cover == "" && name != "" {
		if followed, err := db.Conn.GetBangumiByName(name); err == nil {
			cover = followed.Cover
		}
		if cover == "" {
			if subjectID, err := db.Conn.GetSubjectIDByBangumiName(name); err == nil && subjectID > 0 {
				cover = cachedOverviewCover(int64(subjectID))
			}
		}
		if cover == "" {
			if display := r.displayName(name); display != "" {
				if followed, err := db.Conn.GetBangumiByName(display); err == nil {
					cover = followed.Cover
				}
			}
		}
	}
	cover = kazumiCoverPath(cover)
	r.covers[key] = cover
	return cover
}

func firstPathSegment(path string) string {
	path = strings.Trim(path, "/")
	if index := strings.Index(path, "/"); index >= 0 {
		return path[:index]
	}
	return path
}

func episodeFromFileName(name string) int {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	for i, ch := range base {
		if ch < '0' || ch > '9' {
			base = base[:i]
			break
		}
	}
	value, err := strconv.Atoi(base)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func cachedOverviewCover(subjectID int64) string {
	if subjectID <= 0 {
		return ""
	}
	cache, err := db.Conn.GetBangumiCache(subjectID)
	if err != nil || cache == nil || cache.Overview == "" {
		return ""
	}
	var payload struct {
		Cover string `json:"cover"`
	}
	if json.Unmarshal([]byte(cache.Overview), &payload) != nil {
		return ""
	}
	return payload.Cover
}

func kazumiCoverPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "/covers/") || strings.HasPrefix(raw, "/api/image") {
		return raw
	}
	if _, file := utils.ConvertCoverURLToPath(raw); utils.IsImage(file) {
		return "/covers/" + utils.NormalizePath(raw)
	}
	return raw
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
		clearKazumiProgressHint(id)
	}()
	select {
	case kazumiDownloadSlots <- struct{}{}:
		defer func() { <-kazumiDownloadSlots }()
	case <-ctx.Done():
		markKazumiTaskCanceled(id)
		return
	}
	if ctx.Err() != nil {
		markKazumiTaskCanceled(id)
		return
	}
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
	// Keep the previous copy of this episode until the new download is fully
	// written. Retrying a completed task can therefore no longer leave the user
	// with a truncated file when the source is temporarily unavailable.
	cleanupKazumiStagingFiles(dir, opts.Episode)
	restoreOrphanedKazumiBackups(dir, opts.Episode)
	backups := backupKazumiEpisodeFiles(dir, opts.Episode)
	completed := false
	defer func() {
		cleanupKazumiStagingFiles(dir, opts.Episode)
		if completed {
			removeKazumiBackups(backups)
			return
		}
		restoreKazumiBackups(backups)
		if len(backups) > 0 {
			if info, err := os.Stat(backups[0].original); err == nil {
				task.Size = info.Size()
				task.TotalSize = info.Size()
				task.Progress = 100
				_ = db.Conn.SaveKazumiDownload(task)
			}
		}
	}()

	progressCtx, stopProgress := context.WithCancel(ctx)
	defer stopProgress()
	clearKazumiProgressHint(id)
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
	runErr := downloadKazumiInput(ctx, id, dir, opts.Episode, opts.Source, input, media, referer, ua, origin)
	if runErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			task.Status = db.KazumiDownloadCanceled
			task.Error = "canceled"
		} else {
			task.Status = db.KazumiDownloadFailed
			task.Error = runErr.Error()
		}
		task.Speed = 0
		task.Size = 0
		task.TotalSize = 0
		task.Progress = 0
		_ = db.Conn.SaveKazumiDownload(task)
		return
	}

	path := findKazumiEpisodeFile(dir, opts.Episode)
	if path == "" {
		task.Status = db.KazumiDownloadFailed
		task.Error = "download finished but no output file was found"
		task.Speed = 0
		task.Size = 0
		task.TotalSize = 0
		task.Progress = 0
		_ = db.Conn.SaveKazumiDownload(task)
		return
	}
	if playable := ensureBrowserPlayableKazumiFile(ctx, dir, opts.Episode, path); playable != "" {
		path = playable
	}
	completed = true
	info, err := os.Stat(path)
	if err == nil {
		task.Size = info.Size()
		task.TotalSize = info.Size()
	}
	task.Progress = 100
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

func downloadKazumiInput(ctx context.Context, id int64, dir string, episode int, pageURL, input string, media bool, referer, ua, origin string) error {
	var attempts int
	var failures []string
	attempt := func(label string, fn func(base string) error) bool {
		if err := ctx.Err(); err != nil {
			return false
		}
		attempts++
		base := filepath.Join(dir, fmt.Sprintf(".uani-%d-%d", episode, attempts))
		cleanupKazumiStagingBase(base)
		clearKazumiProgressHint(id)
		if err := fn(base); err != nil {
			cleanupKazumiStagingBase(base)
			failures = append(failures, label+": "+strings.TrimSpace(err.Error()))
			return false
		}
		if _, err := finalizeKazumiStaging(dir, episode, base); err != nil {
			cleanupKazumiStagingBase(base)
			failures = append(failures, label+": "+strings.TrimSpace(err.Error()))
			return false
		}
		return true
	}

	lowerInput := strings.ToLower(input)
	isHLS := strings.Contains(lowerInput, ".m3u8")
	if hasExecutable("yt-dlp") {
		if attempt("yt-dlp", func(base string) error {
			return downloadWithYtDlp(ctx, id, base, input, referer, ua, origin)
		}) {
			return nil
		}
	}
	if media && hasExecutable("ffmpeg") {
		if attempt("ffmpeg", func(base string) error {
			return downloadWithFFmpeg(ctx, base, input, referer, ua, origin, false)
		}) {
			return nil
		}
		// Some streams cannot be copied into MP4 (for example unusual codecs or
		// broken timestamps). Transcoding is slower but turns those failures into
		// a successful download instead of a permanently failed task.
		if ctx.Err() == nil && attempt("ffmpeg-transcode", func(base string) error {
			return downloadWithFFmpeg(ctx, base, input, referer, ua, origin, true)
		}) {
			return nil
		}
	}
	if media && isHLS {
		if attempt("hls", func(base string) error {
			_, err := kazumi.DownloadHLS(ctx, input, base, map[string]string{
				"User-Agent": ua,
				"Referer":    referer,
				"Origin":     origin,
			})
			return err
		}) {
			return nil
		}
	}
	if (media || kazumi.IsMediaURL(input)) && !isHLS {
		if attempt("http", func(base string) error {
			return downloadDirect(ctx, id, base, input, referer, ua, origin)
		}) {
			return nil
		}
	}
	if hasExecutable("yt-dlp") && input != pageURL {
		if attempt("yt-dlp-page", func(base string) error {
			return downloadWithYtDlp(ctx, id, base, pageURL, referer, ua, origin)
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

// kazumiStartupTimeout is how long yt-dlp may spend resolving a page before it
// must have written some bytes. Without it a site that keeps refusing the
// initial request leaves the task at 0% for a very long time because yt-dlp is
// still retrying, and the ffmpeg fallback is never reached.
const kazumiStartupTimeout = 45 * time.Second

func downloadWithYtDlp(ctx context.Context, id int64, outputBase, input, referer, ua, origin string) error {
	args := []string{
		"--no-warnings", "--no-playlist", "--newline",
		"--progress", "--progress-template",
		"download:uani-progress %(progress.downloaded_bytes)s %(progress.total_bytes)s %(progress.speed)s %(progress.fragment_index)s %(progress.fragment_count)s",
		"--retries", "5", "--fragment-retries", "10", "--retry-sleep", "1",
		"--socket-timeout", "15", "--concurrent-fragments", "4",
		"--no-write-info-json", "--no-write-playlist-metafiles",
		"--user-agent", ua,
		"-o", outputBase + ".%(ext)s",
	}
	if referer != "" {
		args = append(args, "--referer", referer)
	}
	if origin != "" {
		args = append(args, "--add-header", "Origin: "+origin)
	}
	args = append(args, input)
	return runYtDlpCommand(ctx, id, outputBase, args...)
}

// runYtDlpCommand streams yt-dlp progress into the task record and stops the
// process when it never starts writing. It returns the tail of stderr so the
// task error stays readable instead of being a bare "exit status 1".
func runYtDlpCommand(ctx context.Context, id int64, outputBase string, args ...string) error {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(child, "yt-dlp", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &tailBuffer{max: 4000}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	var stalled atomic.Bool
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if kazumiStagingHasBytes(outputBase) {
					return
				}
				if time.Since(start) > kazumiStartupTimeout {
					stalled.Store(true)
					cancel()
					return
				}
			}
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		parseYtDlpProgress(id, scanner.Text())
	}
	waitErr := cmd.Wait()
	close(done)
	if stalled.Load() {
		return fmt.Errorf("yt-dlp 长时间没有开始下载，已切换到备用下载方式")
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = waitErr.Error()
		}
		if len(message) > 800 {
			message = message[len(message)-800:]
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func parseYtDlpProgress(id int64, line string) {
	const prefix = "uani-progress "
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, prefix) {
		return
	}
	fields := strings.Fields(strings.TrimPrefix(line, prefix))
	if len(fields) < 5 {
		return
	}
	downloaded := progressInt(fields[0])
	total := progressInt(fields[1])
	speed := progressInt(fields[2])
	progress := 0
	if total > 0 && downloaded > 0 {
		progress = int(downloaded * 100 / total)
	}
	// HLS does not report a reliable total size, but its fragment counters are
	// exact, so use them for the percentage in that case.
	fragmentIndex, fragmentCount := progressInt(fields[3]), progressInt(fields[4])
	if fragmentCount > 0 && fragmentIndex > 0 {
		if byFragment := int(fragmentIndex * 100 / fragmentCount); byFragment > progress {
			progress = byFragment
		}
	}
	if progress > 99 {
		progress = 99
	}
	setKazumiProgressHint(id, downloaded, total, speed, progress)
}

func progressInt(raw string) int64 {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, ".0"))
	if raw == "" || raw == "NA" || raw == "None" || raw == "none" || raw == "-" {
		return 0
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return 0
	}
	return int64(value)
}

// kazumiStagingHasBytes reports whether a download attempt has produced real
// output. yt-dlp writes a tiny .ytdl resume file before any media arrives, so
// that file must not count as progress.
func kazumiStagingHasBytes(base string) bool {
	matches, _ := filepath.Glob(base + ".*")
	for _, match := range matches {
		lower := strings.ToLower(match)
		if strings.HasSuffix(lower, ".ytdl") || strings.HasSuffix(lower, ".json") {
			continue
		}
		if info, err := os.Stat(match); err == nil && !info.IsDir() && info.Size() > 0 {
			return true
		}
	}
	return false
}

// tailBuffer keeps only the last max bytes written to it. It is used for
// subprocess stderr so a noisy failure still leaves a useful message.
type tailBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Write(p)
	if b.buf.Len() > b.max {
		trimmed := b.buf.Bytes()[b.buf.Len()-b.max:]
		kept := append([]byte(nil), trimmed...)
		b.buf.Reset()
		b.buf.Write(kept)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func downloadWithFFmpeg(ctx context.Context, outputBase, input, referer, ua, origin string, transcode bool) error {
	output := outputBase + ".mp4"
	headerValue := "User-Agent: " + ua + "\r\n"
	if referer != "" {
		headerValue += "Referer: " + referer + "\r\n"
	}
	if origin != "" {
		headerValue += "Origin: " + origin + "\r\n"
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-rw_timeout", "30000000",
		"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_at_eof", "1", "-reconnect_delay_max", "5",
		"-user_agent", ua,
		"-referer", referer,
		"-headers", headerValue,
		"-i", input,
	}
	if transcode {
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-c:a", "aac", "-b:a", "192k")
	} else {
		args = append(args, "-c", "copy")
	}
	args = append(args, "-movflags", "+faststart", output)
	return runCommand(ctx, "ffmpeg", args...)
}

func originOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

var (
	mediaClientOnce sync.Once
	mediaClient     *http.Client
)

// mediaHTTPClient keeps cookies/proxy settings from the shared client but
// avoids its 120-second whole-request timeout. A large movie can legitimately
// take longer than two minutes, and aborting it used to leave a failed task.
func mediaHTTPClient() *http.Client {
	mediaClientOnce.Do(func() {
		transport := httpx.NewTransport()
		transport.ResponseHeaderTimeout = 60 * time.Second
		mediaClient = &http.Client{
			Transport: transport,
			Jar:       httpx.Client().Jar,
			Timeout:   30 * time.Minute,
		}
	})
	return mediaClient
}

func downloadDirect(ctx context.Context, id int64, outputBase, input, referer, ua, origin string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * 500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		err := downloadDirectOnce(ctx, id, outputBase, input, referer, ua, origin)
		if err == nil {
			return nil
		}
		lastErr = err
		cleanupKazumiStagingBase(outputBase)
	}
	return lastErr
}

func downloadDirectOnce(ctx context.Context, id int64, outputBase, input, referer, ua, origin string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, input, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", ua)
	request.Header.Set("Accept", "*/*")
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	resp, err := mediaHTTPClient().Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	ext := mediaExtensionFromURL(input)
	if ext == "" {
		ext = extensionForContentType(contentType)
	}
	if ext == "" {
		if strings.Contains(contentType, "html") || strings.Contains(contentType, "json") {
			return fmt.Errorf("服务器返回了网页而不是视频（Content-Type: %s）", resp.Header.Get("Content-Type"))
		}
		ext = ".mp4"
	}
	output := outputBase + ext
	if resp.ContentLength > 0 {
		setKazumiProgressHint(id, 0, resp.ContentLength, 0, 0)
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(output)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(output)
		return closeErr
	}
	if written == 0 {
		_ = os.Remove(output)
		return fmt.Errorf("服务器返回了空文件")
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		_ = os.Remove(output)
		return fmt.Errorf("下载不完整：%d/%d 字节", written, resp.ContentLength)
	}
	return nil
}

func mediaExtensionFromURL(raw string) string {
	if parsed, err := url.Parse(raw); err == nil && parsed.Path != "" {
		ext := strings.ToLower(filepath.Ext(parsed.Path))
		if kazumiVideoExtension(ext) {
			return ext
		}
	}
	ext := strings.ToLower(filepath.Ext(raw))
	if kazumiVideoExtension(ext) {
		return ext
	}
	return ""
}

func extensionForContentType(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch {
	case strings.Contains(contentType, "video/mp4"):
		return ".mp4"
	case strings.Contains(contentType, "video/webm"):
		return ".webm"
	case strings.Contains(contentType, "video/mp2t"), strings.Contains(contentType, "video/mpegts"):
		return ".ts"
	case strings.Contains(contentType, "video/quicktime"):
		return ".mov"
	case strings.Contains(contentType, "matroska"):
		return ".mkv"
	case strings.Contains(contentType, "video/x-flv"), strings.Contains(contentType, "video/flv"):
		return ".flv"
	}
	return ""
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
	task.Size = 0
	task.TotalSize = 0
	task.Progress = 0
	_ = db.Conn.SaveKazumiDownload(task)
}

func markKazumiTaskCanceled(id int64) {
	if db.Conn == nil {
		return
	}
	task, err := db.Conn.GetKazumiDownload(id)
	if err != nil {
		return
	}
	if task.Status != db.KazumiDownloadPending && task.Status != db.KazumiDownloadRunning {
		return
	}
	task.Status = db.KazumiDownloadCanceled
	task.Error = "canceled"
	task.Speed = 0
	task.Size = 0
	task.TotalSize = 0
	task.Progress = 0
	_ = db.Conn.SaveKazumiDownload(task)
}

// kazumiProgressHint carries byte counts reported by a downloader. yt-dlp can
// report the exact total size, which lets the UI show a real percentage instead
// of only "downloaded so far".
type kazumiProgressHint struct {
	downloaded int64
	total      int64
	speed      int64
	progress   int
	updated    time.Time
}

var (
	kazumiProgressHintsMu sync.Mutex
	kazumiProgressHints   = map[int64]*kazumiProgressHint{}
)

func setKazumiProgressHint(id, downloaded, total, speed int64, progress int) {
	if id <= 0 {
		return
	}
	kazumiProgressHintsMu.Lock()
	defer kazumiProgressHintsMu.Unlock()
	hint := kazumiProgressHints[id]
	if hint == nil {
		hint = &kazumiProgressHint{}
		kazumiProgressHints[id] = hint
	}
	// Totals fluctuate while yt-dlp discovers fragments; keep the largest so
	// the percentage never jumps backwards. Downloaded bytes can reset when a
	// second format starts, so the disk total is used as the lower bound.
	if downloaded > hint.downloaded {
		hint.downloaded = downloaded
	}
	if total > hint.total {
		hint.total = total
	}
	if speed > 0 {
		hint.speed = speed
	}
	if progress > hint.progress {
		hint.progress = progress
	}
	hint.updated = time.Now()
}

func getKazumiProgressHint(id int64) (downloaded, total, speed int64, progress int, fresh bool) {
	kazumiProgressHintsMu.Lock()
	defer kazumiProgressHintsMu.Unlock()
	hint := kazumiProgressHints[id]
	if hint == nil {
		return 0, 0, 0, 0, false
	}
	return hint.downloaded, hint.total, hint.speed, hint.progress, time.Since(hint.updated) < 4*time.Second
}

func clearKazumiProgressHint(id int64) {
	kazumiProgressHintsMu.Lock()
	delete(kazumiProgressHints, id)
	kazumiProgressHintsMu.Unlock()
}

func trackKazumiProgress(ctx context.Context, id int64, dir string, episode int) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	prefix := strconv.Itoa(episode) + "."
	stagingPrefix := fmt.Sprintf(".uani-%d-", episode)
	var last, lastTotal, lastSpeed int64 = -1, -1, -1
	var lastProgress = -1
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
			var diskTotal int64
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() || (!strings.HasPrefix(name, prefix) && !strings.HasPrefix(name, stagingPrefix)) {
					continue
				}
				if info, err := entry.Info(); err == nil {
					diskTotal += info.Size()
				}
			}
			now := time.Now()
			downloaded, total, reportedSpeed, reportedProgress, fresh := getKazumiProgressHint(id)
			size := diskTotal
			if downloaded > size {
				size = downloaded
			}
			speed := int64(0)
			sizeChanged := size != last
			if sizeChanged {
				if elapsed := now.Sub(lastTime).Seconds(); elapsed > 0 && size > last && last >= 0 {
					speed = int64(float64(size-last) / elapsed)
				}
				last = size
				lastTime = now
			}
			if fresh && reportedSpeed > 0 {
				speed = reportedSpeed
			} else if !sizeChanged && now.Sub(lastTime) <= 3*time.Second {
				// Hold the last speed for a few seconds so a bursty writer does
				// not make the UI alternate between a real speed and 0 B/s.
				speed = lastSpeed
			}
			progress := reportedProgress
			if progress <= 0 && total > 0 && size > 0 {
				progress = int(size * 100 / total)
			}
			if progress > 99 {
				progress = 99
			}
			if !sizeChanged && total == lastTotal && speed == lastSpeed && progress == lastProgress {
				continue
			}
			lastTotal = total
			lastSpeed = speed
			lastProgress = progress
			item, err := db.Conn.GetKazumiDownload(id)
			if err != nil || item.Status != db.KazumiDownloadRunning {
				continue
			}
			item.Size = size
			if total > 0 {
				item.TotalSize = total
			}
			item.Progress = progress
			item.Speed = speed
			_ = db.Conn.SaveKazumiDownload(item)
		}
	}
}

// cleanupKazumiStagingBase removes the temporary outputs of one download
// attempt. Staging names never collide with the final <episode>.<ext> files.
func cleanupKazumiStagingBase(base string) {
	matches, _ := filepath.Glob(base + ".*")
	for _, match := range matches {
		_ = os.Remove(match)
	}
	_ = os.Remove(base)
}

// cleanupKazumiStagingFiles removes every staged artifact for an episode,
// leaving the final files and retry backups alone.
func cleanupKazumiStagingFiles(dir string, episode int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefixes := []string{
		fmt.Sprintf(".uani-%d-", episode),
		fmt.Sprintf(".uani-remux-%d-", episode),
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				_ = os.Remove(filepath.Join(dir, name))
				break
			}
		}
	}
}

// restoreOrphanedKazumiBackups recovers files left behind when the process was
// interrupted between moving the old episode out of the way and writing the new
// one. Without this the old video could disappear from the Files page forever.
func restoreOrphanedKazumiBackups(dir string, episode int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := fmt.Sprintf(".uani-backup-%d-", episode)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		backup := filepath.Join(dir, entry.Name())
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if !kazumiVideoExtension(ext) {
			_ = os.Remove(backup)
			continue
		}
		target := filepath.Join(dir, strconv.Itoa(episode)+ext)
		if _, err := os.Stat(target); err == nil {
			// A complete file already exists (the retry finished before the
			// crash), so the older backup is no longer needed.
			_ = os.Remove(backup)
			continue
		}
		if err := os.Rename(backup, target); err != nil {
			_ = os.Remove(backup)
		}
	}
}

// finalizeKazumiStaging promotes the largest playable staged file to the
// canonical <episode>.<ext> name. Only this completed file is ever published,
// so a failed attempt can never leave a partial video for playback.
func finalizeKazumiStaging(dir string, episode int, base string) (string, error) {
	matches, _ := filepath.Glob(base + ".*")
	best := ""
	bestSize := int64(0)
	for _, match := range matches {
		if !kazumiVideoExtension(strings.ToLower(filepath.Ext(match))) {
			continue
		}
		info, err := os.Stat(match)
		if err != nil || info.IsDir() || info.Size() <= 0 {
			continue
		}
		if info.Size() > bestSize {
			best = match
			bestSize = info.Size()
		}
	}
	if best == "" {
		return "", fmt.Errorf("下载完成但没有生成有效的视频文件")
	}
	ext := strings.ToLower(filepath.Ext(best))
	target := filepath.Join(dir, strconv.Itoa(episode)+ext)
	cleanupKazumiEpisodeFiles(dir, episode, "")
	_ = os.Remove(target)
	if err := os.Rename(best, target); err != nil {
		return "", err
	}
	cleanupKazumiStagingBase(base)
	return target, nil
}

type kazumiEpisodeBackup struct {
	original string
	backup   string
}

// backupKazumiEpisodeFiles moves already-downloaded files for this episode out
// of the way before a retry. They are restored if every new attempt fails.
func backupKazumiEpisodeFiles(dir string, episode int) []kazumiEpisodeBackup {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	prefix := strconv.Itoa(episode) + "."
	var backups []kazumiEpisodeBackup
	for index, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !kazumiVideoExtension(ext) {
			continue
		}
		original := filepath.Join(dir, name)
		info, err := entry.Info()
		if err != nil || info.Size() <= 0 {
			continue
		}
		backup := filepath.Join(dir, fmt.Sprintf(".uani-backup-%d-%d-%d%s", episode, time.Now().UnixNano(), index, ext))
		if err := os.Rename(original, backup); err != nil {
			continue
		}
		backups = append(backups, kazumiEpisodeBackup{original: original, backup: backup})
	}
	return backups
}

func restoreKazumiBackups(backups []kazumiEpisodeBackup) {
	for _, item := range backups {
		_ = os.Remove(item.original)
		if err := os.Rename(item.backup, item.original); err != nil {
			_ = os.Remove(item.backup)
		}
	}
}

func removeKazumiBackups(backups []kazumiEpisodeBackup) {
	for _, item := range backups {
		_ = os.Remove(item.backup)
	}
}

// ensureBrowserPlayableKazumiFile remuxes TS/MKV/FLV/AVI/RMVB output to MP4
// when ffmpeg is available. Browsers cannot reliably play those containers, so
// this keeps the Kazumi Files play button working for downloads produced by the
// built-in HLS downloader or by yt-dlp's default container choice.
func ensureBrowserPlayableKazumiFile(ctx context.Context, dir string, episode int, path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4", ".webm", ".m4v", ".mov":
		return path
	}
	if !hasExecutable("ffmpeg") {
		return path
	}
	base := filepath.Join(dir, fmt.Sprintf(".uani-remux-%d-%d", episode, time.Now().UnixNano()))
	output := base + ".mp4"
	cleanupKazumiStagingBase(base)
	defer cleanupKazumiStagingBase(base)

	remux := func(reencode bool) error {
		args := []string{
			"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-i", path,
			"-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn",
		}
		if reencode {
			args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-c:a", "aac", "-b:a", "192k")
		} else {
			args = append(args, "-c", "copy")
		}
		args = append(args, "-movflags", "+faststart", output)
		return runCommand(ctx, "ffmpeg", args...)
	}
	if err := remux(false); err != nil {
		_ = os.Remove(output)
		if ctx.Err() != nil {
			return path
		}
		if err := remux(true); err != nil {
			_ = os.Remove(output)
			return path
		}
	}
	if info, err := os.Stat(output); err != nil || info.Size() <= 0 {
		return path
	}
	target := filepath.Join(dir, strconv.Itoa(episode)+".mp4")
	if target == path {
		return path
	}
	_ = os.Remove(target)
	if err := os.Rename(output, target); err != nil {
		return path
	}
	_ = os.Remove(path)
	return target
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
	tasks, err := db.Conn.AllKazumiDownloads()
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
