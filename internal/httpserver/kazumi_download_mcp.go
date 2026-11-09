package httpserver

import (
	"fmt"

	"github.com/HanamiSakiP/u-ani/internal/controllers"
	"github.com/HanamiSakiP/u-ani/internal/kazumi"
)

func toolKazumiSearch(args map[string]any) (any, error) {
	ruleName, _ := argString(args, "rule")
	keyword, _ := argString(args, "keyword")
	if ruleName == "" || keyword == "" {
		return nil, fmt.Errorf("missing required arguments: rule, keyword")
	}
	rule, err := kazumi.CurrentRule(ruleName)
	if err != nil {
		return nil, err
	}
	results, err := kazumi.Search(rule, keyword)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "success", "rule": rule.Name, "results": results}, nil
}

func toolKazumiChapters(args map[string]any) (any, error) {
	ruleName, _ := argString(args, "rule")
	source, _ := argString(args, "source")
	if ruleName == "" || source == "" {
		return nil, fmt.Errorf("missing required arguments: rule, source")
	}
	rule, err := kazumi.CurrentRule(ruleName)
	if err != nil {
		return nil, err
	}
	roads, err := kazumi.Chapters(rule, source)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "success", "roads": roads}, nil
}

func toolKazumiDownload(args map[string]any) (any, error) {
	ruleName, _ := argString(args, "rule")
	name, _ := argString(args, "name")
	source, _ := argString(args, "source")
	title, _ := argString(args, "title")
	episode := intValue(args["episode"])
	task, err := controllers.StartKazumiDownload(controllers.KazumiDownloadOptions{
		Rule: ruleName, Name: name, Title: title, Episode: episode, Source: source,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "success", "task": task}, nil
}

func toolKazumiDownloadStatus() (any, error) {
	tasks, err := controllers.ListKazumiDownloads()
	if err != nil {
		return nil, err
	}
	return map[string]any{"tasks": tasks}, nil
}

func toolKazumiFiles() (any, error) {
	files, err := controllers.ListKazumiFiles()
	if err != nil {
		return nil, err
	}
	return map[string]any{"files": files}, nil
}
