// Package script loads and runs the user's Python bgmi-compatible scripts.
package script

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/model"
	"github.com/HanamiSakiP/u-ani/internal/source"
)

//go:embed runner.py
var runnerSource string

type rawModel struct {
	BangumiName  *string  `json:"bangumi_name"`
	Cover        string   `json:"cover"`
	UpdateTime   string   `json:"update_time"`
	Source       *string  `json:"source"`
	BangumiID    *string  `json:"bangumi_id"`
	SubtitleList []string `json:"subtitle_list"`
	MaxPage      int      `json:"max_page"`
	DueDate      *string  `json:"due_date"`
}

type rawItem struct {
	File    string            `json:"file"`
	OK      bool              `json:"ok"`
	Skipped bool              `json:"skipped"`
	Error   string            `json:"error"`
	Model   *rawModel         `json:"model"`
	URLs    map[string]string `json:"urls"`
}

// Model is the metadata of one script entry.
type Model struct {
	File         string
	Name         string
	Cover        string
	UpdateDay    string
	Source       string
	BangumiID    string
	SubtitleList []string
	MaxPage      int
	Status       int
	Episode      int
	UpdatedTime  int64
}

// Result is the episodes produced by a script run.
type Result struct {
	Script   *db.Scripts
	Episodes []model.Episode
}

func pythonCommand() string {
	if p := os.Getenv("UANI_PYTHON"); p != "" {
		return p
	}
	if p := os.Getenv("BGMI_PYTHON"); p != "" {
		return p
	}
	for _, candidate := range []string{"python3", "python"} {
		if p, err := exec.LookPath(candidate); err == nil {
			return p
		}
	}
	return ""
}

func scriptFiles() []string {
	files, _ := filepath.Glob(filepath.Join(config.Cfg.ScriptPath, "*.py"))
	sort.Strings(files)
	return files
}

func runPython(mode string) ([]rawItem, error) {
	if len(scriptFiles()) == 0 {
		return nil, nil
	}
	python := pythonCommand()
	if python == "" {
		return nil, fmt.Errorf("python interpreter not found, cannot load uani scripts")
	}
	cfgPayload := map[string]any{
		"max_path":     config.Cfg.MaxPath,
		"save_path":    config.Cfg.SavePath,
		"script_path":  config.Cfg.ScriptPath,
		"db_path":      config.Cfg.DBPath,
		"data_source":  string(config.Cfg.DataSource),
		"bgmi_path":    config.UANIHome,
		"proxy":        config.Cfg.Proxy,
		"front_static": config.Cfg.FrontStaticPath,
	}
	cfgJSON, _ := json.Marshal(cfgPayload)

	cmd := exec.Command(python, "-c", runnerSource, mode, config.Cfg.ScriptPath)
	cmd.Env = append(os.Environ(), "UANI_SCRIPT_CONFIG="+string(cfgJSON))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var items []rawItem
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &items); err != nil {
		return nil, fmt.Errorf("failed to parse script runner output: %w", err)
	}
	for _, item := range items {
		if !item.OK && item.Error != "" {
			if item.Skipped {
				logger.Info("Skip load %s because it has reach its due_date", item.File)
			} else {
				logger.Warning("Load script %s failed, ignored", item.File)
				if os.Getenv("DEBUG_SCRIPT") != "" {
					logger.Error("%s: %s", item.File, item.Error)
				}
			}
		}
	}
	return items, nil
}

func ensureDB() error {
	if db.Conn != nil {
		return nil
	}
	_, err := db.New()
	return err
}

