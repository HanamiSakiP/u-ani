package script

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/db"
)

const exampleScript = `import datetime
from typing import Dict

from bgmi.script import ScriptBase
from bgmi.utils import parse_episode


class Script(ScriptBase):
    class Model(ScriptBase.Model):
        bangumi_name = "TEST_BANGUMI"
        cover = ""
        update_time = "Mon"
        due_date = datetime.datetime(2080, 9, 30)

    def get_download_url(self) -> Dict[int, str]:
        resp = [
            {
                "title": "[YMDR][慕留人 -火影忍者新时代-][2017][2][AVC][JAP][BIG5][MP4][1080P]",
                "link": "http://example.com/Bangumi/1/2.torrent",
            },
            {
                "title": "[ZXSUB仲夏动漫字幕组][博人传-火影忍者次世代][03][720P繁体][MP4]",
                "link": "magnet:?xt=urn:btih:233",
            },
        ]

        ret = {}
        for item in resp:
            e = parse_episode(item["title"])
            if e:
                ret[e] = item["link"]

        return ret
`

func TestPythonScriptCompatibility(t *testing.T) {
	if pythonCommand() == "" {
		t.Skip("python interpreter not available")
	}
	oldScriptPath, oldDBPath := config.Cfg.ScriptPath, config.Cfg.DBPath
	if db.Conn != nil {
		_ = db.Conn.Close()
		db.Conn = nil
	}
	dir := t.TempDir()
	config.Cfg.ScriptPath = filepath.Join(dir, "scripts")
	config.Cfg.DBPath = filepath.Join(dir, "bangumi.db")
	t.Cleanup(func() {
		if db.Conn != nil {
			_ = db.Conn.Close()
			db.Conn = nil
		}
		config.Cfg.ScriptPath = oldScriptPath
		config.Cfg.DBPath = oldDBPath
	})
	if err := os.MkdirAll(config.Cfg.ScriptPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Cfg.ScriptPath, "script_example.py"), []byte(exampleScript), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.New(); err != nil {
		t.Fatal(err)
	}

	models := GetModels()
	if len(models) != 1 {
		t.Fatalf("got %d script models, want 1: %+v", len(models), models)
	}
	if models[0].Name != "TEST_BANGUMI" || models[0].UpdateDay != "Mon" {
		t.Errorf("unexpected model: %+v", models[0])
	}

	results := Run()
	if len(results) != 1 {
		t.Fatalf("got %d script results, want 1: %+v", len(results), results)
	}
	episodes := map[int]string{}
	for _, e := range results[0].Episodes {
		episodes[e.Episode] = e.Download
	}
	if len(episodes) != 2 || episodes[2] == "" || episodes[3] == "" {
		t.Errorf("unexpected episodes: %+v", episodes)
	}
}
