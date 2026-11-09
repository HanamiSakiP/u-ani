package httpserver

import (
	"fmt"

	"github.com/HanamiSakiP/u-ani/internal/kazumi"
)

func toolKazumiCatalog(args map[string]any) (any, error) {
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		return nil, err
	}
	keyword, _ := argString(args, "keyword")
	entries, err := repo.Catalog(keyword)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"repository": repo.RepositoryURL(),
		"rules":      entries,
	}, nil
}

func toolKazumiList() (any, error) {
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		return nil, err
	}
	rules, err := repo.ListInstalled()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"repository": repo.RepositoryURL(),
		"rules":      rules,
	}, nil
}

func toolKazumiInstall(args map[string]any) (any, error) {
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		return nil, err
	}
	names := stringSliceValue(args["names"])
	if name, _ := argString(args, "name"); name != "" {
		names = append(names, name)
	}
	if argBool(args, "all") {
		entries, err := repo.Catalog("")
		if err != nil {
			return nil, err
		}
		names = make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("missing required argument: names or all")
	}
	results, err := repo.Install(names)
	if err != nil {
		return nil, err
	}
	return map[string]any{"rules": results}, nil
}

func toolKazumiUpdate(args map[string]any) (any, error) {
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		return nil, err
	}
	names := stringSliceValue(args["names"])
	if name, _ := argString(args, "name"); name != "" {
		names = append(names, name)
	}
	results, err := repo.Update(names)
	if err != nil {
		return nil, err
	}
	return map[string]any{"rules": results}, nil
}

func toolKazumiRemove(args map[string]any) (any, error) {
	repo, err := kazumi.CurrentRepository()
	if err != nil {
		return nil, err
	}
	names := stringSliceValue(args["names"])
	if name, _ := argString(args, "name"); name != "" {
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("missing required argument: names or name")
	}
	removed, err := repo.Remove(names)
	if err != nil {
		return nil, err
	}
	return map[string]any{"removed": removed}, nil
}

func toolKazumiGetNetwork() (any, error) {
	return kazumiSettingsResponse(kazumi.CurrentSettings()), nil
}

func toolKazumiSetNetwork(args map[string]any) (any, error) {
	settings := kazumi.CurrentSettings()
	if value, ok := optionalStringArg(args, "rule_repository"); ok {
		settings.RuleRepository = value
	}
	if value, ok := optionalStringArg(args, "bangumi_acceleration"); ok {
		settings.BangumiAcceleration = value
	}
	if value, ok := optionalStringArg(args, "rules_mirror"); ok {
		settings.RulesMirror = value
	}
	if value, ok := optionalStringArg(args, "image_acceleration"); ok {
		settings.ImageAcceleration = value
	}
	if err := kazumi.SaveSettings(settings); err != nil {
		return nil, err
	}
	return kazumiSettingsResponse(kazumi.CurrentSettings()), nil
}

func optionalStringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key]
	if !ok {
		return "", false
	}
	if value == nil {
		return "", true
	}
	if text, ok := value.(string); ok {
		return text, true
	}
	return fmt.Sprint(value), true
}
