package kazumi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	repoUserAgent = "uani/1.0.1 (Kazumi rule repository; +https://github.com/HanamiSakiP/u-ani)"
	maxRuleBytes  = 8 << 20
	metaFileName  = ".meta.json"
)

var (
	// ErrRuleNotFound means the requested rule is absent from the catalog.
	ErrRuleNotFound = errors.New("rule not found in repository")
	// ErrNotInstalled means the requested rule is not installed locally.
	ErrNotInstalled = errors.New("rule is not installed")
)

// FetchError wraps a repository network or HTTP error.
type FetchError struct {
	URL string
	Err error
}

func (e *FetchError) Error() string {
	return fmt.Sprintf("fetch %s: %v", e.URL, e.Err)
}

func (e *FetchError) Unwrap() error { return e.Err }

// CatalogEntry is one item in a KazumiRules index.json.
type CatalogEntry struct {
	Name               string `json:"name"`
	Version            string `json:"version"`
	UseNativePlayer    bool   `json:"useNativePlayer"`
	AntiCrawlerEnabled bool   `json:"antiCrawlerEnabled"`
	Author             string `json:"author"`
	LastUpdate         int64  `json:"lastUpdate"`
	Installed          bool   `json:"installed"`
	InstalledVersion   string `json:"installed_version"`
}

