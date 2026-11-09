package kazumi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type testRuleServer struct {
	mu      sync.Mutex
	version string
	entries []CatalogEntry
	rules   map[string]map[string]any
}

func newTestRuleServer(t *testing.T) (*testRuleServer, *httptest.Server) {
	t.Helper()
	state := &testRuleServer{
		version: "1.0",
		entries: []CatalogEntry{
			{Name: "AGE", Version: "1.0", Author: "tester", LastUpdate: 1700000000000},
			{Name: "mutefun", Version: "2.0", Author: "other", AntiCrawlerEnabled: true},
		},
		rules: map[string]map[string]any{
			"AGE": {
				"api": "9", "type": "anime", "name": "AGE", "version": "1.0",
				"baseURL": "https://example.com/", "useNativePlayer": true,
			},
			"mutefun": {
				"api": "9", "type": "anime", "name": "mutefun", "version": "2.0",
				"antiCrawlerConfig": map[string]any{"enabled": true},
			},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rules/index.json", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		_ = json.NewEncoder(w).Encode(state.entries)
	})
	mux.HandleFunc("/rules/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/rules/"), ".json")
		state.mu.Lock()
		defer state.mu.Unlock()
		rule, ok := state.rules[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(rule)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return state, server
}

func TestRepositoryInstallListUpdateRemove(t *testing.T) {
	state, server := newTestRuleServer(t)
	dir := t.TempDir()
	repo, err := NewRepository(server.URL+"/rules", RulesMirrorDirect, dir)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := repo.Catalog("age")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "AGE" || entries[0].Installed {
		t.Fatalf("catalog = %#v", entries)
	}

	results, err := repo.Install([]string{"age"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "installed" || results[0].Name != "AGE" {
		t.Fatalf("install results = %#v", results)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGE.json")); err != nil {
		t.Fatalf("expected rule file: %v", err)
	}

	installed, err := repo.ListInstalled()
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Version != "1.0" || !installed[0].UseNativePlayer {
		t.Fatalf("installed = %#v", installed)
	}

	entries, err = repo.Catalog("")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name == "AGE" && (!entry.Installed || entry.InstalledVersion != "1.0") {
			t.Fatalf("catalog should mark AGE installed: %#v", entry)
		}
	}

	state.mu.Lock()
	state.version = "1.1"
	state.entries[0].Version = "1.1"
	state.rules["AGE"]["version"] = "1.1"
	state.mu.Unlock()

	results, err = repo.Update(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "updated" || results[0].Version != "1.1" {
		t.Fatalf("update results = %#v", results)
	}
	installed, err = repo.ListInstalled()
	if err != nil {
		t.Fatal(err)
	}
	if installed[0].Version != "1.1" {
		t.Fatalf("installed version = %q", installed[0].Version)
	}

	removed, err := repo.Remove([]string{"AGE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %#v", removed)
	}
	if _, err := repo.ListInstalled(); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Remove([]string{"AGE"}); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("second remove error = %v", err)
	}
}

func TestRepositoryRejectsMissingAndUnsafeRules(t *testing.T) {
	_, server := newTestRuleServer(t)
	repo, err := NewRepository(server.URL+"/rules", RulesMirrorDirect, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Install([]string{"missing"}); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("missing rule error = %v", err)
	}
	if _, err := repo.Install([]string{"../AGE"}); err == nil {
		t.Fatal("expected unsafe name to fail")
	}
	if _, err := repo.Remove([]string{"missing"}); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("remove missing error = %v", err)
	}
}
