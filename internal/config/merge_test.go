package config

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// T3.1: Test scalar field replacement
func TestMergeServerConfig_ScalarFieldReplacement(t *testing.T) {
	base := &ServerConfig{
		Name:     "test-server",
		URL:      "http://old.com",
		Protocol: "http",
		Command:  "old-cmd",
		Enabled:  false,
		Created:  time.Now().Add(-24 * time.Hour),
	}

	patch := &ServerConfig{
		URL:      "http://new.com",
		Protocol: "sse",
		Command:  "new-cmd",
		Enabled:  true,
	}

	merged, diff, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify scalar fields are replaced
	if merged.URL != patch.URL {
		t.Errorf("URL not replaced: got %s, want %s", merged.URL, patch.URL)
	}
	if merged.Protocol != patch.Protocol {
		t.Errorf("Protocol not replaced: got %s, want %s", merged.Protocol, patch.Protocol)
	}
	if merged.Command != patch.Command {
		t.Errorf("Command not replaced: got %s, want %s", merged.Command, patch.Command)
	}
	if merged.Enabled != patch.Enabled {
		t.Errorf("Enabled not replaced: got %v, want %v", merged.Enabled, patch.Enabled)
	}

	// Verify diff captured changes
	if diff == nil {
		t.Fatal("Expected diff to be generated")
	}
	if _, ok := diff.Modified["url"]; !ok {
		t.Error("URL change not captured in diff")
	}
	if _, ok := diff.Modified["enabled"]; !ok {
		t.Error("Enabled change not captured in diff")
	}
}

// T3.2: Test deep merge for map fields
func TestMergeServerConfig_MapFieldDeepMerge(t *testing.T) {
	base := &ServerConfig{
		Name: "test-server",
		Env: map[string]string{
			"API_KEY": "secret",
			"DEBUG":   "false",
			"MODE":    "production",
		},
		Headers: map[string]string{
			"Authorization": "Bearer old-token",
			"X-Custom":      "value",
		},
	}

	patch := &ServerConfig{
		Env: map[string]string{
			"DEBUG":   "true", // Update existing
			"TIMEOUT": "30",   // Add new
		},
		Headers: map[string]string{
			"Authorization": "Bearer new-token", // Update existing
			"X-Request-Id":  "123",              // Add new
		},
	}

	merged, _, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify env is deep merged
	expectedEnv := map[string]string{
		"API_KEY": "secret",     // Preserved
		"DEBUG":   "true",       // Updated
		"MODE":    "production", // Preserved
		"TIMEOUT": "30",         // Added
	}
	if !reflect.DeepEqual(merged.Env, expectedEnv) {
		t.Errorf("Env not deep merged correctly:\ngot:  %v\nwant: %v", merged.Env, expectedEnv)
	}

	// Verify headers is deep merged
	expectedHeaders := map[string]string{
		"Authorization": "Bearer new-token", // Updated
		"X-Custom":      "value",            // Preserved
		"X-Request-Id":  "123",              // Added
	}
	if !reflect.DeepEqual(merged.Headers, expectedHeaders) {
		t.Errorf("Headers not deep merged correctly:\ngot:  %v\nwant: %v", merged.Headers, expectedHeaders)
	}
}

