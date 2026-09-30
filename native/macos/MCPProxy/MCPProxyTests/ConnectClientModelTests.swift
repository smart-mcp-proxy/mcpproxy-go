import XCTest
import AppKit
@testable import MCPProxy

/// State-machine tests for the native Connect Client form (spec 091 T016).
///
/// Everything the model does is driven from synthesized API responses through
/// `FakeConnectSource`, so these tests pin the invariants — above all SC-002's
/// "no Connect control without a matching rendered preview" — without a core.
@MainActor
final class ConnectClientModelTests: XCTestCase {

    /// Records what the model asked the clock for, so the 2 s reachability poll
    /// is asserted rather than waited on.
    @MainActor
    final class SleepRecorder {
        var intervals: [TimeInterval] = []
        var listStates: [ConnectClientModel.ListState] = []
        var rowSnapshots: [[ConnectClientModel.ClientRow]] = []
        weak var model: ConnectClientModel?

        var sleeper: ConnectClientSleeper {
            { [weak self] interval in
                self?.intervals.append(interval)
                if let model = self?.model {
                    self?.listStates.append(model.list)
                    self?.rowSnapshots.append(model.rows)
                }
            }
        }
    }

    private func makeModel(
        _ source: FakeConnectSource,
        recorder: SleepRecorder? = nil
    ) -> ConnectClientModel {
        let noSleep: ConnectClientSleeper = { _ in }
        let model = ConnectClientModel(source: source, sleeper: recorder?.sleeper ?? noSleep)
        recorder?.model = model
        return model
    }

    // MARK: - List

