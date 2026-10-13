import XCTest
@testable import MCPProxy

/// What happens when the user keeps using the form while a write is still in
/// flight (spec 091 Edge Cases: "double-click protection").
///
/// `ConnectClientModel` is a @MainActor class, so every `await` is a reentrancy
/// point: the list, the entry-name field and the action buttons all stay live
/// while a POST is suspended. These tests pin that the busy state survives that
/// interleaving and that an outcome is only ever shown for the client it
/// belongs to.
@MainActor
final class ConnectClientInFlightTests: XCTestCase {

    /// A one-shot gate: `wait()` suspends until `open()` is called, so a test
    /// can hold a request open and observe the window while it is pending.
    @MainActor
    final class Gate {
        private var continuation: CheckedContinuation<Void, Never>?
        private var isOpen = false

        func wait() async {
            if isOpen { return }
            await withCheckedContinuation { self.continuation = $0 }
        }

        func open() {
            isOpen = true
            continuation?.resume()
            continuation = nil
        }
    }

    private func makeModel(_ source: FakeConnectSource) -> ConnectClientModel {
        ConnectClientModel(source: source, sleeper: { _ in })
    }

    // MARK: - Busy state

    /// The entry-name field is editable while a connect is in flight, and
    /// editing it invalidates the preview. That must not be mistaken for "the
    /// request finished": the busy state is the only thing standing between the
    /// user and a second concurrent read-modify-write of the same config file.
    func testEditingTheEntryNameDuringAConnectKeepsTheFormBusy() async {
        let source = FakeConnectSource()
        let model = makeModel(source)
        await model.select("claude-code")

        var busyAfterEdit: Bool?
        var connectEnabledAfterEdit: Bool?
        source.whileConnectInFlight = { [weak model] in
            guard let model else { return }
            model.entryName = "other-name"
            busyAfterEdit = model.isBusy
            connectEnabledAfterEdit = model.isConnectEnabled
        }

        await model.connect()

        XCTAssertEqual(busyAfterEdit, true,
                       "the POST is still outstanding, so the form is still busy")
        XCTAssertEqual(connectEnabledAfterEdit, false)
        XCTAssertEqual(source.connectCalls.count, 1)
    }

    /// Same for a selection change: clicking another row while the write is in
    /// flight must not unlock the controls.
    func testSelectingAnotherClientDuringAConnectKeepsTheFormBusy() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "claude-code"),
            FakeConnectSource.client(id: "cursor")
        ])]
        let model = makeModel(source)
        await model.loadList()
        await model.select("claude-code")

        var busyDuring: Bool?
        source.duringConnect = { [weak model] in
            guard let model else { return }
            await model.select("cursor")
            busyDuring = model.isBusy
        }

        await model.connect()

        XCTAssertEqual(busyDuring, true,
                       "a write against claude-code is still outstanding")
        XCTAssertEqual(source.connectCalls.count, 1)
    }

    // MARK: - Outcome ownership

    /// The outcome of a write belongs to the client it was performed on. If the
    /// user moved to another client while it was in flight, that client's pane
    /// must not display the first client's success banner — while the first
    /// client's ROW must still pick up its refreshed state.
    func testAnOutcomeIsNeverShownInAnotherClientsPane() async {
        let source = FakeConnectSource()
        source.clientsResults = [.success([
            FakeConnectSource.client(id: "claude-code", name: "Claude Code"),
            FakeConnectSource.client(id: "cursor", name: "Cursor", exists: false)
        ])]
        source.detailResults = [
            .success(FakeConnectSource.client(id: "claude-code", name: "Claude Code")),
            .success(FakeConnectSource.client(id: "cursor", name: "Cursor", exists: false)),
            .success(FakeConnectSource.client(id: "claude-code", name: "Claude Code",
                                              connected: true, serverName: "mcpproxy"))
        ]
        let model = makeModel(source)
        await model.loadList()
        await model.select("claude-code")

        source.duringConnect = { [weak model] in
            await model?.select("cursor")
        }

        await model.connect()

        XCTAssertEqual(model.selection, "cursor")
        if case .succeeded = model.action {
            XCTFail("claude-code's success banner must not render in cursor's pane")
        }
        XCTAssertEqual(source.detailCalls, ["claude-code", "cursor", "claude-code"],
                       "the client that was written to is the one that refreshes")
        XCTAssertEqual(model.rows.first(where: { $0.clientId == "claude-code" })?.stateLabel,
                       "Connected as \"mcpproxy\"",
                       "its row must show the state the write produced")
        XCTAssertEqual(source.previewCalls.map(\.clientId), ["claude-code", "cursor"],
                       "the previous client's preview must not overwrite the current pane's")
    }

    // MARK: - Stale action restore

    /// The post-connect preview refresh captures the action before its await and
    /// writes it back after. If the user starts an undo during that await, the
    /// captured (older) outcome must NOT be restored over it — the form would
    /// then show the connect's success while the undo is still in flight, and
    /// would hide the undo's own result.
    func testThePostConnectRefreshNeverRestoresAnOutcomeOverANewerAction() async {
        let source = FakeConnectSource()
        source.connectResults = [.success(
            FakeConnectSource.result(action: "created", backupPath: "/Users/x/.claude.json.bak"))]
        let model = makeModel(source)
        await model.select("claude-code")

        let undoGate = Gate()
        var undoTask: Task<Void, Never>?
        source.duringUndo = { await undoGate.wait() }
        source.duringPreview = { [weak model] index in
            // Call 1 is the selection's own preview; call 2 is the post-connect
            // refresh — the window in which Undo is offered and clickable.
            guard index == 2, let model else { return }
            XCTAssertTrue(model.isUndoEnabled, "Undo is offered during the refresh")
            undoTask = Task { await model.undo() }
            // Let the undo reach its own suspension point (the POST).
            for _ in 0..<20 where source.undoCalls.isEmpty { await Task.yield() }
        }

        await model.connect()

        XCTAssertEqual(source.undoCalls.count, 1, "the undo request was started")
        XCTAssertEqual(model.action, .inFlight,
                       "the pending undo owns the action; the older connect outcome must not be restored over it")
        XCTAssertTrue(model.isBusy, "a request is still in flight")
        XCTAssertFalse(model.isUndoEnabled, "so the undo cannot be sent a second time")

        undoGate.open()
        await undoTask?.value

        XCTAssertEqual(source.undoCalls.count, 1, "the undo must not be sent twice")
        guard case .succeeded(let result) = model.action else {
            return XCTFail("expected the undo's own outcome, got \(model.action)")
        }
        XCTAssertEqual(result.action, "restored")
    }

    /// The same restore must not bury a FAILED undo under the connect's success.
    func testAFailedUndoDuringTheRefreshKeepsItsOwnMessage() async {
        let source = FakeConnectSource()
        source.connectResults = [.success(FakeConnectSource.result(action: "created"))]
        source.undoResults = [.failure(APIClientError.httpError(statusCode: 500, message: "restore failed"))]
        let model = makeModel(source)
        await model.select("claude-code")

        source.duringPreview = { [weak model] index in
            // The undo runs to completion while the post-connect refresh is
            // suspended, so the restore lands strictly after its outcome.
            guard index == 2, let model else { return }
            await model.undo()
        }

        await model.connect()

        guard case .failed(let message) = model.action else {
            return XCTFail("the undo's failure must survive the preview refresh, got \(model.action)")
        }
        XCTAssertTrue(message.contains("restore failed"), "got: \(message)")
    }
}
