package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// activity_max_response_size documents 0 as "store responses whole". That
// promise is only real if an explicit 0 SURVIVES a save.
//
// It did not. The field was a plain `int` tagged `omitempty`, and SaveConfig is
// json.MarshalIndent, so marshalling an explicit 0 dropped the key entirely. A
// plain GET /api/v1/config -> POST /api/v1/config/apply round trip — the Web UI
// raw-config editor path — was enough: the apply decodes into a ZERO-valued
// config.Config (oauth.UnmaskLiveConfigDocument) and re-marshals, so the key
// vanished from the file and the next restart silently reinstated the 64KB cap.
//
// The tests below exercise the ROUND TRIP, not the struct tag. A tag assertion
// would pass on a fix that drops `omitempty` from a plain int — which inverts
// the failure instead of removing it, because a zero-valued decode then makes
// an ABSENT key indistinguishable from an explicit 0 and every apply omitting
// the key writes 0 and turns the cap OFF for everyone.

func TestActivityMaxResponseSizeExplicitZeroSurvivesMarshalRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	zero := 0
	cfg.ActivityMaxResponseSize = &zero

	encoded, err := json.MarshalIndent(cfg, "", "  ") // exactly what SaveConfig does
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"activity_max_response_size": 0`,
		"an explicit 0 must be written to the file, not omitted by omitempty")

	// Decode into a ZERO-valued Config, the way the /api/v1/config/apply path
	// does — not over DefaultConfig(), which would hide the loss.
	back := &Config{}
	require.NoError(t, json.Unmarshal(encoded, back))

	require.NotNil(t, back.ActivityMaxResponseSize,
		"the off switch was erased by the round trip")
	assert.Equal(t, 0, *back.ActivityMaxResponseSize)
	assert.Equal(t, 0, back.EffectiveActivityMaxResponseSize(),
		"0 means the cap is disabled; it must not resolve back to the default")
}

// The mirror case, and the one that a bare `omitempty` removal would break: a
// config that never mentions the key must stay silent about it after a save,
// and must resolve to the 64KB default rather than to "disabled".
func TestActivityMaxResponseSizeAbsentKeyStaysAbsentAndKeepsTheCap(t *testing.T) {
	absent := &Config{}
	require.Nil(t, absent.ActivityMaxResponseSize)

	encoded, err := json.MarshalIndent(absent, "", "  ")
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "activity_max_response_size",
		"an absent key must not be materialised as an explicit 0 — that would "+
			"turn the cap off for every operator who never set it")

	assert.Equal(t, DefaultActivityMaxResponseSizeBytes, absent.EffectiveActivityMaxResponseSize())
	assert.Equal(t, DefaultActivityMaxResponseSizeBytes, (*Config)(nil).EffectiveActivityMaxResponseSize())
}

// The file-level round trip: write a config carrying an explicit 0, load it,
// save it again, and re-read. This is the sequence the live repro followed.
func TestActivityMaxResponseSizeZeroSurvivesLoadSaveOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
  "listen": "127.0.0.1:18999",
  "data_dir": "`+dir+`",
  "activity_max_response_size": 0,
  "mcpServers": []
}`), 0o600))

	loaded, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, loaded.ActivityMaxResponseSize,
		"an explicit 0 in the file must not be erased by the load")
	require.Equal(t, 0, *loaded.ActivityMaxResponseSize)

	require.NoError(t, SaveConfig(loaded, path))

	reloaded, err := LoadFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, reloaded.ActivityMaxResponseSize,
		"SaveConfig dropped the off switch; the cap is silently back at 64KB")
	assert.Equal(t, 0, *reloaded.ActivityMaxResponseSize)
	assert.Equal(t, 0, reloaded.EffectiveActivityMaxResponseSize())
}

// A negative value is a typo, not a bigger off switch. Resolving it to
// "disabled" would turn a slip of the keyboard into unbounded record growth.
func TestActivityMaxResponseSizeNegativeFallsBackToDefault(t *testing.T) {
	neg := -1
	cfg := &Config{ActivityMaxResponseSize: &neg}
	assert.Equal(t, DefaultActivityMaxResponseSizeBytes, cfg.EffectiveActivityMaxResponseSize())
}