// T3.3: Test deep merge for nested structs
func TestMergeServerConfig_NestedStructDeepMerge(t *testing.T) {
	base := &ServerConfig{
		Name: "test-server",
		Isolation: &IsolationConfig{
			Enabled:     BoolPtr(true),
			Image:       "python:3.11",
			NetworkMode: "bridge",
			WorkingDir:  "/app",
			ExtraArgs:   []string{"-v", "/host:/container"},
		},
		OAuth: &OAuthConfig{
			ClientID:     "old-client",
			ClientSecret: "secret",
			Scopes:       []string{"read"},
			PKCEEnabled:  false,
		},
	}

	patch := &ServerConfig{
		Isolation: &IsolationConfig{
			Image: "python:3.12", // Only update image
		},
		OAuth: &OAuthConfig{
			ClientID:    "new-client", // Update client ID
			PKCEEnabled: true,         // Enable PKCE
		},
	}

	merged, _, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify isolation is deep merged
	if merged.Isolation == nil {
		t.Fatal("Isolation should not be nil")
	}
	if merged.Isolation.Image != "python:3.12" {
		t.Errorf("Isolation.Image not updated: got %s, want python:3.12", merged.Isolation.Image)
	}
	if merged.Isolation.NetworkMode != "bridge" {
		t.Errorf("Isolation.NetworkMode not preserved: got %s, want bridge", merged.Isolation.NetworkMode)
	}
	if merged.Isolation.WorkingDir != "/app" {
		t.Errorf("Isolation.WorkingDir not preserved: got %s, want /app", merged.Isolation.WorkingDir)
	}

	// Verify OAuth is deep merged
	if merged.OAuth == nil {
		t.Fatal("OAuth should not be nil")
	}
	if merged.OAuth.ClientID != "new-client" {
		t.Errorf("OAuth.ClientID not updated: got %s, want new-client", merged.OAuth.ClientID)
	}
	if merged.OAuth.ClientSecret != "secret" {
		t.Errorf("OAuth.ClientSecret not preserved: got %s, want secret", merged.OAuth.ClientSecret)
	}
	if !merged.OAuth.PKCEEnabled {
		t.Error("OAuth.PKCEEnabled not updated: should be true")
	}
}

// T3.4: Test array fields are replaced entirely
func TestMergeServerConfig_ArrayFieldsReplaced(t *testing.T) {
	base := &ServerConfig{
		Name: "test-server",
		Args: []string{"--old", "--args", "--here"},
		Isolation: &IsolationConfig{
			Enabled:   BoolPtr(true),
			ExtraArgs: []string{"-v", "/old:/path"},
		},
		OAuth: &OAuthConfig{
			ClientID: "client",
			Scopes:   []string{"read", "write", "admin"},
		},
	}

	patch := &ServerConfig{
		Args: []string{"--new"}, // Completely new args
		Isolation: &IsolationConfig{
			ExtraArgs: []string{"--new-extra"}, // Completely new extra args
		},
		OAuth: &OAuthConfig{
			Scopes: []string{"read"}, // Reduced scopes
		},
	}

	merged, _, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify Args is replaced, not merged
	expectedArgs := []string{"--new"}
	if !reflect.DeepEqual(merged.Args, expectedArgs) {
		t.Errorf("Args not replaced correctly:\ngot:  %v\nwant: %v", merged.Args, expectedArgs)
	}

	// Verify Isolation.ExtraArgs is replaced
	expectedExtraArgs := []string{"--new-extra"}
	if !reflect.DeepEqual(merged.Isolation.ExtraArgs, expectedExtraArgs) {
		t.Errorf("Isolation.ExtraArgs not replaced correctly:\ngot:  %v\nwant: %v", merged.Isolation.ExtraArgs, expectedExtraArgs)
	}

	// Verify OAuth.Scopes is replaced
	expectedScopes := []string{"read"}
	if !reflect.DeepEqual(merged.OAuth.Scopes, expectedScopes) {
		t.Errorf("OAuth.Scopes not replaced correctly:\ngot:  %v\nwant: %v", merged.OAuth.Scopes, expectedScopes)
	}
}

