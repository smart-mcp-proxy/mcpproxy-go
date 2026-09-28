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
		toolName := name
		prefix := rowServer + ":"
		if len(toolName) > len(prefix) && toolName[:len(prefix)] == prefix {
			toolName = toolName[len(prefix):]
		}
		if profileController.ToolAllowedByProfile(ctx, rowServer, toolName) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}
