package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/model"
)

const (
	BangumiStatusUpdating = 0
	BangumiStatusEnd      = 1

	FollowedStatusDeleted  = 0
	FollowedStatusFollowed = 1
	FollowedStatusUpdated  = 2
	FollowedStatusEnd      = 3

	DownloadStatusNotDownload = 0
	DownloadStatusDownloading = 1
	DownloadStatusDownloaded  = 2

	KazumiDownloadPending  = 0
	KazumiDownloadRunning  = 1
	KazumiDownloadDone     = 2
	KazumiDownloadFailed   = 3
	KazumiDownloadCanceled = 4
)

type DB struct {
	*sql.DB
}

type Bangumi struct {
	ID            string
	Name          string
	SubtitleGroup []string
	UpdateDay     string
	Cover         string
	Status        int
}

type Followed struct {
	BangumiName   string
	Episodes      map[int]bool
	Status        int
	UpdatedTime   int64
	Subtitle      []string
	Include       []string
	Exclude       []string
	Regex         string
	Season        int
	EpisodeOffset int
	DisplayName   string
	IsScript      bool
}

func (f Followed) Episode() int {
	max := 0
	for ep := range f.Episodes {
		if ep > max {
			max = ep
		}
	}
	return max
}

type Download struct {
	ID          int64
	BangumiName string
	Title       string
	Episode     int
	Download    string
	Status      int
	TaskID      *string
}