// T3.5: Test omitted fields are preserved
func TestMergeServerConfig_OmittedFieldsPreserved(t *testing.T) {
	base := &ServerConfig{
		Name:       "test-server",
		URL:        "http://example.com",
		Protocol:   "http",
		Command:    "npx",
		Args:       []string{"--verbose"},
		WorkingDir: "/work",
		Env:        map[string]string{"KEY": "value"},
		Headers:    map[string]string{"Auth": "token"},
		Enabled:    true,
		Isolation: &IsolationConfig{
			Enabled: BoolPtr(true),
			Image:   "python:3.11",
		},
		OAuth: &OAuthConfig{
			ClientID: "client",
		},
	}

	// Patch with only Enabled changed
	patch := &ServerConfig{
		Enabled: false,
	}

	merged, diff, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify all base fields are preserved except Enabled
	if merged.URL != base.URL {
		t.Errorf("URL not preserved: got %s, want %s", merged.URL, base.URL)
	}
	if merged.Protocol != base.Protocol {
		t.Errorf("Protocol not preserved: got %s, want %s", merged.Protocol, base.Protocol)
	}
	if merged.Command != base.Command {
		t.Errorf("Command not preserved: got %s, want %s", merged.Command, base.Command)
	}
	if !reflect.DeepEqual(merged.Args, base.Args) {
		t.Errorf("Args not preserved: got %v, want %v", merged.Args, base.Args)
	}
	if !reflect.DeepEqual(merged.Env, base.Env) {
		t.Errorf("Env not preserved: got %v, want %v", merged.Env, base.Env)
	}
	if merged.Isolation == nil {
		t.Error("Isolation should be preserved")
	}
	if merged.OAuth == nil {
		t.Error("OAuth should be preserved")
	}

	// Verify Enabled was changed
	if merged.Enabled != false {
		t.Error("Enabled should be changed to false")
	}

	// Verify diff only shows enabled change
	if diff != nil && len(diff.Modified) != 1 {
		t.Errorf("Expected 1 modified field, got %d: %+v", len(diff.Modified), diff.Modified)
	}
}

