package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// funnelController is a controller that implements the config funnel the way
// the runtime does: it reads the desired config, runs the caller's mutation and
// applies the result under one mutex. beforeMutate simulates a concurrent write
// that lands between the handler starting and the funnel taking its lock.
type funnelController struct {
	baseController
	mu           sync.Mutex
	desired      *config.Config
	actor        internalRuntime.Actor
	beforeMutate func(*config.Config)
	applyCalls   int // legacy ApplyConfig path: must stay zero
}

func (c *funnelController) GetCurrentConfig() *config.Config { return &config.Config{APIKey: "k"} }
func (c *funnelController) GetConfig() (*config.Config, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *c.desired
	return &cp, nil
}
func (c *funnelController) GetConfigPath() string { return "/tmp/mcp_config.json" }
func (c *funnelController) ApplyConfig(*config.Config, string) (*internalRuntime.ConfigApplyResult, error) {
	c.applyCalls++
	return &internalRuntime.ConfigApplyResult{Success: true}, nil
}

func (c *funnelController) MutateConfig(
	_ context.Context, actor internalRuntime.Actor,
	mutate func(*config.Config) (internalRuntime.ChangeHint, error),
	_ internalRuntime.TokenRewrite,
) (*internalRuntime.ConfigApplyResult, *internalRuntime.ConfigDiff, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.beforeMutate != nil {
		c.beforeMutate(c.desired)
	}
	c.actor = actor
	edited := *c.desired
	if _, err := mutate(&edited); err != nil {
		return nil, nil, err
	}
	c.desired = &edited
	return &internalRuntime.ConfigApplyResult{Success: true, AppliedImmediately: true}, &internalRuntime.ConfigDiff{}, nil
}

func funnelRequest(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "k")
	req.Header.Set("X-MCPProxy-Surface", "cli")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// A profile create that lands after the PATCH handler started must not be
// lost: the read-merge runs inside the funnel, on the config it reads under the
// lock (Spec 108-f F2, T072b).
func TestPatchConfig_MergeHappensInsideLock(t *testing.T) {
	ctrl := &funnelController{desired: &config.Config{Listen: "127.0.0.1:8080"}}
	ctrl.beforeMutate = func(desired *config.Config) {
		desired.Profiles = append(desired.Profiles, config.ProfileConfig{Name: "raced", Servers: []string{"a"}})
	}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	w := funnelRequest(t, srv, http.MethodPatch, "/api/v1/config", `{"listen":"127.0.0.1:9090"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	require.Equal(t, "127.0.0.1:9090", ctrl.desired.Listen)
	require.Len(t, ctrl.desired.Profiles, 1, "the concurrent profile write survived the PATCH")
	assert.Equal(t, "raced", ctrl.desired.Profiles[0].Name)
	assert.Equal(t, 0, ctrl.applyCalls, "the legacy read-then-apply path is not used when the funnel exists")
	assert.Equal(t, profile.SurfaceCLI, ctrl.actor.Surface, "the write is attributed to the caller's surface")
}

func TestApplyConfigAndDockerIsolation_UseTheFunnel(t *testing.T) {
	ctrl := &funnelController{desired: &config.Config{Listen: "127.0.0.1:8080", DockerIsolation: config.DefaultDockerIsolationConfig()}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	w := funnelRequest(t, srv, http.MethodPatch, "/api/v1/config/docker-isolation", `{"enabled":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, ctrl.desired.DockerIsolation)
	assert.True(t, ctrl.desired.DockerIsolation.Enabled)

	doc, _ := json.Marshal(map[string]any{"listen": "127.0.0.1:7070"})
	w = funnelRequest(t, srv, http.MethodPost, "/api/v1/config/apply", string(doc))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "127.0.0.1:7070", ctrl.desired.Listen)
	assert.Equal(t, 0, ctrl.applyCalls)
}

// A ValidationError from the funnel (an unknown anonymous_profile) is the
// caller's 400 with the offending field, never a 500.
type refusingFunnel struct {
	funnelController
	err error
}

func (c *refusingFunnel) MutateConfig(_ context.Context, _ internalRuntime.Actor, _ func(*config.Config) (internalRuntime.ChangeHint, error), _ internalRuntime.TokenRewrite) (*internalRuntime.ConfigApplyResult, *internalRuntime.ConfigDiff, error) {
	return nil, nil, c.err
}

func TestPatchConfig_FunnelValidationErrorIs400WithField(t *testing.T) {
	ctrl := &refusingFunnel{
		funnelController: funnelController{desired: &config.Config{}},
		err:              &internalRuntime.ValidationError{Field: "anonymous_profile", Message: `unknown profile "ghost"`},
	}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)
	w := funnelRequest(t, srv, http.MethodPatch, "/api/v1/config", `{"anonymous_profile":"ghost"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "anonymous_profile", body["field"])
	assert.Equal(t, `unknown profile "ghost"`, body["error"])
}

// #1466: with the env telemetry lock active, a case-variant duplicate of the
// locked key cannot smuggle a change past the lock — the comparison is on the
// typed value the document resolves to.
func TestApplyConfig_TelemetryLockRefusesCaseVariantDuplicate(t *testing.T) {
	t.Setenv("DO_NOT_TRACK", "1")
	off := false
	ctrl := &funnelController{desired: &config.Config{Listen: "127.0.0.1:8080", Telemetry: &config.TelemetryConfig{Enabled: &off}}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	docs := []string{
		`{"telemetry":{"enabled":false},"Telemetry":{"ENABLED":true}}`,
		`{"Telemetry":{"ENABLED":true},"telemetry":{"anonymous_id":"x"}}`,
		`{"telemetry":{"anonymous_id":"x"},"Telemetry":{"ENABLED":true}}`,
	}
	for _, doc := range docs {
		w := funnelRequest(t, srv, http.MethodPost, "/api/v1/config/apply", doc)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "doc %s: %s", doc, w.Body.String())
	}
	require.NotNil(t, ctrl.desired.Telemetry.Enabled)
	assert.False(t, *ctrl.desired.Telemetry.Enabled, "the locked value was not changed")
}
