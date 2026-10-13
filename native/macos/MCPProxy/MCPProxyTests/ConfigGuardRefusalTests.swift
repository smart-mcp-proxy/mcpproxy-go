import XCTest
@testable import MCPProxy

/// `PATCH /config` 409 keeps the structured guard-refusal body so Settings can
/// render GuardRefusalView instead of a red one-liner.
final class ConfigGuardRefusalTests: XCTestCase {
    func testGuardRefusalKeepsItsBody() {
        let data = Data(#"{"success":false,"error":"would expose bindings","code":"binding_bypassable_without_auth","bindings":[],"fixes":[]}"#.utf8)
        guard case .service(let status, let body) = APIClient.patchConfigError(status: 409, data: data) else {
            return XCTFail("expected .service")
        }
        XCTAssertEqual(status, 409)
        XCTAssertTrue(body.isGuardRefusal)
    }

    func testOtherErrorsStayPlainHTTPErrors() {
        let data = Data(#"{"success":false,"error":"bad value"}"#.utf8)
        guard case .httpError(let status, let message) = APIClient.patchConfigError(status: 409, data: data) else {
            return XCTFail("expected .httpError")
        }
        XCTAssertEqual(status, 409)
        XCTAssertEqual(message, "bad value")
    }
}
