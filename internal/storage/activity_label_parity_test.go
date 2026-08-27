package storage

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A backend activity type with no Web-UI label used to fall through the label
// map's `|| type` passthrough and paint the raw storage enum into the Activity
// table's Type column, beside title-cased prose (#1065). Four types had escaped
// that way.
//
// This guard lives in Go, not vitest, on purpose: .github/workflows/frontend.yml
// is path-filtered to frontend/**, so a Go-only change that adds an activity
// type would never run a frontend test. unit-tests.yml runs `go test ./...`
// with no path filter, so this one fires on exactly the change it exists to
// catch. Same shape as cmd/generate-types/main_test.go, which reads a frontend
// file from Go for the same reason.
func TestEveryActivityTypeHasAWebUILabel(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "utils", "activity.ts"))
	require.NoError(t, err, "frontend/src/utils/activity.ts must be readable")

	block := regexp.MustCompile(`ACTIVITY_TYPE_LABELS[^=]*=\s*\{([\s\S]*?)\n\}`).FindSubmatch(src)
	require.NotNil(t, block, "ACTIVITY_TYPE_LABELS not found in frontend/src/utils/activity.ts")

	labelled := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*'?([a-z_]+)'?\s*:`).FindAllSubmatch(block[1], -1) {
		labelled[string(m[1])] = true
	}
	require.GreaterOrEqualf(t, len(labelled), len(ValidActivityTypes),
		"parsed only %d labels — the regex has drifted from the file's shape", len(labelled))

	for _, typ := range ValidActivityTypes {
		assert.Truef(t, labelled[typ],
			"activity type %q has no label: add it to ACTIVITY_TYPE_LABELS in frontend/src/utils/activity.ts, "+
				"or the Activity table renders it as raw snake_case", typ)
	}
}

// ValidActivityTypes has no production consumer, so nothing stops a new
// constant from being declared and forgotten here — which would make the test
// above vacuously green. Close that hole by reading the const block itself.
func TestValidActivityTypesCoversEveryConstant(t *testing.T) {
	src, err := os.ReadFile("activity_models.go")
	require.NoError(t, err)

	var declared []string
	for _, m := range regexp.MustCompile(`ActivityType\w+\s+ActivityType\s*=\s*"([a-z_]+)"`).FindAllStringSubmatch(string(src), -1) {
		declared = append(declared, m[1])
	}
	require.GreaterOrEqual(t, len(declared), 13, "parsed too few ActivityType constants — the regex has drifted")

	assert.ElementsMatch(t, declared, ValidActivityTypes,
		"ValidActivityTypes must list exactly the declared ActivityType constants")
}