    func testListLoadsFromTheStatOnlyAggregate() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "claude-code", name: "Claude Code"),
            FakeConnectSource.client(id: "cursor", name: "Cursor", exists: false)
        ])]
        let model = makeModel(source)

        XCTAssertEqual(model.list, .loading)
        await model.loadList()

        guard case .loaded(let rows) = model.list else {
            return XCTFail("expected .loaded, got \(model.list)")
        }
        XCTAssertEqual(rows.map(\.clientId), ["claude-code", "cursor"])
        // Opening the list must not read any client config content — i.e. no
        // per-client detail call is made (FR-002 / SC-004).
        XCTAssertTrue(source.detailCalls.isEmpty)
        XCTAssertTrue(source.previewCalls.isEmpty)
    }

    /// FR-013: while the core is unreachable the form waits, polls every 2 s and
    /// populates itself when the core answers — with no user action.
    func testUnreachableCorePollsEveryTwoSecondsUntilItAnswers() async {
        let source = FakeConnectSource()
        source.clientsResults = [
            .failure(APIClientError.notReady),
            .success([FakeConnectSource.client(id: "claude-code")])
        ]
        let recorder = SleepRecorder()
        let model = makeModel(source, recorder: recorder)

        await model.loadList()

        XCTAssertEqual(recorder.intervals, [ConnectClientModel.pollInterval])
        XCTAssertEqual(ConnectClientModel.pollInterval, 2)
        XCTAssertEqual(recorder.listStates.count, 1)
        guard case .coreUnreachable(let reason) = recorder.listStates[0] else {
            return XCTFail("expected .coreUnreachable while polling, got \(recorder.listStates[0])")
        }
        XCTAssertFalse(reason.isEmpty, "the waiting state must say what is wrong")
        guard case .loaded(let rows) = model.list else {
            return XCTFail("expected .loaded after the core answered, got \(model.list)")
        }
        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(source.clientsCallCount, 2)
    }

    // MARK: - Selection → detail + preview

    func testSelectingAClientResolvesDetailAndPreview() async {
        let source = FakeConnectSource()
        source.detailResults = [.success(
            FakeConnectSource.client(id: "claude-code", connected: true,
                                     accessState: .accessible, serverName: "mcpproxy"))]
        source.previewResults = [.success(FakeConnectSource.preview())]
        let model = makeModel(source)

        await model.select("claude-code")

        XCTAssertEqual(model.selection, "claude-code")
        XCTAssertEqual(source.detailCalls, ["claude-code"])
        XCTAssertEqual(source.previewCalls.map(\.clientId), ["claude-code"])
        guard case .resolved(let detail) = model.detail else {
            return XCTFail("expected resolved detail, got \(model.detail)")
        }
        XCTAssertTrue(detail.connected)
        XCTAssertEqual(detail.serverName, "mcpproxy")
        guard case .resolved = model.preview else {
            return XCTFail("expected resolved preview, got \(model.preview)")
        }
    }

    func testEntryNameDefaultsToMCPProxyAndIsPreviewed() async {
        let source = FakeConnectSource()
        let model = makeModel(source)

        XCTAssertEqual(model.entryName, "mcpproxy")
        await model.select("claude-code")

        XCTAssertEqual(source.previewCalls.first?.serverName, "mcpproxy")
    }

    // MARK: - SC-002: the Connect control is structurally preview-bound

    func testConnectControlDoesNotExistBeforeAPreviewIsResolved() async {
        let source = FakeConnectSource()
        let model = makeModel(source)

        XCTAssertFalse(model.connectControlExists, "no selection, no preview, no control")

        await model.select("claude-code")
        XCTAssertTrue(model.connectControlExists)
    }

    /// Editing the entry name destroys the rendered preview immediately — before
    /// any refetch — so no write can be bound to a preview of a different input.
    func testEditingTheEntryNameDiscardsThePreviewAndTheControl() async {
        let source = FakeConnectSource()
        let model = makeModel(source)
        await model.select("claude-code")
        XCTAssertTrue(model.connectControlExists)

        model.entryName = "my-proxy"

        XCTAssertEqual(model.preview, .idle)
        XCTAssertFalse(model.connectControlExists)

        await model.refreshPreview()

        XCTAssertTrue(model.connectControlExists)
        XCTAssertEqual(source.previewCalls.map(\.serverName), ["mcpproxy", "my-proxy"])
    }

    /// A preview that arrives for inputs the user has already changed must not
    /// resurrect the control (the late-response race).
    func testAPreviewForStaleInputsIsDiscarded() async {
        let source = FakeConnectSource()
        source.previewResults = [.success(FakeConnectSource.preview(serverName: "mcpproxy"))]
        let model = makeModel(source)
        await model.select("claude-code")

        model.entryName = "my-proxy"
        // The scripted preview still answers with the OLD entry name.
        await model.refreshPreview()

        XCTAssertFalse(model.connectControlExists,
                       "a preview naming another entry must not gate this write")
    }

    func testARefusedPreviewOffersNoConnectControlAndShowsTheCoreReasonVerbatim() async {
        let reason = "opencode requires an existing config file; create one first"
        let source = FakeConnectSource()
        source.previewResults = [.success(
            FakeConnectSource.preview(client: "opencode", accessState: .absent, refusal: reason))]
        let model = makeModel(source)

        await model.select("opencode")

        XCTAssertFalse(model.connectControlExists)
        XCTAssertEqual(model.connectRefusal, reason)
    }

    func testAnUnreadableConfigOffersNoConnectControl() async {
        let source = FakeConnectSource()
        source.previewResults = [
            .success(FakeConnectSource.preview(accessState: .malformed)),
            .success(FakeConnectSource.preview(accessState: .denied))
        ]
        let model = makeModel(source)

        await model.select("cursor")
        XCTAssertFalse(model.connectControlExists, "malformed config: no Connect")

        await model.refreshPreview()
        XCTAssertFalse(model.connectControlExists, "denied access: no Connect")
    }

    func testAFailedPreviewOffersNoConnectControlAndKeepsTheCoreMessage() async {
        let source = FakeConnectSource()
        source.previewResults = [.failure(
            APIClientError.httpError(statusCode: 403, message: "operation not permitted"))]
        let model = makeModel(source)

        await model.select("cursor")

        XCTAssertFalse(model.connectControlExists)
        guard case .failed(let message) = model.preview else {
            return XCTFail("expected a failed preview, got \(model.preview)")
        }
        XCTAssertTrue(message.contains("operation not permitted"), "got: \(message)")
    }

    // MARK: - Connect

    func testAnAddSendsTheTokenWithoutForce() async {
        let source = FakeConnectSource()
        source.previewResults = [.success(FakeConnectSource.preview(entryExists: false, token: "tok-add"))]
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()

        XCTAssertEqual(source.connectCalls, [
            .init(clientId: "claude-code", serverName: "mcpproxy",
                  force: false, preconditionToken: "tok-add")
        ])
    }

    /// A replace overwrites, so it sends `force` — but only ever TOGETHER with
    /// the token, which is the actual safety (FR-005).
    func testAReplaceSendsForceTogetherWithTheToken() async {
        let source = FakeConnectSource()
        source.previewResults = [.success(FakeConnectSource.preview(
            entryExists: true,
            summary: ConnectEntrySummary(entryName: "old-proxy", type: "http"),
            token: "tok-replace"))]
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()

        XCTAssertEqual(source.connectCalls.first?.force, true)
        XCTAssertEqual(source.connectCalls.first?.preconditionToken, "tok-replace")
    }

    /// The form never sends the overwrite flag without a valid token, so against
    /// a core that issues no token a replace is simply not offered.
    func testAReplaceWithoutATokenIsNotOfferedAtAll() async {
        let source = FakeConnectSource()
        source.previewResults = [.success(
            FakeConnectSource.preview(entryExists: true, token: nil))]
        let model = makeModel(source)

        await model.select("claude-code")
        await model.connect()

        XCTAssertFalse(model.connectControlExists)
        XCTAssertTrue(source.connectCalls.isEmpty, "force must never be sent tokenless")
    }

    /// ...and when it is not offered, the form says why. A resolved preview, a
    /// backup promise and no button with no explanation is a silent dead end.
    func testABlockedReplaceStatesWhyAndPromisesNoBackup() async {
        let source = FakeConnectSource()
        source.previewResults = [.success(
            FakeConnectSource.preview(entryExists: true, token: nil))]
        let model = makeModel(source)

        await model.select("claude-code")

        XCTAssertFalse(model.connectControlExists)
        let reason = try? XCTUnwrap(model.connectBlockedReason)
        XCTAssertFalse((reason ?? "").isEmpty, "a hidden control must have a stated reason")
        XCTAssertNil(model.currentPreview?.safetyNetStatement,
                     "no backup is promised for a write that cannot be started")
    }

    /// Against a PRE-091 core (no precondition_token field at all) the write
    /// falls back to its legacy behaviour, so the control stays available and
    /// the replace is sent with force and NO token — the alternative is a form
    /// that shows a full preview and offers nothing, forever.
    func testAReplaceAgainstAPre091CoreIsOfferedAndSendsNoToken() async {
        let source = FakeConnectSource()
        source.previewResults = [.success(
            FakeConnectSource.preview(entryExists: true, token: nil, coreSupportsTokens: false))]
        let model = makeModel(source)

        await model.select("claude-code")

        XCTAssertTrue(model.connectControlExists,
                      "a core that never speaks the token protocol must not disable the form")
        XCTAssertNil(model.connectBlockedReason)

        await model.connect()

        XCTAssertEqual(source.connectCalls.count, 1)
        XCTAssertEqual(source.connectCalls.first?.force, true)
        XCTAssertNil(source.connectCalls.first?.preconditionToken,
                     "there is no token to send; the core falls back to its legacy write")
    }

    func testASuccessfulConnectRefreshesTheClientState() async {
        let source = FakeConnectSource()
        source.detailResults = [
            .success(FakeConnectSource.client(id: "claude-code", connected: false)),
            .success(FakeConnectSource.client(id: "claude-code", connected: true,
                                              serverName: "mcpproxy"))
        ]
        source.connectResults = [.success(
            FakeConnectSource.result(action: "created", backupPath: "/Users/x/.claude.json.bak"))]
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()

        guard case .succeeded(let result) = model.action else {
            return XCTFail("expected .succeeded, got \(model.action)")
        }
        XCTAssertEqual(result.action, "created")
        XCTAssertEqual(source.detailCalls, ["claude-code", "claude-code"],
                       "the affected client's state must refresh from the core")
        guard case .resolved(let detail) = model.detail, detail.connected else {
            return XCTFail("expected the refreshed detail to show connected, got \(model.detail)")
        }
    }

    /// Drift → `.conflict` → exactly ONE automatic re-preview, and no retry of
    /// the write (research D9).
    func testAPreconditionFailureConflictsAndRePreviewsExactlyOnce() async {
        let source = FakeConnectSource()
        source.connectResults = [.failure(APIClientError.connectConflict(
            action: "precondition_failed", message: "the config changed since the preview",
            displayPath: "~/.claude.json",
            reloadHint: "Run /mcp in Claude Code (or restart it) to load MCPProxy"))]
        let model = makeModel(source)
        await model.select("claude-code")
        XCTAssertEqual(source.previewCalls.count, 1)

        await model.connect()

        guard case .conflict(let reason) = model.action else {
            return XCTFail("expected .conflict, got \(model.action)")
        }
        XCTAssertEqual(reason, "the config changed since the preview")
        XCTAssertEqual(source.connectCalls.count, 1, "the write must not be retried")
        XCTAssertEqual(source.previewCalls.count, 2, "exactly one automatic re-preview")
        // Review round 3 finding: connectConflict(from:) used to discard the
        // core's display_path/reload_hint (filled on every ConnectResult
        // branch, conflicts included) before it ever reached the model.
        XCTAssertEqual(model.actionDisplayPath, "~/.claude.json")
        XCTAssertEqual(model.actionReloadHint,
                       "Run /mcp in Claude Code (or restart it) to load MCPProxy")
    }

    /// The legacy 409 cannot occur in this flow (a replace always sends force),
    /// and if it does it is a plain failure — re-previewing on it would loop.
    func testALegacyAlreadyExistsConflictIsAFailureNotARePreview() async {
        let source = FakeConnectSource()
        source.connectResults = [.failure(APIClientError.connectConflict(
            action: "already_exists", message: "entry already exists",
            displayPath: "~/.cursor/mcp.json",
            reloadHint: "Reload the Cursor window (or restart Cursor) to load MCPProxy"))]
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()

        guard case .failed(let message) = model.action else {
            return XCTFail("expected .failed, got \(model.action)")
        }
        XCTAssertEqual(message, "entry already exists")
        XCTAssertEqual(source.previewCalls.count, 1, "must not re-preview and loop")
        // Review round 3 finding: the legacy already_exists failure means an
        // entry is already there under this path — the path/reload hint must
        // reach the model the same way the precondition_failed conflict's do.
        XCTAssertEqual(model.actionDisplayPath, "~/.cursor/mcp.json")
        XCTAssertEqual(model.actionReloadHint,
                       "Reload the Cursor window (or restart Cursor) to load MCPProxy")
    }

    /// A generic failure (not a `connectConflict`) must not carry over a
    /// stale path/hint from an earlier action — beginRequest() resets both.
    func testAGenericFailureCarriesNoStaleDisplayPathOrReloadHint() async {
        let source = FakeConnectSource()
        source.connectResults = [
            .failure(APIClientError.connectConflict(
                action: "already_exists", message: "entry already exists",
                displayPath: "~/.cursor/mcp.json", reloadHint: "Reload Cursor")),
            .failure(APIClientError.httpError(statusCode: 500, message: "internal error")),
        ]
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()
        XCTAssertNotNil(model.actionDisplayPath, "precondition: the first failure set a path")

        await model.connect()

        guard case .failed = model.action else {
            return XCTFail("expected .failed, got \(model.action)")
        }
        XCTAssertNil(model.actionDisplayPath, "a generic failure must not keep the prior action's path")
        XCTAssertNil(model.actionReloadHint, "a generic failure must not keep the prior action's hint")
    }

    func testAFailedConnectKeepsTheCoreMessage() async {
        let source = FakeConnectSource()
        source.connectResults = [.failure(
            APIClientError.httpError(statusCode: 400, message: "config is not writable"))]
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()

        guard case .failed(let message) = model.action else {
            return XCTFail("expected .failed, got \(model.action)")
        }
        XCTAssertTrue(message.contains("config is not writable"), "got: \(message)")
    }

    func testActionsAreDisabledWhileARequestIsInFlight() async {
        let source = FakeConnectSource()
        let model = makeModel(source)
        await model.select("claude-code")

        var inFlightState: ConnectClientModel.ActionState?
        var connectEnabledInFlight: Bool?
        source.whileConnectInFlight = { [weak model] in
            inFlightState = model?.action
            connectEnabledInFlight = model?.isConnectEnabled
        }

        await model.connect()

        XCTAssertEqual(inFlightState, .inFlight)
        XCTAssertEqual(connectEnabledInFlight, false,
                       "double-click protection: the button disables while in flight")
        XCTAssertTrue(model.isConnectEnabled, "and re-enables once the request settles")
    }

    // MARK: - Transport

    /// Off-socket the mutating controls are DISABLED with an explanation — the
    /// list, detail and preview keep working (spec's non-socket edge case).
    func testOffSocketDisablesMutatingControlsWithAnExplanation() async {
        let source = FakeConnectSource()
        source.transportKind = .tcp
        let model = makeModel(source)

        await model.select("claude-code")

        XCTAssertTrue(model.connectControlExists, "the preview still renders")
        XCTAssertFalse(model.isConnectEnabled)
        let reason = try? XCTUnwrap(model.mutatingDisabledReason)
        XCTAssertFalse(reason?.isEmpty ?? true, "the user must be told why")
        guard case .resolved = model.preview else {
            return XCTFail("previews must remain available off-socket")
        }
    }

    func testOnSocketTheMutatingControlsCarryNoExplanation() async {
        let source = FakeConnectSource()
        let model = makeModel(source)

        await model.select("claude-code")

        XCTAssertNil(model.mutatingDisabledReason)
        XCTAssertTrue(model.isConnectEnabled)
    }

    /// Belt and braces for the same rule at the model level: with the transport
    /// off-socket, invoking connect sends nothing at all.
    func testOffSocketConnectSendsNothing() async {
        let source = FakeConnectSource()
        source.transportKind = .tcp
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()

        XCTAssertTrue(source.connectCalls.isEmpty)
    }

    // MARK: - US2: configuration state at a glance (T020)

    /// The list speaks only the cheap truth — a statement about the config FILE,
    /// derived from the existence-only aggregate. Rendering it must not read a
    /// single client config's contents (FR-002 / SC-004).
    func testListRowsRenderStatOnlyStatesWithoutReadingAnyConfig() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "claude-code", name: "Claude Code", exists: true),
            FakeConnectSource.client(id: "cursor", name: "Cursor", exists: false),
            FakeConnectSource.client(id: "windsurf", name: "Windsurf", exists: false,
                                     supported: false, reason: "Not available on this platform")
        ])]
        let model = makeModel(source)

        await model.loadList()

        XCTAssertEqual(model.rows.map(\.clientId), ["claude-code", "cursor", "windsurf"])
        XCTAssertEqual(model.rows.map(\.stateLabel),
                       ["Config present", "No config found", "Not available on this platform"])
        // FR-009: an unsupported client is visible but disabled with its reason,
        // never hidden.
        XCTAssertEqual(model.rows.map(\.isSelectable), [true, true, false])
        XCTAssertEqual(model.rows.map(\.displayName), ["Claude Code", "Cursor", "Windsurf"])
        // "No config found" is a claim about the file, never about the app.
        XCTAssertFalse(
            model.rows.contains { $0.stateLabel.localizedCaseInsensitiveContains("installed") },
            "labels must never claim an application is or is not installed")
        XCTAssertTrue(source.detailCalls.isEmpty, "opening the list reads no config contents")
        XCTAssertTrue(source.previewCalls.isEmpty)
    }

    /// "No config found" must name the files the verdict is about: a user whose
    /// opencode.jsonc exists needs to see which paths were actually checked
    /// before concluding the client is not set up.
    func testANoConfigRowNamesTheCheckedPaths() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(
                id: "opencode", name: "OpenCode", exists: false,
                checkedPaths: ["/Users/x/.config/opencode/opencode.jsonc",
                               "/Users/x/.config/opencode/opencode.json"]),
            FakeConnectSource.client(id: "cursor", name: "Cursor", exists: false),
            FakeConnectSource.client(id: "claude-code", name: "Claude Code", exists: true)
        ])]
        let model = makeModel(source)

        await model.loadList()

        // Same directory: name the files once and the directory once.
        XCTAssertEqual(
            model.rows[0].note,
            "Looked for opencode.jsonc or opencode.json in /Users/x/.config/opencode")
        // A core without checked_paths still names its single config_path.
        XCTAssertEqual(model.rows[1].note, "Looked for /Users/x/.cursor/config.json")
        // A present config needs no explanation of where it was looked for.
        XCTAssertNil(model.rows[2].note)
    }

    /// The looked-for hint never displaces a real caveat: an explicit core note
    /// (e.g. a bridge requirement) outranks it — and keeps the warning channel.
    func testACoreNoteOutranksTheLookedForHint() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "claude-desktop", exists: false,
                                     note: "Requires the bundled stdio bridge")
        ])]
        let model = makeModel(source)

        await model.loadList()

        XCTAssertEqual(model.rows.first?.note, "Requires the bundled stdio bridge")
        XCTAssertEqual(model.rows.first?.noteIsWarning, true)
    }

    /// A denied stat is not evidence of absence. The aggregate list classifies a
    /// permission-blocked stat as denied WITHOUT remediation; that row must not
    /// say "Looked for …" — the files were never checked, and the note would
    /// name the wrong problem under an "Access not granted" label.
    func testADeniedRowWithoutRemediationGetsNoLookedForNote() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "cursor", exists: false, accessState: .denied)
        ])]
        let model = makeModel(source)

        await model.loadList()

        XCTAssertEqual(model.rows.first?.stateLabel, "Access not granted")
        XCTAssertNil(model.rows.first?.note)
    }

    /// The looked-for hint is informational, not cautionary — it must not light
    /// up the warning channel every real remediation uses.
    func testTheLookedForHintIsNotAWarning() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "cursor", exists: false)
        ])]
        let model = makeModel(source)

        await model.loadList()

        XCTAssertEqual(model.rows.first?.noteIsWarning, false)
    }

    /// Paths under the actual home directory — which is where every real client
    /// config lives — render tilde-abbreviated, with the shared directory named
    /// once.
    func testCheckedPathsUnderTheRealHomeAreTildeAbbreviated() async {
        let home = NSHomeDirectory()
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(
                id: "opencode", exists: false,
                checkedPaths: ["\(home)/.config/opencode/opencode.jsonc",
                               "\(home)/.config/opencode/opencode.json"]),
            FakeConnectSource.client(id: "cursor", exists: false,
                                     checkedPaths: ["\(home)/.cursor/mcp.json"])
        ])]
        let model = makeModel(source)

        await model.loadList()

        XCTAssertEqual(model.rows[0].note,
                       "Looked for opencode.jsonc or opencode.json in ~/.config/opencode")
        XCTAssertEqual(model.rows[1].note, "Looked for ~/.cursor/mcp.json")
    }

    /// An unsupported client the core gave no reason for still renders disabled
    /// with a defined label rather than an empty one.
    func testAnUnsupportedRowWithoutAReasonStillCarriesALabel() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "codex", supported: false)
        ])]
        let model = makeModel(source)

        await model.loadList()

        XCTAssertEqual(model.rows.first?.isSelectable, false)
        XCTAssertEqual(model.rows.first?.stateLabel, "Not supported on this platform")
    }

    /// US2 scenario 2: the authoritative "connected, and under which entry name"
    /// appears only once the user selects the client — the explicit read.
    func testSelectingResolvesConnectedStateAndEntryNameInTheRow() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "claude-code", name: "Claude Code", exists: true)
        ])]
        source.detailResults = [.success(FakeConnectSource.client(
            id: "claude-code", name: "Claude Code", exists: true, connected: true,
            accessState: .accessible, serverName: "my-proxy"))]
        let model = makeModel(source)
        await model.loadList()
        XCTAssertEqual(model.rows.first?.stateLabel, "Config present")
        XCTAssertEqual(model.rows.first?.connected, false)

        await model.select("claude-code")

        XCTAssertEqual(model.rows.first?.stateLabel, "Connected as \"my-proxy\"")
        XCTAssertEqual(model.rows.first?.connected, true)
    }

    /// Review round 3 finding: `ClientStatus.reloadHint` is decoded and
    /// unit-tested (APIClientTests) but nothing in the row builder ever reads
    /// it — the same "decoded but never rendered" defect class round 1 found
    /// and fixed on ConnectPreview/ConnectResult. A connected row must surface
    /// its reload hint, exactly like the Web UI wizard's Verify step already
    /// does for every connected client (Spec 109-b FR-037/FR-042): most
    /// clients only read their config at startup, so a connect that succeeded
    /// is not yet a client that has picked it up.
    func testAConnectedRowSurfacesItsReloadHint() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "cursor", name: "Cursor", exists: true)
        ])]
        source.detailResults = [.success(FakeConnectSource.client(
            id: "cursor", name: "Cursor", exists: true, connected: true,
            accessState: .accessible, serverName: "mcpproxy",
            reloadHint: "Reload the Cursor window (or restart Cursor) to load MCPProxy"))]
        let model = makeModel(source)
        await model.loadList()

        await model.select("cursor")

        XCTAssertEqual(model.rows.first?.note,
                       "Reload the Cursor window (or restart Cursor) to load MCPProxy")
        XCTAssertEqual(model.rows.first?.noteIsWarning, false)
    }

    /// A real caveat (e.g. a bridge requirement) still outranks the reload
    /// hint — the same priority `testACoreNoteOutranksTheLookedForHint` pins
    /// for the not-connected case.
    func testACoreNoteOutranksTheReloadHintToo() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "claude-desktop", exists: true)
        ])]
        source.detailResults = [.success(FakeConnectSource.client(
            id: "claude-desktop", exists: true, connected: true,
            note: "Requires the bundled stdio bridge",
            reloadHint: "Restart Claude Desktop to load MCPProxy"))]
        let model = makeModel(source)
        await model.loadList()

        await model.select("claude-desktop")

        XCTAssertEqual(model.rows.first?.note, "Requires the bundled stdio bridge")
        XCTAssertEqual(model.rows.first?.noteIsWarning, true)
    }

    /// FR-009: the two unreadable access states get their defined labels, and
    /// denied carries the core's remediation.
    func testUnreadableAndDeniedRowsCarryTheirMappedLabels() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "cursor", exists: true)
        ])]
        source.detailResults = [
            .success(FakeConnectSource.client(id: "cursor", exists: true, accessState: .malformed)),
            .success(FakeConnectSource.client(id: "cursor", exists: true, accessState: .denied,
                                              remediation: "Grant Full Disk Access to MCPProxy"))
        ]
        let model = makeModel(source)
        await model.loadList()

        await model.select("cursor")
        XCTAssertEqual(model.rows.first?.stateLabel, "Config unreadable")

        await model.refreshDetail()
        XCTAssertEqual(model.rows.first?.stateLabel, "Access not granted")
        XCTAssertEqual(model.rows.first?.note, "Grant Full Disk Access to MCPProxy")
    }

    /// US2 scenario 3: a completed connect refreshes the AFFECTED client from the
    /// core — not the whole list, and never by the tray reading a config itself.
    func testOnlyTheAffectedClientRefreshesAfterAConnect() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "claude-code", name: "Claude Code", exists: true),
            FakeConnectSource.client(id: "cursor", name: "Cursor", exists: false)
        ])]
        source.detailResults = [
            .success(FakeConnectSource.client(id: "claude-code", name: "Claude Code",
                                              exists: true, connected: false)),
            .success(FakeConnectSource.client(id: "claude-code", name: "Claude Code",
                                              exists: true, connected: true,
                                              serverName: "mcpproxy"))
        ]
        let model = makeModel(source)
        await model.loadList()
        await model.select("claude-code")

        await model.connect()

        XCTAssertEqual(source.clientsCallCount, 1,
                       "the whole list must not be refetched after an action")
        XCTAssertEqual(source.detailCalls, ["claude-code", "claude-code"],
                       "only the affected client's state is re-read")
        XCTAssertEqual(model.rows.map(\.stateLabel),
                       ["Connected as \"mcpproxy\"", "No config found"])
    }

    /// A disconnect settles the same way: the affected row re-reads, the rest of
    /// the list is left alone.
    func testOnlyTheAffectedClientRefreshesAfterADisconnect() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "claude-code", name: "Claude Code", exists: true),
            FakeConnectSource.client(id: "cursor", name: "Cursor", exists: false)
        ])]
        source.detailResults = [
            .success(FakeConnectSource.client(id: "claude-code", name: "Claude Code",
                                              exists: true, connected: true,
                                              serverName: "mcpproxy")),
            .success(FakeConnectSource.client(id: "claude-code", name: "Claude Code",
                                              exists: true, connected: false))
        ]
        let model = makeModel(source)
        await model.loadList()
        await model.select("claude-code")
        XCTAssertEqual(model.rows.first?.stateLabel, "Connected as \"mcpproxy\"")

        model.requestDisconnect()
        await model.confirmDisconnect()

        XCTAssertEqual(source.disconnectCalls, ["claude-code"])
        XCTAssertEqual(source.clientsCallCount, 1)
        XCTAssertEqual(model.rows.map(\.stateLabel), ["Config present", "No config found"])
    }

    /// The rows exist only for a loaded list; while waiting there is nothing to
    /// render and nothing to select.
    func testRowsAreEmptyUntilTheListLoads() async {
        let source = FakeConnectSource()
        source.clientsResults = [
            .failure(APIClientError.notReady),
            .success([FakeConnectSource.client(id: "claude-code")])
        ]
        let recorder = SleepRecorder()
        let model = makeModel(source, recorder: recorder)

        XCTAssertTrue(model.rows.isEmpty, "nothing to render while loading")
        await model.loadList()

        XCTAssertEqual(recorder.rowSnapshots.first?.isEmpty, true,
                       "an unreachable core renders no rows")
        XCTAssertEqual(model.rows.count, 1)
    }

    // MARK: - US3: session-scoped undo (T022)

    /// FR-006: undo exists for a connect performed in THIS open form, and for
    /// nothing else — not for a freshly opened form over an already connected
    /// client, whose backup identity this session never saw.
    func testUndoIsNotOfferedBeforeAConnect() async {
        let source = FakeConnectSource()
        source.detailResults = [.success(FakeConnectSource.client(
            id: "claude-code", connected: true, serverName: "mcpproxy"))]
        let model = makeModel(source)

        await model.select("claude-code")

        XCTAssertFalse(model.undoControlExists,
                       "a connect this session did not perform has no undo")
        await model.undo()
        XCTAssertTrue(source.undoCalls.isEmpty)
    }

    /// The undo carries the backup identity of exactly that connect — and the
    /// core wants the BARE FILENAME, so a full path must not be sent (it is a
    /// 400 by contract).
    func testUndoCarriesTheBackupIdentityOfThatConnectAsABareFilename() async {
        let source = FakeConnectSource()
        source.connectResults = [.success(FakeConnectSource.result(
            action: "updated",
            backupPath: "/Users/x/.claude.json.20260731-120000.bak"))]
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()
        XCTAssertTrue(model.undoControlExists)
        await model.undo()

        XCTAssertEqual(source.undoCalls.count, 1)
        XCTAssertEqual(source.undoCalls.first?.clientId, "claude-code")
        XCTAssertEqual(source.undoCalls.first?.backupName,
                       ".claude.json.20260731-120000.bak",
                       "the core rejects a path; only the bare backup filename is valid")
    }

    /// The created-file case: no backup exists, so no identity is sent and the
    /// core removes the file the connect created.
    func testUndoAfterACreateSendsNoBackupIdentity() async {
        let source = FakeConnectSource()
        source.connectResults = [.success(FakeConnectSource.result(action: "created"))]
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()
        await model.undo()

        XCTAssertEqual(source.undoCalls.count, 1)
        XCTAssertNil(source.undoCalls.first?.backupName,
                     "an empty identity must be absent, not an empty string")
    }

    func testUndoDisappearsOnceUsed() async {
        let source = FakeConnectSource()
        let model = makeModel(source)
        await model.select("claude-code")
        await model.connect()
        XCTAssertTrue(model.undoControlExists)

        await model.undo()

        XCTAssertFalse(model.undoControlExists)
        await model.undo()
        XCTAssertEqual(source.undoCalls.count, 1, "a used undo cannot be replayed")
    }

    /// Review round 5 finding: a 409 from POST /connect/{client}/undo (config
    /// drifted since the connect this undo is reversing) must surface the
    /// core's display_path/reload_hint the same way connect()'s conflict does
    /// — undo()'s catch used to be a generic catch-all that discarded both.
    func testUndoConflictSurfacesDisplayPathAndReloadHint() async {
        let source = FakeConnectSource()
        source.connectResults = [.success(FakeConnectSource.result(action: "updated"))]
        // "conflict" is the real wire action an undo 409 sends
        // (internal/connect/undo.go's drift check) — undo()'s catch does not
        // discriminate on action the way connect()'s does, so this is not
        // load-bearing today, but the fixture should still say what the core
        // actually sends.
        source.undoResults = [.failure(APIClientError.connectConflict(
            action: "conflict", message: "the config changed since the connect",
            displayPath: "~/.claude.json",
            reloadHint: "Run /mcp in Claude Code (or restart it) to load MCPProxy"))]
        let model = makeModel(source)
        await model.select("claude-code")
        await model.connect()

        await model.undo()

        guard case .failed(let message) = model.action else {
            return XCTFail("expected .failed, got \(model.action)")
        }
        XCTAssertEqual(message, "the config changed since the connect")
        XCTAssertEqual(model.actionDisplayPath, "~/.claude.json")
        XCTAssertEqual(model.actionReloadHint,
                       "Run /mcp in Claude Code (or restart it) to load MCPProxy")
        // The connect stands, so the affordance stands: the user can retry.
        XCTAssertTrue(model.undoControlExists)
    }

    /// FR-006: closing the form ends the undo's scope; the core keeps no
    /// cross-session undo state, so offering it after a reopen would lie.
    func testUndoDisappearsWhenTheFormCloses() async {
        let source = FakeConnectSource()
        let model = makeModel(source)
        await model.select("claude-code")
        await model.connect()
        XCTAssertTrue(model.undoControlExists)

        model.formWillClose()

        XCTAssertFalse(model.undoControlExists)
        await model.undo()
        XCTAssertTrue(source.undoCalls.isEmpty)
    }

    func testAFailedConnectOffersNoUndo() async {
        let source = FakeConnectSource()
        source.connectResults = [.failure(
            APIClientError.httpError(statusCode: 400, message: "config is not writable"))]
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()

        XCTAssertFalse(model.undoControlExists, "nothing was written, nothing to undo")
    }

    /// The undo belongs to the client it was performed on: browsing to another
    /// client hides it, and coming back shows it again.
    func testUndoIsScopedToTheClientItWasPerformedOn() async {
        let source = FakeConnectSource()
        let model = makeModel(source)
        await model.select("claude-code")
        await model.connect()
        XCTAssertTrue(model.undoControlExists)

        await model.select("cursor")
        XCTAssertFalse(model.undoControlExists)

        await model.select("claude-code")
        XCTAssertTrue(model.undoControlExists)
    }

    /// Off-socket the undo is visible but disabled, like every mutating control.
    func testOffSocketUndoSendsNothing() async {
        let source = FakeConnectSource()
        let model = makeModel(source)
        await model.select("claude-code")
        await model.connect()
        source.transportKind = .tcp

        await model.undo()

        XCTAssertTrue(source.undoCalls.isEmpty)
        XCTAssertFalse(model.isUndoEnabled)
    }

    // MARK: - US3: disconnect confirmation (T022)

    /// US3 scenario 3 / FR-006: the confirmation names the config file and the
    /// entry, and NOTHING is sent until it is confirmed.
    func testDisconnectAsksForAConfirmationNamingTheFileAndEntryBeforeSendingAnything() async {
        let source = FakeConnectSource()
        source.detailResults = [.success(FakeConnectSource.client(
            id: "claude-code", connected: true, serverName: "my-proxy"))]
        let model = makeModel(source)
        await model.select("claude-code")

        model.requestDisconnect()

        let confirmation = try? XCTUnwrap(model.pendingDisconnect)
        XCTAssertEqual(confirmation?.clientId, "claude-code")
        XCTAssertEqual(confirmation?.entryName, "my-proxy")
        XCTAssertEqual(confirmation?.configPath, "/Users/x/.claude-code/config.json")
        XCTAssertTrue(confirmation?.message.contains("my-proxy") ?? false,
                      "the confirmation must name the entry: \(confirmation?.message ?? "")")
        XCTAssertTrue(confirmation?.message.contains("/Users/x/.claude-code/config.json") ?? false,
                      "the confirmation must name the file: \(confirmation?.message ?? "")")
        XCTAssertTrue(source.disconnectCalls.isEmpty, "asking must not remove anything")

        await model.confirmDisconnect()

        XCTAssertEqual(source.disconnectCalls, ["claude-code"])
        XCTAssertNil(model.pendingDisconnect)
    }

    /// The confirmation shows the core's home-shortened `display_path`, the same
    /// form the adjacent row and result view use, not the raw absolute path.
    func testDisconnectConfirmationShowsTheHomeShortenedDisplayPath() async {
        let source = FakeConnectSource()
        source.detailResults = [.success(FakeConnectSource.client(
            id: "claude-code", connected: true, serverName: "mcpproxy",
            displayPath: "~/.claude-code/config.json"))]
        let model = makeModel(source)
        await model.select("claude-code")

        model.requestDisconnect()

        let confirmation = model.pendingDisconnect
        XCTAssertEqual(confirmation?.configPath, "~/.claude-code/config.json")
        XCTAssertTrue(confirmation?.message.contains("~/.claude-code/config.json") ?? false,
                      "the confirmation must name the shortened file: \(confirmation?.message ?? "")")
        XCTAssertFalse(confirmation?.message.contains("/Users/x/") ?? true,
                       "the raw absolute path must not appear: \(confirmation?.message ?? "")")
    }

    func testCancellingTheDisconnectConfirmationSendsNothing() async {
        let source = FakeConnectSource()
        source.detailResults = [.success(FakeConnectSource.client(
            id: "claude-code", connected: true, serverName: "mcpproxy"))]
        let model = makeModel(source)
        await model.select("claude-code")

        model.requestDisconnect()
        model.cancelDisconnect()
        await model.confirmDisconnect()

        XCTAssertNil(model.pendingDisconnect)
        XCTAssertTrue(source.disconnectCalls.isEmpty)
    }

    /// A client that is not connected has no entry to remove, so the control is
    /// absent and asking for it does nothing.
    func testDisconnectIsNotOfferedForAClientThatIsNotConnected() async {
        let source = FakeConnectSource()
        source.detailResults = [.success(
            FakeConnectSource.client(id: "cursor", connected: false))]
        let model = makeModel(source)
        await model.select("cursor")

        XCTAssertFalse(model.disconnectControlExists)
        model.requestDisconnect()

        XCTAssertNil(model.pendingDisconnect)
    }

    /// Selecting another client abandons a confirmation the user left open, so a
    /// later confirm cannot remove an entry from the wrong client's config.
    func testChangingTheSelectionAbandonsAPendingDisconnectConfirmation() async {
        let source = FakeConnectSource()
        source.detailResults = [
            .success(FakeConnectSource.client(id: "claude-code", connected: true,
                                              serverName: "mcpproxy")),
            .success(FakeConnectSource.client(id: "cursor", connected: false))
        ]
        let model = makeModel(source)
        await model.select("claude-code")
        model.requestDisconnect()
        XCTAssertNotNil(model.pendingDisconnect)

        await model.select("cursor")

        XCTAssertNil(model.pendingDisconnect)
        await model.confirmDisconnect()
        XCTAssertTrue(source.disconnectCalls.isEmpty)
    }

    func testOffSocketDisconnectSendsNothing() async {
        let source = FakeConnectSource()
        source.transportKind = .tcp
        source.detailResults = [.success(FakeConnectSource.client(
            id: "claude-code", connected: true, serverName: "mcpproxy"))]
        let model = makeModel(source)
        await model.select("claude-code")

        model.requestDisconnect()
        await model.confirmDisconnect()

        XCTAssertTrue(source.disconnectCalls.isEmpty)
        XCTAssertFalse(model.isDisconnectEnabled)
    }

    // MARK: - List selection (FR-009)

    /// An unsupported client stays visible but is not a selection. Clicking it
    /// still moves the list's own highlight, so the model has to say what the
    /// selection may be — otherwise the highlight sits on one client while the
    /// detail pane, the preview and an ENABLED Connect button still target
    /// another, and the user writes a config they were not looking at.
    func testAnUnsupportedRowIsNotASelectionAndSnapsBack() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "cursor", name: "Cursor"),
            FakeConnectSource.client(id: "opencode", name: "OpenCode", supported: false,
                                     reason: "not supported on this platform")
        ])]
        let model = makeModel(source)
        await model.loadList()
        await model.select("cursor")

        XCTAssertEqual(model.listSelection(for: "opencode"), "cursor",
                       "the highlight must snap back to the client the form is showing")
        XCTAssertEqual(model.listSelection(for: "cursor"), "cursor")
        XCTAssertEqual(model.listSelection(for: nil), "cursor",
                       "clearing the highlight must not orphan the pane either")
        XCTAssertEqual(model.selection, "cursor", "and nothing about the form changed")
    }

    /// With nothing selected yet, an unsupported row resolves to no selection —
    /// the pane keeps its "select a client" placeholder.
    func testAnUnsupportedRowResolvesToNoSelectionWhenNoneExists() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "opencode", name: "OpenCode", supported: false)
        ])]
        let model = makeModel(source)
        await model.loadList()

        XCTAssertNil(model.listSelection(for: "opencode"))
        XCTAssertNil(model.selection)
    }
}

