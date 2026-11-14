package update

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
	"github.com/HanamiSakiP/u-ani/internal/logger"
	"github.com/HanamiSakiP/u-ani/internal/season"
	"github.com/HanamiSakiP/u-ani/internal/source"
	"github.com/HanamiSakiP/u-ani/internal/utils"
)

func oldVersionFile() string {
	return filepath.Join(config.UANIHome, "old")
}

// CreateDir creates all uani data directories.
func CreateDir() error {
	paths := []string{
		config.UANIHome,
		config.Cfg.SavePath,
		config.Cfg.CachePath,
		config.Cfg.TmpPath,
		config.Cfg.LogPath,
		config.Cfg.ScriptPath,
		config.Cfg.ToolsPath,
		config.Cfg.FrontStaticPath,
		filepath.Join(config.UANIHome, "source"),
		filepath.Join(config.Cfg.CachePath, "covers"),
		filepath.Join(config.Cfg.CachePath, "kazumi"),
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
		logger.Success("%s created successfully", p)
	}
	return nil
}

// InitDB creates the SQLite schema.
func InitDB() error {
	_, err := db.Open(config.Cfg.DBPath)
	return err
}

const cronVBS = "set ws=wscript.createobject(\"wscript.shell\")\nws.run \"powershell.exe $env:UANI_LOG='error'; uani cal --update --cover ; uani update\", 0\n"

// InstallCrontab registers the periodic update jobs.
func InstallCrontab() {
	logger.Info("Installing crontab job")
	if config.IsWindows {
		_ = os.WriteFile(filepath.Join(config.UANIHome, "cron.vbs"), []byte(cronVBS), 0o644)
		_ = exec.Command("schtasks", "/Create", "/SC", "HOURLY", "/TN", "uani updater",
			"/TR", filepath.Join(config.UANIHome, "cron.vbs"), "/IT", "/F").Run()
		return
	}
	out, _ := exec.Command("crontab", "-l").Output()
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		if strings.Contains(line, "uani update") || strings.Contains(line, "uani cal") ||
			strings.Contains(line, "bgmi update") || strings.Contains(line, "bgmi cal") {
			continue
		}
		lines = append(lines, line)
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "uani"
	}
	lines = append(lines,
		fmt.Sprintf("10 */2 * * * LC_ALL=en_US.UTF-8 %s update", exe),
		fmt.Sprintf("0 */12 * * * LC_ALL=en_US.UTF-8 %s cal --update --cover", exe),
	)
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := cmd.Run(); err != nil {
		logger.Warning("Failed to install crontab job: %v", err)
	}
}

func getTableColumns(dbPath, table string) []string {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil
	}
	defer conn.Close()
	rows, err := conn.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err == nil {
			cols = append(cols, name)
		}
	}
	return cols
}

func hasColumn(cols []string, name string) bool {
	for _, c := range cols {
		if c == name {
			return true
		}
	}
	return false
}

func execSQL(dbPath, query string, args ...any) error {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Exec(query, args...)
	return err
}

func fixJSONColumn(dbPath, table, pkCol, col string) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return
	}
	defer conn.Close()
	rows, err := conn.Query(fmt.Sprintf("SELECT %s, %s FROM %s", pkCol, col, table))
	if err != nil {
		return
	}
	type pair struct {
		pk  any
		val any
	}
	var items []pair
	for rows.Next() {
		var pk, val any
		if err := rows.Scan(&pk, &val); err == nil {
			items = append(items, pair{pk, val})
		}
	}
	rows.Close()
	for _, it := range items {
		s, _ := it.val.(string)
		switch {
		case s == "":
			_, _ = conn.Exec(fmt.Sprintf("UPDATE %s SET %s = '[]' WHERE %s = ?", table, col, pkCol), it.pk)
		case !strings.HasPrefix(s, "["):
			parts := []string{}
			for _, p := range strings.Split(s, ",") {
				if p = strings.TrimSpace(p); p != "" {
					parts = append(parts, fmt.Sprintf("%q", p))
				}
			}
			_, _ = conn.Exec(fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", table, col, pkCol), "["+strings.Join(parts, ", ")+"]", it.pk)
		}
	}
}

