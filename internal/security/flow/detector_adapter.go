package flow

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/security"

// DetectorAdapter adapts security.Detector to the SensitiveDataDetector interface.
type DetectorAdapter struct {
	detector *security.Detector
}

// NewDetectorAdapter wraps a security.Detector for use with FlowService.
func NewDetectorAdapter(d *security.Detector) *DetectorAdapter {
	return &DetectorAdapter{detector: d}
}

// Scan delegates to the underlying detector and converts the result.
func (a *DetectorAdapter) Scan(arguments, response string) *DetectionResult {
	r := a.detector.Scan(arguments, response)
	if r == nil {
		return &DetectionResult{Detected: false}
	}

	result := &DetectionResult{
		Detected: r.Detected,
	}

	for _, d := range r.Detections {
		result.Detections = append(result.Detections, DetectionEntry{
			Type:     d.Type,
			Category: string(d.Category),
			Severity: string(d.Severity),
			Location: d.Location,
		})
	}

	return result
}
