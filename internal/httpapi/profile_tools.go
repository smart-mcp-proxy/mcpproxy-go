package httpapi

import "context"

type profileToolVisibilityController interface {
	SearchToolsForProfile(context.Context, string, int, func(string) bool) ([]map[string]interface{}, bool, error)
	ToolAllowedByProfile(context.Context, string, string) bool
}

func filterProfileToolRows(controller ServerController, ctx context.Context, serverName string, rows []map[string]interface{}) []map[string]interface{} {
	profileController, ok := controller.(profileToolVisibilityController)
	if !ok {
		return rows
	}
	filtered := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		name, _ := row["name"].(string)
		rowServer := serverName
		if rowServer == "" {
			rowServer, _ = row["server_name"].(string)
		}
		if name == "" || rowServer == "" {
			continue
		}
		// StateView names are raw upstream registration identities. A raw name
		// may itself start with "<server>:"; preserve it for the policy check
		// so a rule such as github:github:erase is not bypassed as erase.
		if profileController.ToolAllowedByProfile(ctx, rowServer, name) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}
