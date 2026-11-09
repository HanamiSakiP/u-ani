package update

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/config"
)

func createV4DB(t *testing.T, withKeyword bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bangumi.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	bangumiSQL := `
CREATE TABLE bangumi (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	subtitle_group TEXT NOT NULL DEFAULT '',
	update_time CHAR(5) NOT NULL DEFAULT 'Unknown',
	cover TEXT NOT NULL DEFAULT '',
	status INTEGER NOT NULL DEFAULT 0
);`
	if withKeyword {
		bangumiSQL = `
CREATE TABLE bangumi (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	subtitle_group TEXT NOT NULL DEFAULT '',
	keyword TEXT NOT NULL DEFAULT '',
	update_time CHAR(5) NOT NULL DEFAULT 'Unknown',
	cover TEXT NOT NULL DEFAULT '',
	status INTEGER NOT NULL DEFAULT 0
);`
	}
	if _, err := conn.Exec(bangumiSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
CREATE TABLE followed (
	id INTEGER PRIMARY KEY,
	bangumi_name TEXT NOT NULL UNIQUE,
	episode INTEGER NOT NULL DEFAULT 0,
	status INTEGER NOT NULL DEFAULT 1,
	updated_time INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE filter (
	bangumi_name TEXT PRIMARY KEY,
	subtitle TEXT NOT NULL DEFAULT '',
	include TEXT NOT NULL DEFAULT '',
	exclude TEXT NOT NULL DEFAULT '',
	regex TEXT NOT NULL DEFAULT ''
);
CREATE TABLE download (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	title TEXT NOT NULL,
	episode INTEGER NOT NULL,
	download TEXT NOT NULL,
	status INTEGER NOT NULL
);`); err != nil {
		t.Fatal(err)
	}

	if withKeyword {
		_, err = conn.Exec(`INSERT INTO bangumi (id, name, subtitle_group, keyword, update_time, cover, status)
			VALUES (1, 'TestAnime 第二季', '[]', 'test', 'Mon', '/cover/test.jpg', 0)`)
	} else {
		_, err = conn.Exec(`INSERT INTO bangumi (id, name, subtitle_group, update_time, cover, status)
			VALUES (1, 'NoKeywordAnime', '[]', 'Mon', '', 0)`)
	}
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(`
INSERT INTO followed (bangumi_name, episode, status, updated_time) VALUES ('TestAnime 第二季', 5, 1, 1000);
INSERT INTO filter (bangumi_name, subtitle, include, exclude, regex) VALUES ('TestAnime 第二季', 'sub1,sub2', 'keyword1', 'bad', '.*720.*');
INSERT INTO download (id, name, title, episode, download, status) VALUES (1, 'TestAnime 第二季', 'TestAnime EP01', 1, 'magnet:?xt=test', 2);`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMigrateFromV4(t *testing.T) {
	path := createV4DB(t, true)

	oldPath, oldSource := config.Cfg.SavePath, config.Cfg.DataSource
	config.Cfg.SavePath = t.TempDir()
	// Force source.New() to fail so migration does not touch the network.
	config.Cfg.DataSource = "does-not-exist"
	t.Cleanup(func() {
		config.Cfg.SavePath = oldPath
		config.Cfg.DataSource = oldSource
	})

	migrateFromV4(path)

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	bangumiCols := getTableColumns(path, "bangumi")
	if hasColumn(bangumiCols, "keyword") || hasColumn(bangumiCols, "update_time") {
		t.Error("v4 bangumi columns were not migrated")
	}
	if !hasColumn(bangumiCols, "update_day") {
		t.Error("missing update_day column")
	}
	var id, name, updateDay string
	if err := conn.QueryRow("SELECT id, name, update_day FROM bangumi").Scan(&id, &name, &updateDay); err != nil {
		t.Fatal(err)
	}
	if id != "test" || name != "TestAnime 第二季" || updateDay != "Mon" {
		t.Errorf("unexpected bangumi row: id=%q name=%q update_day=%q", id, name, updateDay)
	}

	var episodes, subtitle, include, exclude, regex string
	var season int
	err = conn.QueryRow(`SELECT episodes, subtitle, "include", "exclude", regex, season
		FROM followed WHERE bangumi_name = ?`, "TestAnime 第二季").
		Scan(&episodes, &subtitle, &include, &exclude, &regex, &season)
	if err != nil {
		t.Fatal(err)
	}
	if episodes != "[1, 2, 3, 4, 5]" {
		t.Errorf("episodes = %q", episodes)
	}
	if subtitle != `["sub1", "sub2"]` || include != `["keyword1"]` || exclude != `["bad"]` || regex != ".*720.*" {
		t.Errorf("filter not merged: subtitle=%q include=%q exclude=%q regex=%q", subtitle, include, exclude, regex)
	}
	if season != 2 {
		t.Errorf("season = %d, want 2", season)
	}

	var downloadName, taskID string
	var taskIDNull sql.NullString
	err = conn.QueryRow("SELECT bangumi_name, task_id FROM download WHERE id = 1").Scan(&downloadName, &taskIDNull)
	if err != nil {
		t.Fatal(err)
	}
	taskID = taskIDNull.String
	if downloadName != "TestAnime 第二季" || taskID != "" {
		t.Errorf("download row: bangumi_name=%q task_id=%q", downloadName, taskID)
	}

	var filterCount int
	if err := conn.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='filter'").Scan(&filterCount); err != nil {
		t.Fatal(err)
	}
	if filterCount != 0 {
		t.Error("filter table should have been dropped")
	}
}

func TestMigrateFromV4WithoutKeywordFallsBackToLocalID(t *testing.T) {
	path := createV4DB(t, false)

	oldPath, oldSource := config.Cfg.SavePath, config.Cfg.DataSource
	config.Cfg.SavePath = t.TempDir()
	config.Cfg.DataSource = "does-not-exist"
	t.Cleanup(func() {
		config.Cfg.SavePath = oldPath
		config.Cfg.DataSource = oldSource
	})

	migrateFromV4(path)

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var id, name string
	if err := conn.QueryRow("SELECT id, name FROM bangumi").Scan(&id, &name); err != nil {
		t.Fatal(err)
	}
	if id != "1" || name != "NoKeywordAnime" {
		t.Errorf("unexpected bangumi row: id=%q name=%q", id, name)
	}
}