// T3.6: Test explicit null removes field
func TestMergeServerConfig_ExplicitNullRemovesField(t *testing.T) {
	base := &ServerConfig{
		Name: "test-server",
		Isolation: &IsolationConfig{
			Enabled: BoolPtr(true),
			Image:   "python:3.11",
		},
		OAuth: &OAuthConfig{
			ClientID: "client",
		},
	}

	patch := &ServerConfig{}

	opts := DefaultMergeOptions()
	opts = opts.WithRemoveMarker("isolation")

	merged, diff, err := MergeServerConfig(base, patch, opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify isolation is removed
	if merged.Isolation != nil {
		t.Error("Isolation should be removed with explicit null marker")
	}

	// Verify OAuth is preserved
	if merged.OAuth == nil {
		t.Error("OAuth should be preserved")
	}

	// Verify diff captures removal
	if diff == nil {
		t.Fatal("Expected diff to be generated")
	}
	found := false
	for _, field := range diff.Removed {
		if field == "isolation" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Isolation removal not captured in diff")
	}
}

// T3.7: Test immutable fields cannot be changed
func TestMergeServerConfig_ImmutableFields(t *testing.T) {
	created := time.Now().Add(-24 * time.Hour)
	base := &ServerConfig{
		Name:    "test-server",
		Created: created,
	}

	// Try to change name
	patch := &ServerConfig{
		Name: "new-name",
	}

	_, _, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	if err == nil {
		t.Fatal("Expected error when changing immutable field 'name'")
	}
	if !errors.Is(err, ErrImmutableField) {
		t.Errorf("Expected ErrImmutableField, got: %v", err)
	}

	// Try to change created
	patch = &ServerConfig{
		Created: time.Now(),
	}

	_, _, err = MergeServerConfig(base, patch, DefaultMergeOptions())
	if err == nil {
		t.Fatal("Expected error when changing immutable field 'created'")
	}
	if !errors.Is(err, ErrImmutableField) {
		t.Errorf("Expected ErrImmutableField, got: %v", err)
	}
}

// T3.8: Test ConfigDiff correctly captures all changes
func TestMergeServerConfig_ConfigDiffCapture(t *testing.T) {
	base := &ServerConfig{
		Name:     "test-server",
		URL:      "http://old.com",
		Protocol: "http",
		Env:      map[string]string{"OLD": "value"},
		Isolation: &IsolationConfig{
			Enabled: BoolPtr(true),
			Image:   "python:3.11",
		},
	}

	patch := &ServerConfig{
		URL:      "http://new.com",
		Protocol: "sse",
		Env:      map[string]string{"NEW": "value"},
		Isolation: &IsolationConfig{
			Image: "python:3.12",
		},
	}

	opts := DefaultMergeOptions()
	opts.GenerateDiff = true

	_, diff, err := MergeServerConfig(base, patch, opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if diff == nil {
		t.Fatal("Expected diff to be generated")
	}

	// Check modified fields
	if _, ok := diff.Modified["url"]; !ok {
		t.Error("URL change not captured in diff")
	}
	if _, ok := diff.Modified["protocol"]; !ok {
		t.Error("Protocol change not captured in diff")
	}
	if _, ok := diff.Modified["env"]; !ok {
		t.Error("Env change not captured in diff")
	}
	if _, ok := diff.Modified["isolation"]; !ok {
		t.Error("Isolation change not captured in diff")
	}

	// Verify timestamp is set
	if diff.Timestamp.IsZero() {
		t.Error("Diff timestamp should be set")
	}
}

// T3.9: Test thread safety - merge doesn't modify inputs
func TestMergeServerConfig_ThreadSafety(t *testing.T) {
	base := &ServerConfig{
		Name: "test-server",
		URL:  "http://base.com",
		Env: map[string]string{
			"KEY": "base-value",
		},
		Isolation: &IsolationConfig{
			Image: "base-image",
		},
	}

	patch := &ServerConfig{
		URL: "http://patch.com",
		Env: map[string]string{
			"KEY": "patch-value",
		},
		Isolation: &IsolationConfig{
			Image: "patch-image",
		},
	}

	// Save original values
	baseURL := base.URL
	baseEnvKey := base.Env["KEY"]
	baseImage := base.Isolation.Image
	patchURL := patch.URL
	patchEnvKey := patch.Env["KEY"]
	patchImage := patch.Isolation.Image

	merged, _, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify base is not modified
	if base.URL != baseURL {
		t.Errorf("Base URL was modified: got %s, want %s", base.URL, baseURL)
	}
	if base.Env["KEY"] != baseEnvKey {
		t.Errorf("Base Env was modified: got %s, want %s", base.Env["KEY"], baseEnvKey)
	}
	if base.Isolation.Image != baseImage {
		t.Errorf("Base Isolation was modified: got %s, want %s", base.Isolation.Image, baseImage)
	}

	// Verify patch is not modified
	if patch.URL != patchURL {
		t.Errorf("Patch URL was modified: got %s, want %s", patch.URL, patchURL)
	}
	if patch.Env["KEY"] != patchEnvKey {
		t.Errorf("Patch Env was modified: got %s, want %s", patch.Env["KEY"], patchEnvKey)
	}
	if patch.Isolation.Image != patchImage {
		t.Errorf("Patch Isolation was modified: got %s, want %s", patch.Isolation.Image, patchImage)
	}

	// Verify merged has the right values
	if merged.URL != patchURL {
		t.Errorf("Merged URL incorrect: got %s, want %s", merged.URL, patchURL)
	}

	// Modify merged and verify it doesn't affect base or patch
	merged.URL = "http://modified.com"
	merged.Env["KEY"] = "modified-value"

	if base.URL != baseURL {
		t.Error("Modifying merged affected base URL")
	}
	if patch.URL != patchURL {
		t.Error("Modifying merged affected patch URL")
	}
}

// T3.10: Edge case - merge with empty base (new server)
func TestMergeServerConfig_EmptyBase(t *testing.T) {
	patch := &ServerConfig{
		Name:     "new-server",
		URL:      "http://new.com",
		Protocol: "http",
		Enabled:  true,
		Isolation: &IsolationConfig{
			Enabled: BoolPtr(true),
			Image:   "python:3.11",
		},
	}

	merged, diff, err := MergeServerConfig(nil, patch, DefaultMergeOptions())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify merged equals patch
	if merged.Name != patch.Name {
		t.Errorf("Name mismatch: got %s, want %s", merged.Name, patch.Name)
	}
	if merged.URL != patch.URL {
		t.Errorf("URL mismatch: got %s, want %s", merged.URL, patch.URL)
	}
	if merged.Isolation == nil {
		t.Error("Isolation should be copied from patch")
	}

	// Diff should be empty (no changes tracked for new config)
	if diff != nil && !diff.IsEmpty() {
		t.Logf("Diff for new config: %+v", diff)
	}
}

// T3.11: Edge case - merge with empty patch (no changes)
func TestMergeServerConfig_EmptyPatch(t *testing.T) {
	base := &ServerConfig{
		Name:     "test-server",
		URL:      "http://base.com",
		Protocol: "http",
		Env:      map[string]string{"KEY": "value"},
		Isolation: &IsolationConfig{
			Enabled: BoolPtr(true),
			Image:   "python:3.11",
		},
	}

	merged, diff, err := MergeServerConfig(base, nil, DefaultMergeOptions())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify merged equals base
	if merged.Name != base.Name {
		t.Errorf("Name mismatch: got %s, want %s", merged.Name, base.Name)
	}
	if merged.URL != base.URL {
		t.Errorf("URL mismatch: got %s, want %s", merged.URL, base.URL)
	}

	// Diff should be nil (no changes)
	if diff != nil {
		t.Errorf("Expected nil diff for empty patch, got: %+v", diff)
	}
}

// T3.12: Edge case - complex nested merge
func TestMergeServerConfig_ComplexNestedMerge(t *testing.T) {
	base := &ServerConfig{
		Name:       "complex-server",
		URL:        "http://base.com",
		Protocol:   "http",
		Command:    "npx",
		Args:       []string{"--old", "--args"},
		WorkingDir: "/base/dir",
		Env: map[string]string{
			"API_KEY": "secret",
			"DEBUG":   "false",
			"MODE":    "production",
		},
		Headers: map[string]string{
			"Authorization": "Bearer old",
			"X-Custom":      "value",
		},
		Enabled:     true,
		Quarantined: true,
		Isolation: &IsolationConfig{
			Enabled:     BoolPtr(true),
			Image:       "python:3.11",
			NetworkMode: "bridge",
			WorkingDir:  "/app",
			ExtraArgs:   []string{"-v", "/host:/container"},
			LogDriver:   "json-file",
		},
		OAuth: &OAuthConfig{
			ClientID:     "old-client",
			ClientSecret: "secret",
			Scopes:       []string{"read", "write"},
			PKCEEnabled:  false,
			ExtraParams:  map[string]string{"audience": "api.old.com"},
		},
	}

	patch := &ServerConfig{
		URL:         "http://new.com",
		Args:        []string{"--new"},
		Enabled:     false,
		Quarantined: false,
		Env: map[string]string{
			"DEBUG":   "true",
			"TIMEOUT": "30",
		},
		Headers: map[string]string{
			"Authorization": "Bearer new",
		},
		Isolation: &IsolationConfig{
			Image:     "python:3.12",
			ExtraArgs: []string{"--new-extra"},
		},
		OAuth: &OAuthConfig{
			ClientID:    "new-client",
			PKCEEnabled: true,
			ExtraParams: map[string]string{
				"audience": "api.new.com",
				"resource": "https://api.new.com",
			},
		},
	}

	merged, diff, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify scalar field updates
	if merged.URL != "http://new.com" {
		t.Errorf("URL not updated")
	}
	if merged.Enabled != false {
		t.Errorf("Enabled not updated")
	}
	if merged.Quarantined != false {
		t.Errorf("Quarantined not updated")
	}

	// Verify scalar field preservation
	if merged.Protocol != "http" {
		t.Errorf("Protocol not preserved")
	}
	if merged.Command != "npx" {
		t.Errorf("Command not preserved")
	}
	if merged.WorkingDir != "/base/dir" {
		t.Errorf("WorkingDir not preserved")
	}

	// Verify array replacement
	if !reflect.DeepEqual(merged.Args, []string{"--new"}) {
		t.Errorf("Args not replaced: got %v", merged.Args)
	}

	// Verify map deep merge
	if merged.Env["API_KEY"] != "secret" {
		t.Errorf("Env.API_KEY not preserved")
	}
	if merged.Env["DEBUG"] != "true" {
		t.Errorf("Env.DEBUG not updated")
	}
	if merged.Env["MODE"] != "production" {
		t.Errorf("Env.MODE not preserved")
	}
	if merged.Env["TIMEOUT"] != "30" {
		t.Errorf("Env.TIMEOUT not added")
	}

	if merged.Headers["Authorization"] != "Bearer new" {
		t.Errorf("Headers.Authorization not updated")
	}
	if merged.Headers["X-Custom"] != "value" {
		t.Errorf("Headers.X-Custom not preserved")
	}

	// Verify nested struct deep merge - Isolation
	if merged.Isolation == nil {
		t.Fatal("Isolation should not be nil")
	}
	if merged.Isolation.Image != "python:3.12" {
		t.Errorf("Isolation.Image not updated")
	}
	if merged.Isolation.NetworkMode != "bridge" {
		t.Errorf("Isolation.NetworkMode not preserved")
	}
	if merged.Isolation.WorkingDir != "/app" {
		t.Errorf("Isolation.WorkingDir not preserved")
	}
	if merged.Isolation.LogDriver != "json-file" {
		t.Errorf("Isolation.LogDriver not preserved")
	}
	if !reflect.DeepEqual(merged.Isolation.ExtraArgs, []string{"--new-extra"}) {
		t.Errorf("Isolation.ExtraArgs not replaced: got %v", merged.Isolation.ExtraArgs)
	}

	// Verify nested struct deep merge - OAuth
	if merged.OAuth == nil {
		t.Fatal("OAuth should not be nil")
	}
	if merged.OAuth.ClientID != "new-client" {
		t.Errorf("OAuth.ClientID not updated")
	}
	if merged.OAuth.ClientSecret != "secret" {
		t.Errorf("OAuth.ClientSecret not preserved")
	}
	if !merged.OAuth.PKCEEnabled {
		t.Errorf("OAuth.PKCEEnabled not updated")
	}
	if merged.OAuth.ExtraParams["audience"] != "api.new.com" {
		t.Errorf("OAuth.ExtraParams.audience not updated")
	}
	if merged.OAuth.ExtraParams["resource"] != "https://api.new.com" {
		t.Errorf("OAuth.ExtraParams.resource not added")
	}

	// Verify diff captured expected changes
	if diff == nil {
		t.Fatal("Expected diff to be generated")
	}
	if len(diff.Modified) < 5 {
		t.Errorf("Expected at least 5 modified fields, got %d", len(diff.Modified))
	}

	t.Logf("Complex merge successful with %d modifications", len(diff.Modified))
}

// Test MergeMap function directly
func TestMergeMap(t *testing.T) {
	tests := []struct {
		name     string
		dst      map[string]string
		src      map[string]string
		expected map[string]string
	}{
		{
			name:     "both nil",
			dst:      nil,
			src:      nil,
			expected: nil,
		},
		{
			name:     "dst nil",
			dst:      nil,
			src:      map[string]string{"a": "1"},
			expected: map[string]string{"a": "1"},
		},
		{
			name:     "src nil",
			dst:      map[string]string{"a": "1"},
			src:      nil,
			expected: map[string]string{"a": "1"},
		},
		{
			name:     "merge with update",
			dst:      map[string]string{"a": "1", "b": "2"},
			src:      map[string]string{"b": "3", "c": "4"},
			expected: map[string]string{"a": "1", "b": "3", "c": "4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeMap(tt.dst, tt.src)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("MergeMap() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// Test MergeIsolationConfig function directly
func TestMergeIsolationConfig(t *testing.T) {
	base := &IsolationConfig{
		Enabled:     BoolPtr(true),
		Image:       "python:3.11",
		NetworkMode: "bridge",
		ExtraArgs:   []string{"--old"},
	}

	patch := &IsolationConfig{
		Image:     "python:3.12",
		ExtraArgs: []string{"--new"},
	}

	result := MergeIsolationConfig(base, patch, false)

	if result.Image != "python:3.12" {
		t.Errorf("Image not updated: got %s", result.Image)
	}
	if result.NetworkMode != "bridge" {
		t.Errorf("NetworkMode not preserved: got %s", result.NetworkMode)
	}
	if !reflect.DeepEqual(result.ExtraArgs, []string{"--new"}) {
		t.Errorf("ExtraArgs not replaced: got %v", result.ExtraArgs)
	}
}

// Test MergeOAuthConfig function directly
func TestMergeOAuthConfig(t *testing.T) {
	base := &OAuthConfig{
		ClientID:     "old-client",
		ClientSecret: "secret",
		Scopes:       []string{"read", "write"},
		PKCEEnabled:  false,
	}

	patch := &OAuthConfig{
		ClientID:    "new-client",
		PKCEEnabled: true,
	}

	result := MergeOAuthConfig(base, patch, false)

	if result.ClientID != "new-client" {
		t.Errorf("ClientID not updated: got %s", result.ClientID)
	}
	if result.ClientSecret != "secret" {
		t.Errorf("ClientSecret not preserved: got %s", result.ClientSecret)
	}
	if !result.PKCEEnabled {
		t.Errorf("PKCEEnabled not updated")
	}
	// Scopes should be preserved since patch.Scopes is nil
	if !reflect.DeepEqual(result.Scopes, []string{"read", "write"}) {
		t.Errorf("Scopes not preserved: got %v", result.Scopes)
	}
}

// Test that Updated timestamp is always set
func TestMergeServerConfig_UpdatedTimestamp(t *testing.T) {
	oldUpdated := time.Now().Add(-1 * time.Hour)
	base := &ServerConfig{
		Name:    "test-server",
		Updated: oldUpdated,
	}

	patch := &ServerConfig{
		URL: "http://new.com",
	}

	before := time.Now()
	merged, _, err := MergeServerConfig(base, patch, DefaultMergeOptions())
	after := time.Now()

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if merged.Updated.Before(before) || merged.Updated.After(after) {
		t.Errorf("Updated timestamp not set correctly: got %v, expected between %v and %v",
			merged.Updated, before, after)
	}
}

// Test ConfigDiff.IsEmpty
func TestConfigDiff_IsEmpty(t *testing.T) {
	// Nil diff
	var nilDiff *ConfigDiff
	if !nilDiff.IsEmpty() {
		t.Error("Nil diff should be empty")
	}

	// Empty diff
	emptyDiff := &ConfigDiff{}
	if !emptyDiff.IsEmpty() {
		t.Error("Empty diff should be empty")
	}

	// Diff with modified
	diffWithMod := &ConfigDiff{
		Modified: map[string]FieldChange{"url": {}},
	}
	if diffWithMod.IsEmpty() {
		t.Error("Diff with modified should not be empty")
	}

	// Diff with added
	diffWithAdd := &ConfigDiff{
		Added: []string{"field"},
	}
	if diffWithAdd.IsEmpty() {
		t.Error("Diff with added should not be empty")
	}

	// Diff with removed
	diffWithRem := &ConfigDiff{
		Removed: []string{"field"},
	}
	if diffWithRem.IsEmpty() {
		t.Error("Diff with removed should not be empty")
	}
}