/// Presentation wiring for the Connect Client form (spec 091 T018): one shared
/// route into the form, and accessibility identifiers a UI test can rely on.
@MainActor
final class ConnectClientPresentationTests: XCTestCase {

    /// The tray item and (FR-012) the dashboard must both go through the same
    /// route, so no native path can reach a connect without the preview step.
    func testTheMenuItemIsTitledConnectClientAndCarriesBothTargetAndAction() {
        let item = ConnectClientMenuRouter.shared.makeMenuItem()

        XCTAssertEqual(item.title, ConnectClientPresentation.menuTitle)
        XCTAssertEqual(item.title, "Connect Client…")
        XCTAssertNotNil(item.action)
        XCTAssertTrue(item.target === ConnectClientMenuRouter.shared,
                      "a nil target makes the row silently do nothing")
    }

    func testDispatchingTheMenuItemPostsTheSharedPresentationRoute() throws {
        let item = ConnectClientMenuRouter.shared.makeMenuItem()
        let received = expectation(description: "presentation route posted")
        let token = NotificationCenter.default.addObserver(
            forName: ConnectClientPresentation.route, object: nil, queue: .main
        ) { _ in received.fulfill() }
        defer { NotificationCenter.default.removeObserver(token) }

        let dispatched = NSApplication.shared.sendAction(
            try XCTUnwrap(item.action), to: item.target, from: item)

        XCTAssertTrue(dispatched, "the menu item did not dispatch")
        wait(for: [received], timeout: 1)
    }