// InstalledRule describes a locally installed rule.
type InstalledRule struct {
	Name               string `json:"name"`
	Version            string `json:"version"`
	API                string `json:"api"`
	Type               string `json:"type"`
	Author             string `json:"author,omitempty"`
	UseNativePlayer    bool   `json:"useNativePlayer"`
	AntiCrawlerEnabled bool   `json:"antiCrawlerEnabled"`
	InstalledAt        int64  `json:"installed_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

// InstallResult describes one install or update operation.
type InstallResult struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	PreviousVersion string `json:"previous_version,omitempty"`
	Status          string `json:"status"`
}

// Repository is a local view of a KazumiRules repository.
type Repository struct {
	repository string
	mirror     string
	dir        string
}

var repoMu sync.Mutex

// NewRepository creates a repository manager. An empty directory uses the
// standard ${UANI_PATH}/kazumi/rules path.
func NewRepository(repository, mirror, dir string) (*Repository, error) {
	repo, err := NormalizeRuleRepository(repository)
	if err != nil {
		return nil, err
	}
	mirror, err = NormalizeRulesMirror(mirror)
	if err != nil {
		return nil, err
	}
	if dir == "" {
		dir = RulesDir()
	}
	return &Repository{repository: repo, mirror: mirror, dir: dir}, nil
}

// CurrentRepository builds a manager from the active configuration.
func CurrentRepository() (*Repository, error) {
	settings := CurrentSettings()
	return NewRepository(settings.RuleRepository, settings.RulesMirror, RulesDir())
}

// RepositoryURL returns the normalized repository base URL.
func (r *Repository) RepositoryURL() string { return r.repository }

// Mirror returns the active rule repository mirror.
func (r *Repository) Mirror() string { return r.mirror }

// Catalog fetches index.json. When keyword is non-empty it matches rule names
// and authors case-insensitively.
func (r *Repository) Catalog(keyword string) ([]CatalogEntry, error) {
	entries, err := r.fetchCatalog()
	if err != nil {
		return nil, err
	}
	installed := map[string]string{}
	repoMu.Lock()
	if values, err := r.listInstalledLocked(); err == nil {
		for _, item := range values {
			installed[item.Name] = item.Version
		}
	}
	repoMu.Unlock()

	keyword = strings.ToLower(strings.TrimSpace(keyword))
	out := make([]CatalogEntry, 0, len(entries))
	for _, entry := range entries {
		if keyword != "" && !strings.Contains(strings.ToLower(entry.Name), keyword) &&
			!strings.Contains(strings.ToLower(entry.Author), keyword) {
			continue
		}
		if version, ok := installed[entry.Name]; ok {
			entry.Installed = true
			entry.InstalledVersion = version
		}
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// ListInstalled returns all locally installed rules, sorted by name.
func (r *Repository) ListInstalled() ([]InstalledRule, error) {
	repoMu.Lock()
	defer repoMu.Unlock()
	return r.listInstalledLocked()
}

// ReadRule returns the raw JSON of an installed rule.
func (r *Repository) ReadRule(name string) ([]byte, error) {
	repoMu.Lock()
	defer repoMu.Unlock()
	return r.readRuleRawLocked(name)
}

// LoadRule parses an installed rule for searching or chapter parsing.
func (r *Repository) LoadRule(name string) (*Rule, error) {
	raw, err := r.ReadRule(name)
	if err != nil {
		return nil, err
	}
	rule, err := ParseRule(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid rule %s: %w", name, err)
	}
	if rule.Name == "" {
		rule.Name = name
	}
	return rule, nil
}

// SaveRule writes a locally edited/imported rule and updates its metadata.
func (r *Repository) SaveRule(name string, raw []byte) error {
	name = strings.TrimSpace(name)
	if err := validateRuleName(name); err != nil {
		return err
	}
	meta, err := parseRule(raw)
	if err != nil {
		return fmt.Errorf("invalid rule: %w", err)
	}
	if meta.Name == "" {
		meta.Name = name
	}
	repoMu.Lock()
	defer repoMu.Unlock()
	if err := r.writeRuleLocked(name, raw); err != nil {
		return err
	}
	metadata, _ := r.readMetadata()
	now := time.Now().Unix()
	item := metadata[name]
	if item.InstalledAt == 0 {
		item.InstalledAt = now
	}
	item.Repository = "local"
	item.Version = meta.Version
	item.UpdatedAt = now
	metadata[name] = item
	return r.writeMetadata(metadata)
}

// CurrentRule loads an installed rule through the active configuration.
func CurrentRule(name string) (*Rule, error) {
	repo, err := CurrentRepository()
	if err != nil {
		return nil, err
	}
	return repo.LoadRule(name)
}

// ReadCurrentRule returns the raw JSON of an installed rule.
func ReadCurrentRule(name string) ([]byte, error) {
	repo, err := CurrentRepository()
	if err != nil {
		return nil, err
	}
	return repo.ReadRule(name)
}

// Install installs the named rules from the configured repository.
func (r *Repository) Install(names []string) ([]InstallResult, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("no rule names given")
	}
	entries, err := r.fetchCatalog()
	if err != nil {
		return nil, err
	}

	type preparedRule struct {
		entry CatalogEntry
		raw   []byte
		meta  ruleMetadata
	}
	prepared := make([]preparedRule, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if err := validateRuleName(name); err != nil {
			return nil, err
		}
		entry, ok := findCatalogEntry(entries, name)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrRuleNotFound, name)
		}
		raw, err := r.fetchRuleRaw(entry.Name)
		if err != nil {
			return nil, err
		}
		meta, err := parseRule(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid rule %s: %w", entry.Name, err)
		}
		if meta.Name == "" {
			meta.Name = entry.Name
		}
		prepared = append(prepared, preparedRule{entry: entry, raw: raw, meta: meta})
	}

	repoMu.Lock()
	defer repoMu.Unlock()
	metadata, _ := r.readMetadata()
	now := time.Now().Unix()
	results := make([]InstallResult, 0, len(prepared))
	for _, item := range prepared {
		previous := ""
		if existing, err := r.readInstalledLocked(item.entry.Name); err == nil {
			previous = existing.Version
		}
		version := item.meta.Version
		if version == "" {
			version = item.entry.Version
		}
		if err := r.writeRuleLocked(item.entry.Name, item.raw); err != nil {
			return results, err
		}
		old := metadata[item.entry.Name]
		if old.InstalledAt == 0 {
			old.InstalledAt = now
		}
		old.Repository = r.repository
		old.Version = version
		old.UpdatedAt = now
		metadata[item.entry.Name] = old
		status := "installed"
		if previous != "" {
			status = "updated"
			if previous == version {
				status = "unchanged"
			}
		}
		results = append(results, InstallResult{
			Name:            item.entry.Name,
			Version:         version,
			PreviousVersion: previous,
			Status:          status,
		})
	}
	if err := r.writeMetadata(metadata); err != nil {
		return results, err
	}
	return results, nil
}

// Update refreshes the named rules, or every installed rule when names is
// empty.
func (r *Repository) Update(names []string) ([]InstallResult, error) {
	if len(names) == 0 {
		installed, err := r.ListInstalled()
		if err != nil {
			return nil, err
		}
		for _, item := range installed {
			names = append(names, item.Name)
		}
	}
	if len(names) == 0 {
		return []InstallResult{}, nil
	}

	entries, err := r.fetchCatalog()
	if err != nil {
		return nil, err
	}
	type preparedRule struct {
		entry CatalogEntry
		raw   []byte
		meta  ruleMetadata
	}
	prepared := make([]preparedRule, 0, len(names))
	for _, name := range names {
		entry, ok := findCatalogEntry(entries, strings.TrimSpace(name))
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrRuleNotFound, name)
		}
		raw, err := r.fetchRuleRaw(entry.Name)
		if err != nil {
			return nil, err
		}
		meta, err := parseRule(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid rule %s: %w", entry.Name, err)
		}
		if meta.Name == "" {
			meta.Name = entry.Name
		}
		prepared = append(prepared, preparedRule{entry: entry, raw: raw, meta: meta})
	}

	repoMu.Lock()
	defer repoMu.Unlock()
	metadata, _ := r.readMetadata()
	now := time.Now().Unix()
	results := make([]InstallResult, 0, len(prepared))
	for _, item := range prepared {
		previous := ""
		if existing, err := r.readInstalledLocked(item.entry.Name); err == nil {
			previous = existing.Version
		}
		version := item.meta.Version
		if version == "" {
			version = item.entry.Version
		}
		if err := r.writeRuleLocked(item.entry.Name, item.raw); err != nil {
			return results, err
		}
		old := metadata[item.entry.Name]
		if old.InstalledAt == 0 {
			old.InstalledAt = now
		}
		old.Repository = r.repository
		old.Version = version
		old.UpdatedAt = now
		metadata[item.entry.Name] = old
		status := "installed"
		if previous != "" {
			status = "updated"
			if previous == version {
				status = "unchanged"
			}
		}
		results = append(results, InstallResult{
			Name:            item.entry.Name,
			Version:         version,
			PreviousVersion: previous,
			Status:          status,
		})
	}
	if err := r.writeMetadata(metadata); err != nil {
		return results, err
	}
	return results, nil
}

// Remove deletes named rules from local storage.
func (r *Repository) Remove(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("no rule names given")
	}
	repoMu.Lock()
	defer repoMu.Unlock()
	metadata, _ := r.readMetadata()
	removed := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if err := validateRuleName(name); err != nil {
			return removed, err
		}
		path := r.rulePath(name)
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				return removed, fmt.Errorf("%w: %s", ErrNotInstalled, name)
			}
			return removed, err
		}
		delete(metadata, name)
		removed = append(removed, name)
	}
	if err := r.writeMetadata(metadata); err != nil {
		return removed, err
	}
	return removed, nil
}

func (r *Repository) fetchCatalog() ([]CatalogEntry, error) {
	rawURL, err := CatalogURL(r.repository)
	if err != nil {
		return nil, err
	}
	data, err := r.fetch(rawURL)
	if err != nil {
		return nil, err
	}
	var entries []CatalogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode rule catalog: %w", err)
	}
	return entries, nil
}

func (r *Repository) fetchRuleRaw(name string) ([]byte, error) {
	rawURL, err := RuleURL(r.repository, name)
	if err != nil {
		return nil, err
	}
	return r.fetch(rawURL)
}

func (r *Repository) fetch(rawURL string) ([]byte, error) {
	target := ApplyRulesMirror(rawURL, r.mirror)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	headers := map[string]string{
		"User-Agent": repoUserAgent,
		"Accept":     "application/json",
	}
	resp, err := Get(ctx, target, headers, r.mirror == RulesMirrorGHProxy)
	if err != nil {
		return nil, &FetchError{URL: target, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &FetchError{
			URL: target,
			Err: fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRuleBytes))
	if err != nil {
		return nil, &FetchError{URL: target, Err: err}
	}
	return data, nil
}

func findCatalogEntry(entries []CatalogEntry, name string) (CatalogEntry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name, name) {
			return entry, true
		}
	}
	return CatalogEntry{}, false
}

type ruleMetadata struct {
	Name               string `json:"name"`
	Version            string `json:"version"`
	API                string `json:"api"`
	Type               string `json:"type"`
	Author             string `json:"author"`
	UseNativePlayer    bool   `json:"useNativePlayer"`
	AntiCrawlerEnabled bool   `json:"antiCrawlerEnabled"`
}

func parseRule(raw []byte) (ruleMetadata, error) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return ruleMetadata{}, err
	}
	if object == nil {
		return ruleMetadata{}, fmt.Errorf("rule must be a JSON object")
	}
	var meta ruleMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ruleMetadata{}, err
	}
	if anti, ok := object["antiCrawlerConfig"].(map[string]any); ok {
		if enabled, ok := anti["enabled"].(bool); ok {
			meta.AntiCrawlerEnabled = enabled
		}
	}
	return meta, nil
}

type storedMetadata struct {
	Repository  string `json:"repository"`
	Version     string `json:"version"`
	InstalledAt int64  `json:"installed_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

func (r *Repository) readMetadata() (map[string]storedMetadata, error) {
	out := map[string]storedMetadata{}
	data, err := os.ReadFile(filepath.Join(r.dir, metaFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	if len(data) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]storedMetadata{}, err
	}
	return out, nil
}

func (r *Repository) writeMetadata(metadata map[string]storedMetadata) error {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(r.dir, metaFileName), data)
}