// KazumiDownload records a streaming episode downloaded from a Kazumi rule.
type KazumiDownload struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	BangumiID   int    `json:"bangumi_id"`
	Episode     int    `json:"episode"`
	Title       string `json:"title"`
	Rule        string `json:"rule"`
	Source      string `json:"source"`
	MediaURL    string `json:"media_url"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	Size        int64  `json:"size"`
	Speed       int64  `json:"speed"`
	Error       string `json:"error"`
	CreatedTime int64  `json:"created_time"`
	UpdatedTime int64  `json:"updated_time"`
	// DisplayName and Cover are derived at read time; they are not stored in
	// the database but let the Web UI render a richer task list.
	DisplayName string `json:"display_name,omitempty"`
	Cover       string `json:"cover,omitempty"`
}

type Subtitle struct {
	ID   string
	Name string
}

type Scripts struct {
	BangumiName string
	Episodes    map[int]bool
	Status      int
	UpdatedTime int64
	UpdateDay   string
	Cover       string
}

// ManualBangumi records a subscription created from an external metadata
// provider (currently bgm.tv) and the aliases used to search torrent sites.
type ManualBangumi struct {
	BangumiName string
	Provider    string
	ProviderID  string
	SearchNames []string
	ForeignURL  string
	UpdatedTime int64
}

func (s Scripts) Episode() int {
	max := 0
	for ep := range s.Episodes {
		if ep > max {
			max = ep
		}
	}
	return max
}

type FollowedBangumi struct {
	Followed Followed
	Bangumi  Bangumi
}

type WeeklyBangumi struct {
	ID             string
	Name           string
	SubtitleGroup  []string
	UpdateDay      string
	Cover          string
	Status         int
	FollowedStatus *int
	Episode        *int
}

var Conn *DB

func Open(path string) (*DB, error) {
	if path == "" {
		path = config.Cfg.DBPath
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	_, _ = sqlDB.Exec("PRAGMA busy_timeout = 5000")
	db := &DB{DB: sqlDB}
	if err := db.Init(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	Conn = db
	return db, nil
}

func New() (*DB, error) {
	return Open(config.Cfg.DBPath)
}

func (d *DB) Init() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS bangumi (
			id TEXT NOT NULL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			subtitle_group TEXT NOT NULL DEFAULT '[]',
			update_day CHAR(5) NOT NULL DEFAULT 'Unknown',
			cover TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS followed (
			bangumi_name TEXT NOT NULL PRIMARY KEY,
			episodes TEXT NOT NULL DEFAULT '[]',
			status INTEGER NOT NULL DEFAULT 1,
			updated_time INTEGER NOT NULL DEFAULT 0,
			subtitle TEXT NOT NULL DEFAULT '[]',
			"include" TEXT NOT NULL DEFAULT '[]',
			"exclude" TEXT NOT NULL DEFAULT '[]',
			regex TEXT NOT NULL DEFAULT '',
			season INTEGER NOT NULL DEFAULT 1,
			episode_offset INTEGER NOT NULL DEFAULT 0,
			display_name TEXT NOT NULL DEFAULT '',
			is_script INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS download (
			id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
			bangumi_name TEXT NOT NULL,
			title TEXT NOT NULL,
			episode INTEGER NOT NULL,
			download TEXT NOT NULL,
			status INTEGER NOT NULL,
			task_id TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS subtitle (
			id TEXT NOT NULL PRIMARY KEY,
			name TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS scripts (
			bangumi_name TEXT NOT NULL PRIMARY KEY,
			episodes TEXT NOT NULL DEFAULT '[]',
			status INTEGER NOT NULL,
			updated_time INTEGER NOT NULL DEFAULT 0,
			update_day TEXT NOT NULL DEFAULT 'Unknown',
			cover TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS manual_bangumi (
			bangumi_name TEXT NOT NULL PRIMARY KEY,
			provider TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			search_names TEXT NOT NULL DEFAULT '[]',
			foreign_url TEXT NOT NULL DEFAULT '',
			updated_time INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS kazumi_download (
			id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
			name TEXT NOT NULL,
			bangumi_id INTEGER NOT NULL DEFAULT 0,
			episode INTEGER NOT NULL,
			title TEXT NOT NULL,
			rule TEXT NOT NULL,
			source TEXT NOT NULL,
			media_url TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			status INTEGER NOT NULL DEFAULT 0,
			size INTEGER NOT NULL DEFAULT 0,
			speed INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			created_time INTEGER NOT NULL DEFAULT 0,
			updated_time INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS bangumi_link (
			bangumi_name TEXT NOT NULL PRIMARY KEY,
			subject_id INTEGER NOT NULL,
			updated_time INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS bangumi_cache (
			subject_id INTEGER NOT NULL PRIMARY KEY,
			overview TEXT NOT NULL DEFAULT '',
			characters TEXT NOT NULL DEFAULT '',
			staff TEXT NOT NULL DEFAULT '',
			relations TEXT NOT NULL DEFAULT '',
			comments TEXT NOT NULL DEFAULT '',
			updated_time INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS bangumi_collection (
			subject_id INTEGER NOT NULL PRIMARY KEY,
			type INTEGER NOT NULL DEFAULT 0,
			updated_time INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS custom_alias (
			bangumi_name TEXT NOT NULL,
			alias TEXT NOT NULL,
			updated_time INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bangumi_name, alias)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := d.Exec(stmt); err != nil {
			return err
		}
	}
	// Add columns introduced by v5 when opening an older database.
	_ = d.ensureColumn("followed", "season", "INTEGER NOT NULL DEFAULT 1")
	_ = d.ensureColumn("followed", "episode_offset", "INTEGER NOT NULL DEFAULT 0")
	_ = d.ensureColumn("followed", "display_name", "TEXT NOT NULL DEFAULT ''")
	_ = d.ensureColumn("download", "task_id", "TEXT")
	_ = d.ensureColumn("kazumi_download", "bangumi_id", "INTEGER NOT NULL DEFAULT 0")
	_ = d.ensureColumn("kazumi_download", "speed", "INTEGER NOT NULL DEFAULT 0")
	_ = d.ensureColumn("scripts", "episodes", "TEXT NOT NULL DEFAULT '[]'")
	_ = d.ensureColumn("scripts", "updated_time", "INTEGER NOT NULL DEFAULT 0")
	_ = d.ensureColumn("scripts", "update_day", "TEXT NOT NULL DEFAULT 'Unknown'")
	_ = d.ensureColumn("scripts", "cover", "TEXT NOT NULL DEFAULT ''")
	_ = d.ResetKazumiDownloads()
	return nil
}

func (d *DB) ensureColumn(table, column, def string) error {
	rows, err := d.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	_, err = d.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
	return err
}

func decodeIntSet(raw string) map[int]bool {
	out := map[int]bool{}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return out
	}
	var ints []int
	if err := json.Unmarshal([]byte(raw), &ints); err == nil {
		for _, n := range ints {
			out[n] = true
		}
		return out
	}
	// v4 stored comma-separated values.
	for _, p := range strings.Split(raw, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out[n] = true
		}
	}
	return out
}

func decodeStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out
	}
	for _, p := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func encodeIntSet(set map[int]bool) string {
	ints := make([]int, 0, len(set))
	for n := range set {
		ints = append(ints, n)
	}
	sort.Ints(ints)
	data, _ := json.Marshal(ints)
	return string(data)
}

func encodeStringList(list []string) string {
	if list == nil {
		list = []string{}
	}
	data, _ := json.Marshal(list)
	return string(data)
}

func scanBangumi(scanner interface{ Scan(...any) error }) (Bangumi, error) {
	var b Bangumi
	var subtitle string
	err := scanner.Scan(&b.ID, &b.Name, &subtitle, &b.UpdateDay, &b.Cover, &b.Status)
	b.SubtitleGroup = decodeStringList(subtitle)
	return b, err
}

const bangumiColumns = "id, name, subtitle_group, update_day, cover, status"

func (d *DB) GetBangumiByName(name string) (*Bangumi, error) {
	b, err := scanBangumi(d.QueryRow("SELECT "+bangumiColumns+" FROM bangumi WHERE name = ? LIMIT 1", name))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &b, nil
}

func (d *DB) GetBangumiByID(id string) (*Bangumi, error) {
	b, err := scanBangumi(d.QueryRow("SELECT "+bangumiColumns+" FROM bangumi WHERE id = ? LIMIT 1", id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &b, nil
}

func (d *DB) GetBangumiByNameContains(name string) (*Bangumi, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNotFound
	}
	b, err := scanBangumi(d.QueryRow("SELECT "+bangumiColumns+" FROM bangumi WHERE instr(name, ?) > 0 LIMIT 1", name))
	if err == nil {
		return &b, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Case-insensitive contains.
	b, err = scanBangumi(d.QueryRow("SELECT "+bangumiColumns+" FROM bangumi WHERE instr(lower(name), lower(?)) > 0 LIMIT 1", name))
	if err == nil {
		return &b, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Fuzzy/normalized scan for similar names.
	all, err := d.AllBangumi()
	if err != nil {
		return nil, err
	}
	target := normalizeBangumiMatchName(name)
	if target == "" {
		return nil, ErrNotFound
	}
	var best *Bangumi
	bestScore := 0
	for i := range all {
		candidate := &all[i]
		norm := normalizeBangumiMatchName(candidate.Name)
		if norm == "" {
			continue
		}
		score := 0
		switch {
		case norm == target:
			score = 100
		case strings.Contains(norm, target):
			score = 80 + len(target)*2 - (len(norm) - len(target))
		case strings.Contains(target, norm):
			score = 70 + len(norm)*2 - (len(target) - len(norm))
		}
		if score <= 0 {
			continue
		}
		if candidate.Status == BangumiStatusUpdating {
			score += 3
		}
		if score > bestScore {
			bestScore = score
			best = candidate
		}
	}
	if best != nil && bestScore >= 60 {
		return best, nil
	}
	return nil, ErrNotFound
}

func normalizeBangumiMatchName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"（", "(", "）", ")", "【", "[", "】", "]", "！", "!", "？", "?", "：", ":", "～", "~",
		"・", "", "·", "", " ", "", "\t", "", "\n", "", "-", "", "_", "", "/", "", "／", "",
	)
	s = replacer.Replace(s)
	s = strings.TrimSpace(s)
	return s
}

func (d *DB) AllBangumi() ([]Bangumi, error) {
	rows, err := d.Query("SELECT " + bangumiColumns + " FROM bangumi")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bangumi
	for rows.Next() {
		b, err := scanBangumi(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetBangumiCover updates only the cover URL of an existing bangumi.
func (d *DB) SetBangumiCover(name, cover string) error {
	_, err := d.Exec("UPDATE bangumi SET cover = ? WHERE name = ?", cover, name)
	return err
}

func (d *DB) HasUpdating() (bool, error) {
	var n int
	err := d.QueryRow("SELECT COUNT(*) FROM bangumi WHERE status = ?", BangumiStatusUpdating).Scan(&n)
	return n > 0, err
}

func (d *DB) SaveBangumi(data model.WebsiteBangumi) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existingID string
	err = tx.QueryRow("SELECT id FROM bangumi WHERE id = ? LIMIT 1", data.ID).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows {
		var byName string
		err = tx.QueryRow("SELECT id FROM bangumi WHERE name = ? LIMIT 1", data.Name).Scan(&byName)
		if err == nil {
			existingID = byName
		} else if err != sql.ErrNoRows {
			return err
		}
	}

	subtitleIDs := make([]string, 0, len(data.SubtitleGroup))
	for _, s := range data.SubtitleGroup {
		subtitleIDs = append(subtitleIDs, s.ID)
	}
	sort.Strings(subtitleIDs)

	if existingID != "" {
		if _, err := tx.Exec(
			`UPDATE bangumi SET id = ?, cover = ?, update_day = ?, status = ?, subtitle_group = CASE WHEN ? = '[]' THEN subtitle_group ELSE ? END WHERE id = ?`,
			data.ID, data.Cover, data.UpdateDay, BangumiStatusUpdating, encodeStringList(subtitleIDs), encodeStringList(subtitleIDs), existingID,
		); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(
			`INSERT INTO bangumi (id, name, subtitle_group, update_day, cover, status) VALUES (?, ?, ?, ?, ?, ?)`,
			data.ID, data.Name, encodeStringList(subtitleIDs), data.UpdateDay, data.Cover, BangumiStatusUpdating,
		); err != nil {
			return err
		}
	}

	for _, s := range data.SubtitleGroup {
		if _, err := tx.Exec(
			`INSERT INTO subtitle (id, name) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET name = excluded.name`,
			s.ID, s.Name,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) MarkAllEnd(now int64) error {
	// Recently updated subscriptions and manual subscriptions (for example
	// from bgm.tv) are not part of the source calendar, so keep their status.
	rows, err := d.Query(`SELECT bangumi_name FROM followed WHERE updated_time > ?
		UNION SELECT bangumi_name FROM manual_bangumi`, now-2*7*24*3600)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		names = append(names, n)
	}
	rows.Close()
	if len(names) == 0 {
		_, err = d.Exec("UPDATE bangumi SET status = ?", BangumiStatusEnd)
		return err
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(names)), ",")
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	_, err = d.Exec("UPDATE bangumi SET status = ? WHERE name NOT IN ("+placeholders+")", append([]any{BangumiStatusEnd}, args...)...)
	return err
}

func beforeToday(timestamp, now int64) bool {
	if timestamp == 0 {
		return true
	}
	updated := time.Unix(timestamp, 0)
	current := time.Unix(now, 0)
	uy, ud := updated.Year(), updated.YearDay()
	cy, cd := current.Year(), current.YearDay()
	return uy < cy || (uy == cy && ud < cd)
}

func (d *DB) RefreshLifecycle(now int64) error {
	rows, err := d.Query("SELECT bangumi_name, updated_time FROM followed WHERE status = ?", FollowedStatusUpdated)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var n string
		var ts int64
		if err := rows.Scan(&n, &ts); err != nil {
			rows.Close()
			return err
		}
		if beforeToday(ts, now) {
			names = append(names, n)
		}
	}
	rows.Close()
	for _, n := range names {
		if _, err := d.Exec("UPDATE followed SET status = ? WHERE bangumi_name = ?", FollowedStatusFollowed, n); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) GetUpdatingBangumi(status *int) (map[string][]WeeklyBangumi, error) {
	if err := d.RefreshLifecycle(time.Now().Unix()); err != nil {
		return nil, err
	}
	query := `SELECT b.id, b.name, b.subtitle_group, b.update_day, b.cover, b.status, f.status, f.episodes
		FROM bangumi b LEFT JOIN followed f ON b.name = f.bangumi_name
		WHERE b.status = ?`
	args := []any{BangumiStatusUpdating}
	if status != nil {
		query += " AND f.status = ?"
		args = append(args, *status)
	}
	query += " ORDER BY b.rowid"
	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]WeeklyBangumi{}
	for rows.Next() {
		var item WeeklyBangumi
		var subtitle string
		var fstatus sql.NullInt64
		var episodes sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &subtitle, &item.UpdateDay, &item.Cover, &item.Status, &fstatus, &episodes); err != nil {
			return nil, err
		}
		item.SubtitleGroup = decodeStringList(subtitle)
		if fstatus.Valid {
			s := int(fstatus.Int64)
			item.FollowedStatus = &s
		}
		if episodes.Valid {
			set := decodeIntSet(episodes.String)
			if len(set) > 0 {
				max := 0
				for ep := range set {
					if ep > max {
						max = ep
					}
				}
				item.Episode = &max
			}
		}
		key := strings.ToLower(item.UpdateDay)
		out[key] = append(out[key], item)
	}
	return out, rows.Err()
}

func scanFollowed(scanner interface{ Scan(...any) error }) (Followed, error) {
	var f Followed
	var episodes, subtitle, include, exclude string
	var isScript int
	err := scanner.Scan(&f.BangumiName, &episodes, &f.Status, &f.UpdatedTime, &subtitle, &include, &exclude, &f.Regex, &f.Season, &f.EpisodeOffset, &f.DisplayName, &isScript)
	f.Episodes = decodeIntSet(episodes)
	f.Subtitle = decodeStringList(subtitle)
	f.Include = decodeStringList(include)
	f.Exclude = decodeStringList(exclude)
	f.IsScript = isScript != 0
	return f, err
}

const followedColumns = `bangumi_name, episodes, status, updated_time, subtitle, "include", "exclude", regex, season, episode_offset, display_name, is_script`

func (d *DB) GetFollowed(name string) (*Followed, error) {
	f, err := scanFollowed(d.QueryRow("SELECT "+followedColumns+" FROM followed WHERE bangumi_name = ? LIMIT 1", name))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &f, nil
}

func (d *DB) GetFollowedNotDeleted(name string) (*Followed, error) {
	f, err := scanFollowed(d.QueryRow("SELECT "+followedColumns+" FROM followed WHERE bangumi_name = ? AND status != ? LIMIT 1", name, FollowedStatusDeleted))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &f, nil
}

func (d *DB) SaveFollowed(f *Followed) error {
	if f.Season == 0 {
		f.Season = 1
	}
	_, err := d.Exec(
		`INSERT INTO followed (`+followedColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(bangumi_name) DO UPDATE SET episodes=excluded.episodes, status=excluded.status,
		 updated_time=excluded.updated_time, subtitle=excluded.subtitle, "include"=excluded."include",
		 "exclude"=excluded."exclude", regex=excluded.regex, season=excluded.season,
		 episode_offset=excluded.episode_offset, display_name=excluded.display_name, is_script=excluded.is_script`,
		f.BangumiName, encodeIntSet(f.Episodes), f.Status, f.UpdatedTime, encodeStringList(f.Subtitle),
		encodeStringList(f.Include), encodeStringList(f.Exclude), f.Regex, f.Season, f.EpisodeOffset, f.DisplayName, boolInt(f.IsScript),
	)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (d *DB) AllFollowed() ([]Followed, error) {
	rows, err := d.Query("SELECT " + followedColumns + " FROM followed")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Followed
	for rows.Next() {
		f, err := scanFollowed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (d *DB) AllFollowedBangumi(bangumiStatus int) ([]FollowedBangumi, error) {
	if err := d.RefreshLifecycle(time.Now().Unix()); err != nil {
		return nil, err
	}
	rows, err := d.Query(
		`SELECT f.`+strings.ReplaceAll(followedColumns, ", ", ", f.")+`, b.`+strings.ReplaceAll(bangumiColumns, ", ", ", b.")+`
		 FROM followed f JOIN bangumi b ON f.bangumi_name = b.name
		 WHERE f.status != ? AND b.status = ? ORDER BY f.updated_time DESC`,
		FollowedStatusDeleted, bangumiStatus,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FollowedBangumi
	for rows.Next() {
		var f Followed
		var b Bangumi
		var episodes, subtitle, include, exclude, bSubtitle string
		var isScript int
		if err := rows.Scan(
			&f.BangumiName, &episodes, &f.Status, &f.UpdatedTime, &subtitle, &include, &exclude, &f.Regex, &f.Season, &f.EpisodeOffset, &f.DisplayName, &isScript,
			&b.ID, &b.Name, &bSubtitle, &b.UpdateDay, &b.Cover, &b.Status,
		); err != nil {
			return nil, err
		}
		f.Episodes = decodeIntSet(episodes)
		f.Subtitle = decodeStringList(subtitle)
		f.Include = decodeStringList(include)
		f.Exclude = decodeStringList(exclude)
		f.IsScript = isScript != 0
		b.SubtitleGroup = decodeStringList(bSubtitle)
		out = append(out, FollowedBangumi{Followed: f, Bangumi: b})
	}
	return out, rows.Err()
}

func (d *DB) DeleteAllFollowed() error {
	_, err := d.Exec("DELETE FROM followed")
	return err
}

func (d *DB) RecreateSourceTables() error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"manual_bangumi", "subtitle", "download", "followed", "bangumi"} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) RecreateScriptsTable() error {
	_, err := d.Exec("DELETE FROM scripts")
	return err
}

func scanDownload(scanner interface{ Scan(...any) error }) (Download, error) {
	var dl Download
	var task sql.NullString
	err := scanner.Scan(&dl.ID, &dl.BangumiName, &dl.Title, &dl.Episode, &dl.Download, &dl.Status, &task)
	if task.Valid {
		s := task.String
		dl.TaskID = &s
	}
	return dl, err
}

const downloadColumns = "id, bangumi_name, title, episode, download, status, task_id"

func (d *DB) GetDownload(bangumi, download string, episode int, title string) (*Download, error) {
	dl, err := scanDownload(d.QueryRow("SELECT "+downloadColumns+" FROM download WHERE bangumi_name=? AND download=? AND episode=? AND title=? LIMIT 1", bangumi, download, episode, title))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &dl, nil
}

func (d *DB) SaveDownload(dl *Download) error {
	if dl.ID == 0 {
		res, err := d.Exec(
			`INSERT INTO download (bangumi_name, title, episode, download, status, task_id) VALUES (?, ?, ?, ?, ?, ?)`,
			dl.BangumiName, dl.Title, dl.Episode, dl.Download, dl.Status, dl.TaskID,
		)
		if err != nil {
			return err
		}
		dl.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := d.Exec(
		`UPDATE download SET bangumi_name=?, title=?, episode=?, download=?, status=?, task_id=? WHERE id=?`,
		dl.BangumiName, dl.Title, dl.Episode, dl.Download, dl.Status, dl.TaskID, dl.ID,
	)
	return err
}

func (d *DB) GetDownloads(status *int) ([]Download, error) {
	query := "SELECT " + downloadColumns + " FROM download"
	var args []any
	if status != nil {
		query += " WHERE status = ?"
		args = append(args, *status)
	}
	query += " ORDER BY status"
	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Download
	for rows.Next() {
		dl, err := scanDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dl)
	}
	return out, rows.Err()
}

func (d *DB) MarkDownloadsDownloaded(name string, episodes []int) error {
	if len(episodes) == 0 {
		return nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(episodes)), ",")
	args := []any{DownloadStatusDownloaded, name}
	for _, ep := range episodes {
		args = append(args, ep)
	}
	_, err := d.Exec("UPDATE download SET status=?, task_id=NULL WHERE bangumi_name=? AND episode IN ("+placeholders+")", args...)
	return err
}