    /// FR-010: the identifiers are a contract with the UI tests, so they are
    /// pinned literally — renaming one silently breaks an external caller.
    func testAccessibilityIdentifiersAreStable() {
        XCTAssertEqual(ConnectClientAccessibility.list, "connect-client-list")
        XCTAssertEqual(ConnectClientAccessibility.row("claude-code"),
                       "connect-client-row-claude-code")
        XCTAssertEqual(ConnectClientAccessibility.preview, "connect-client-preview")
        XCTAssertEqual(ConnectClientAccessibility.entryText, "connect-client-entry-text")
        XCTAssertEqual(ConnectClientAccessibility.configPath, "connect-client-config-path")
        XCTAssertEqual(ConnectClientAccessibility.existingSummary,
                       "connect-client-existing-summary")
        XCTAssertEqual(ConnectClientAccessibility.safetyNet, "connect-client-safety-net")
        XCTAssertEqual(ConnectClientAccessibility.credentialNotice,
                       "connect-client-credential-notice")
        XCTAssertEqual(ConnectClientAccessibility.refusal, "connect-client-refusal")
        XCTAssertEqual(ConnectClientAccessibility.entryNameField, "connect-client-entry-name")
        XCTAssertEqual(ConnectClientAccessibility.connectButton, "connect-client-connect")
    }

