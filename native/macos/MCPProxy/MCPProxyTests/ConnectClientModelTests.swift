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
            action: "precondition_failed", message: "the config changed since the preview"))]
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
    }

    /// The legacy 409 cannot occur in this flow (a replace always sends force),
    /// and if it does it is a plain failure — re-previewing on it would loop.
    func testALegacyAlreadyExistsConflictIsAFailureNotARePreview() async {
        let source = FakeConnectSource()
        source.connectResults = [.failure(APIClientError.connectConflict(
            action: "already_exists", message: "entry already exists"))]
        let model = makeModel(source)
        await model.select("claude-code")

        await model.connect()

        guard case .failed(let message) = model.action else {
            return XCTFail("expected .failed, got \(model.action)")
        }
        XCTAssertEqual(message, "entry already exists")
        XCTAssertEqual(source.previewCalls.count, 1, "must not re-preview and loop")
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

        await model.performDisconnect()

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
        XCTAssertTrue(identifiers.allSatisfy { $0.hasPrefix("connect-client-") })
    }
}
