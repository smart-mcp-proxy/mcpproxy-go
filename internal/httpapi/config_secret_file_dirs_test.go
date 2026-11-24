package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

type secretDirsFunnelController struct {
	funnelController
	cfgPath string
}

func (c *secretDirsFunnelController) GetConfigPath() string { return c.cfgPath }

func secretDirsHTTPServer(t *testing.T) (*Server, *secretDirsFunnelController, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, config.ConfigFileName)
	w := config.SecretFileDirs{"/run/secrets"}
	cfg := &config.Config{Listen: "127.0.0.1:8080", DataDir: dir, SecretFileDirs: &w}
	require.NoError(t, config.SaveConfig(cfg, path))

	ctrl := &secretDirsFunnelController{
		funnelController: funnelController{desired: cfg},
		cfgPath:          path,
	}
	return NewServer(ctrl, zap.NewNop().Sugar(), nil), ctrl, path
}

func TestApplyConfig_RefusesSecretFileDirsChange(t *testing.T) {
	srv, ctrl, _ := secretDirsHTTPServer(t)

	w := funnelRequest(t, srv, http.MethodPost, "/api/v1/config/apply",
		`{"listen":"127.0.0.1:8080","secret_file_dirs":["*"]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), config.ErrSecretFileDirsNotWritable.Error())
	onDiskDirs := config.SecretFileDirs{"/run/secrets"}
	assert.True(t, config.SecretFileDirsEqual(ctrl.desired.SecretFileDirs, &onDiskDirs))
}

func TestPatchConfig_RefusesSecretFileDirsChange(t *testing.T) {
	srv, ctrl, _ := secretDirsHTTPServer(t)

	w := funnelRequest(t, srv, http.MethodPatch, "/api/v1/config", `{"secret_file_dirs":["*"]}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), config.ErrSecretFileDirsNotWritable.Error())
	onDiskDirs := config.SecretFileDirs{"/run/secrets"}
	assert.True(t, config.SecretFileDirsEqual(ctrl.desired.SecretFileDirs, &onDiskDirs))
}

func TestPatchConfig_AllowsUnrelatedEdit(t *testing.T) {
	srv, ctrl, path := secretDirsHTTPServer(t)

	w := funnelRequest(t, srv, http.MethodPatch, "/api/v1/config", `{"tools_limit":12}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 12, ctrl.desired.ToolsLimit)

	onDisk, err := config.DecodeConfigFile(path)
	require.NoError(t, err)
	assert.True(t, config.SecretFileDirsEqual(onDisk.SecretFileDirs, ctrl.desired.SecretFileDirs))
}

func TestApplyConfig_RoundTripPreservesSecretFileDirsFromDisk(t *testing.T) {
	srv, _, path := secretDirsHTTPServer(t)
	onDisk, err := config.DecodeConfigFile(path)
	require.NoError(t, err)
	raw, err := json.Marshal(onDisk)
	require.NoError(t, err)

	w := funnelRequest(t, srv, http.MethodPost, "/api/v1/config/apply", string(raw))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}