    func testAccessibilityIdentifiersAreUnique() {
        let identifiers = ConnectClientAccessibility.allIdentifiers
        XCTAssertEqual(Set(identifiers).count, identifiers.count,
                       "two elements sharing an identifier make a UI test ambiguous")
        // Spec 108-k K22 names three identifiers `connect-profile-picker`,
        // `connect-lock-toggle` and `connect-mgmt-notice` (what
        // `mcpproxy-ui-test` looks for), so the contract prefix is `connect-`.
        XCTAssertTrue(identifiers.allSatisfy { $0.hasPrefix("connect-") })
    }

    // MARK: - Spec 108-k T042m: profile, mode, masked credential, D5 notice

    private func boundPreview(
        credential: String? = "mcp_cli_••••", profile: String? = nil, keyless: Bool = false,
        containsAPIKey: Bool = false
    ) -> ConnectPreviewModel {
        ConnectPreviewModel(
            client: "claude-code", configPath: "/Users/x/.claude-code/config.json", serverName: "mcpproxy",
            entryText: "{}", entryExists: false, containsAPIKey: containsAPIKey, accessState: .accessible,
            preconditionToken: "tok", credential: credential, profile: profile, keyless: keyless)
    }

    private func selectedModel(
        _ source: FakeConnectSource, profiles: [ProfileView] = [], requireMCPAuth: Bool? = nil
    ) async -> ConnectClientModel {
        source.clientsResults = [.success([FakeConnectSource.client(id: "claude-code", name: "Claude Code")])]
        source.detailResults = [.success(FakeConnectSource.client(id: "claude-code", name: "Claude Code"))]
        source.contextResult = ConnectBindingContext(profiles: profiles, requireMCPAuth: requireMCPAuth)
        let model = ConnectClientModel(source: source, sleeper: { _ in })
        await model.loadList()
        await model.select("claude-code")
        return model
    }

