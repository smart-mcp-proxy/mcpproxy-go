package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// UpdateFailureRequest is the entire wire body of
// POST /api/v1/telemetry/update-failure (spec 095 FR-009). One field, one
// closed enum — the stage is the ONLY failure information that ever leaves the
// tray, so the decode is strict on both unknown fields and trailing values.
type UpdateFailureRequest struct {
	// Stage is the failure stage of the update session.
	Stage string `json:"stage" enums:"appcast,download,install,other"`
}

// updateFailureStages is the closed set accepted by the endpoint. Anything else
// is a 400 that records nothing.
var updateFailureStages = map[string]struct{}{
	"appcast":  {},
	"download": {},
	"install":  {},
	"other":    {},
}

// maxUpdateFailureBody caps the request body. The valid body is ~25 bytes;
// the cap only exists so a hostile caller cannot stream into the decoder.
const maxUpdateFailureBody = 1 << 10

// handleRecordUpdateFailure godoc
// @Summary     Record a desktop auto-update failure occurrence (Spec 095)
// @Description Records one terminal update-session failure, identified only by its
// @Description stage (appcast, download, install, other). The body carries no error
// @Description text, URL, or version — the stage is the only value transmitted.
// @Description Returns 204 both when the occurrence was durably persisted and when
// @Description telemetry is inactive at event time (config opt-out, environment
// @Description opt-out, CI, or dev build), in which case nothing is recorded.
// @Description Callers cannot and need not distinguish the two.
// @Tags        telemetry
// @Accept      json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       body body UpdateFailureRequest true "Update failure stage"
// @Success     204 "Accepted (recorded, or a deliberate no-op while telemetry is inactive)"
// @Failure     400 {object} contracts.ErrorResponse "Malformed body, unknown field, trailing value, or stage outside the closed set"
// @Failure     500 {object} contracts.ErrorResponse "Persistence failure"
// @Router      /api/v1/telemetry/update-failure [post]
func (s *Server) handleRecordUpdateFailure(w http.ResponseWriter, r *http.Request) {
	// MaxBytesReader (not io.LimitReader): a LimitReader would silently
	// truncate at the cap, so a valid body padded out to the limit with a
	// trailing JSON value after it would present a clean EOF to the trailing
	// check below and sneak past FR-011. MaxBytesReader makes the overflow a
	// decode error instead.
	r.Body = http.MaxBytesReader(w, r.Body, maxUpdateFailureBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var body UpdateFailureRequest
	if err := dec.Decode(&body); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	// DisallowUnknownFields only guards the first value; a second Decode must
	// hit EOF or the request carried trailing JSON (or trailing garbage).
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		s.writeError(w, r, http.StatusBadRequest, "request body must contain exactly one JSON object")
		return
	}
	if _, ok := updateFailureStages[body.Stage]; !ok {
		s.writeError(w, r, http.StatusBadRequest, `stage must be one of "appcast", "download", "install", "other"`)
		return
	}

	// The controller evaluates the telemetry gate and performs the increment
	// behind one call, so there is no gate/record race here. recorded=false is
	// a deliberate no-op, not a failure.
	recorded, err := s.controller.RecordUpdateFailure(body.Stage)
	if err != nil {
		// 204 promises durability (FR-011), so a failed write must not
		// masquerade as success. The stage is a closed enum, safe to log.
		s.getRequestLogger(r).Warnw("Failed to record update failure", "stage", body.Stage, "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "failed to record update failure")
		return
	}
	if !recorded {
		s.getRequestLogger(r).Debugw("Update failure not recorded: telemetry inactive", "stage", body.Stage)
	}

	w.WriteHeader(http.StatusNoContent)
}
