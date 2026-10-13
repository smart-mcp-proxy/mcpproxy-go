// HealthFixtureRows.swift
// MCPProxyTests
//
// The ten `internal/health` derivation-table rows (mirrors
// internal/health/status_test.go, T041) as decoded-from-JSON fixtures, shared by
// HealthVocabularyTests (status labels) and ServerStatusLineTests (Spec 109 T049,
// the row/tray/detail status line) so the table is not copied a third time.

import Foundation

enum HealthFixtureRows {
    struct Row {
        let name: String
        let json: String
        let wantStatus: String
        let wantUsable: Bool
        let wantLabel: String
    }

    static let all: [Row] = [
        Row(name: "disabled",
            json: #"{"level":"healthy","admin_state":"disabled","summary":"Disabled","action":"enable","status":"disabled","usable":false,"actions":["enable"]}"#,
            wantStatus: "disabled", wantUsable: false, wantLabel: "Disabled"),
        Row(name: "quarantined + OAuth login required",
            json: #"{"level":"degraded","admin_state":"quarantined","summary":"Sign-in required","action":"login","status":"sign_in_required","usable":false,"actions":["login","approve"]}"#,
            wantStatus: "sign_in_required", wantUsable: false, wantLabel: "Sign-in required"),
        Row(name: "quarantined transport fault",
            json: #"{"level":"unhealthy","admin_state":"quarantined","summary":"Quarantined — Connection refused","action":"approve","status":"error","usable":false,"actions":["approve","view_logs"]}"#,
            wantStatus: "error", wantUsable: false, wantLabel: "Error"),
        Row(name: "quarantined otherwise",
            json: #"{"level":"healthy","admin_state":"quarantined","summary":"Quarantined for review","action":"approve","status":"needs_review","usable":false,"actions":["approve"]}"#,
            wantStatus: "needs_review", wantUsable: false, wantLabel: "Needs review"),
        Row(name: "missing secret",
            json: #"{"level":"unhealthy","admin_state":"enabled","summary":"Missing secret","action":"set_secret","status":"needs_secret","usable":false,"actions":["set_secret"]}"#,
            wantStatus: "needs_secret", wantUsable: false, wantLabel: "Secret required"),
        Row(name: "config error",
            json: #"{"level":"unhealthy","admin_state":"enabled","summary":"OAuth configuration error","action":"configure","status":"needs_config","usable":false,"actions":["configure"]}"#,
            wantStatus: "needs_config", wantUsable: false, wantLabel: "Needs configuration"),
        Row(name: "connecting",
            json: #"{"level":"healthy","admin_state":"enabled","summary":"Connecting...","status":"connecting","usable":false,"actions":[]}"#,
            wantStatus: "connecting", wantUsable: false, wantLabel: "Connecting"),
        Row(name: "connection error",
            json: #"{"level":"unhealthy","admin_state":"enabled","summary":"Connection refused","action":"restart","status":"error","usable":false,"actions":["restart","view_logs"]}"#,
            wantStatus: "error", wantUsable: false, wantLabel: "Error"),
        Row(name: "ready, token refresh retrying (still usable)",
            json: #"{"level":"degraded","admin_state":"enabled","summary":"Token refresh pending","action":"view_logs","status":"ready","usable":true,"actions":["view_logs"]}"#,
            wantStatus: "ready", wantUsable: true, wantLabel: "Online"),
        Row(name: "connected healthy",
            json: #"{"level":"healthy","admin_state":"enabled","summary":"Connected (5 tools)","status":"ready","usable":true,"actions":[]}"#,
            wantStatus: "ready", wantUsable: true, wantLabel: "Online"),
    ]
}