    func testTheDefaultProfileIsAllServersAndNothingIsSent() async {
        let source = FakeConnectSource()
        let model = await selectedModel(source)
        XCTAssertEqual(model.profile, "")
        XCTAssertEqual(model.mode, .switchable)
        XCTAssertEqual(model.binding, .unspecified, "untouched: a reconnect keeps its binding")
        XCTAssertEqual(source.previewBindings, [.unspecified])
    }

    func testChoosingAProfileMakesTheModeLockedAndRefetchesThePreview() async {
        let source = FakeConnectSource()
        let ro = ProfileView(name: "work-ro", title: "Work Read-only", servers: ["github"], managementTools: false)
        let model = await selectedModel(source, profiles: [ro])
        model.chooseProfile("work-ro")
        XCTAssertEqual(model.mode, .locked)
        XCTAssertEqual(model.binding, ConnectBinding(profile: "work-ro", mode: .locked))
        XCTAssertEqual(model.preview, .idle, "the stale preview is destroyed at once")
        XCTAssertFalse(model.connectControlExists)

        await model.refreshPreview()
        XCTAssertEqual(source.previewBindings.last, ConnectBinding(profile: "work-ro", mode: .locked))

        model.setLocked(false)
        XCTAssertEqual(model.binding, ConnectBinding(profile: "work-ro", mode: .switchable))
    }

