package config

import (
	"encoding/json"
	"testing"
	"time"
)

func fullServerFixture(t *testing.T) *ServerConfig {
	t.Helper()
	var sc ServerConfig
	if err := json.Unmarshal([]byte(`{
		"name":"full","url":"http://127.0.0.1:9/mcp","protocol":"http","command":"node",
		"args":["a","b"],"working_dir":"/tmp","env":{"K":"v"},"headers":{"H":"v"},
		"oauth":{"client_id":"c","scopes":["s"]},"enabled":true,"quarantined":false,
		"trust_mode":"scan","shared":true,"isolation":{"image":"img"},
		"reconnect_on_use":true,"expose_prompts":true,"forward_headers":["X-Tenant"],
		"launcher_wait_timeout":"5s","health_check_interval":"30s","tool_discovery_interval":"60s",
		"init_timeout":"10s","max_concurrent_requests":3,"queue_size":4,"queue_timeout":"7s",
		"toon_output":"on","enabled_tools":["e"],"disabled_tools":["d"],
		"auto_approve_tool_changes":true,"source_registry_id":"reg","source_registry_provenance":"p",
		"auth_broker":{"mode":"oauth_connect","token_endpoint":"https://idp.example/token","authorization_endpoint":"https://idp.example/authorize"}
	}`), &sc); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return &sc
}

func TestServerEntryEqual(t *testing.T) {
	base := fullServerFixture(t)

	if !ServerEntryEqual(base, CopyServerConfig(base)) {
		t.Fatal("a deep copy must compare equal")
	}

	stamped := CopyServerConfig(base)
	stamped.Created = time.Now()
	stamped.Updated = time.Now()
	if !ServerEntryEqual(base, stamped) {
		t.Fatal("Created/Updated must be ignored")
	}

	cases := map[string]func(sc *ServerConfig){
		"args":        func(sc *ServerConfig) { sc.Args = []string{"a"} },
		"env":         func(sc *ServerConfig) { sc.Env["K"] = "w" },
		"enabled":     func(sc *ServerConfig) { sc.Enabled = false },
		"quarantined": func(sc *ServerConfig) { sc.Quarantined = true },
		"explicit":    func(sc *ServerConfig) { sc.MarkQuarantineExplicitlySet(false) },
		"shared":      func(sc *ServerConfig) { sc.Shared = false },
		"auth_broker": func(sc *ServerConfig) { sc.AuthBroker = nil },
		"forward":     func(sc *ServerConfig) { sc.ForwardHeaders = nil },
		"trust_mode":  func(sc *ServerConfig) { sc.TrustMode = "manual" },
		"toon":        func(sc *ServerConfig) { sc.ToonOutput = "off" },
		"disabled":    func(sc *ServerConfig) { sc.DisabledTools = []string{"x"} },
		"expose":      func(sc *ServerConfig) { f := false; sc.ExposePrompts = &f },
	}
	for name, mutate := range cases {
		changed := CopyServerConfig(base)
		mutate(changed)
		if ServerEntryEqual(base, changed) {
			t.Errorf("%s: a changed field must compare unequal", name)
		}
	}

	if !ServerEntryEqual(nil, nil) || ServerEntryEqual(base, nil) {
		t.Fatal("nil handling")
	}
}

// CopyServerConfig is the deep copy every commit starts from (Spec 116): a
// field it drops is a field every commit silently resets.
func TestCopyServerConfigKeepsEverySerializedField(t *testing.T) {
	base := fullServerFixture(t)
	a, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(CopyServerConfig(base))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("CopyServerConfig dropped a field:\n src: %s\ncopy: %s", a, b)
	}
}
