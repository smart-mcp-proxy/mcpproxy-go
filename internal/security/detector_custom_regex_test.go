package security

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// sensitive_data_detection.custom_patterns comes straight from the user's
// config file; one bad regex must not take down NewDetector (startup, config
// reload, credential screen) or the other custom patterns.
func TestNewDetector_InvalidCustomRegexIsSkipped(t *testing.T) {
	cfg := config.DefaultSensitiveDataDetectionConfig()
	cfg.CustomPatterns = []config.CustomPattern{
		{Name: "broken", Regex: "("},
		{Name: "ticket", Regex: `TICKET-\d{6}`},
	}

	var d *Detector
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("NewDetector panicked on an invalid custom regex: %v", r)
			}
		}()
		d = NewDetector(cfg)
	}()

	res := d.Scan(`{"note":"see TICKET-123456"}`, "")
	found := false
	for _, det := range res.Detections {
		if det.Type == "ticket" {
			found = true
		}
	}
	if !found {
		t.Errorf("valid custom pattern next to the broken one was not applied: %+v", res.Detections)
	}
}