    func testAllServersIsAlwaysSwitchableAndCannotBeLocked() async {
        let model = await selectedModel(FakeConnectSource())
        model.chooseProfile("")
        XCTAssertEqual(model.mode, .switchable)
        model.setLocked(true)
        XCTAssertEqual(model.mode, .switchable, "there is nothing to lock on All servers")
        XCTAssertEqual(model.binding, ConnectBinding(profile: "", mode: .switchable), "an explicit All servers")
    }

    func testTheWriteCarriesTheChosenBinding() async {
        let source = FakeConnectSource()
        let model = await selectedModel(source, profiles: [ProfileView(name: "work-ro")])
        model.chooseProfile("work-ro")
        await model.refreshPreview()
        await model.connect()
        XCTAssertEqual(source.connectBindings, [ConnectBinding(profile: "work-ro", mode: .locked)])
    }

    func testThePreviewShowsAMaskedCredentialAndNoApiKeyNotice() async throws {
        let source = FakeConnectSource()
        source.previewResults = [.success(boundPreview())]
        let model = await selectedModel(source)
        let preview = try XCTUnwrap(model.currentPreview)
        XCTAssertEqual(preview.credentialLine, "Credential: mcp_cli_•••• (client credential)")
        XCTAssertEqual(model.credentialDisclosure,
                       "MCPProxy writes a client credential for Claude Code. The admin API key is never written.")
        XCTAssertNil(preview.credentialNotice, "the API-key text is gone")
        XCTAssertFalse(preview.credentialDisclosure(clientName: "X")?.contains("embeds the MCPProxy API key") ?? false)
    }

    func testAnOlderCoreThatStillEmbedsTheKeyKeepsTheLegacyNotice() async throws {
        let source = FakeConnectSource()
        source.previewResults = [.success(boundPreview(credential: nil, containsAPIKey: true))]
        let model = await selectedModel(source)
        XCTAssertEqual(model.credentialDisclosure, "This entry embeds the MCPProxy API key in the client's config file.")
    }

