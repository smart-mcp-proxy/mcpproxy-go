import XCTest
@testable import MCPProxy

final class GlanceLinksTests: XCTestCase {

    func testSessionAndKeyAreBothAppended() {
        XCTAssertEqual(
            activityURLString(baseURL: "http://127.0.0.1:8080", apiKey: "k1", sessionID: "sess-42"),
            "http://127.0.0.1:8080/ui/activity?session=sess-42&apikey=k1"
        )
    }

    func testMissingKeyOmitsTheParameter() {
        XCTAssertEqual(
            activityURLString(baseURL: "http://127.0.0.1:8080", apiKey: "", sessionID: "sess-42"),
            "http://127.0.0.1:8080/ui/activity?session=sess-42"
        )
    }

    func testMissingSessionOpensTheUnfilteredLog() {
        XCTAssertEqual(
            activityURLString(baseURL: "http://127.0.0.1:8080", apiKey: "k1", sessionID: nil),
            "http://127.0.0.1:8080/ui/activity?apikey=k1"
        )
        XCTAssertEqual(
            activityURLString(baseURL: "http://127.0.0.1:8080", apiKey: "", sessionID: ""),
            "http://127.0.0.1:8080/ui/activity"
        )
    }

    func testSessionIDIsPercentEncoded() {
        let url = activityURLString(baseURL: "http://127.0.0.1:8080", apiKey: "", sessionID: "a b&c")
        XCTAssertEqual(url, "http://127.0.0.1:8080/ui/activity?session=a%20b%26c")
        XCTAssertNotNil(URL(string: url))
    }

    func testNonDefaultPortIsPreserved() {
        XCTAssertEqual(
            activityURLString(baseURL: "http://127.0.0.1:18080", apiKey: "k", sessionID: "s"),
            "http://127.0.0.1:18080/ui/activity?session=s&apikey=k"
        )
    }
}
