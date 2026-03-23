import XCTest
@testable import MCPProxy

final class SSEParserTests: XCTestCase {

    // MARK: - Basic Event Parsing

    func testParseSimpleEvent() {
        var parser = SSEParser()

        // Feed: "event: status\ndata: {"running":true}\n\n"
        XCTAssertNil(parser.feed("event: status"))
        XCTAssertNil(parser.feed("data: {\"running\":true}"))

        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.event, "status")
        XCTAssertEqual(event?.data, "{\"running\":true}")
        XCTAssertNil(event?.id)
        XCTAssertNil(event?.retry)
    }

    func testParseEventWithDefaultMessageType() {
        var parser = SSEParser()

        // No "event:" field means the type defaults to "message"
        XCTAssertNil(parser.feed("data: hello"))
        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.event, "message")
        XCTAssertEqual(event?.data, "hello")
    }

    // MARK: - Multi-line Data

    func testParseMultiLineData() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed("event: log"))
        XCTAssertNil(parser.feed("data: line one"))
        XCTAssertNil(parser.feed("data: line two"))
        XCTAssertNil(parser.feed("data: line three"))

        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.event, "log")
        XCTAssertEqual(event?.data, "line one\nline two\nline three")
    }

    func testParseMultiLineJSON() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed("event: status"))
        XCTAssertNil(parser.feed("data: {"))
        XCTAssertNil(parser.feed("data:   \"running\": true,"))
        XCTAssertNil(parser.feed("data:   \"listen_addr\": \"127.0.0.1:8080\""))
        XCTAssertNil(parser.feed("data: }"))

        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.event, "status")
        // Multi-line data fields are joined with \n
        XCTAssertTrue(event!.data.contains("\"running\": true"))
    }

    // MARK: - ID and Retry Fields

    func testParseEventWithIdAndRetry() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed("event: servers.changed"))
        XCTAssertNil(parser.feed("id: 42"))
        XCTAssertNil(parser.feed("retry: 5000"))
        XCTAssertNil(parser.feed("data: {\"reason\":\"reconnected\"}"))

        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.event, "servers.changed")
        XCTAssertEqual(event?.id, "42")
        XCTAssertEqual(event?.retry, 5000)
        XCTAssertEqual(event?.data, "{\"reason\":\"reconnected\"}")
    }

    func testRetryWithNonIntegerIsIgnored() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed("retry: not-a-number"))
        XCTAssertNil(parser.feed("data: test"))

        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertNil(event?.retry)
    }

    func testIdWithNullCharacterIsIgnored() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed("id: contains\0null"))
        XCTAssertNil(parser.feed("data: test"))

        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertNil(event?.id)
    }

    // MARK: - ID and Retry Persistence

    func testIdPersistsAcrossEvents() {
        var parser = SSEParser()

        // First event sets the id
        XCTAssertNil(parser.feed("id: 1"))
        XCTAssertNil(parser.feed("data: first"))
        let event1 = parser.feed("")
        XCTAssertEqual(event1?.id, "1")

        // Second event without explicit id should still carry the last id
        XCTAssertNil(parser.feed("data: second"))
        let event2 = parser.feed("")
        XCTAssertEqual(event2?.id, "1")
    }

    func testRetryPersistsAcrossEvents() {
        var parser = SSEParser()

        // First event sets retry
        XCTAssertNil(parser.feed("retry: 3000"))
        XCTAssertNil(parser.feed("data: first"))
        let event1 = parser.feed("")
        XCTAssertEqual(event1?.retry, 3000)

        // Second event should still carry the retry value
        XCTAssertNil(parser.feed("data: second"))
        let event2 = parser.feed("")
        XCTAssertEqual(event2?.retry, 3000)
    }

    // MARK: - Comment Lines

    func testCommentLinesAreIgnored() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed(": this is a comment"))
        XCTAssertNil(parser.feed("event: ping"))
        XCTAssertNil(parser.feed(": another comment"))
        XCTAssertNil(parser.feed("data: pong"))

        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.event, "ping")
        XCTAssertEqual(event?.data, "pong")
    }

    func testCommentOnlyDoesNotProduceEvent() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed(": keep-alive"))
        // Blank line with no data buffered should not produce an event
        let event = parser.feed("")
        XCTAssertNil(event)
    }

    // MARK: - Empty Events

    func testEmptyDataDoesNotProduceEvent() {
        var parser = SSEParser()

        // An event with only "event:" but no "data:" should not dispatch
        XCTAssertNil(parser.feed("event: ping"))
        let event = parser.feed("")
        XCTAssertNil(event, "Event without data field should not be dispatched per SSE spec")
    }

    func testConsecutiveBlankLinesProduceNothing() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed(""))
        XCTAssertNil(parser.feed(""))
        XCTAssertNil(parser.feed(""))
    }

    // MARK: - Unknown Fields

    func testUnknownFieldsAreIgnored() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed("event: test"))
        XCTAssertNil(parser.feed("custom: value"))
        XCTAssertNil(parser.feed("data: payload"))

        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.event, "test")
        XCTAssertEqual(event?.data, "payload")
    }

    // MARK: - Field Parsing Edge Cases

    func testFieldWithNoColon() {
        var parser = SSEParser()

        // A line with no colon treats the whole line as the field name with empty value
        XCTAssertNil(parser.feed("data"))
        // "data" with empty value still appends to the data buffer
        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.data, "")
    }

    func testFieldWithColonButNoValue() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed("data:"))
        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.data, "")
    }

    func testFieldValueSpaceStripping() {
        var parser = SSEParser()

        // Per SSE spec, only ONE leading space after the colon is stripped
        XCTAssertNil(parser.feed("data:  two spaces"))
        let event = parser.feed("")
        XCTAssertNotNil(event)
        // First space stripped, second preserved
        XCTAssertEqual(event?.data, " two spaces")
    }

    func testFieldWithNoSpaceAfterColon() {
        var parser = SSEParser()

        XCTAssertNil(parser.feed("data:no-space"))
        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.data, "no-space")
    }

    // MARK: - Reset

    func testResetClearsAllState() {
        var parser = SSEParser()

        // Build up some state
        XCTAssertNil(parser.feed("id: 99"))
        XCTAssertNil(parser.feed("retry: 1000"))
        XCTAssertNil(parser.feed("event: test"))
        XCTAssertNil(parser.feed("data: partial"))

        // Reset before dispatching
        parser.reset()

        // Now feed a fresh event -- id and retry should be gone
        XCTAssertNil(parser.feed("data: fresh"))
        let event = parser.feed("")
        XCTAssertNotNil(event)
        XCTAssertEqual(event?.event, "message") // default since event type was reset
        XCTAssertEqual(event?.data, "fresh")
        XCTAssertNil(event?.id)
        XCTAssertNil(event?.retry)
    }

    // MARK: - Multiple Events in Sequence

    func testMultipleEventsInSequence() {
        var parser = SSEParser()

        // First event
        XCTAssertNil(parser.feed("event: status"))
        XCTAssertNil(parser.feed("data: {\"running\":true}"))
        let event1 = parser.feed("")
        XCTAssertNotNil(event1)
        XCTAssertEqual(event1?.event, "status")

        // Second event
        XCTAssertNil(parser.feed("event: servers.changed"))
        XCTAssertNil(parser.feed("data: {\"server\":\"github\"}"))
        let event2 = parser.feed("")
        XCTAssertNotNil(event2)
        XCTAssertEqual(event2?.event, "servers.changed")
        XCTAssertEqual(event2?.data, "{\"server\":\"github\"}")
    }

    func testEventTypeResetsAfterDispatch() {
        var parser = SSEParser()

        // First event with explicit type
        XCTAssertNil(parser.feed("event: custom"))
        XCTAssertNil(parser.feed("data: first"))
        let event1 = parser.feed("")
        XCTAssertEqual(event1?.event, "custom")

        // Second event without explicit type should default to "message"
        XCTAssertNil(parser.feed("data: second"))
        let event2 = parser.feed("")
        XCTAssertEqual(event2?.event, "message")
    }

    // MARK: - Realistic SSE Stream

    func testRealisticSSEStream() {
        var parser = SSEParser()
        var events: [SSEEvent] = []

        let lines = [
            ": connected to MCPProxy SSE stream",
            "",
            "event: status",
            "id: 1",
            "data: {\"running\":true,\"listen_addr\":\"127.0.0.1:8080\"}",
            "",
            ": keep-alive",
            "",
            "event: servers.changed",
            "id: 2",
            "data: {\"reason\":\"tool_update\",\"server\":\"github-server\"}",
            "",
            "event: config.reloaded",
            "id: 3",
            "data: {\"source\":\"file_watcher\"}",
            "",
        ]

        for line in lines {
            if let event = parser.feed(line) {
                events.append(event)
            }
        }

        XCTAssertEqual(events.count, 3)

        XCTAssertEqual(events[0].event, "status")
        XCTAssertEqual(events[0].id, "1")
        XCTAssertTrue(events[0].data.contains("running"))

        XCTAssertEqual(events[1].event, "servers.changed")
        XCTAssertEqual(events[1].id, "2")

        XCTAssertEqual(events[2].event, "config.reloaded")
        XCTAssertEqual(events[2].id, "3")
    }
}