    // D5: a client credential cannot reach management tools unless its profile says so.
    func testTheManagementNoticeShowsForAllServersAndForAProfileWithoutManagementTools() async {
        let source = FakeConnectSource()
        source.previewResults = [.success(boundPreview())]
        let profiles = [
            ProfileView(name: "ro", managementTools: false),
            ProfileView(name: "legacy"),
            ProfileView(name: "mgmt", managementTools: true),
        ]
        let model = await selectedModel(source, profiles: profiles)
        XCTAssertEqual(ConnectClientModel.managementNotice,
                       "This client can no longer add, change or restart servers; manage servers from the Web UI, the macOS app or the CLI")

        XCTAssertTrue(model.showsManagementNotice, "All servers (untouched)")
        model.chooseProfile("ro")
        await model.refreshPreview()
        XCTAssertTrue(model.showsManagementNotice, "management_tools: false")
        model.chooseProfile("legacy")
        await model.refreshPreview()
        XCTAssertTrue(model.showsManagementNotice, "management_tools unset (legacy)")
        model.chooseProfile("mgmt")
        await model.refreshPreview()
        XCTAssertFalse(model.showsManagementNotice, "management_tools: true hides it")
        model.chooseProfile("")
        await model.refreshPreview()
        XCTAssertTrue(model.showsManagementNotice)
    }

    func testNoManagementNoticeForAKeylessEntry() async {
        let source = FakeConnectSource()
        source.previewResults = [.success(boundPreview(credential: nil, keyless: true))]
        let model = await selectedModel(source)
        XCTAssertFalse(model.showsManagementNotice, "a keyless entry writes no credential")
    }

    func testKeylessIsOfferedOnlyWhileAuthenticationIsOffAndClearsTheProfile() async {
        let off = await selectedModel(FakeConnectSource(), profiles: [ProfileView(name: "ro")], requireMCPAuth: false)
        XCTAssertTrue(off.keylessAvailable)
        off.chooseProfile("ro")
        off.setKeyless(true)
        XCTAssertTrue(off.keyless)
        XCTAssertEqual(off.profile, "", "keyless clears the profile")
        XCTAssertEqual(off.binding, ConnectBinding(keyless: true))

        let on = await selectedModel(FakeConnectSource(), requireMCPAuth: true)
        XCTAssertFalse(on.keylessAvailable)
        on.setKeyless(true)
        XCTAssertFalse(on.keyless, "refused while require_mcp_auth is on")

        let unknown = await selectedModel(FakeConnectSource(), requireMCPAuth: nil)
        XCTAssertFalse(unknown.keylessAvailable, "offered only when known to be off")
    }

    func testSelectingAnotherClientResetsTheBinding() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "claude-code"), FakeConnectSource.client(id: "cursor"),
        ])]
        let model = ConnectClientModel(source: source, sleeper: { _ in })
        await model.loadList()
        await model.select("claude-code")
        model.chooseProfile("ro")
        await model.select("cursor")
        XCTAssertEqual(model.binding, .unspecified)
        XCTAssertEqual(model.profile, "")
    }

    func testAnInitialProfileIsAppliedOnceToTheFirstSelection() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([FakeConnectSource.client(id: "claude-code")])]
        let model = ConnectClientModel(source: source, sleeper: { _ in })
        model.initialProfile = "work-ro"
        await model.loadList()
        await model.select("claude-code")
        XCTAssertEqual(model.profile, "work-ro")
        XCTAssertEqual(source.previewBindings.last, ConnectBinding(profile: "work-ro", mode: .locked))
        XCTAssertNil(model.initialProfile)
    }

    func testAGuardRefusalOfTheWriteIsKeptForTheGuardView() async {
        let source = FakeConnectSource()
        source.connectResults = [.failure(APIClientError.service(status: 409, body: ServiceErrorBody(
            error: "could escape", code: "binding_bypassable_without_auth",
            bindings: [BindingRef(clientId: "claude-code", profile: "ro", mode: .locked)],
            fixes: [GuardFix(kind: "require_mcp_auth", target: nil)])))]
        let model = await selectedModel(source)
        model.chooseProfile("ro")
        await model.refreshPreview()
        await model.connect()
        XCTAssertEqual(model.guardRefusal?.fixes?.first?.kind, "require_mcp_auth")
        if case .failed(let text) = model.action { XCTAssertEqual(text, "could escape") } else { XCTFail("\(model.action)") }
    }

    func testAConflictingTokenShowsItsRemediation() async {
        let source = FakeConnectSource()
        source.connectResults = [.failure(APIClientError.service(status: 409, body: ServiceErrorBody(
            error: "token name client-claude-code is held by an agent token",
            conflictingToken: "client-claude-code", remediation: "Revoke that token, then connect again.")))]
        let model = await selectedModel(source)
        await model.connect()
        if case .failed(let text) = model.action {
            XCTAssertTrue(text.contains("held by an agent token"))
            XCTAssertTrue(text.contains("Revoke that token"))
        } else { XCTFail("\(model.action)") }
        XCTAssertNil(model.guardRefusal)
    }

    func testTheResultLineUsesTheCliFormat() {
        let locked = APIClient.ConnectResult(
            success: true, action: "created", credential: "mcp_cli_••••", tokenName: "client-codex",
            profile: "work-ro-mac", mode: .locked)
        XCTAssertEqual(locked.credentialLine, "Credential: mcp_cli_•••• (token client-codex, profile work-ro-mac, locked)")
        let all = APIClient.ConnectResult(
            success: true, action: "created", credential: "mcp_cli_••••", tokenName: "client-codex", profile: "", mode: .switchable)
        XCTAssertEqual(all.credentialLine, "Credential: mcp_cli_•••• (token client-codex, all servers, switchable)")
        XCTAssertEqual(APIClient.ConnectResult(success: true, keyless: true).credentialLine, "Credential: none (keyless)")
        XCTAssertNil(APIClient.ConnectResult(success: true, action: "removed").credentialLine, "a disconnect embeds none")
        XCTAssertNil(APIClient.ConnectResult(success: true).credentialLine)
    }

    /// 109-b's `reloadHint` / `displayPath` stay intact next to the new fields
    /// (the later merge keeps both field sets).
    func testTheSpec109bFieldsSurviveBesideTheBindingFields() throws {
        let json = """
        {"success":true,"client":"cursor","config_path":"/Users/x/.cursor/mcp.json","display_path":"~/.cursor/mcp.json",
         "reload_hint":"Restart Cursor","action":"created","credential":"mcp_cli_••••","token_name":"client-cursor","profile":"ro","mode":"locked","rotation":"finalized"}
        """
        let result = try JSONDecoder().decode(APIClient.ConnectResult.self, from: Data(json.utf8))
        XCTAssertEqual(result.effectiveDisplayPath, "~/.cursor/mcp.json")
        XCTAssertEqual(result.reloadHint, "Restart Cursor")
        XCTAssertEqual(result.rotation, "finalized")
        XCTAssertEqual(result.tokenName, "client-cursor")
    }

    func testAnUndoResultCarriesTheRevokedCredential() throws {
        let result = try JSONDecoder().decode(APIClient.ConnectResult.self, from: Data(
            #"{"success":true,"action":"restored","credential_revoked":"client-cursor"}"#.utf8))
        XCTAssertEqual(result.credentialRevoked, "client-cursor")
    }

    func testClientStatusDecodesTheCredentialState() throws {
        let json = #"{"id":"cursor","name":"Cursor","config_path":"/x","exists":true,"connected":true,"supported":true,"credential_state":"admin_key"}"#
        let status = try JSONDecoder().decode(APIClient.ClientStatus.self, from: Data(json.utf8))
        XCTAssertEqual(status.credentialState, .adminKey)
    }
}
