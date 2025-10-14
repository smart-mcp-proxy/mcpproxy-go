package configsvc

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

func TestNewService(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		DataDir: "/tmp/test",
		Servers: []*config.ServerConfig{
			{Name: "test-server", Enabled: true},
		},
	}

	svc := NewService(cfg, "/tmp/config.json", zap.NewNop())
	if svc == nil {
		t.Fatal("Expected non-nil service")
	}

	snapshot := svc.Current()
	if snapshot == nil {
		t.Fatal("Expected non-nil snapshot")
	}

	if snapshot.Version != 0 {
		t.Errorf("Expected version 0, got %d", snapshot.Version)
	}

	if snapshot.Path != "/tmp/config.json" {
		t.Errorf("Expected path /tmp/config.json, got %s", snapshot.Path)
	}

	if snapshot.ServerCount() != 1 {
		t.Errorf("Expected 1 server, got %d", snapshot.ServerCount())
	}
}

func TestService_Current_LockFree(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
	}

	svc := NewService(cfg, "/tmp/config.json", zap.NewNop())

	// Multiple concurrent reads should not block
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				snapshot := svc.Current()
				if snapshot == nil {
					t.Error("Got nil snapshot")
				}
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestService_Update(t *testing.T) {
	cfg := &config.Config{
		Listen:  "127.0.0.1:8080",
		Servers: []*config.ServerConfig{},
	}

	svc := NewService(cfg, "/tmp/config.json", zap.NewNop())

	// Update with new config
	newCfg := &config.Config{
		Listen: "127.0.0.1:9090",
		Servers: []*config.ServerConfig{
			{Name: "new-server", Enabled: true},
		},
	}

	err := svc.Update(newCfg, UpdateTypeModify, "test")
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify update
	snapshot := svc.Current()
	if snapshot.Version != 1 {
		t.Errorf("Expected version 1, got %d", snapshot.Version)
	}

	if snapshot.Config.Listen != "127.0.0.1:9090" {
		t.Errorf("Expected listen 127.0.0.1:9090, got %s", snapshot.Config.Listen)
	}

	if snapshot.ServerCount() != 1 {
		t.Errorf("Expected 1 server, got %d", snapshot.ServerCount())
	}
}