func migrateFromV4(dbPath string) {
	logger.Info("Migrating database from v4 to v5...")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		logger.Error("open database: %v", err)
		return
	}
	defer conn.Close()

	bangumiCols := getTableColumns(dbPath, "bangumi")
	if hasColumn(bangumiCols, "keyword") || hasColumn(bangumiCols, "update_time") {
		logger.Info("Migrating bangumi table: recreate with v5 schema")
		idExpr := "CAST(id AS TEXT)"
		if hasColumn(bangumiCols, "keyword") {
			idExpr = "COALESCE(NULLIF(keyword, ''), CAST(id AS TEXT))"
		}
		updateDayCol := "update_time"
		if hasColumn(bangumiCols, "update_day") {
			updateDayCol = "update_day"
		}
		_, _ = conn.Exec(`CREATE TABLE IF NOT EXISTS bangumi_new (
			id TEXT PRIMARY KEY NOT NULL, name TEXT NOT NULL UNIQUE,
			subtitle_group TEXT NOT NULL DEFAULT '[]', update_day CHAR(5) NOT NULL DEFAULT 'Unknown',
			cover TEXT NOT NULL DEFAULT '', status INTEGER NOT NULL DEFAULT 0)`)
		_, _ = conn.Exec(fmt.Sprintf(`INSERT OR IGNORE INTO bangumi_new (id, name, subtitle_group, update_day, cover, status)
			SELECT %s, name, CASE WHEN subtitle_group = '' OR subtitle_group IS NULL THEN '[]' ELSE subtitle_group END,
			COALESCE(%s, 'Unknown'), cover, status FROM bangumi`, idExpr, updateDayCol))
		_, _ = conn.Exec("DROP TABLE bangumi")
		_, _ = conn.Exec("ALTER TABLE bangumi_new RENAME TO bangumi")
	}

	followedCols := getTableColumns(dbPath, "followed")
	if hasColumn(followedCols, "episode") && !hasColumn(followedCols, "episodes") {
		logger.Info("Migrating followed table: episode (scalar) -> episodes (set), merge filter")
		_, _ = conn.Exec(`CREATE TABLE IF NOT EXISTS followed_new (
			bangumi_name TEXT PRIMARY KEY NOT NULL, episodes TEXT NOT NULL DEFAULT '[]',
			status INTEGER NOT NULL DEFAULT 1, updated_time INTEGER NOT NULL DEFAULT 0,
			subtitle TEXT NOT NULL DEFAULT '[]', "include" TEXT NOT NULL DEFAULT '[]',
			"exclude" TEXT NOT NULL DEFAULT '[]', regex TEXT NOT NULL DEFAULT '',
			season INTEGER NOT NULL DEFAULT 1, episode_offset INTEGER NOT NULL DEFAULT 0,
			display_name TEXT NOT NULL DEFAULT '', is_script INTEGER NOT NULL DEFAULT 0)`)
		rows, err := conn.Query("SELECT bangumi_name, episode, status, updated_time FROM followed")
		type row struct {
			name    string
			episode int
			status  int
			updated int64
		}
		var data []row
		if err == nil {
			for rows.Next() {
				var r row
				if err := rows.Scan(&r.name, &r.episode, &r.status, &r.updated); err == nil {
					data = append(data, r)
				}
			}
			rows.Close()
		}
		for _, r := range data {
			episodes := "[]"
			if r.episode > 0 {
				parts := make([]string, 0, r.episode)
				for i := 1; i <= r.episode; i++ {
					parts = append(parts, fmt.Sprint(i))
				}
				episodes = "[" + strings.Join(parts, ", ") + "]"
			}
			var subtitle, include, exclude, regex string
			_ = conn.QueryRow("SELECT subtitle, \"include\", \"exclude\", regex FROM filter WHERE bangumi_name = ?", r.name).Scan(&subtitle, &include, &exclude, &regex)
			normalize := func(s string) string {
				if s == "" || strings.HasPrefix(s, "[") {
					if s == "" {
						return "[]"
					}
					return s
				}
				return strings.ReplaceAll(strings.ReplaceAll(s, ",", `", "`), "", "")
			}
			_ = normalize
			toJSON := func(s string) string {
				if s == "" {
					return "[]"
				}
				if strings.HasPrefix(s, "[") {
					return s
				}
				parts := []string{}
				for _, p := range strings.Split(s, ",") {
					if p = strings.TrimSpace(p); p != "" {
						parts = append(parts, fmt.Sprintf("%q", p))
					}
				}
				return "[" + strings.Join(parts, ", ") + "]"
			}
			_, _ = conn.Exec(`INSERT OR IGNORE INTO followed_new
				(bangumi_name, episodes, status, updated_time, subtitle, "include", "exclude", regex)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				r.name, episodes, r.status, r.updated, toJSON(subtitle), toJSON(include), toJSON(exclude), regex)
		}
		_, _ = conn.Exec("DROP TABLE followed")
		_, _ = conn.Exec("ALTER TABLE followed_new RENAME TO followed")
	}

	downloadCols := getTableColumns(dbPath, "download")
	if hasColumn(downloadCols, "name") && !hasColumn(downloadCols, "bangumi_name") {
		logger.Info("Migrating download table: rename name -> bangumi_name, add task_id")
		_, _ = conn.Exec(`CREATE TABLE IF NOT EXISTS download_new (
			id INTEGER PRIMARY KEY NOT NULL, bangumi_name TEXT NOT NULL, title TEXT NOT NULL,
			episode INTEGER NOT NULL, download TEXT NOT NULL, status INTEGER NOT NULL, task_id TEXT)`)
		_, _ = conn.Exec(`INSERT INTO download_new (id, bangumi_name, title, episode, download, status)
			SELECT id, name, title, episode, download, status FROM download`)
		_, _ = conn.Exec("DROP TABLE download")
		_, _ = conn.Exec("ALTER TABLE download_new RENAME TO download")
	} else if !hasColumn(downloadCols, "task_id") {
		_, _ = conn.Exec("ALTER TABLE download ADD COLUMN task_id TEXT")
	}

	_, _ = conn.Exec("DROP TABLE IF EXISTS filter")
	fixJSONColumn(dbPath, "bangumi", "id", "subtitle_group")
	fixJSONColumn(dbPath, "followed", "bangumi_name", "subtitle")
	fixJSONColumn(dbPath, "followed", "bangumi_name", `"include"`)
	fixJSONColumn(dbPath, "followed", "bangumi_name", `"exclude"`)
	_, _ = conn.Exec("UPDATE followed SET episodes = '[]' WHERE episodes = '' OR episodes IS NULL")

	rows, err := conn.Query("SELECT bangumi_name FROM followed")
	if err == nil {
		var names []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err == nil {
				names = append(names, n)
			}
		}
		rows.Close()
		for _, n := range names {
			if s := season.ParseSeason(n); s != 1 {
				_, _ = conn.Exec("UPDATE followed SET season = ? WHERE bangumi_name = ?", s, n)
			}
		}
	}

	oldCover := filepath.Join(config.Cfg.SavePath, "cover")
	newCover := utils.CoverCacheDir()
	if info, err := os.Stat(oldCover); err == nil && info.IsDir() {
		if _, err := os.Stat(newCover); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(newCover), 0o755); err == nil {
				if err := os.Rename(oldCover, newCover); err == nil {
					logger.Info("Renamed cover directory: %s -> %s", oldCover, newCover)
				}
			}
		}
	}
	logger.Info("Migration from v4 to v5 completed successfully!")

	logger.Info("Refreshing bangumi metadata from data source...")
	if src, err := source.New(); err == nil {
		if _, err := source.Fetch(src, false); err == nil {
			logger.Info("Bangumi IDs refreshed successfully.")
		} else {
			logger.Warning("Failed to refresh bangumi IDs (can fix later with `uani cal --update`): %v", err)
		}
	}
}

func needsV4Migration(dbPath string) bool {
	if _, err := os.Stat(dbPath); err != nil {
		return false
	}
	cols := getTableColumns(dbPath, "bangumi")
	if hasColumn(cols, "keyword") || hasColumn(cols, "update_time") {
		return true
	}
	followedCols := getTableColumns(dbPath, "followed")
	return hasColumn(followedCols, "episode") && !hasColumn(followedCols, "episodes")
}

type version struct {
	major, minor, patch int
	pre                 string
}

func parseVersion(s string) version {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	var v version
	rest := s
	if i := strings.IndexAny(rest, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"); i >= 0 {
		v.pre = rest[i:]
		rest = rest[:i]
	}
	parts := strings.Split(rest, ".")
	if len(parts) > 0 {
		fmt.Sscanf(parts[0], "%d", &v.major)
	}
	if len(parts) > 1 {
		fmt.Sscanf(parts[1], "%d", &v.minor)
	}
	if len(parts) > 2 {
		fmt.Sscanf(parts[2], "%d", &v.patch)
	}
	return v
}

func (v version) less(other version) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	if v.minor != other.minor {
		return v.minor < other.minor
	}
	if v.patch != other.patch {
		return v.patch < other.patch
	}
	rank := func(pre string) int {
		if pre == "" {
			return 3
		}
		switch {
		case strings.HasPrefix(pre, "rc"):
			return 2
		case strings.HasPrefix(pre, "b"):
			return 1
		default:
			return 0
		}
	}
	return rank(v.pre) < rank(other.pre)
}

func refreshLegacyBangumiIDs(dbPath string) {
	src, err := source.New()
	if err != nil {
		return
	}
	bangumiList, err := src.FetchBangumiCalendar()
	if err != nil || len(bangumiList) == 0 {
		logger.Warning("Failed to check legacy bangumi IDs (fix with `uani cal --update`): %v", err)
		return
	}
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return
	}
	rows, err := conn.Query("SELECT name, id FROM bangumi")
	current := map[string]string{}
	if err == nil {
		for rows.Next() {
			var name, id string
			if err := rows.Scan(&name, &id); err == nil {
				current[name] = id
			}
		}
		rows.Close()
	}
	conn.Close()
	mismatch := false
	for _, b := range bangumiList {
		if id, ok := current[b.Name]; ok && id != b.ID {
			mismatch = true
			break
		}
	}
	if !mismatch {
		return
	}
	logger.Warning("Bangumi source IDs differ from data source, refreshing...")
	if _, err := source.Fetch(src, false); err == nil {
		logger.Info("Bangumi IDs refreshed successfully.")
	} else {
		logger.Warning("Failed to refresh bangumi IDs (fix with `uani cal --update`): %v", err)
	}
}

// UpdateDatabase applies schema migrations and records the current version.
func UpdateDatabase() {
	dbPath := config.Cfg.DBPath
	if _, err := os.Stat(oldVersionFile()); err != nil {
		if needsV4Migration(dbPath) {
			logger.Warning("Detected v4 database (no version file), performing migration to v5...")
			migrateFromV4(dbPath)
		}
		_ = os.WriteFile(oldVersionFile(), []byte(config.Version), 0o644)
		return
	}

	data, _ := os.ReadFile(oldVersionFile())
	previous := parseVersion(string(data))
	migrated := false
	if previous.less(parseVersion("5.0.0a0")) || needsV4Migration(dbPath) {
		logger.Warning("Detected v4 database, performing migration to v5...")
		migrateFromV4(dbPath)
		migrated = true
	}

	if previous.less(parseVersion("5.0.0a4")) {
		if !hasColumn(getTableColumns(dbPath, "followed"), "season") {
			_ = execSQL(dbPath, "ALTER TABLE followed ADD COLUMN season INTEGER NOT NULL DEFAULT 1")
		}
		if !hasColumn(getTableColumns(dbPath, "followed"), "episode_offset") {
			_ = execSQL(dbPath, "ALTER TABLE followed ADD COLUMN episode_offset INTEGER NOT NULL DEFAULT 0")
		}
		if !hasColumn(getTableColumns(dbPath, "followed"), "display_name") {
			_ = execSQL(dbPath, "ALTER TABLE followed ADD COLUMN display_name TEXT NOT NULL DEFAULT ''")
		}
		if !hasColumn(getTableColumns(dbPath, "download"), "task_id") {
			_ = execSQL(dbPath, "ALTER TABLE download ADD COLUMN task_id TEXT")
		}
	}

	scriptsCols := getTableColumns(dbPath, "scripts")
	if len(scriptsCols) > 0 {
		if !hasColumn(scriptsCols, "episodes") {
			_ = execSQL(dbPath, "ALTER TABLE scripts ADD COLUMN episodes TEXT NOT NULL DEFAULT '[]'")
		}
		if !hasColumn(scriptsCols, "updated_time") {
			_ = execSQL(dbPath, "ALTER TABLE scripts ADD COLUMN updated_time INTEGER NOT NULL DEFAULT 0")
		}
		if !hasColumn(scriptsCols, "update_day") {
			_ = execSQL(dbPath, "ALTER TABLE scripts ADD COLUMN update_day TEXT NOT NULL DEFAULT 'Unknown'")
		}
		if !hasColumn(scriptsCols, "cover") {
			_ = execSQL(dbPath, "ALTER TABLE scripts ADD COLUMN cover TEXT NOT NULL DEFAULT ''")
		}
	}

	if previous.less(parseVersion("5.0.0a4")) && !migrated {
		refreshLegacyBangumiIDs(dbPath)
	}
	_ = os.WriteFile(oldVersionFile(), []byte(config.Version), 0o644)
}

// Now is a helper for tests.
func Now() int64 { return time.Now().Unix() }