func (r *Repository) listInstalledLocked() ([]InstalledRule, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []InstalledRule{}, nil
		}
		return nil, err
	}
	metadata, _ := r.readMetadata()
	out := make([]InstalledRule, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == metaFileName {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		installed, err := r.readInstalledLocked(name)
		if err != nil {
			continue
		}
		if meta, ok := metadata[name]; ok {
			if meta.Version != "" {
				installed.Version = meta.Version
			}
			installed.InstalledAt = meta.InstalledAt
			installed.UpdatedAt = meta.UpdatedAt
		}
		out = append(out, installed)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (r *Repository) readRuleRawLocked(name string) ([]byte, error) {
	if err := validateRuleName(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(r.rulePath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotInstalled, name)
		}
		return nil, err
	}
	return data, nil
}

func (r *Repository) readInstalledLocked(name string) (InstalledRule, error) {
	if err := validateRuleName(name); err != nil {
		return InstalledRule{}, err
	}
	path := r.rulePath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return InstalledRule{}, fmt.Errorf("%w: %s", ErrNotInstalled, name)
		}
		return InstalledRule{}, err
	}
	meta, err := parseRule(data)
	if err != nil {
		return InstalledRule{}, err
	}
	if meta.Name == "" {
		meta.Name = name
	}
	info, _ := os.Stat(path)
	installedAt := int64(0)
	updatedAt := int64(0)
	if info != nil {
		installedAt = info.ModTime().Unix()
		updatedAt = installedAt
	}
	return InstalledRule{
		Name:               meta.Name,
		Version:            meta.Version,
		API:                meta.API,
		Type:               meta.Type,
		Author:             meta.Author,
		UseNativePlayer:    meta.UseNativePlayer,
		AntiCrawlerEnabled: meta.AntiCrawlerEnabled,
		InstalledAt:        installedAt,
		UpdatedAt:          updatedAt,
	}, nil
}

func (r *Repository) writeRuleLocked(name string, raw []byte) error {
	if err := validateRuleName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(r.rulePath(name), raw)
}

func (r *Repository) rulePath(name string) string {
	return filepath.Join(r.dir, name+".json")
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}
