package httpserver

import (
	"fmt"

	"github.com/HanamiSakiP/u-ani/internal/controllers"
)

func toolBangumiSyncStatus() (any, error) {
	return controllers.BangumiSyncStatus(), nil
}

func toolBangumiSync(args map[string]any) (any, error) {
	var types []string
	if raw, present := args["types"]; present && raw != nil {
		types = stringSliceValue(raw)
	}
	summary, err := controllers.SyncSubscriptions(controllers.SyncOptions{
		Types:   types,
		Episode: nullableIntArg(args, "episode"),
		Latest:  argBool(args, "latest"),
		DryRun:  argBool(args, "dry_run"),
		Limit:   intValue(args["limit"]),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":  "success",
		"summary": summary,
		"sync":    controllers.BangumiSyncStatus(),
	}, nil
}

func toolBangumiSetToken(args map[string]any) (any, error) {
	token, ok := argString(args, "access_token")
	if !ok || token == "" {
		return nil, fmt.Errorf("missing required argument: access_token")
	}
	user, err := controllers.SetBangumiToken(token)
	if err != nil {
		return nil, err
	}
	if raw, present := args["enabled"]; present && raw != nil {
		enabled := argBool(args, "enabled")
		var types *[]string
		if value, ok := args["sync_types"]; ok && value != nil {
			values := stringSliceValue(value)
			types = &values
		}
		if err := controllers.ConfigureBangumiSync(&enabled, types); err != nil {
			return nil, err
		}
	} else if raw, present := args["sync_types"]; present && raw != nil {
		values := stringSliceValue(raw)
		if err := controllers.ConfigureBangumiSync(nil, &values); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"status":   "success",
		"username": user.Username,
		"sync":     controllers.BangumiSyncStatus(),
	}, nil
}

func toolBangumiConfigureSync(args map[string]any) (any, error) {
	var enabled *bool
	if raw, present := args["enabled"]; present && raw != nil {
		value := argBool(args, "enabled")
		enabled = &value
	}
	var types *[]string
	if raw, present := args["sync_types"]; present && raw != nil {
		values := stringSliceValue(raw)
		types = &values
	}
	if enabled == nil && types == nil {
		return nil, fmt.Errorf("provide enabled and/or sync_types")
	}
	if err := controllers.ConfigureBangumiSync(enabled, types); err != nil {
		return nil, err
	}
	return map[string]any{
		"status": "success",
		"sync":   controllers.BangumiSyncStatus(),
	}, nil
}

func toolBangumiLogout() (any, error) {
	if err := controllers.ClearBangumiToken(); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":  "success",
		"message": "Bangumi account removed",
		"sync":    controllers.BangumiSyncStatus(),
	}, nil
}