// GetModels returns metadata for all loaded scripts, ensuring their DB rows.
func GetModels() []Model {
	if err := ensureDB(); err != nil {
		logger.Warning("%v", err)
		return nil
	}
	_ = db.Conn.RefreshScriptsLifecycle(now())
	items, err := runPython("meta")
	if err != nil {
		logger.Warning("Load scripts failed: %v", err)
		return nil
	}
	var out []Model
	for _, item := range items {
		if !item.OK || item.Model == nil || item.Model.BangumiName == nil || *item.Model.BangumiName == "" {
			continue
		}
		name := *item.Model.BangumiName
		row, err := db.Conn.EnsureScript(name)
		if err != nil {
			logger.Warning("Failed to load script %s: %v", name, err)
			continue
		}
		sourceName := ""
		if item.Model.Source != nil {
			sourceName = *item.Model.Source
		}
		bangumiID := ""
		if item.Model.BangumiID != nil {
			bangumiID = *item.Model.BangumiID
		}
		out = append(out, Model{
			File:         item.File,
			Name:         name,
			Cover:        item.Model.Cover,
			UpdateDay:    item.Model.UpdateTime,
			Source:       sourceName,
			BangumiID:    bangumiID,
			SubtitleList: item.Model.SubtitleList,
			MaxPage:      item.Model.MaxPage,
			Status:       row.Status,
			Episode:      row.Episode(),
			UpdatedTime:  row.UpdatedTime,
		})
	}
	return out
}

// GetDownloadCover returns the covers declared by scripts.
func GetDownloadCover() []string {
	var covers []string
	for _, m := range GetModels() {
		if m.Cover != "" {
			covers = append(covers, m.Cover)
		}
	}
	return covers
}

// Run executes get_download_url for every loaded script.
func Run() []Result {
	if err := ensureDB(); err != nil {
		logger.Warning("%v", err)
		return nil
	}
	_ = db.Conn.RefreshScriptsLifecycle(now())
	items, err := runPython("run")
	if err != nil {
		logger.Warning("Load scripts failed: %v", err)
		return nil
	}
	var out []Result
	for _, item := range items {
		if !item.OK || item.Model == nil || item.Model.BangumiName == nil || *item.Model.BangumiName == "" {
			continue
		}
		name := *item.Model.BangumiName
		logger.Info("fetching %s ...", name)
		row, err := db.Conn.EnsureScript(name)
		if err != nil {
			logger.Warning("Failed to load script %s: %v", name, err)
			continue
		}

		urls := map[int]string{}
		for key, value := range item.URLs {
			n, err := strconv.Atoi(key)
			if err != nil {
				continue
			}
			urls[n] = value
		}

		if len(urls) == 0 && item.Model.Source != nil && *item.Model.Source != "" && item.Model.BangumiID != nil {
			src, err := source.NewByName(*item.Model.Source)
			if err != nil {
				logger.Error("Failed to fetch script items\nerror: %v", err)
				continue
			}
			episodes, err := src.FetchEpisodeOfBangumi(*item.Model.BangumiID, item.Model.MaxPage, item.Model.SubtitleList)
			if err != nil {
				logger.Error("Failed to fetch script items\nerror: %v", err)
				continue
			}
			for _, e := range episodes {
				urls[e.Episode] = e.Download
			}
		}

		if len(urls) == 0 {
			logger.Info("Got nothing, quit script %s.", name)
			continue
		}
		keys := make([]int, 0, len(urls))
		for ep := range urls {
			keys = append(keys, ep)
		}
		sort.Ints(keys)
		episodes := make([]model.Episode, 0, len(keys))
		for _, ep := range keys {
			episodes = append(episodes, model.Episode{
				Name:     name,
				Title:    fmt.Sprintf("[%s][%d]", name, ep),
				Episode:  ep,
				Download: urls[ep],
			})
		}
		out = append(out, Result{Script: row, Episodes: episodes})
	}
	return out
}

// MarkSeen updates a script row with the episodes seen but not downloaded.
func MarkSeen(script *db.Scripts, episodes []model.Episode, at int64) error {
	for _, e := range episodes {
		script.Episodes[e.Episode] = true
	}
	script.UpdatedTime = at
	return db.Conn.SaveScript(script)
}

// SaveScript persists a script row.
func SaveScript(script *db.Scripts) error {
	return db.Conn.SaveScript(script)
}

func now() int64 {
	return time.Now().Unix()
}