func TestService_Subscribe(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
	}

	svc := NewService(cfg, "/tmp/config.json", zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updateCh := svc.Subscribe(ctx)

	// Should receive initial snapshot
	select {
	case update := <-updateCh:
		if update.Type != UpdateTypeInit {
			t.Errorf("Expected UpdateTypeInit, got %s", update.Type)
		}
		if update.Snapshot.Version != 0 {
			t.Errorf("Expected version 0, got %d", update.Snapshot.Version)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Did not receive initial snapshot")
	}

	// Perform an update
	newCfg := &config.Config{
		Listen: "127.0.0.1:9090",
	}

	err := svc.Update(newCfg, UpdateTypeModify, "test")
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Should receive update notification
	select {
	case update := <-updateCh:
		if update.Type != UpdateTypeModify {
			t.Errorf("Expected UpdateTypeModify, got %s", update.Type)
		}
		if update.Snapshot.Version != 1 {
			t.Errorf("Expected version 1, got %d", update.Snapshot.Version)
		}
		if update.Source != "test" {
			t.Errorf("Expected source 'test', got %s", update.Source)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Did not receive update notification")
	}
}

func TestService_MultipleSubscribers(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
	}

	svc := NewService(cfg, "/tmp/config.json", zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create multiple subscribers
	sub1 := svc.Subscribe(ctx)
	sub2 := svc.Subscribe(ctx)
	sub3 := svc.Subscribe(ctx)

	// Drain initial snapshots
	<-sub1
	<-sub2
	<-sub3

	// Perform an update
	newCfg := &config.Config{
		Listen: "127.0.0.1:9090",
	}

	err := svc.Update(newCfg, UpdateTypeModify, "test")
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// All subscribers should receive the update
	timeout := time.After(1 * time.Second)

	select {
	case <-sub1:
	case <-timeout:
		t.Error("Subscriber 1 did not receive update")
	}

	select {
	case <-sub2:
	case <-timeout:
		t.Error("Subscriber 2 did not receive update")
	}

	select {
	case <-sub3:
	case <-timeout:
		t.Error("Subscriber 3 did not receive update")
	}
}

func TestSnapshot_Clone(t *testing.T) {
	original := &config.Config{
		Listen:  "127.0.0.1:8080",
		DataDir: "/tmp/test",
		Servers: []*config.ServerConfig{
			{
				Name:    "test-server",
				Enabled: true,
				Headers: map[string]string{"Auth": "Bearer token"},
				Env:     map[string]string{"API_KEY": "secret"},
				Args:    []string{"arg1", "arg2"},
			},
		},
	}

	snapshot := &Snapshot{
		Config:    original,
		Path:      "/tmp/config.json",
		Version:   1,
		Timestamp: time.Now(),
	}

	cloned := snapshot.Clone()

	// Verify deep copy
	if cloned == original {
		t.Error("Clone returned same pointer, expected deep copy")
	}

	// Modify original
	original.Listen = "127.0.0.1:9090"
	original.Servers[0].Name = "modified"
	original.Servers[0].Headers["Auth"] = "Bearer newtoken"

	// Cloned should be unchanged
	if cloned.Listen != "127.0.0.1:8080" {
		t.Errorf("Clone was mutated: expected 127.0.0.1:8080, got %s", cloned.Listen)
	}

	if cloned.Servers[0].Name != "test-server" {
		t.Errorf("Clone server was mutated: expected test-server, got %s", cloned.Servers[0].Name)
	}

	if cloned.Servers[0].Headers["Auth"] != "Bearer token" {
		t.Errorf("Clone headers were mutated: expected 'Bearer token', got %s", cloned.Servers[0].Headers["Auth"])
	}
}

func TestSnapshot_GetServer(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "server1", Enabled: true},
			{Name: "server2", Enabled: false},
		},
	}

	snapshot := &Snapshot{
		Config:    cfg,
		Path:      "/tmp/config.json",
		Version:   1,
		Timestamp: time.Now(),
	}

	// Test existing server
	srv := snapshot.GetServer("server1")
	if srv == nil {
		t.Fatal("Expected to find server1")
	}

	if srv.Name != "server1" {
		t.Errorf("Expected server1, got %s", srv.Name)
	}

	// Test non-existent server
	srv = snapshot.GetServer("nonexistent")
	if srv != nil {
		t.Error("Expected nil for non-existent server")
	}

	// Verify returned server is a copy
	srv = snapshot.GetServer("server1")
	srv.Name = "modified"

	// Original should be unchanged
	if cfg.Servers[0].Name != "server1" {
		t.Error("GetServer did not return a copy, original was mutated")
	}
}

func TestSnapshot_ServerNames(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "server1"},
			{Name: "server2"},
			{Name: "server3"},
		},
	}

	snapshot := &Snapshot{
		Config:    cfg,
		Version:   1,
		Timestamp: time.Now(),
	}

	names := snapshot.ServerNames()
	if len(names) != 3 {
		t.Errorf("Expected 3 names, got %d", len(names))
	}

	expected := map[string]bool{
		"server1": true,
		"server2": true,
		"server3": true,
	}

	for _, name := range names {
		if !expected[name] {
			t.Errorf("Unexpected server name: %s", name)
		}
	}
}

func TestService_Close(t *testing.T) {
	cfg := &config.Config{
		Listen: "127.0.0.1:8080",
	}

	svc := NewService(cfg, "/tmp/config.json", zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create subscriber
	sub := svc.Subscribe(ctx)

	// Drain initial snapshot
	<-sub

	// Close service
	svc.Close()

	// Channel should be closed
	select {
	case _, ok := <-sub:
		if ok {
			t.Error("Expected subscriber channel to be closed")
		}
	case <-time.After(1 * time.Second):
		t.Error("Subscriber channel not closed after service close")
	}
}