func (d *DB) MaxDownloadEpisode(name string) (int, error) {
	var max sql.NullInt64
	err := d.QueryRow("SELECT MAX(episode) FROM download WHERE bangumi_name=?", name).Scan(&max)
	if err != nil {
		return 0, err
	}
	if max.Valid {
		return int(max.Int64), nil
	}
	return 0, nil
}

func (d *DB) GetSubtitlesByIDs(ids []string) ([]Subtitle, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := d.Query("SELECT id, name FROM subtitle WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]string{}
	for rows.Next() {
		var s Subtitle
		if err := rows.Scan(&s.ID, &s.Name); err != nil {
			return nil, err
		}
		byID[s.ID] = s.Name
	}
	out := []Subtitle{}
	for _, id := range ids {
		if name, ok := byID[id]; ok {
			out = append(out, Subtitle{ID: id, Name: name})
		}
	}
	return out, rows.Err()
}

func (d *DB) GetSubtitlesByNames(names []string) ([]Subtitle, error) {
	if len(names) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(names)), ",")
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	rows, err := d.Query("SELECT id, name FROM subtitle WHERE name IN ("+placeholders+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subtitle
	for rows.Next() {
		var s Subtitle
		if err := rows.Scan(&s.ID, &s.Name); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) EnsureScript(name string) (*Scripts, error) {
	s, err := d.GetScript(name)
	if err == nil {
		return s, nil
	}
	if err != ErrNotFound {
		return nil, err
	}
	s = &Scripts{BangumiName: name, Episodes: map[int]bool{}, Status: FollowedStatusFollowed, UpdateDay: "Unknown"}
	if _, err := d.Exec("INSERT INTO scripts (bangumi_name, episodes, status, updated_time, update_day, cover) VALUES (?, ?, ?, 0, ?, '')",
		s.BangumiName, encodeIntSet(s.Episodes), s.Status, s.UpdateDay); err != nil {
		return nil, err
	}
	return s, nil
}

func scanScript(scanner interface{ Scan(...any) error }) (Scripts, error) {
	var s Scripts
	var episodes string
	err := scanner.Scan(&s.BangumiName, &episodes, &s.Status, &s.UpdatedTime, &s.UpdateDay, &s.Cover)
	s.Episodes = decodeIntSet(episodes)
	return s, err
}

const scriptColumns = "bangumi_name, episodes, status, updated_time, update_day, cover"

func (d *DB) GetScript(name string) (*Scripts, error) {
	s, err := scanScript(d.QueryRow("SELECT "+scriptColumns+" FROM scripts WHERE bangumi_name = ? LIMIT 1", name))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

func (d *DB) AllScripts() ([]Scripts, error) {
	rows, err := d.Query("SELECT " + scriptColumns + " FROM scripts")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Scripts
	for rows.Next() {
		s, err := scanScript(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) SaveScript(s *Scripts) error {
	_, err := d.Exec(
		`INSERT INTO scripts (`+scriptColumns+`) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(bangumi_name) DO UPDATE SET episodes=excluded.episodes, status=excluded.status,
		 updated_time=excluded.updated_time, update_day=excluded.update_day, cover=excluded.cover`,
		s.BangumiName, encodeIntSet(s.Episodes), s.Status, s.UpdatedTime, s.UpdateDay, s.Cover,
	)
	return err
}

func (d *DB) RefreshScriptsLifecycle(now int64) error {
	rows, err := d.Query("SELECT bangumi_name, updated_time FROM scripts WHERE status = ?", FollowedStatusUpdated)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var n string
		var ts int64
		if err := rows.Scan(&n, &ts); err != nil {
			rows.Close()
			return err
		}
		if beforeToday(ts, now) {
			names = append(names, n)
		}
	}
	rows.Close()
	for _, n := range names {
		if _, err := d.Exec("UPDATE scripts SET status = ? WHERE bangumi_name = ?", FollowedStatusFollowed, n); err != nil {
			return err
		}
	}
	return nil
}

const manualColumns = "bangumi_name, provider, provider_id, search_names, foreign_url, updated_time"

func scanManual(scanner interface{ Scan(...any) error }) (ManualBangumi, error) {
	var m ManualBangumi
	var searchNames string
	err := scanner.Scan(&m.BangumiName, &m.Provider, &m.ProviderID, &searchNames, &m.ForeignURL, &m.UpdatedTime)
	m.SearchNames = decodeStringList(searchNames)
	return m, err
}

// GetManualBangumi returns the external provider mapping for a subscription.
func (d *DB) GetManualBangumi(name string) (*ManualBangumi, error) {
	m, err := scanManual(d.QueryRow("SELECT "+manualColumns+" FROM manual_bangumi WHERE bangumi_name = ? LIMIT 1", name))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// GetManualBangumiByProviderID finds a subscription by its external provider
// identifier, e.g. the bgm.tv subject id.
func (d *DB) GetManualBangumiByProviderID(provider, providerID string) (*ManualBangumi, error) {
	m, err := scanManual(d.QueryRow("SELECT "+manualColumns+" FROM manual_bangumi WHERE provider = ? AND provider_id = ? LIMIT 1", provider, providerID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// SaveManualBangumi creates or updates an external provider mapping.
func (d *DB) SaveManualBangumi(m *ManualBangumi) error {
	_, err := d.Exec(
		`INSERT INTO manual_bangumi (`+manualColumns+`) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(bangumi_name) DO UPDATE SET provider=excluded.provider, provider_id=excluded.provider_id,
		 search_names=excluded.search_names, foreign_url=excluded.foreign_url, updated_time=excluded.updated_time`,
		m.BangumiName, m.Provider, m.ProviderID, encodeStringList(m.SearchNames), m.ForeignURL, m.UpdatedTime,
	)
	return err
}

// DeleteManualBangumi removes an external provider mapping.
func (d *DB) DeleteManualBangumi(name string) error {
	_, err := d.Exec("DELETE FROM manual_bangumi WHERE bangumi_name = ?", name)
	return err
}

// DeleteAllManualBangumi removes all external provider mappings.
func (d *DB) DeleteAllManualBangumi() error {
	_, err := d.Exec("DELETE FROM manual_bangumi")
	return err
}

const kazumiDownloadColumns = "id, name, bangumi_id, episode, title, rule, source, media_url, path, status, size, speed, error, created_time, updated_time"

func scanKazumiDownload(scanner interface{ Scan(...any) error }) (KazumiDownload, error) {
	var item KazumiDownload
	err := scanner.Scan(&item.ID, &item.Name, &item.BangumiID, &item.Episode, &item.Title, &item.Rule, &item.Source,
		&item.MediaURL, &item.Path, &item.Status, &item.Size, &item.Speed, &item.Error, &item.CreatedTime, &item.UpdatedTime)
	return item, err
}

// SaveKazumiDownload inserts or updates a Kazumi streaming download record.
func (d *DB) SaveKazumiDownload(item *KazumiDownload) error {
	if item.CreatedTime == 0 {
		item.CreatedTime = time.Now().Unix()
	}
	item.UpdatedTime = time.Now().Unix()
	if item.ID == 0 {
		res, err := d.Exec(
			`INSERT INTO kazumi_download (name, bangumi_id, episode, title, rule, source, media_url, path, status, size, speed, error, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.Name, item.BangumiID, item.Episode, item.Title, item.Rule, item.Source, item.MediaURL, item.Path,
			item.Status, item.Size, item.Speed, item.Error, item.CreatedTime, item.UpdatedTime,
		)
		if err != nil {
			return err
		}
		item.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := d.Exec(
		`UPDATE kazumi_download SET name=?, bangumi_id=?, episode=?, title=?, rule=?, source=?, media_url=?, path=?, status=?, size=?, speed=?, error=?, updated_time=? WHERE id=?`,
		item.Name, item.BangumiID, item.Episode, item.Title, item.Rule, item.Source, item.MediaURL, item.Path,
		item.Status, item.Size, item.Speed, item.Error, item.UpdatedTime, item.ID,
	)
	return err
}

// GetKazumiDownload returns one streaming download record.
func (d *DB) GetKazumiDownload(id int64) (*KazumiDownload, error) {
	item, err := scanKazumiDownload(d.QueryRow("SELECT "+kazumiDownloadColumns+" FROM kazumi_download WHERE id = ? LIMIT 1", id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &item, nil
}

// ListKazumiDownloads returns streaming download records, newest first.
func (d *DB) ListKazumiDownloads(limit int) ([]KazumiDownload, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.Query("SELECT "+kazumiDownloadColumns+" FROM kazumi_download ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KazumiDownload{}
	for rows.Next() {
		item, err := scanKazumiDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// AllKazumiDownloads returns every streaming download record without the
// page-sized limit used by the Web UI. Cache export and task enrichment need
// the complete library.
func (d *DB) AllKazumiDownloads() ([]KazumiDownload, error) {
	rows, err := d.Query("SELECT " + kazumiDownloadColumns + " FROM kazumi_download ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KazumiDownload{}
	for rows.Next() {
		item, err := scanKazumiDownload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeleteKazumiDownload removes a streaming download record.
func (d *DB) DeleteKazumiDownload(id int64) error {
	_, err := d.Exec("DELETE FROM kazumi_download WHERE id = ?", id)
	return err
}

// ResetKazumiDownloads marks tasks left running by a previous process as failed.
func (d *DB) ResetKazumiDownloads() error {
	_, err := d.Exec("UPDATE kazumi_download SET status = ?, error = ?, speed = 0, updated_time = ? WHERE status IN (?, ?)",
		KazumiDownloadFailed, "interrupted by restart", time.Now().Unix(), KazumiDownloadPending, KazumiDownloadRunning)
	return err
}

// BangumiCache stores locally cached Bangumi subject detail sections.
type BangumiCache struct {
	SubjectID   int64
	Overview    string
	Characters  string
	Staff       string
	Relations   string
	Comments    string
	UpdatedTime int64
}

const bangumiCacheColumns = "subject_id, overview, characters, staff, relations, comments, updated_time"

func scanBangumiCache(scanner interface{ Scan(...any) error }) (BangumiCache, error) {
	var item BangumiCache
	err := scanner.Scan(&item.SubjectID, &item.Overview, &item.Characters, &item.Staff, &item.Relations, &item.Comments, &item.UpdatedTime)
	return item, err
}

// GetBangumiCache returns the cached sections for a subject.
func (d *DB) GetBangumiCache(subjectID int64) (*BangumiCache, error) {
	item, err := scanBangumiCache(d.QueryRow("SELECT "+bangumiCacheColumns+" FROM bangumi_cache WHERE subject_id = ? LIMIT 1", subjectID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &item, nil
}

// ListBangumiCaches returns every cached subject detail row. It is used by
// cache import/export so complete metadata can be moved between instances.
func (d *DB) ListBangumiCaches() ([]BangumiCache, error) {
	rows, err := d.Query("SELECT " + bangumiCacheColumns + " FROM bangumi_cache ORDER BY subject_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BangumiCache{}
	for rows.Next() {
		item, err := scanBangumiCache(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SaveBangumiCache inserts or updates cached subject sections.
func (d *DB) SaveBangumiCache(item *BangumiCache) error {
	item.UpdatedTime = time.Now().Unix()
	_, err := d.Exec(
		`INSERT INTO bangumi_cache (`+bangumiCacheColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(subject_id) DO UPDATE SET overview=excluded.overview, characters=excluded.characters,
		 staff=excluded.staff, relations=excluded.relations, comments=excluded.comments, updated_time=excluded.updated_time`,
		item.SubjectID, item.Overview, item.Characters, item.Staff, item.Relations, item.Comments, item.UpdatedTime,
	)
	return err
}

// ClearBangumiCache removes all cached subject detail sections while keeping
// followed subscriptions, links, downloads and other user data intact.
func (d *DB) ClearBangumiCache() error {
	_, err := d.Exec("DELETE FROM bangumi_cache")
	return err
}

// GetBangumiLink returns a locally linked bgm.tv subject id for a subscription.
func (d *DB) GetBangumiLink(name string) (int, error) {
	var id int
	err := d.QueryRow("SELECT subject_id FROM bangumi_link WHERE bangumi_name = ? LIMIT 1", name).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return id, nil
}

// SaveBangumiLink stores or updates a subscription -> bgm.tv subject mapping.
func (d *DB) SaveBangumiLink(name string, subjectID int) error {
	_, err := d.Exec(
		`INSERT INTO bangumi_link (bangumi_name, subject_id, updated_time) VALUES (?, ?, ?)
		 ON CONFLICT(bangumi_name) DO UPDATE SET subject_id=excluded.subject_id, updated_time=excluded.updated_time`,
		name, subjectID, time.Now().Unix(),
	)
	return err
}

// GetBangumiCollection returns a cached Bangumi collection type.
func (d *DB) GetBangumiCollection(subjectID int) (int, error) {
	var value int
	err := d.QueryRow("SELECT type FROM bangumi_collection WHERE subject_id = ? LIMIT 1", subjectID).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return value, nil
}

// SaveBangumiCollection caches a Bangumi collection type.
func (d *DB) SaveBangumiCollection(subjectID, collectionType int) error {
	_, err := d.Exec(
		`INSERT INTO bangumi_collection (subject_id, type, updated_time) VALUES (?, ?, ?)
		 ON CONFLICT(subject_id) DO UPDATE SET type=excluded.type, updated_time=excluded.updated_time`,
		subjectID, collectionType, time.Now().Unix(),
	)
	return err
}

// DeleteBangumiCollection clears the cached Bangumi collection type.
func (d *DB) DeleteBangumiCollection(subjectID int) error {
	_, err := d.Exec(`DELETE FROM bangumi_collection WHERE subject_id = ?`, subjectID)
	return err
}

// HasDeletedFollowedBySubjectID reports whether subjectID is linked to a
// subscription that has already been unsubscribed.
func (d *DB) HasDeletedFollowedBySubjectID(subjectID int) bool {
	if subjectID <= 0 {
		return false
	}
	var exists int
	err := d.QueryRow(`SELECT 1 FROM followed f WHERE f.status = ? AND (
		EXISTS (SELECT 1 FROM bangumi_link l WHERE l.bangumi_name = f.bangumi_name AND l.subject_id = ?)
		OR EXISTS (SELECT 1 FROM manual_bangumi m WHERE m.bangumi_name = f.bangumi_name AND m.provider = 'bgmtv' AND CAST(m.provider_id AS INTEGER) = ?)
	) LIMIT 1`, FollowedStatusDeleted, subjectID, subjectID).Scan(&exists)
	return err == nil && exists == 1
}

// FindFollowedBySubjectID locates a subscription linked to a bgm.tv subject.
func (d *DB) FindFollowedBySubjectID(subjectID int) (*FollowedBangumi, error) {
	if subjectID <= 0 {
		return nil, ErrNotFound
	}
	var name string
	err := d.QueryRow(`SELECT f.bangumi_name FROM followed f WHERE f.status != ? AND f.bangumi_name IN (
			SELECT bangumi_name FROM bangumi_link WHERE subject_id = ?
			UNION
			SELECT bangumi_name FROM manual_bangumi WHERE provider = 'bgmtv' AND provider_id = ?
		) LIMIT 1`, FollowedStatusDeleted, subjectID, strconv.Itoa(subjectID)).Scan(&name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	followed, err := d.GetFollowedNotDeleted(name)
	if err != nil {
		return nil, err
	}
	bangumi, err := d.GetBangumiByName(name)
	if err != nil {
		return nil, err
	}
	return &FollowedBangumi{Followed: *followed, Bangumi: *bangumi}, nil
}

// GetSubjectIDByBangumiName returns the linked bgm.tv subject id for a local
// subscription, if one has been recorded.
func (d *DB) GetSubjectIDByBangumiName(name string) (int, error) {
	var id int
	err := d.QueryRow(`SELECT subject_id FROM bangumi_link WHERE bangumi_name = ?
		UNION ALL SELECT CAST(provider_id AS INTEGER) FROM manual_bangumi
			WHERE bangumi_name = ? AND provider = 'bgmtv' LIMIT 1`, name, name).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return id, nil
}

// ListCustomAliases returns user-defined aliases for a subscription.
func (d *DB) ListCustomAliases(name string) ([]string, error) {
	rows, err := d.Query("SELECT alias FROM custom_alias WHERE bangumi_name = ? ORDER BY alias", name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, err
		}
		out = append(out, alias)
	}
	return out, rows.Err()
}

// SaveCustomAlias adds a user-defined alias.
func (d *DB) SaveCustomAlias(name, alias string) error {
	_, err := d.Exec("INSERT INTO custom_alias (bangumi_name, alias, updated_time) VALUES (?, ?, ?) ON CONFLICT(bangumi_name, alias) DO UPDATE SET updated_time=excluded.updated_time",
		name, alias, time.Now().Unix())
	return err
}

// DeleteCustomAlias removes a user-defined alias.
func (d *DB) DeleteCustomAlias(name, alias string) error {
	_, err := d.Exec("DELETE FROM custom_alias WHERE bangumi_name = ? AND alias = ?", name, alias)
	return err
}

// KazumiEpisodesForName returns successfully downloaded Kazumi episode numbers.
func (d *DB) KazumiEpisodesForName(name string) ([]int, error) {
	return d.KazumiEpisodesForNames([]string{name}, 0)
}

// KazumiEpisodesForNames matches downloaded episodes by name or aliases.
func (d *DB) KazumiEpisodesForNames(names []string, subjectID int) ([]int, error) {
	seen := map[string]bool{}
	values := []string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			values = append(values, name)
		}
	}
	conditions := []string{}
	args := []any{KazumiDownloadDone}
	if len(values) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(values)), ",")
		conditions = append(conditions, "name IN ("+placeholders+")")
		for _, name := range values {
			args = append(args, name)
		}
	}
	if subjectID > 0 {
		conditions = append(conditions, "bangumi_id = ?")
		args = append(args, subjectID)
	}
	if len(conditions) == 0 {
		return []int{}, nil
	}
	query := "SELECT DISTINCT episode FROM kazumi_download WHERE status = ? AND path != '' AND (" + strings.Join(conditions, " OR ") + ") ORDER BY episode"
	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var episode int
		if err := rows.Scan(&episode); err != nil {
			return nil, err
		}
		out = append(out, episode)
	}
	return out, rows.Err()
}

// KazumiVideosForNames returns completed downloaded video files keyed by
// episode number. Later tasks win when the same episode exists more than once.
func (d *DB) KazumiVideosForNames(names []string, subjectID int) (map[int]string, error) {
	seen := map[string]bool{}
	values := []string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			values = append(values, name)
		}
	}
	conditions := []string{}
	args := []any{KazumiDownloadDone}
	if len(values) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(values)), ",")
		conditions = append(conditions, "name IN ("+placeholders+")")
		for _, name := range values {
			args = append(args, name)
		}
	}
	if subjectID > 0 {
		conditions = append(conditions, "bangumi_id = ?")
		args = append(args, subjectID)
	}
	out := map[int]string{}
	if len(conditions) == 0 {
		return out, nil
	}
	query := "SELECT episode, path FROM kazumi_download WHERE status = ? AND path != '' AND (" + strings.Join(conditions, " OR ") + ") ORDER BY id"
	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var episode int
		var path string
		if err := rows.Scan(&episode, &path); err != nil {
			return nil, err
		}
		out[episode] = path
	}
	return out, rows.Err()
}
