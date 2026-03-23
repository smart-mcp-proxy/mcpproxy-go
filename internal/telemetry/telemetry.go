package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// HeartbeatPayload is the anonymous telemetry payload sent periodically.
type HeartbeatPayload struct {
	AnonymousID          string `json:"anonymous_id"`
	Version              string `json:"version"`
	Edition              string `json:"edition"`
	OS                   string `json:"os"`
	Arch                 string `json:"arch"`
	GoVersion            string `json:"go_version"`
	ServerCount          int    `json:"server_count"`
	ConnectedServerCount int    `json:"connected_server_count"`
	ToolCount            int    `json:"tool_count"`
	UptimeHours          int    `json:"uptime_hours"`
	RoutingMode          string `json:"routing_mode"`
	QuarantineEnabled    bool   `json:"quarantine_enabled"`
	Timestamp            string `json:"timestamp"`
}

// RuntimeStats is an interface to decouple from the runtime package.
type RuntimeStats interface {
	GetServerCount() int
	GetConnectedServerCount() int
	GetToolCount() int
	GetRoutingMode() string
	IsQuarantineEnabled() bool
}

// Service manages anonymous telemetry heartbeats and feedback submission.
type Service struct {
	config    *config.Config
	cfgPath   string
	version   string
	edition   string
	endpoint  string
	logger    *zap.Logger
	stats     RuntimeStats
	startTime time.Time
	client    *http.Client

	// Feedback rate limiter (max 5 per hour)
	feedbackLimiter *RateLimiter

	// For testing: override initial delay and heartbeat interval
	initialDelay      time.Duration
	heartbeatInterval time.Duration
}

// New creates a new telemetry service.
func New(cfg *config.Config, cfgPath, version, edition string, logger *zap.Logger) *Service {
	return &Service{
		config:            cfg,
		cfgPath:           cfgPath,
		version:           version,
		edition:           edition,
		endpoint:          cfg.GetTelemetryEndpoint(),
		logger:            logger,
		startTime:         time.Now(),
		client:            &http.Client{Timeout: 10 * time.Second},
		feedbackLimiter:   NewRateLimiter(5),
		initialDelay:      5 * time.Minute,
		heartbeatInterval: 24 * time.Hour,
	}
}

// SetRuntimeStats sets the runtime stats provider (called after runtime is fully initialized).
func (s *Service) SetRuntimeStats(stats RuntimeStats) {
	s.stats = stats
}

// Start begins the telemetry heartbeat loop. This is a blocking call; run in a goroutine.
func (s *Service) Start(ctx context.Context) {
	// Skip if telemetry is disabled
	if !s.config.IsTelemetryEnabled() {
		s.logger.Info("Telemetry disabled by configuration")
		return
	}

	// Skip for non-semver (dev) builds
	if !isValidSemver(s.version) {
		s.logger.Info("Telemetry disabled for non-semver version",
			zap.String("version", s.version))
		return
	}

	// Ensure anonymous ID exists
	s.ensureAnonymousID()

	s.logger.Info("Telemetry service starting",
		zap.String("endpoint", s.endpoint),
		zap.Duration("initial_delay", s.initialDelay),
		zap.Duration("interval", s.heartbeatInterval))

	// Wait initial delay (avoid noise from short-lived processes)
	select {
	case <-time.After(s.initialDelay):
	case <-ctx.Done():
		s.logger.Info("Telemetry service stopped during initial delay")
		return
	}

	// Send first heartbeat
	s.sendHeartbeat()

	// Then send every interval
	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.sendHeartbeat()
		case <-ctx.Done():
			s.logger.Info("Telemetry service stopped")
			return
		}
	}
}

func (s *Service) sendHeartbeat() {
	payload := s.buildHeartbeat()

	data, err := json.Marshal(payload)
	if err != nil {
		s.logger.Debug("Failed to marshal heartbeat", zap.Error(err))
		return
	}

	url := s.endpoint + "/heartbeat"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		s.logger.Debug("Failed to create heartbeat request", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		s.logger.Debug("Failed to send heartbeat", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	s.logger.Debug("Heartbeat sent", zap.Int("status", resp.StatusCode))
}

func (s *Service) buildHeartbeat() HeartbeatPayload {
	payload := HeartbeatPayload{
		AnonymousID: s.config.GetAnonymousID(),
		Version:     s.version,
		Edition:     s.edition,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		GoVersion:   runtime.Version(),
		UptimeHours: int(time.Since(s.startTime).Hours()),
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	if s.stats != nil {
		payload.ServerCount = s.stats.GetServerCount()
		payload.ConnectedServerCount = s.stats.GetConnectedServerCount()
		payload.ToolCount = s.stats.GetToolCount()
		payload.RoutingMode = s.stats.GetRoutingMode()
		payload.QuarantineEnabled = s.stats.IsQuarantineEnabled()
	}

	return payload
}

func (s *Service) ensureAnonymousID() {
	if s.config.GetAnonymousID() != "" {
		return
	}

	// Generate a new UUIDv4
	newID := uuid.New().String()

	// Persist to config
	if s.config.Telemetry == nil {
		s.config.Telemetry = &config.TelemetryConfig{}
	}
	s.config.Telemetry.AnonymousID = newID

	// Save config to disk
	if s.cfgPath != "" {
		if err := config.SaveConfig(s.config, s.cfgPath); err != nil {
			s.logger.Warn("Failed to persist anonymous telemetry ID",
				zap.Error(err))
		} else {
			s.logger.Info("Generated and persisted anonymous telemetry ID",
				zap.String("id", newID))
		}
	}
}

// isValidSemver checks if the version string is a valid semantic version.
func isValidSemver(v string) bool {
	if v == "" {
		return false
	}
	// semver.IsValid requires "v" prefix
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return semver.IsValid(v)
}
